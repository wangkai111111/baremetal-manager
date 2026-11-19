package platform

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"baremetal-manager/backend/internal/pxeservice"
	"baremetal-manager/backend/internal/service"
	"github.com/gin-gonic/gin"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestPhysicalInstallIdentityAndAddress(t *testing.T) {
	for _, mac := range []string{"", "bad", "00:00:00:00:00:00", "ff:ff:ff:ff:ff:ff", "01:00:5e:00:00:01", pxeservice.TestVMMAC} {
		if _, err := installationTarget("Dell", "R750", mac); err == nil {
			t.Fatalf("invalid physical MAC accepted: %q", mac)
		}
	}
	if target, err := installationTarget("Dell", "R750", "00:11:22:33:44:55"); err != nil || target != "physical" {
		t.Fatal("physical machine rejected")
	}
	if target, err := installationTarget("BMM Test VM", "QEMU PXE Lab", pxeservice.TestVMMAC); err != nil || target != "test_vm" {
		t.Fatal("test VM compatibility broken")
	}
	if _, err := installationHTTPBase(installationSnapshot{Target: "physical"}); err == nil {
		t.Fatal("physical installation silently used the lab HTTP address")
	}
	snap := installationSnapshot{Target: "physical", Subnet: "172.16.20.0/24"}
	for _, ip := range []string{"", "192.168.77.100", "172.26.0.2", "::1"} {
		if physicalCallbackIP(snap, ip) != "" {
			t.Fatal("unrelated callback address persisted")
		}
	}
	if physicalCallbackIP(snap, "172.16.20.123") != "172.16.20.123" {
		t.Fatal("physical DHCP address not recognized")
	}
	snap.MAC = "00:11:22:33:44:55"
	snap.HTTPBase = "http://172.16.20.23:18080/api/v1/provision"
	snap.Content = testVMAutoinstall
	seed, err := renderInstallationSeed(snap)
	if err != nil {
		t.Fatal(err)
	}
	ai := seed["autoinstall"].(map[string]any)
	network := fmt.Sprint(ai["network"])
	if !strings.Contains(network, snap.MAC) || strings.Contains(network, pxeservice.TestVMMAC) || ai["shutdown"] != "reboot" {
		t.Fatal("physical installer matched the VM NIC or did not reboot")
	}
}

// Uses a disposable DB, temporary ISO and provisioning tree. Physical preparation
// must complete without contacting QEMU, BMC, or any target machine.
func TestPhysicalInstallIntegration(t *testing.T) {
	dsn := os.Getenv("PLATFORM_TEST_DSN")
	if dsn == "" {
		t.Skip("requires migrated disposable database")
	}
	t.Setenv("BMC_ENCRYPTION_KEY", "01234567890123456789012345678901")
	t.Setenv("PXE_HTTP_BASE", "http://172.16.20.23:18080/api/v1/provision")
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	conn, _ := db.DB()
	defer conn.Close()
	for _, mode := range []string{"success", "timeout", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			tx := db.Begin()
			defer tx.Rollback()
			seedIntegrationAdmin(t, tx)
			var sc scope
			if err := tx.Raw("SELECT tenant_id AS tenant,id AS project,(SELECT id FROM users WHERE username='integration-admin') AS user FROM projects WHERE deleted_at IS NULL ORDER BY id LIMIT 1").Scan(&sc).Error; err != nil || sc.Project == 0 {
				t.Fatal("project fixture missing", err)
			}
			sc.Admin = true
			var networkID, assetID int64
			if err := tx.Raw("INSERT INTO networks(tenant_id,project_id,name,subnet,dhcp_enabled) VALUES (?,?,'physical fixture','172.16.20.0/24',true) RETURNING id", sc.Tenant, sc.Project).Scan(&networkID).Error; err != nil {
				t.Fatal(err)
			}
			if err := tx.Raw(`INSERT INTO assets(tenant_id,project_id,name,manufacturer,model,mac_address,bmc_address,bmc_user,bmc_secret_ciphertext,bmc_secret_key_id) VALUES (?,?,'physical fixture','Dell','R750','00:11:22:33:44:55','192.0.2.5','test',?,'v1') RETURNING id`, sc.Tenant, sc.Project, []byte("unused")).Scan(&assetID).Error; err != nil {
				t.Fatal(err)
			}
			root := t.TempDir()
			t.Setenv("PXE_TFTP_ROOT", root)
			media := makeInstallationMediaFixture(t, root, true)
			planID, err := ensureTestVMInstallPlan(tx, sc, networkID, testVMISOMetadata{ObjectKey: media.ObjectKey, SHA256: media.SHA256, Size: media.SizeBytes})
			if err != nil {
				t.Fatal(err)
			}
			if err := tx.Exec("UPDATE images SET version='24.04.1' WHERE object_key=?", media.ObjectKey).Error; err != nil {
				t.Fatal(err)
			}
			a := &App{store: &store{db: tx}}
			id, opID, err := a.enqueueInstallation(context.Background(), sc, assetID, planID, networkID, root, nil, auditMeta{})
			if err != nil {
				t.Fatal(err)
			}
			var row struct{ ConfigSnapshot json.RawMessage }
			tx.Table("installations").Select("config_snapshot").Where("id=?", id).Take(&row)
			var snap installationSnapshot
			if err := json.Unmarshal(row.ConfigSnapshot, &snap); err != nil || snap.Target != "physical" || snap.IP != "" || snap.Subnet != "172.16.20.0/24" {
				t.Fatal("incorrect physical snapshot", err)
			}
			token, err := service.DecryptCredential(snap.TokenEncrypted)
			if err != nil {
				t.Fatal(err)
			}
			var op operation
			tx.Table("operations").Where("id=?", opID).Updates(map[string]any{"status": "running", "started_at": time.Now()})
			tx.Table("operations").Where("id=?", opID).Take(&op)
			if err := a.installationStep(context.Background(), op, "prepare", "preparing"); err != nil {
				t.Fatal(err)
			}
			if err := a.prepareTestInstallation(context.Background(), op); err != nil {
				t.Fatal(err)
			}
			// PostgreSQL now() is fixed at the surrounding fixture transaction start.
			// The real worker commits preparation before callbacks arrive.
			tx.Table("installations").Where("id=?", id).Update("started_at", gorm.Expr("now()-interval '1 second'"))
			tx.Table("operations").Where("id=?", opID).Update("started_at", gorm.Expr("now()-interval '1 second'"))
			script, err := os.ReadFile(filepath.Join(root, "ipxe", "001122334455.ipxe"))
			if err != nil || !strings.Contains(string(script), "172.16.20.23:18080") || strings.Contains(string(script), "192.168.77.") {
				t.Fatal("physical boot still depends on isolated VM network", err)
			}
			confirm := func(testScope scope, stopped bool) int {
				r := gin.New()
				r.Use(func(c *gin.Context) { c.Set("scope", testScope) })
				r.POST("/assets/:id/install/confirm-stopped", a.confirmPhysicalInstallStopped)
				w := httptest.NewRecorder()
				req := httptest.NewRequest("POST", fmt.Sprintf("/assets/%d/install/confirm-stopped", assetID), strings.NewReader(fmt.Sprintf(`{"operation_id":%d,"stopped":%t}`, opID, stopped)))
				req.Header.Set("Content-Type", "application/json")
				r.ServeHTTP(w, req)
				return w.Code
			}
			if mode != "success" {
				if mode == "timeout" {
					tx.Table("installations").Where("id=?", id).Update("deadline_at", time.Now().Add(-time.Second))
					if err := a.expireInstallationWithStop(context.Background(), opID, func(context.Context, int64, int64) error { t.Fatal("physical timeout operated test VM"); return nil }); err != nil {
						t.Fatal(err)
					}
					var state installProgress
					tx.Table("installations").Where("id=?", id).Take(&state)
					if state.Status != "failed" || !state.CleanupPending {
						t.Fatal("physical timeout released machine prematurely")
					}
				}
				if confirm(sc, false) != 400 {
					t.Fatal("stop accepted without physical confirmation")
				}
				wrong := sc
				wrong.Project++
				if confirm(wrong, true) == 200 {
					t.Fatal("cross-project stop accepted")
				}
				if confirm(sc, true) != 200 {
					t.Fatal("physical stop confirmation failed")
				}
				var state installProgress
				tx.Table("installations").Where("id=?", id).Take(&state)
				if state.CleanupPending || (mode == "timeout" && state.Status != "failed") || (mode == "cancel" && state.Status != "cancelled") {
					t.Fatal("incorrect stop state")
				}
				return
			}
			callback := func(body string) {
				r := gin.New()
				r.POST("/callback/:token", a.installCallback)
				w := httptest.NewRecorder()
				req := httptest.NewRequest("POST", "/callback/"+token, strings.NewReader(body))
				req.RemoteAddr = "172.16.20.123:1234"
				req.Header.Set("Content-Type", "application/json")
				r.ServeHTTP(w, req)
				if w.Code != 200 {
					t.Fatalf("callback failed: %d", w.Code)
				}
			}
			callback(`{"status":"installing"}`)
			callback(`{"status":"post_install"}`)
			callback(`{"status":"post_install","stage":"first_boot"}`)
			if err := a.resumeInstallDiskBoot(context.Background(), opID, func(context.Context, string, string, *pxeservice.TestVMConfig) (pxeservice.TestVMStatus, int, error) {
				t.Fatal("physical installation launched test VM")
				return pxeservice.TestVMStatus{}, 0, nil
			}); err != nil {
				t.Fatal(err)
			}
			callback(`{"status":"succeeded","stage":"first_boot"}`)
			var asset struct{ Status, PowerState, ManagementIP string }
			if err := tx.Table("assets").Select("status,power_state,host(management_ip) AS management_ip").Where("id=?", assetID).Take(&asset).Error; err != nil || asset.Status != "online" || asset.PowerState != "on" || asset.ManagementIP != "172.16.20.123" {
				t.Fatal("physical success not persisted", err)
			}
		})
	}
}
