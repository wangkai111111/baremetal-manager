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
	case "networks", "ip_pools", "ip_allocations", "pxe_configs":
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
	case "rack_labels":
		return "racks"
	}
	return table
}
func (a *App) routes() *gin.Engine {
	r := gin.New()
	r.Use(gin.Recovery())
	r.GET("/health", func(c *gin.Context) { ok(c, gin.H{"status": "alive"}) })
	r.GET("/ready", a.readiness)
	r.GET("/health/ready", a.readiness)
	v := r.Group("/api/v1")
	v.POST("/auth/login", a.login)
	v.GET("/brand", a.brand)
	v.GET("/docs/openapi.json", a.openAPI)
	auth := v.Group("")
	auth.Use(a.guard)
	auth.GET("/auth/me", func(c *gin.Context) { ok(c, current(c)) })
	auth.PUT("/auth/password", a.password)
	auth.GET("/tenants", a.tenants)
	auth.POST("/tenants", a.createTenant)
	auth.GET("/projects", a.projects)
	auth.POST("/projects", a.createProject)
	auth.POST("/auth/switch-project", a.switchProject)
	auth.GET("/settings", a.settings)
	auth.PUT("/settings", a.updateSettings)
	auth.POST("/settings/smtp-test", a.smtpTest)
	p := auth.Group("/projects/:projectId")
	p.Use(a.project)
	a.iamRoutes(auth, p)
	p.POST("/switch", func(c *gin.Context) { ok(c, contextScope(c)) })
	for _, res := range resources {
		resource := res
		code := permissionResource(res.table)
		p.GET("/"+res.path, a.require(code+":read"), a.listResource(resource))
		p.GET("/"+res.path+"/:id", a.require(code+":read"), a.getResource(resource))
		if !res.readonly {
			if res.table == "images" {
				p.POST("/"+res.path, a.require("images:write"), a.createImage)
			} else {
				p.POST("/"+res.path, a.require(code+":write"), a.writeResource(resource, true))
			}
			p.PUT("/"+res.path+"/:id", a.require(code+":write"), a.writeResource(resource, false))
			p.DELETE("/"+res.path+"/:id", a.require(code+":write"), a.deleteResource(resource))
		}
	}
	p.GET("/images/:id/download", a.require("images:read"), a.downloadImage)
	p.POST("/alerts/:id/:transition", a.require("alerts:write"), a.alertTransition)
	p.POST("/assets/:id/actions", a.action)
	p.POST("/assets/:id/collect", a.require("assets:operate"), a.collect)
	p.GET("/assets/:id/sol", a.require("sol:access"), a.sol)
	p.POST("/assets/:id/sol-ticket", a.require("sol:access"), a.solTicket)
	p.POST("/assets/:id/install", a.require("install:operate"), unsupported)
	p.POST("/assets/:id/firmware-upgrade", a.require("firmware:operate"), unsupported)
	p.POST("/installations", a.require("install:write"), unsupported)
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
