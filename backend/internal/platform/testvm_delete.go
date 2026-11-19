package platform

import (
	"context"
	"errors"
	"net/http"
	"time"

	"baremetal-manager/backend/internal/pxeservice"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Keep creation and deletion serialized, then lock the project and asset in the
// same order as operation submission. Historical operations retain their asset.
func (a *App) deleteTestVM(ctx context.Context, sc scope, expectedAssetID int64, control func(context.Context, string, string, *pxeservice.TestVMConfig) (pxeservice.TestVMStatus, int, error)) error {
	return a.store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SELECT pg_advisory_xact_lock(684219734)").Error; err != nil {
			return err
		}
		if err := lockScope(tx, sc); err != nil {
			return err
		}
		var assets []struct {
			ID, TenantID, ProjectID int64
			Manufacturer, Model     string
		}
		if err := tx.Table("assets").Clauses(clause.Locking{Strength: "UPDATE"}).Select("id,tenant_id,project_id,manufacturer,model").Where("mac_address=? AND deleted_at IS NULL", pxeservice.TestVMMAC).Find(&assets).Error; err != nil {
			return err
		}
		if len(assets) > 1 {
			return errors.New("固定测试 MAC 对应多个资产，请先检查资产归属")
		}
		var assetID int64
		for _, asset := range assets {
			if asset.TenantID != sc.Tenant || asset.ProjectID != sc.Project || !isTestVMIdentity(asset.Manufacturer, asset.Model, pxeservice.TestVMMAC) {
				return errors.New("测试 VM 属于其他组织/项目或固定 MAC 被其他资产占用，禁止删除")
			}
			assetID = asset.ID
		}
		if assetID != 0 && assetID != expectedAssetID {
			return errors.New("测试 VM 已发生变化，请刷新后重新确认删除")
		}
		vm, _, err := control(ctx, http.MethodGet, "/test-vm", nil)
		if err != nil {
			return err
		}
		// An unclaimed controller is allowed so a lost response / failed database
		// commit can be retried. Creation uses the same advisory lock and checks
		// all live assets before claiming the fixed VM for another project.
		if err := testVMOwner(vm, sc.Tenant, sc.Project, false); err != nil {
			return err
		}
		if assetID != 0 {
			var count int64
			if err := tx.Table("installations").Where("asset_id=? AND deleted_at IS NULL AND (cleanup_pending OR status IN ('pending','preparing','booting','installing','post_install','unknown'))", assetID).Count(&count).Error; err != nil {
				return err
			}
			if count != 0 {
				return errors.New("测试 VM 正在安装或等待安装清理，请先取消安装并等待清理完成后再删除")
			}
			if err := tx.Table("operations").Where("asset_id=? AND deleted_at IS NULL AND status IN ('pending','running','retry_wait','unknown')", assetID).Count(&count).Error; err != nil {
				return err
			}
			if count != 0 {
				return errors.New("测试 VM 仍有未结束的任务，请等待任务结束后再删除")
			}
			if err := tx.Table("device_leases").Where("asset_id=? AND expires_at>now()", assetID).Count(&count).Error; err != nil {
				return err
			}
			if count != 0 {
				return errors.New("测试 VM 仍被任务占用，请稍后重试")
			}
			now := time.Now()
			if err := a.store.recordAuditNote(tx, sc, sc.Audit, "delete", "assets", assetID, "success", map[string]any{"message": "删除测试 VM、系统磁盘、固件状态及串口日志；保留安装历史和 PXE 资源"}); err != nil {
				return err
			}
			for _, table := range []string{"asset_tags", "asset_monitor_configs", "maintenance_window_assets"} {
				if err := tx.Table(table).Where("tenant_id=? AND project_id=? AND asset_id=? AND deleted_at IS NULL", sc.Tenant, sc.Project, assetID).Updates(map[string]any{"deleted_at": now, "update_by": sc.User}).Error; err != nil {
					return err
				}
			}
			if err := tx.Table("assets").Where("id=?", assetID).Updates(map[string]any{"deleted_at": now, "power_state": "off", "update_by": sc.User}).Error; err != nil {
				return err
			}
		}
		cfg := pxeservice.TestVMConfig{TenantID: sc.Tenant, ProjectID: sc.Project, Firmware: vm.Firmware}
		if cfg.Firmware == "" {
			cfg.Firmware = "bios"
		}
		// Do all database validation/writes before removing the disk. Any controller
		// failure rolls back the record changes; partial file cleanup is retryable.
		_, _, err = control(ctx, http.MethodDelete, "/test-vm", &cfg)
		return err
	})
}
