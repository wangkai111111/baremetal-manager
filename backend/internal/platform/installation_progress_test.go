package platform

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"baremetal-manager/backend/internal/pxeservice"
	"baremetal-manager/backend/internal/service"
	"baremetal-manager/backend/migrations"
	"github.com/gin-gonic/gin"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestInstallStepsMonotonicAndBounded(t *testing.T) {
	now := time.Now()
	steps := newInstallSteps(now)
	for i, key := range []string{"prepare", "reboot", "pxe_boot", "download", "install", "post_install", "first_boot"} {
		changed, err := advanceInstallSteps(steps, key, "confirmed", now.Add(time.Duration(i+1)*time.Second))
		if !changed || err != nil {
			t.Fatalf("advance %s: %v", key, err)
		}
		deadline := *steps[i+1].DeadlineAt
		changed, err = advanceInstallSteps(steps, key, "duplicate", now.Add(time.Hour))
		if changed || err != nil || !steps[i+1].DeadlineAt.Equal(deadline) {
			t.Fatal("duplicate extended step deadline")
		}
		changed, _ = advanceInstallSteps(steps, "queued", "late", now)
		if changed {
			t.Fatal("late event regressed progress")
		}
	}
	terminalInstallSteps(steps, "succeeded", "disk verified", now.Add(time.Minute))
	for _, s := range steps {
		if s.Status != "succeeded" || s.StartedAt == nil || s.CompletedAt == nil {
			t.Fatalf("incomplete step %s", s.Key)
		}
	}
	if changed, _ := advanceInstallSteps(steps, "install", "late", now.Add(time.Hour)); changed {
		t.Fatal("terminal state reopened")
	}
}
func TestInstallStepsFailureAndDeadlines(t *testing.T) {
	now := time.Now()
	steps := newInstallSteps(now)
	if installDeadlinePassed(installProgress{}, steps, now.Add(119*time.Second)) {
		t.Fatal("early expiry")
	}
	if !installDeadlinePassed(installProgress{}, steps, now.Add(120*time.Second)) {
		t.Fatal("deadline not enforced")
	}
	future := now.Add(time.Hour)
	past := now.Add(-time.Second)
	if !installDeadlinePassed(installProgress{DeadlineAt: &past}, steps, now) {
		t.Fatal("total deadline ignored")
	}
	if installDeadlinePassed(installProgress{DeadlineAt: &future}, steps, now) {
		t.Fatal("future deadline expired")
	}
	advanceInstallSteps(steps, "pxe_boot", "waiting", now.Add(time.Second))
	if steps[1].Status != "skipped" || steps[1].StartedAt != nil {
		t.Fatal("missing evidence fabricated a timestamp")
	}
	terminalInstallSteps(steps, "failed", "PXE timed out", now.Add(4*time.Minute))
	if steps[3].Status != "failed" || steps[4].Status != "skipped" {
		t.Fatal("failure did not close steps")
	}
	if changed, _ := advanceInstallSteps(steps, "install", "late", now.Add(time.Hour)); changed {
		t.Fatal("failed step revived")
	}
}

func TestInstallationTimeoutCallbackAndTaskIntegration(t *testing.T) {
	dsn := os.Getenv("PLATFORM_TEST_DSN")
	if dsn == "" {
		t.Skip("requires migrated disposable database with PXE fixture")
	}
	t.Setenv("BMC_ENCRYPTION_KEY", "01234567890123456789012345678901")
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	conn, _ := db.DB()
	defer conn.Close()
	if err := migrations.Apply(db); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"success", "timeout-stop-success", "timeout-stop-retry", "timeout-queued", "cancel", "disk-boot"} {
		t.Run(mode, func(t *testing.T) {
			tx := db.Begin()
			defer tx.Rollback()
			seedIntegrationAdmin(t, tx)
			var fixture struct{ TenantID, ProjectID, AssetID, PlanID, UserID int64 }
			if err := tx.Raw(`SELECT a.tenant_id,a.project_id,a.id AS asset_id,p.id AS plan_id,(SELECT id FROM users WHERE username='integration-admin') AS user_id FROM assets a JOIN install_plans p ON p.tenant_id=a.tenant_id AND p.project_id=a.project_id AND p.name=? WHERE a.manufacturer='BMM Test VM' AND a.model='QEMU PXE Lab' AND a.deleted_at IS NULL LIMIT 1`, testVMInstallName).Scan(&fixture).Error; err != nil {
				t.Fatal(err)
			}
			if fixture.AssetID == 0 {
				t.Skip("no PXE fixture")
			}
			var op operation
			key := fmt.Sprintf("progress-test-%d", time.Now().UnixNano())
			if err := tx.Raw(`INSERT INTO operations(tenant_id,project_id,asset_id,user_id,kind,idempotency_key,status,timeout_seconds,started_at) VALUES (?,?,?,?,'install',?,'running',3600,now()) RETURNING *`, fixture.TenantID, fixture.ProjectID, fixture.AssetID, fixture.UserID, key).Scan(&op).Error; err != nil {
				t.Fatal(err)
			}
			token := strings.Repeat("progress-test-token", 3)
			now := time.Now()
			steps := newInstallSteps(now)
			for _, key := range []string{"prepare", "reboot", "pxe_boot"} {
				advanceInstallSteps(steps, key, "confirmed", now)
			}
			if strings.HasPrefix(mode, "timeout") {
				past := now.Add(-time.Second)
				steps[3].DeadlineAt = &past
			}
			if mode == "disk-boot" {
				for _, key := range []string{"download", "install", "post_install", "first_boot"} {
					advanceInstallSteps(steps, key, "confirmed", now)
				}
			}
			if mode == "timeout-queued" {
				steps = newInstallSteps(now.Add(-3 * time.Minute))
				if err := tx.Table("operations").Where("id=?", op.ID).Updates(map[string]any{"status": "pending", "started_at": nil}).Error; err != nil {
					t.Fatal(err)
				}
			}
			encoded, _ := json.Marshal(steps)
			var instID int64
			if err := tx.Raw(`INSERT INTO installations(tenant_id,project_id,asset_id,operation_id,install_plan_id,config_snapshot,callback_token_hash,callback_expires_at,confirmed_by,confirmed_at,status,started_at,progress,deadline_at) VALUES (?,?,?,?,?,'{}',?,now()+interval '1 hour',?,now(),'booting',now(),?::jsonb,now()+interval '1 hour') RETURNING id`, fixture.TenantID, fixture.ProjectID, fixture.AssetID, op.ID, fixture.PlanID, service.CallbackTokenHash(token), fixture.UserID, string(encoded)).Scan(&instID).Error; err != nil {
				t.Fatal(err)
			}
			app := &App{store: &store{db: tx}}
			callback := func(status, stage string) int {
				w := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(w)
				c.Request = httptest.NewRequest("POST", "/", strings.NewReader(fmt.Sprintf(`{"status":%q,"stage":%q}`, status, stage)))
				c.Request.Header.Set("Content-Type", "application/json")
				c.Params = gin.Params{{Key: "token", Value: token}}
				app.installCallback(c)
				return w.Code
			}
			if mode == "success" {
				for _, s := range []struct{ status, stage string }{{"installing", "installing"}, {"post_install", "post_install"}, {"post_install", "first_boot"}, {"succeeded", "first_boot"}} {
					if code := callback(s.status, s.stage); code != 200 {
						t.Fatalf("callback %s: %d", s.status, code)
					}
				}
				if callback("installing", "late") != 200 {
					t.Fatal("late callback should be acknowledged without regression")
				}
				var status string
				tx.Table("operations").Select("status").Where("id=?", op.ID).Scan(&status)
				if status != "succeeded" {
					t.Fatal("success regressed")
				}
			} else if mode == "disk-boot" {
				calls, posts := 0, 0
				running := false
				wrongOwner := false
				control := func(ctx context.Context, method, path string, cfg *pxeservice.TestVMConfig) (pxeservice.TestVMStatus, int, error) {
					calls++
					owner := pxeservice.TestVMConfig{TenantID: op.TenantID, ProjectID: op.ProjectID, Firmware: "uefi"}
					if wrongOwner {
						owner.ProjectID++
					}
					if method == "POST" {
						posts++
						if path != "/test-vm/boot-disk" || cfg == nil {
							t.Fatal("wrong disk boot action")
						}
						running = true
					}
					return pxeservice.TestVMStatus{TestVMConfig: owner, Running: running}, 200, nil
				}
				tx.Table("installations").Where("id=?", instID).Update("status", "installing")
				if err := app.resumeInstallDiskBoot(context.Background(), op.ID, control); err != nil || calls != 0 {
					t.Fatal("disk started before final installation callback")
				}
				tx.Table("installations").Where("id=?", instID).Update("status", "post_install")
				wrongOwner = true
				if err := app.resumeInstallDiskBoot(context.Background(), op.ID, control); err == nil || posts != 0 {
					t.Fatal("cross-project disk boot allowed")
				}
				wrongOwner = false
				if err := app.resumeInstallDiskBoot(context.Background(), op.ID, control); err != nil || posts != 1 {
					t.Fatalf("disk boot not reconciled: %v", err)
				}
				if err := app.resumeInstallDiskBoot(context.Background(), op.ID, control); err != nil || posts != 1 {
					t.Fatal("running VM was restarted")
				}
				running = false
				before := calls
				tx.Table("installations").Where("id=?", instID).Update("deadline_at", now.Add(-time.Second))
				if err := app.resumeInstallDiskBoot(context.Background(), op.ID, control); err != nil || calls != before {
					t.Fatal("expired task restarted VM")
				}
				tx.Table("operations").Where("id=?", op.ID).Update("status", "cancelled")
				if err := app.resumeInstallDiskBoot(context.Background(), op.ID, control); err != nil || calls != before {
					t.Fatal("cancelled task restarted VM")
				}
			} else if mode == "cancel" {
				if err := finishInstallProgress(tx, op, "cancelled", "cancelled"); err != nil {
					t.Fatal(err)
				}
				tx.Table("installations").Where("id=?", instID).Updates(map[string]any{"status": "cancelled", "completed_at": now, "callback_revoked_at": now})
				if callback("succeeded", "first_boot") != 404 {
					t.Fatal("cancelled token accepted")
				}
			} else {
				if callback("installing", "installing") != 410 {
					t.Fatal("expired stage revived by callback")
				}
				calls := 0
				stop := func(context.Context, int64, int64) error {
					calls++
					if mode == "timeout-stop-retry" && calls == 1 {
						return errors.New("unavailable")
					}
					return nil
				}
				if err := app.expireInstallationWithStop(context.Background(), op.ID, stop); err != nil {
					t.Fatal(err)
				}
				var inst installProgress
				tx.Table("installations").Where("id=?", instID).Take(&inst)
				if inst.Status != "failed" || !strings.Contains(inst.Error, "超时") || inst.CleanupPending != (mode == "timeout-stop-retry") {
					t.Fatalf("bad timeout state: %+v", inst)
				}
				if err := app.expireInstallationWithStop(context.Background(), op.ID, stop); err != nil {
					t.Fatal(err)
				}
				tx.Table("installations").Where("id=?", instID).Take(&inst)
				if inst.CleanupPending {
					t.Fatal("cleanup not retried")
				}
				if callback("succeeded", "first_boot") != 404 {
					t.Fatal("expired callback credential accepted")
				}
				var status string
				tx.Table("operations").Select("status").Where("id=?", op.ID).Scan(&status)
				if status != "timeout" {
					t.Fatalf("task status %s", status)
				}
			}
			// Test task logs have real persisted timestamps and can be scanned.
			if mode != "cancel" {
				logs, err := app.taskLogRows(scope{Tenant: fixture.TenantID, Project: fixture.ProjectID}, op.ID, 200, 0)
				if err != nil || len(logs) == 0 {
					t.Fatalf("task logs unavailable: %v", err)
				}
			}
			// Test the actual wired handler contract and exclusion of credentials/snapshots.
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest("GET", "/", nil)
			c.Params = gin.Params{{Key: "id", Value: fmt.Sprint(op.ID)}}
			c.Set("scope", scope{Tenant: fixture.TenantID, Project: fixture.ProjectID, Admin: true})
			app.getTask(c)
			if w.Code != 200 || !strings.Contains(w.Body.String(), `"steps"`) || strings.Contains(w.Body.String(), token) || strings.Contains(w.Body.String(), "config_snapshot") {
				t.Fatalf("invalid task detail response: %d", w.Code)
			}
		})
	}
}
