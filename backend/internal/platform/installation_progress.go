package platform

import (
	"baremetal-manager/backend/internal/pxeservice"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type installStep struct {
	Key            string     `json:"key"`
	Title          string     `json:"title"`
	Status         string     `json:"status"`
	TimeoutSeconds int        `json:"timeout_seconds"`
	StartedAt      *time.Time `json:"started_at"`
	CompletedAt    *time.Time `json:"completed_at"`
	DeadlineAt     *time.Time `json:"deadline_at"`
	Message        string     `json:"message"`
}

type installProgress struct {
	ID             int64           `json:"id"`
	OperationID    int64           `json:"operation_id"`
	Status         string          `json:"status"`
	Error          string          `json:"error"`
	Progress       json.RawMessage `json:"steps" gorm:"column:progress"`
	DeadlineAt     *time.Time      `json:"deadline_at"`
	StartedAt      *time.Time      `json:"started_at"`
	CompletedAt    *time.Time      `json:"completed_at"`
	CleanupPending bool            `json:"cleanup_pending"`
}

func newInstallSteps(now time.Time) []installStep {
	steps := []installStep{
		{Key: "queued", Title: "任务排队", TimeoutSeconds: 120},
		{Key: "prepare", Title: "校验所选镜像、提取内核与生成配置", TimeoutSeconds: 600},
		{Key: "reboot", Title: "重启 VM 并强制网络引导", TimeoutSeconds: 60},
		{Key: "pxe_boot", Title: "DHCP / TFTP / iPXE 引导", TimeoutSeconds: 180},
		{Key: "download", Title: "下载内核、initrd 与系统镜像", TimeoutSeconds: 900},
		{Key: "install", Title: "自动分区与安装操作系统", TimeoutSeconds: 1800},
		{Key: "post_install", Title: "应用账户、SSH 与启动配置", TimeoutSeconds: 300},
		{Key: "first_boot", Title: "从磁盘启动并验证系统", TimeoutSeconds: 300},
	}
	for i := range steps {
		steps[i].Status = "pending"
	}
	startInstallStep(&steps[0], now, "已提交任务，等待执行器领取")
	return steps
}
func startInstallStep(s *installStep, now time.Time, message string) {
	s.Status = "running"
	s.StartedAt = &now
	deadline := now.Add(time.Duration(s.TimeoutSeconds) * time.Second)
	s.DeadlineAt = &deadline
	s.Message = message
}

// Monotonic advancement: duplicate and delayed events cannot reset deadlines or regress progress.
func advanceInstallSteps(steps []installStep, key, message string, now time.Time) (bool, error) {
	target := -1
	current := -1
	for i := range steps {
		if steps[i].Key == key {
			target = i
		}
		if steps[i].Status == "running" {
			current = i
		}
		if steps[i].Status == "failed" || steps[i].Status == "cancelled" {
			return false, nil
		}
	}
	if target < 0 {
		return false, errors.New("invalid installation step")
	}
	if current > target || steps[target].Status == "succeeded" || steps[target].Status == "skipped" {
		return false, nil
	}
	if current == target {
		return false, nil
	}
	for i := 0; i < target; i++ {
		if steps[i].Status == "running" {
			steps[i].Status = "succeeded"
			steps[i].CompletedAt = &now
		}
		if steps[i].Status == "pending" {
			steps[i].Status = "skipped"
			steps[i].Message = "未收到独立阶段事件，已由后续阶段确认"
		}
	}
	startInstallStep(&steps[target], now, message)
	return true, nil
}
func terminalInstallSteps(steps []installStep, status, message string, now time.Time) {
	for i := range steps {
		if steps[i].Status == "running" {
			steps[i].Status = status
			steps[i].CompletedAt = &now
			steps[i].Message = message
		} else if steps[i].Status == "pending" {
			steps[i].Status = "skipped"
			steps[i].Message = "任务已结束，此步骤未执行"
		}
	}
}

// Caller must hold the operation lock, followed by the installation lock.
func recordInstallStep(tx *gorm.DB, op operation, key, message string) error {
	var inst installProgress
	if err := tx.Table("installations").Clauses(clause.Locking{Strength: "UPDATE"}).Where("operation_id=? AND deleted_at IS NULL", op.ID).Take(&inst).Error; err != nil {
		return err
	}
	if inst.Status == "succeeded" || inst.Status == "failed" || inst.Status == "cancelled" {
		return nil
	}
	var steps []installStep
	if err := json.Unmarshal(inst.Progress, &steps); err != nil {
		return err
	}
	if len(steps) == 0 {
		return nil
	} // Historical tasks have no fabricated step timings.
	now := time.Now()
	if installDeadlinePassed(inst, steps, now) {
		return gorm.ErrRecordNotFound
	}
	changed, err := advanceInstallSteps(steps, key, message, now)
	if err != nil || !changed {
		return err
	}
	encoded, _ := json.Marshal(steps)
	if err := tx.Table("installations").Where("id=?", inst.ID).Update("progress", string(encoded)).Error; err != nil {
		return err
	}
	return installEvent(tx, op, inst.ID, "step/"+key, key, message)
}
func installEvent(tx *gorm.DB, op operation, id int64, eventKey, stage, message string) error {
	r := tx.Exec(`INSERT INTO installation_events(tenant_id,project_id,installation_id,event_key,stage,message,occurred_at) VALUES (?,?,?,?,?,?,clock_timestamp()) ON CONFLICT (tenant_id,project_id,installation_id,event_key) WHERE deleted_at IS NULL DO NOTHING`, op.TenantID, op.ProjectID, id, eventKey, stage, message)
	if r.Error != nil || r.RowsAffected == 0 {
		return r.Error
	}
	var sequence int
	if err := tx.Table("operation_logs").Select("COALESCE(MAX(sequence),0)+1").Where("operation_id=?", op.ID).Scan(&sequence).Error; err != nil {
		return err
	}
	return operationLog(tx, op, sequence, "info", message)
}
func (a *App) installationStep(ctx context.Context, op operation, key, message string) error {
	return a.store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Table("operations").Clauses(clause.Locking{Strength: "UPDATE"}).Where("id=? AND status='running'", op.ID).Take(&op).Error; err != nil {
			return err
		}
		return recordInstallStep(tx, op, key, message)
	})
}
func finishInstallProgress(tx *gorm.DB, op operation, status, message string) error {
	var inst installProgress
	if err := tx.Table("installations").Clauses(clause.Locking{Strength: "UPDATE"}).Where("operation_id=?", op.ID).Take(&inst).Error; err != nil {
		return err
	}
	var steps []installStep
	if err := json.Unmarshal(inst.Progress, &steps); err != nil {
		return err
	}
	terminalInstallSteps(steps, status, message, time.Now())
	data, _ := json.Marshal(steps)
	if err := tx.Table("installations").Where("id=?", inst.ID).Update("progress", string(data)).Error; err != nil {
		return err
	}
	return installEvent(tx, op, inst.ID, "terminal/"+status, status, message)
}
func (a *App) installationTaskData(tx *gorm.DB, opID int64) (gin.H, error) {
	var inst installProgress
	if err := tx.Table("installations").Where("operation_id=? AND deleted_at IS NULL", opID).Take(&inst).Error; err != nil {
		return nil, err
	}
	var events []struct {
		Stage      string    `json:"stage"`
		Message    string    `json:"message"`
		OccurredAt time.Time `json:"occurred_at"`
	}
	if err := tx.Table("installation_events").Select("stage,message,occurred_at").Where("installation_id=? AND tenant_id=(SELECT tenant_id FROM installations WHERE id=?) AND project_id=(SELECT project_id FROM installations WHERE id=?) AND deleted_at IS NULL", inst.ID, inst.ID, inst.ID).Order("occurred_at,id").Limit(250).Scan(&events).Error; err != nil {
		return nil, err
	}
	return gin.H{"installation": inst, "events": events, "server_time": time.Now()}, nil
}

// Runs independently of HTTP polling. Locks fence callbacks, cancellation and worker side effects.
func (a *App) expireInstallations(ctx context.Context) error {
	var ids []int64
	if err := a.store.db.WithContext(ctx).Table("installations").Select("operation_id").Where("deleted_at IS NULL AND (cleanup_pending OR (status IN ('pending','preparing','booting','installing','post_install','unknown') AND (deadline_at<=now() OR EXISTS (SELECT 1 FROM jsonb_array_elements(progress) s WHERE s->>'status'='running' AND (s->>'deadline_at')::timestamptz<=now()))))").Order("id").Limit(20).Scan(&ids).Error; err != nil {
		return err
	}
	for _, id := range ids {
		if err := a.expireInstallation(ctx, id); err != nil {
			return err
		}
	}
	return nil
}
func (a *App) expireInstallation(ctx context.Context, id int64) error {
	return a.expireInstallationWithStop(ctx, id, stopInstallationVM)
}
func stopInstallationVM(ctx context.Context, tenantID, projectID int64) error {
	vm, _, err := testVMCall(ctx, "GET", "/test-vm", nil)
	if err == nil {
		err = testVMOwner(vm, tenantID, projectID, true)
	}
	if err == nil {
		_, _, err = testVMCall(ctx, "POST", "/test-vm/abort", &vm.TestVMConfig)
	}
	return err
}
func (a *App) expireInstallationWithStop(ctx context.Context, id int64, stop func(context.Context, int64, int64) error) error {
	return a.store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var op operation
		if err := tx.Table("operations").Clauses(clause.Locking{Strength: "UPDATE"}).Where("id=?", id).Take(&op).Error; err != nil {
			return err
		}
		var inst installProgress
		if err := tx.Table("installations").Clauses(clause.Locking{Strength: "UPDATE"}).Where("operation_id=?", id).Take(&inst).Error; err != nil {
			return err
		}
		var steps []installStep
		if err := json.Unmarshal(inst.Progress, &steps); err != nil {
			return err
		}
		now := time.Now()
		message := "安装超过总时限（60 分钟），已终止任务"
		expired := inst.DeadlineAt != nil && !now.Before(*inst.DeadlineAt)
		for _, step := range steps {
			if step.Status == "running" && step.DeadlineAt != nil && !now.Before(*step.DeadlineAt) {
				expired = true
				message = fmt.Sprintf("%s超时（%d 秒）：未收到下一阶段确认，请检查任务事件与串口日志", step.Title, step.TimeoutSeconds)
				break
			}
		}
		terminal := inst.Status == "succeeded" || inst.Status == "failed" || inst.Status == "cancelled"
		if (!expired || terminal) && !inst.CleanupPending {
			return nil
		}
		if inst.CleanupPending {
			message = inst.Error
		}
		// Never operate a physical machine. Owner and identity are checked before abort.
		var vmAsset struct{ Manufacturer, Model, MACAddress string }
		if err := tx.Table("assets").Select("manufacturer,model,mac_address").Where("id=? AND tenant_id=? AND project_id=?", op.AssetID, op.TenantID, op.ProjectID).Take(&vmAsset).Error; err != nil {
			return err
		}
		stopped := false
		if isTestVMIdentity(vmAsset.Manufacturer, vmAsset.Model, vmAsset.MACAddress) {
			stopCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
			stopped = stop(stopCtx, op.TenantID, op.ProjectID) == nil
			cancel()
		}
		if stopped && inst.CleanupPending {
			message = strings.TrimSuffix(message, "；尚未确认 VM 停止，已阻止新安装，后台继续重试停止")
			if err := installEvent(tx, op, inst.ID, "cleanup/stopped", "cleanup", "已确认测试 VM 停止，解除任务占用"); err != nil {
				return err
			}
			if err := tx.Table("operations").Where("id=?", id).Update("error", message).Error; err != nil {
				return err
			}
		}
		if !stopped && !inst.CleanupPending {
			message += "；尚未确认 VM 停止，已阻止新安装，后台继续重试停止"
		}
		if !terminal {
			if err := finishInstallProgress(tx, op, "failed", message); err != nil {
				return err
			}
			if err := tx.Table("operations").Where("id=?", id).Updates(map[string]any{"status": "timeout", "started_at": gorm.Expr("COALESCE(started_at,now())"), "completed_at": now, "error": message}).Error; err != nil {
				return err
			}
		}
		if err := tx.Table("installations").Where("id=?", inst.ID).Updates(map[string]any{"status": "failed", "error": message, "cleanup_pending": !stopped, "callback_revoked_at": now, "started_at": gorm.Expr("COALESCE(started_at,now())"), "completed_at": gorm.Expr("COALESCE(completed_at,now())")}).Error; err != nil {
			return err
		}
		if stopped {
			if err := tx.Table("device_leases").Where("operation_id=?", id).Update("expires_at", now).Error; err != nil {
				return err
			}
			return tx.Table("assets").Where("id=? AND tenant_id=? AND project_id=?", op.AssetID, op.TenantID, op.ProjectID).Update("power_state", "off").Error
		}
		return nil
	})
}

// Token-scoped media requests provide real evidence, rather than a simulated progress timer.
func (a *App) observeInstallRequest(ctx context.Context, token, key, event, message string) error {
	var op operation
	if err := a.store.db.WithContext(ctx).Table("operations o").Select("o.*").Joins("JOIN installations i ON i.operation_id=o.id").Where("i.callback_token_hash=? AND i.deleted_at IS NULL AND i.callback_revoked_at IS NULL AND i.callback_expires_at>now() AND i.status IN ('booting','installing','post_install','unknown') AND o.status IN ('running','unknown')", serviceTokenHash(token)).Take(&op).Error; err != nil {
		return err
	}
	return a.store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Table("operations").Clauses(clause.Locking{Strength: "UPDATE"}).Where("id=?", op.ID).Take(&op).Error; err != nil {
			return err
		}
		var inst installProgress
		if err := tx.Table("installations").Clauses(clause.Locking{Strength: "UPDATE"}).Where("operation_id=? AND callback_revoked_at IS NULL AND callback_expires_at>now() AND status IN ('booting','installing','post_install','unknown')", op.ID).Take(&inst).Error; err != nil {
			return err
		}
		var steps []installStep
		if err := json.Unmarshal(inst.Progress, &steps); err != nil {
			return err
		}
		if installDeadlinePassed(inst, steps, time.Now()) {
			return gorm.ErrRecordNotFound
		}
		if key != "" {
			if err := recordInstallStep(tx, op, key, message); err != nil {
				return err
			}
		}
		if event == "ipxe_requested" {
			return nil
		}
		return installEvent(tx, op, inst.ID, event, key, message)
	})
}

// Keep credential hashing in one place, never put tokens in progress or logs.
func serviceTokenHash(token string) string { return fmt.Sprintf("%x", sha256New(token)) }

func installDeadlinePassed(inst installProgress, steps []installStep, now time.Time) bool {
	if inst.DeadlineAt != nil && !now.Before(*inst.DeadlineAt) {
		return true
	}
	for _, s := range steps {
		if s.Status == "running" && s.DeadlineAt != nil && !now.Before(*s.DeadlineAt) {
			return true
		}
	}
	return false
}

// PXE QEMU exits only when the installer has shut down for reboot (-no-reboot).
// Start a fresh QEMU with disk priority after the final configuration callback.
// This avoids relying on OVMF fallback order or leaving the VM in its boot menu.
func (a *App) resumeInstallDiskBoots(ctx context.Context) error {
	var ids []int64
	if err := a.store.db.WithContext(ctx).Table("installations").Select("operation_id").Where("deleted_at IS NULL AND status='post_install' AND callback_revoked_at IS NULL AND deadline_at>now() AND EXISTS (SELECT 1 FROM jsonb_array_elements(progress) s WHERE s->>'key'='first_boot' AND s->>'status'='running')").Limit(20).Scan(&ids).Error; err != nil {
		return err
	}
	for _, id := range ids {
		if err := a.resumeInstallDiskBoot(ctx, id, testVMCall); err != nil {
			return err
		}
	}
	return nil
}
func (a *App) resumeInstallDiskBoot(ctx context.Context, id int64, control func(context.Context, string, string, *pxeservice.TestVMConfig) (pxeservice.TestVMStatus, int, error)) error {
	return a.store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var op operation
		if err := tx.Table("operations").Clauses(clause.Locking{Strength: "UPDATE"}).Where("id=?", id).Take(&op).Error; err != nil {
			return err
		}
		if op.Status != "running" && op.Status != "unknown" {
			return nil
		}
		var inst installProgress
		if err := tx.Table("installations").Clauses(clause.Locking{Strength: "UPDATE"}).Where("operation_id=?", id).Take(&inst).Error; err != nil {
			return err
		}
		if inst.Status != "post_install" || inst.CleanupPending {
			return nil
		}
		var steps []installStep
		if err := json.Unmarshal(inst.Progress, &steps); err != nil {
			return err
		}
		ready := false
		for _, step := range steps {
			if step.Key == "first_boot" && step.Status == "running" {
				ready = true
			}
		}
		if !ready || installDeadlinePassed(inst, steps, time.Now()) {
			return nil
		}
		var asset struct{ Manufacturer, Model, MACAddress string }
		if err := tx.Table("assets").Select("manufacturer,model,mac_address").Where("id=? AND tenant_id=? AND project_id=?", op.AssetID, op.TenantID, op.ProjectID).Take(&asset).Error; err != nil {
			return err
		}
		if !isTestVMIdentity(asset.Manufacturer, asset.Model, asset.MACAddress) {
			return errors.New("only the fixed test VM can resume disk boot")
		}
		bootCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
		defer cancel()
		vm, _, err := control(bootCtx, "GET", "/test-vm", nil)
		if err != nil {
			return err
		}
		if err := testVMOwner(vm, op.TenantID, op.ProjectID, true); err != nil {
			return err
		}
		if vm.Running {
			return nil
		}
		result, _, err := control(bootCtx, "POST", "/test-vm/boot-disk", &vm.TestVMConfig)
		if err != nil {
			return err
		}
		if !result.Running {
			return errors.New("disk boot was not confirmed")
		}
		return installEvent(tx, op, inst.ID, "disk-boot/start", "first_boot", "安装器已退出，已启动磁盘中的 Ubuntu，等待系统验证回报")
	})
}
