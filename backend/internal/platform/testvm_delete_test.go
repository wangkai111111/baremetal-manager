package platform

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"baremetal-manager/backend/internal/pxeservice"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestDeleteTestVMIntegration(t *testing.T) {
	dsn := os.Getenv("PLATFORM_TEST_DSN")
	if dsn == "" {
		t.Skip("requires disposable database with PXE fixture")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	conn, _ := db.DB()
	defer conn.Close()
	for _, mode := range []string{"success", "controller-failure", "other-owner", "other-asset-scope", "installing", "cleanup-pending", "pending-task", "leased", "lost-controller-state", "replaced-vm"} {
		t.Run(mode, func(t *testing.T) {
			tx := db.Begin()
			defer tx.Rollback()
			seedIntegrationAdmin(t, tx)
			var f struct{ TenantID, ProjectID, AssetID, PlanID, UserID int64 }
			if err := tx.Raw(`SELECT a.tenant_id,a.project_id,a.id AS asset_id,p.id AS plan_id,(SELECT id FROM users WHERE username='integration-admin') AS user_id FROM assets a JOIN install_plans p ON p.tenant_id=a.tenant_id AND p.project_id=a.project_id AND p.name=? AND p.deleted_at IS NULL WHERE a.manufacturer='BMM Test VM' AND a.model='QEMU PXE Lab' AND a.deleted_at IS NULL LIMIT 1`, testVMInstallName).Scan(&f).Error; err != nil {
				t.Fatal(err)
			}
			if f.AssetID == 0 {
				t.Skip("no PXE fixture")
			}
			sc := scope{Tenant: f.TenantID, Project: f.ProjectID, User: f.UserID, Admin: true, Audit: auditMeta{ClientIP: "192.0.2.45"}}
			owner := pxeservice.TestVMConfig{TenantID: sc.Tenant, ProjectID: sc.Project, Firmware: "uefi"}
			if mode == "other-owner" {
				owner.ProjectID++
			}
			if mode == "other-asset-scope" {
				sc.Project++
			}
			if mode == "lost-controller-state" {
				owner = pxeservice.TestVMConfig{}
			}
			var historyBefore int64
			if err := tx.Table("installations").Where("asset_id=?", f.AssetID).Count(&historyBefore).Error; err != nil {
				t.Fatal(err)
			}
			if mode == "installing" || mode == "cleanup-pending" {
				status := "installing"
				if mode == "cleanup-pending" {
					status = "failed"
				}
				if err := tx.Exec(`UPDATE installations SET status=?,cleanup_pending=? WHERE id=(SELECT max(id) FROM installations WHERE asset_id=?)`, status, mode == "cleanup-pending", f.AssetID).Error; err != nil {
					t.Fatal(err)
				}
				if historyBefore == 0 {
					t.Fatal("requires installation history")
				}
			}
			if mode == "pending-task" || mode == "leased" {
				var opID int64
				status := "pending"
				if mode == "leased" {
					status = "succeeded"
				}
				if err := tx.Raw(`INSERT INTO operations(tenant_id,project_id,asset_id,user_id,kind,idempotency_key,status,timeout_seconds) VALUES (?,?,?,?,'install',?,?,3600) RETURNING id`, f.TenantID, f.ProjectID, f.AssetID, f.UserID, fmt.Sprintf("delete-vm-%d", time.Now().UnixNano()), status).Scan(&opID).Error; err != nil {
					t.Fatal(err)
				}
				if mode == "leased" {
					if err := tx.Exec(`INSERT INTO device_leases(tenant_id,project_id,asset_id,operation_id,holder_id,expires_at) VALUES (?,?,?,?,'delete-vm-test',now()+interval '1 hour') ON CONFLICT (tenant_id,project_id,asset_id) DO UPDATE SET expires_at=EXCLUDED.expires_at`, f.TenantID, f.ProjectID, f.AssetID, opID).Error; err != nil {
						t.Fatal(err)
					}
				}
			}
			deletes := 0
			control := func(_ context.Context, method, path string, cfg *pxeservice.TestVMConfig) (pxeservice.TestVMStatus, int, error) {
				if path != "/test-vm" {
					t.Fatalf("unexpected controller path: %s", path)
				}
				if method == "DELETE" {
					deletes++
					if cfg == nil || cfg.TenantID != f.TenantID || cfg.ProjectID != f.ProjectID || cfg.Firmware == "" {
						t.Fatal("invalid deletion owner")
					}
					if mode == "controller-failure" {
						return pxeservice.TestVMStatus{}, 500, errors.New("failed to stop QEMU")
					}
					owner = pxeservice.TestVMConfig{}
				} else if method != "GET" {
					t.Fatalf("unexpected controller method: %s", method)
				}
				return pxeservice.TestVMStatus{TestVMConfig: owner}, 200, nil
			}
			app := &App{store: &store{db: tx}}
			expected := f.AssetID
			if mode == "replaced-vm" {
				expected++
			}
			err := app.deleteTestVM(context.Background(), sc, expected, control)
			success := mode == "success" || mode == "lost-controller-state"
			if (err == nil) != success {
				t.Fatalf("delete success=%v error=%v", success, err)
			}
			if !success && mode != "controller-failure" && deletes != 0 {
				t.Fatal("guard allowed destructive controller call")
			}
			var live int64
			if err := tx.Table("assets").Where("id=? AND deleted_at IS NULL", f.AssetID).Count(&live).Error; err != nil {
				t.Fatal(err)
			}
			if (live == 0) != success {
				t.Fatal("asset deletion did not match controller result")
			}
			var audits []struct{ ClientIP, Snapshot string }
			if err := tx.Table("audits").Select("host(client_ip) AS client_ip, after_snapshot::text AS snapshot").Where("tenant_id=? AND project_id=? AND user_id=? AND action='delete' AND target_type='assets' AND target_id=?", f.TenantID, f.ProjectID, f.UserID, fmt.Sprint(f.AssetID)).Find(&audits).Error; err != nil {
				t.Fatal(err)
			}
			if success {
				if len(audits) != 1 || audits[0].ClientIP != "192.0.2.45" || !strings.Contains(audits[0].Snapshot, "BMM Test VM") {
					t.Fatalf("missing readable audit: %+v", audits)
				}
				var historyAfter int64
				tx.Table("installations").Where("asset_id=?", f.AssetID).Count(&historyAfter)
				if historyAfter != historyBefore {
					t.Fatal("installation history was removed")
				}
				if err := app.deleteTestVM(context.Background(), sc, expected, control); err != nil {
					t.Fatalf("retry failed: %v", err)
				}
				// The old asset remains for historical foreign keys; a new test VM
				// can reuse its fixed addresses after soft deletion.
				if err := tx.Exec(`INSERT INTO assets(tenant_id,project_id,name,manufacturer,model,bmc_address,bmc_user,bmc_secret_ciphertext,bmc_secret_key_id,mac_address) SELECT tenant_id,project_id,name,manufacturer,model,bmc_address,bmc_user,bmc_secret_ciphertext,bmc_secret_key_id,mac_address FROM assets WHERE id=?`, f.AssetID).Error; err != nil {
					t.Fatalf("cannot recreate after deletion: %v", err)
				}
			} else if len(audits) != 0 {
				t.Fatal("failed deletion left a success audit")
			}
		})
	}
}
