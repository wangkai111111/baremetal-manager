package platform

import (
	"github.com/gin-gonic/gin"
	"strings"
)

func permissionResource(table string) string {
	switch table {
	case "operations", "operation_logs":
		return "tasks"
	case "sol_sessions":
		return "sol"
	case "networks", "ip_pools":
		return "network"
	case "images", "image_templates":
		return "images"
	case "kickstart_templates", "partition_templates", "post_install_scripts", "install_plans", "install_plan_scripts", "installations", "installation_events":
		return "install"
	case "firmware_packages", "firmware_baselines", "firmware_upgrades", "firmware_inventories":
		return "firmware"
	case "sensor_records", "asset_monitor_configs":
		return "monitor"
	case "alerts", "alert_rules", "alert_channels", "alert_rule_channels", "alert_events", "notification_deliveries", "maintenance_windows", "maintenance_window_assets":
		return "alerts"
	case "audits":
		return "audit"
	case "asset_tags", "asset_hardwares", "discovery_candidates":
		return "assets"
	// 组织与工单权限映射
	case "departments":
		return "departments"
	case "work_orders":
		return "work_orders"
	}
	return table
}

func (a *App) routes() *gin.Engine {
	r := gin.New()
	// New validated these values; tests/direct deployments default to no trusted proxies.
	if err := r.SetTrustedProxies(a.trustedProxies); err != nil {
		panic(err)
	}
	r.RemoteIPHeaders = []string{"X-Forwarded-For"}
	r.Use(gin.Recovery())
	r.GET("/health", func(c *gin.Context) { ok(c, gin.H{"status": "alive"}) })
	r.GET("/ready", a.readiness)
	r.GET("/health/ready", a.readiness)
	v := r.Group("/api/v1")
	v.POST("/auth/login", a.login)
	v.GET("/brand", a.brand)
	v.GET("/docs/openapi.json", a.openAPI)
	// 系统初始化设置状态接口（公开访问）
	v.GET("/setup/status", a.setupStatus)
	v.POST("/setup/config", a.setupConfig)
	// ============================================================
	// PXE 交付接口：正在网络引导的机器不持有任何凭据，必须在 auth guard 之前公开注册。
	// 磁盘侧文件位于 {cfg.PXE.TFTPRoot}，下发范围由处理器限定在 netboot/iso/nocloud 三个子目录。
	// ============================================================
	prov := v.Group("/provision")
	prov.GET("/netboot/*filepath", a.serveNetbootFile)
	prov.GET("/iso/*filepath", a.serveISOFile)
	prov.GET("/nocloud/:mac/:file", a.serveAutoinstall)
	prov.GET("/nocloud-token/:seedToken/:mac/:file", a.serveAutoinstall)
	prov.GET("/ipxe/:mac", a.serveIPXEScript)
	prov.GET("/media-token/:seedToken/:kind/:file", a.serveInstallationMedia)
	prov.POST("/install-callback/:token", a.installCallback)
	// 共享路径契约要求这些文件同时可直接以 {cfg.PXE.HTTPBase}/netboot|iso|nocloud 访问，
	// 因此在不加任何中间件的引擎根上再挂一组等价别名（iPXE 脚本里写死的就是这些短路径）。
	r.GET("/netboot/*filepath", a.serveNetbootFile)
	r.GET("/iso/*filepath", a.serveISOFile)
	r.GET("/nocloud/:mac/:file", a.serveAutoinstall)
	r.GET("/nocloud-token/:seedToken/:mac/:file", a.serveAutoinstall)
	auth := v.Group("")
	auth.Use(a.guard)
	auth.GET("/auth/me", a.authMe)
	auth.PUT("/auth/password", a.password)
	auth.GET("/tenants", a.tenants)
	auth.POST("/tenants", a.createTenant)
	auth.GET("/projects", a.projects)
	auth.POST("/projects", a.createProject)
	auth.POST("/auth/switch-project", a.switchProject)
	auth.GET("/pxe-services", a.pxeServices)
	auth.PUT("/pxe-services", a.pxeServices)
	auth.POST("/pxe-services/:service/:action", a.pxeServices)
	auth.GET("/settings", a.settings)
	auth.PUT("/settings", a.updateSettings)
	auth.POST("/settings/smtp-test", a.smtpTest)
	p := auth.Group("/projects/:projectId")
	p.Use(a.project)
	a.iamRoutes(auth, p)
	p.GET("/users/:id/access-grants", a.userAccessGrants)
	p.PUT("/users/:id/access-grants", a.userAccessGrants)
	p.GET("/dashboard/resources", a.require("assets:read"), a.resourceDashboard)
	p.GET("/dashboard/metrics/trend", a.require("monitor:read"), a.metricsTrend)
	p.POST("/switch", func(c *gin.Context) { ok(c, contextScope(c)) })
	p.POST("/assets/test-vm", a.testVM)
	p.GET("/assets/test-vm", a.testVM)
	p.DELETE("/assets/test-vm", a.testVM)
	p.POST("/assets/test-vm/:action", a.testVM)
	for _, res := range resources {
		resource := res
		code := permissionResource(res.table)
		p.GET("/"+res.path, a.require(code+":read"), a.listResource(resource))
		if res.path == "tasks" {
			p.GET("/tasks/:id", a.require("tasks:read"), a.getTask)
			p.GET("/tasks/:id/logs", a.require("tasks:read"), a.listTaskLogs)
		} else {
			p.GET("/"+res.path+"/:id", a.require(code+":read"), a.getResource(resource))
		}
		if !res.readonly {
			if res.table == "alert_channels" {
				p.POST("/"+res.path, a.require("alerts:write"), a.writeChannel(true))
			} else if res.table == "images" {
				p.POST("/"+res.path, a.require("images:write"), a.createImage)
			} else {
				p.POST("/"+res.path, a.require(code+":write"), a.writeResource(resource, true))
			}
			if res.table == "alert_channels" {
				p.PUT("/"+res.path+"/:id", a.require("alerts:write"), a.writeChannel(false))
				p.POST("/"+res.path+"/:id/test", a.require("alerts:write"), a.testChannel)
			} else {
				p.PUT("/"+res.path+"/:id", a.require(code+":write"), a.writeResource(resource, false))
			}
			p.DELETE("/"+res.path+"/:id", a.require(code+":write"), a.deleteResource(resource))
		}
	}

	// ============================================================
	// 部门管理自定义路由（自用模式）
	// ============================================================
	p.GET("/departments", a.require("departments:read"), a.listDepartments)
	p.POST("/departments", a.require("departments:write"), a.createDepartment)
	p.PUT("/departments/:id", a.require("departments:write"), a.updateDepartment)
	p.DELETE("/departments/:id", a.require("departments:write"), a.deleteDepartment)
	p.GET("/departments/:id/stats", a.require("departments:read"), a.getDepartmentStats)

	// ============================================================
	// 资产折旧与维保告警路由（自用模式）
	// ============================================================
	p.GET("/assets/depreciation", a.require("assets:read"), a.listAssetDepreciation)
	p.PUT("/assets/:id/depreciation", a.require("assets:write"), a.saveAssetDepreciation)
	p.PUT("/assets/:id/lifecycle", a.require("assets:write"), a.saveAssetLifecycle)
	p.GET("/assets/:id/depreciation", a.require("assets:read"), a.getAssetDepreciation)
	p.GET("/assets/warranty-alerts", a.require("assets:read"), a.listWarrantyAlerts)

	// ============================================================
	// 资产分配至部门路由（自用模式）
	// ============================================================
	p.POST("/assets/:id/assign", a.require("assets:write"), a.assignAssetToDepartment)
	p.POST("/assets/batch-assign", a.require("assets:write"), a.batchAssignAssets)

	// ============================================================
	// 工单管理路由
	// ============================================================
	p.GET("/work-orders", a.require("work_orders:read"), a.listWorkOrders)
	p.POST("/work-orders", a.require("work_orders:write"), a.createWorkOrder)
	p.GET("/work-orders/:id", a.require("work_orders:read"), a.getWorkOrder)
	p.PUT("/work-orders/:id", a.require("work_orders:write"), a.updateWorkOrder)
	p.PUT("/work-orders/:id/assign", a.require("work_orders:write"), a.assignWorkOrder)
	p.GET("/work-orders/:id/messages", a.require("work_orders:read"), a.listWorkOrderMessages)
	p.POST("/work-orders/:id/messages", a.require("work_orders:write"), a.createWorkOrderMessage)
	p.PUT("/work-orders/:id/:transition", a.require("work_orders:write"), a.transitionWorkOrder)

	p.GET("/images/catalog", a.require("images:read"), a.imageCatalog)
	p.POST("/images/catalog/:catalogId/download", a.require("images:write"), a.queueCatalogImage)
	p.GET("/images/:id/download", a.require("images:read"), a.downloadImage)
	p.POST("/alerts/:id/:transition", a.require("alerts:write"), a.alertTransition)
	p.POST("/assets/:id/actions", a.action)
	p.POST("/assets/batch-actions", a.batchActions)
	p.POST("/assets/:id/collect", a.require("assets:operate"), a.collect)
	p.GET("/assets/:id/collection", a.require("assets:read"), a.collection)
	p.GET("/assets/:id/sol", a.require("sol:access"), a.sol)
	p.POST("/assets/:id/sol-ticket", a.require("sol:access"), a.solTicket)
	p.GET("/assets/:id/initial-access", a.require("assets:operate"), a.initialAccess)
	p.POST("/assets/:id/install", a.require("install:operate"), a.installWithPreflight)
	p.POST("/assets/:id/firmware-upgrade", a.require("firmware:operate"), unsupported)
	p.POST("/installations", a.require("install:write"), a.installWithPreflight)
	p.POST("/firmware-upgrades", a.require("firmware:write"), unsupported)
	// 浏览器 WebSocket 使用短时一次性票据，不能将登录 JWT 放入 URL。
	v.GET("/sol/connect", a.solWithTicket)
	r.NoRoute(func(c *gin.Context) {
		if strings.HasPrefix(c.Request.URL.Path, "/api/") {
			fail(c, 404, "route not found")
			return
		}
		fail(c, 404, "not found")
	})
	return r
}
func unsupported(c *gin.Context) {
	fail(c, 501, "real installation and firmware execution are not supported")
}
