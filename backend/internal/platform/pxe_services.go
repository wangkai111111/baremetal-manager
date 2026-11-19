package platform

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"baremetal-manager/backend/internal/pxeservice"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// RunPXEServices runs only the fixed dnsmasq supervisor, never the application API.
func RunPXEServices(ctx context.Context, db *gorm.DB) error {
	return pxeservice.Run(ctx, func(ctx context.Context) (bool, error) {
		var count int64
		e := db.WithContext(ctx).Table("installations").Where("deleted_at IS NULL AND (cleanup_pending OR status IN ('pending','preparing','booting','installing','post_install','unknown'))").Count(&count).Error
		if e != nil || count > 0 {
			return count > 0, e
		}
		e = db.WithContext(ctx).Table("operations").Where("deleted_at IS NULL AND kind='install' AND status IN ('pending','running','retry_wait','unknown')").Count(&count).Error
		return count > 0, e
	})
}
func (a *App) validatePXEBinding(ctx context.Context, cfg *pxeservice.Config) error {
	var row struct {
		Subnet, Start, End string
		DHCPEnabled        bool
	}
	result := a.store.db.WithContext(ctx).Table("networks n").Select("n.subnet::text AS subnet,n.dhcp_enabled,host(p.start_address) AS start,host(p.end_address) AS end").Joins("JOIN ip_pools p ON p.network_id=n.id AND p.tenant_id=n.tenant_id AND p.project_id=n.project_id AND p.deleted_at IS NULL").Where("n.id=? AND n.tenant_id=? AND n.project_id=? AND p.id=? AND n.deleted_at IS NULL", cfg.NetworkID, cfg.TenantID, cfg.ProjectID, cfg.PoolID).Take(&row)
	if result.Error != nil {
		return errors.New("网络与地址池必须属于明确的同一租户/项目")
	}
	if !row.DHCPEnabled {
		return errors.New("所选网络尚未允许 DHCP（此标记不会自动启动服务）")
	}
	cfg.Subnet = row.Subnet
	cfg.Start = row.Start
	cfg.End = row.End
	return cfg.Validate("dhcp", false)
}
func (a *App) pxeServices(c *gin.Context) {
	if !current(c).IsPlatformAdmin {
		fail(c, 403, "platform administrator required")
		return
	}
	path := "/status"
	method := c.Request.Method
	var body []byte
	if method == "PUT" {
		var cfg pxeservice.Config
		if c.ShouldBindJSON(&cfg) != nil {
			fail(c, 400, "invalid configuration")
			return
		}
		var validation error
		if cfg.NetworkID == 0 && cfg.PoolID == 0 && cfg.TenantID == 0 && cfg.ProjectID == 0 {
			validation = cfg.Validate("tftp", false)
		} else {
			validation = a.validatePXEBinding(c.Request.Context(), &cfg)
		}
		if validation != nil {
			fail(c, 400, validation.Error())
			return
		}
		body, _ = json.Marshal(cfg)
		path = "/config"
	} else if method == "POST" {
		service, action := c.Param("service"), c.Param("action")
		if (service != "dhcp" && service != "tftp") || (action != "start" && action != "stop") {
			fail(c, 400, "unsupported service action")
			return
		}
		if action == "start" {
			data, status, e := pxeservice.Call(c.Request.Context(), "GET", "/status", nil)
			var state pxeservice.Status
			if e != nil || status != 200 || json.Unmarshal(data, &state) != nil {
				fail(c, 503, "无法读取实际服务状态")
				return
			}
			original := state.Config
			if service == "dhcp" {
				e = a.validatePXEBinding(c.Request.Context(), &state.Config)
			} else {
				e = state.Config.Validate("tftp", false)
			}
			if e != nil {
				fail(c, 409, e.Error())
				return
			}
			if state.Config != original {
				fail(c, 409, "网络或地址池已变化，请停止服务并重新保存全局配置")
				return
			}
		}
		path = "/" + service + "/" + action
	}
	data, status, e := pxeservice.Call(c.Request.Context(), method, path, body)
	if e != nil {
		fail(c, 503, e.Error())
		return
	}
	if status != 200 {
		fail(c, status, strings.TrimSpace(string(data)))
		return
	}
	var value any
	if json.Unmarshal(data, &value) != nil {
		fail(c, 502, "invalid controller response")
		return
	}
	ok(c, value)
}
func (a *App) installWithPreflight(c *gin.Context) {
	sc := contextScope(c)
	if !a.store.allowed(sc, "assets:operate") {
		fail(c, 403, "permission denied")
		return
	}
	var in struct {
		ImageID       int64               `json:"image_id"`
		InstallPlanID int64               `json:"install_plan_id"`
		Confirmed     bool                `json:"confirmed"`
		Access        *installAccessInput `json:"access"`
	}
	if c.ShouldBindJSON(&in) != nil || in.InstallPlanID <= 0 || !in.Confirmed || number(c.Param("id")) <= 0 {
		fail(c, 400, "必须指定 install_plan_id 并以 confirmed:true 明确授权清空测试虚机磁盘")
		return
	}
	if err := in.Access.validate(); err != nil {
		fail(c, 400, err.Error())
		return
	}
	var asset int64
	if c.Param("id") != "" {
		if e := a.store.db.Table("assets").Select("id").Where("id=? AND tenant_id=? AND project_id=? AND deleted_at IS NULL", number(c.Param("id")), sc.Tenant, sc.Project).Scan(&asset).Error; e != nil || asset == 0 {
			fail(c, 404, "asset not found")
			return
		}
	}
	data, status, e := pxeservice.Call(c.Request.Context(), "GET", "/status", nil)
	var state pxeservice.Status
	if e != nil || status != 200 || json.Unmarshal(data, &state) != nil {
		fail(c, 409, "PXE 预检失败：控制服务不可达，实际状态未知")
		return
	}
	if state.Config.TenantID != sc.Tenant || state.Config.ProjectID != sc.Project {
		fail(c, 409, "PXE 预检失败：全局服务未绑定当前项目")
		return
	}
	if !state.DHCP || !state.TFTP {
		fail(c, 409, "PXE 预检失败：DHCP 与 TFTP 必须均实际运行")
		return
	}
	original := state.Config
	if e = a.validatePXEBinding(c.Request.Context(), &state.Config); e != nil || state.Config != original {
		fail(c, 409, "PXE 预检失败：网络/地址池配置无效或已经变化")
		return
	}
	// The controller checks both boot files at every start; recheck the mounted root before install.
	if e = state.Config.BootFiles(); e != nil {
		fail(c, 409, "PXE 预检失败："+e.Error())
		return
	}
	id, opID, err := a.enqueueInstallation(c.Request.Context(), sc, asset, in.InstallPlanID, state.Config.NetworkID, state.Config.Root, in.Access, requestMeta(c, sc.User), in.ImageID)
	if err != nil {
		fail(c, 409, err.Error())
		return
	}
	c.JSON(202, gin.H{"code": 0, "data": gin.H{"installation_id": id, "operation_id": opID, "status": "pending"}, "message": "已授权测试虚机清盘，等待 PXE 安装回调"})
}
