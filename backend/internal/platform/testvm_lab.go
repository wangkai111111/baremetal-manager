package platform

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"baremetal-manager/backend/internal/pxeservice"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (a *App) cancelTestVMInstallation(ctx context.Context, sc scope, vm pxeservice.TestVMStatus) error {
	return a.store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var row struct{ ID, OperationID, AssetID int64 }
		if err := tx.Table("installations i").Select("i.id,i.operation_id,i.asset_id").Joins("JOIN assets a ON a.id=i.asset_id AND a.tenant_id=i.tenant_id AND a.project_id=i.project_id").Where("i.tenant_id=? AND i.project_id=? AND i.deleted_at IS NULL AND i.status IN ('pending','preparing','booting','installing','post_install','unknown') AND a.manufacturer='BMM Test VM' AND a.model='QEMU PXE Lab' AND a.mac_address=? AND a.deleted_at IS NULL", sc.Tenant, sc.Project, pxeservice.TestVMMAC).Order("i.id DESC").Take(&row).Error; err != nil {
			return errors.New("没有可取消的测试安装任务，请刷新状态")
		}
		var op operation
		if err := tx.Table("operations").Clauses(clause.Locking{Strength: "UPDATE"}).Where("id=? AND kind='install' AND status IN ('pending','running','retry_wait','unknown')", row.OperationID).Take(&op).Error; err != nil {
			return errors.New("任务状态已变化，请刷新后重试")
		}
		var inst struct{ Status string }
		if err := tx.Table("installations").Select("status").Clauses(clause.Locking{Strength: "UPDATE"}).Where("id=? AND status IN ('pending','preparing','booting','installing','post_install','unknown')", row.ID).Take(&inst).Error; err != nil {
			return errors.New("正在准备任务或状态已变化，请稍后重试")
		}
		if _, _, err := testVMCall(ctx, "POST", "/test-vm/abort", &vm.TestVMConfig); err != nil {
			return err
		}
		now := time.Now()
		if err := finishInstallProgress(tx, op, "cancelled", "管理员已停止测试 VM 并取消安装"); err != nil {
			return err
		}
		if err := tx.Table("installations").Where("id=?", row.ID).Updates(map[string]any{"status": "cancelled", "started_at": gorm.Expr("COALESCE(started_at,now())"), "cleanup_pending": false, "callback_revoked_at": now, "completed_at": now, "error": "管理员已停止测试 VM 并取消安装"}).Error; err != nil {
			return err
		}
		if err := tx.Table("operations").Where("id=?", row.OperationID).Updates(map[string]any{"status": "cancelled", "started_at": gorm.Expr("COALESCE(started_at,now())"), "completed_at": now}).Error; err != nil {
			return err
		}
		if err := tx.Table("device_leases").Where("operation_id=?", row.OperationID).Update("expires_at", now).Error; err != nil {
			return err
		}
		if err := tx.Table("assets").Where("id=? AND tenant_id=? AND project_id=?", row.AssetID, sc.Tenant, sc.Project).Update("power_state", "off").Error; err != nil {
			return err
		}
		return a.store.audit(tx, sc, "cancel", "installations", row.ID)
	})
}

func (a *App) testVMLabStatus(c *gin.Context, vm pxeservice.TestVMStatus, sc scope) {
	var assetID, planID int64
	db := a.store.db.WithContext(c.Request.Context())
	if err := db.Table("assets").Select("id").Where("tenant_id=? AND project_id=? AND manufacturer='BMM Test VM' AND model='QEMU PXE Lab' AND mac_address=? AND deleted_at IS NULL", sc.Tenant, sc.Project, pxeservice.TestVMMAC).Scan(&assetID).Error; err != nil {
		fail(c, 500, "测试资产状态读取失败")
		return
	}
	if err := db.Table("install_plans").Select("id").Where("tenant_id=? AND project_id=? AND name=? AND deleted_at IS NULL", sc.Tenant, sc.Project, testVMInstallName).Scan(&planID).Error; err != nil {
		fail(c, 500, "安装方案读取失败")
		return
	}
	var installation installProgress
	if assetID != 0 {
		if err := db.Table("installations").Select("id,operation_id,status,COALESCE(error,'') AS error,progress,deadline_at,started_at,completed_at,cleanup_pending").Where("tenant_id=? AND project_id=? AND asset_id=? AND deleted_at IS NULL", sc.Tenant, sc.Project, assetID).Order("id DESC").Limit(1).Scan(&installation).Error; err != nil {
			fail(c, 500, "安装状态读取失败")
			return
		}
	}
	var images []installationMedia
	if err := queryScope(db, resource{table: "images"}, sc).Order("id DESC").Find(&images).Error; err != nil {
		fail(c, 500, "可选镜像读取失败")
		return
	}
	imageOptions := make([]gin.H, 0, len(images))
	for _, image := range images {
		reason := image.unavailableReason()
		imageOptions = append(imageOptions, gin.H{"id": image.ID, "name": image.Name, "version": image.Version, "architecture": image.Architecture, "disabled": reason != "", "reason": reason})
	}
	data, status, err := pxeservice.Call(c.Request.Context(), http.MethodGet, "/status", nil)
	var network pxeservice.Status
	networkKnown := err == nil && status == 200 && json.Unmarshal(data, &network) == nil
	ready := networkKnown && network.DHCP && network.TFTP && network.Config.TenantID == sc.Tenant && network.Config.ProjectID == sc.Project && network.Config.Interface == pxeservice.TestVMBridge && network.Config.Subnet == "192.168.77.0/24" && network.Config.Server == "192.168.77.1"
	ok(c, struct {
		pxeservice.TestVMStatus
		Images       []gin.H         `json:"images"`
		AssetID      int64           `json:"asset_id"`
		PlanID       int64           `json:"plan_id"`
		NetworkKnown bool            `json:"network_known"`
		NetworkReady bool            `json:"network_ready"`
		Installation installProgress `json:"installation"`
	}{vm, imageOptions, assetID, planID, networkKnown, ready, installation})
}

// Prepare only the fixed lab bridge; never replace a running service on another network.
func (a *App) prepareTestVMNetwork(ctx context.Context, sc scope) error {
	var ids testVMRecords
	db := a.store.db.WithContext(ctx)
	if err := db.Table("networks").Select("id").Where("tenant_id=? AND project_id=? AND name='BMM Test VM' AND subnet='192.168.77.0/24'::cidr AND deleted_at IS NULL", sc.Tenant, sc.Project).Scan(&ids.NetworkID).Error; err != nil {
		return err
	}
	if err := db.Table("ip_pools").Select("id").Where("tenant_id=? AND project_id=? AND network_id=? AND name='BMM Test VM' AND deleted_at IS NULL", sc.Tenant, sc.Project, ids.NetworkID).Scan(&ids.PoolID).Error; err != nil {
		return err
	}
	if ids.NetworkID == 0 || ids.PoolID == 0 {
		return errors.New("请先创建测试 VM 及其装机网络")
	}
	cfg := pxeservice.Config{TenantID: sc.Tenant, ProjectID: sc.Project, NetworkID: ids.NetworkID, PoolID: ids.PoolID, Interface: pxeservice.TestVMBridge, Subnet: "192.168.77.0/24", Start: "192.168.77.100", End: "192.168.77.200", Server: "192.168.77.1", Root: "/var/lib/tftpboot", BIOS: "undionly.kpxe", EFI: "ipxe.efi"}
	if err := a.validatePXEBinding(ctx, &cfg); err != nil {
		return err
	}
	data, status, err := pxeservice.Call(ctx, "GET", "/status", nil)
	var current pxeservice.Status
	if err != nil || status != 200 || json.Unmarshal(data, &current) != nil {
		return errors.New("无法读取实际 PXE 服务状态")
	}
	if current.Config != cfg {
		if current.DHCP || current.TFTP {
			return errors.New("PXE 正在使用其他配置，请先在 PXE 配置页确认并停止服务")
		}
		body, _ := json.Marshal(cfg)
		data, status, err = pxeservice.Call(ctx, "PUT", "/config", body)
		if err != nil {
			return err
		}
		if status != 200 {
			return fmt.Errorf("PXE 配置失败: %s", data)
		}
	}
	for _, name := range []string{"tftp", "dhcp"} {
		data, status, err = pxeservice.Call(ctx, "POST", "/"+name+"/start", nil)
		if err != nil {
			return err
		}
		if status != 200 {
			return fmt.Errorf("%s 启动失败: %s", name, data)
		}
	}
	return nil
}
