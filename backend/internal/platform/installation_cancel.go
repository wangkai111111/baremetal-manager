package platform

import (
	"errors"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Physical machines have no managed power process. Keep their lease until an
// operator confirms they are stopped, including after a timeout or failed callback.
func (a *App) confirmPhysicalInstallStopped(c *gin.Context) {
	sc := contextScope(c)
	var in struct {
		OperationID int64 `json:"operation_id"`
		Stopped     bool  `json:"stopped"`
	}
	if c.ShouldBindJSON(&in) != nil || !in.Stopped || in.OperationID <= 0 {
		fail(c, 400, "请先手动关闭目标物理机，再确认停止本次装机任务")
		return
	}
	err := a.store.db.WithContext(c.Request.Context()).Transaction(func(tx *gorm.DB) error {
		var asset struct {
			ID                              int64
			Manufacturer, Model, MACAddress string
		}
		if err := queryScope(tx, resource{table: "assets"}, sc).Where("id=?", number(c.Param("id"))).Take(&asset).Error; err != nil {
			return err
		}
		if isTestVMIdentity(asset.Manufacturer, asset.Model, asset.MACAddress) {
			return errors.New("测试 VM 请在 PXE 实验室取消安装")
		}
		var op operation
		if err := tx.Table("operations").Clauses(clause.Locking{Strength: "UPDATE"}).Where("id=? AND asset_id=? AND tenant_id=? AND project_id=? AND kind='install' AND deleted_at IS NULL", in.OperationID, asset.ID, sc.Tenant, sc.Project).Take(&op).Error; err != nil {
			return err
		}
		var inst installProgress
		if err := tx.Table("installations").Clauses(clause.Locking{Strength: "UPDATE"}).Where("operation_id=? AND deleted_at IS NULL", op.ID).Take(&inst).Error; err != nil {
			return err
		}
		active := op.Status == "pending" || op.Status == "running" || op.Status == "unknown" || op.Status == "retry_wait"
		if !active && !inst.CleanupPending {
			return errors.New("此任务已结束且无需确认停止，请刷新状态")
		}
		now := time.Now()
		message := "操作人已确认物理机关闭；安装凭据已撤销，解除任务占用。已经写入的磁盘数据不会还原"
		updates := map[string]any{"cleanup_pending": false, "callback_revoked_at": now}
		if active {
			if err := finishInstallProgress(tx, op, "cancelled", message); err != nil {
				return err
			}
			updates["status"] = "cancelled"
			updates["started_at"] = gorm.Expr("COALESCE(started_at,now())")
			updates["completed_at"] = now
			if err := tx.Table("operations").Where("id=?", op.ID).Updates(map[string]any{"status": "cancelled", "started_at": gorm.Expr("COALESCE(started_at,now())"), "completed_at": now}).Error; err != nil {
				return err
			}
		}
		if err := tx.Table("installations").Where("id=?", inst.ID).Updates(updates).Error; err != nil {
			return err
		}
		if err := installEvent(tx, op, inst.ID, "physical/stopped", "cleanup", message); err != nil {
			return err
		}
		if err := tx.Table("device_leases").Where("operation_id=?", op.ID).Update("expires_at", now).Error; err != nil {
			return err
		}
		if err := tx.Table("assets").Where("id=? AND tenant_id=? AND project_id=?", asset.ID, sc.Tenant, sc.Project).Update("power_state", "off").Error; err != nil {
			return err
		}
		return a.store.recordAudit(tx, sc, requestMeta(c, sc.User), "cancel", "installations", inst.ID, "success")
	})
	if err != nil {
		fail(c, 409, err.Error())
		return
	}
	ok(c, gin.H{"stopped": true})
}
