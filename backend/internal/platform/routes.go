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
	// 双模式新增表权限映射
	case "departments":
		return "departments"
	case "products":
		return "products"
	case "orders":
		return "orders"
	case "bills":
		return "finance"
	case "accounts", "payments":
		return "billing"
	case "invoices":
		return "finance"
	case "coupons":
		return "marketing"
	case "work_orders":
		return "work_orders"
	}
	return table
}

// resourceMode 返回资源所需的访问模式
// 返回空字符串表示两种模式都可访问
func resourceMode(table string) string {
	switch table {
	case "departments":
		return ModeSelfUse // 部门管理仅自用模式
	case "products", "orders", "bills", "accounts", "payments", "invoices", "coupons":
		return ModeExternalOperation // 产品/订单/账单/账户/支付/发票/优惠券仅运营模式
	}
	return "" // 两种模式都可访问
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
	// 系统初始化设置状态接口（公开访问）
	v.GET("/setup/status", a.setupStatus)
	// 租户自助注册接口（公开访问，无需认证）
	v.POST("/auth/register", a.registerTenant)
	auth := v.Group("")
	auth.Use(a.guard)
	// 系统初始化设置接口（需要管理员认证）
	auth.POST("/setup/mode", a.setupMode)
	auth.POST("/setup/config", a.setupConfig)
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
		// 根据资源模式要求添加中间件
		modeMiddleware := a.getModeMiddleware(res.table)
		p.GET("/"+res.path, append(modeMiddleware, a.require(code+":read"), a.listResource(resource))...)
		p.GET("/"+res.path+"/:id", append(modeMiddleware, a.require(code+":read"), a.getResource(resource))...)
		if !res.readonly {
			if res.table == "images" {
				p.POST("/"+res.path, append(modeMiddleware, a.require("images:write"), a.createImage)...)
			} else {
				p.POST("/"+res.path, append(modeMiddleware, a.require(code+":write"), a.writeResource(resource, true))...)
			}
			p.PUT("/"+res.path+"/:id", append(modeMiddleware, a.require(code+":write"), a.writeResource(resource, false))...)
			p.DELETE("/"+res.path+"/:id", append(modeMiddleware, a.require(code+":write"), a.deleteResource(resource))...)
		}
	}

	// ============================================================
	// 部门管理自定义路由（自用模式）
	// ============================================================
	deptSelfUse := []gin.HandlerFunc{a.requireSelfUse()}
	p.GET("/departments/:id/stats", append(deptSelfUse, a.require("departments:read"), a.getDepartmentStats)...)

	// ============================================================
	// 资产折旧与维保告警路由（自用模式）
	// ============================================================
	p.GET("/assets/:id/depreciation", append(deptSelfUse, a.require("assets:read"), a.getAssetDepreciation)...)
	p.GET("/assets/warranty-alerts", append(deptSelfUse, a.require("assets:read"), a.listWarrantyAlerts)...)

	// ============================================================
	// 资产分配至部门路由（自用模式）
	// ============================================================
	p.POST("/assets/:id/assign", append(deptSelfUse, a.require("assets:write"), a.assignAssetToDepartment)...)
	p.POST("/assets/batch-assign", append(deptSelfUse, a.require("assets:write"), a.batchAssignAssets)...)

	// ============================================================
	// 产品管理路由（运营模式，租户级作用域）
	// ============================================================
	externalOp := []gin.HandlerFunc{a.requireExternalOperation()}
	p.GET("/products", append(externalOp, a.require("products:read"), a.listProducts)...)
	p.POST("/products", append(externalOp, a.require("products:write"), a.createProduct)...)
	p.GET("/products/:id", append(externalOp, a.require("products:read"), a.getProduct)...)
	p.PUT("/products/:id", append(externalOp, a.require("products:write"), a.updateProduct)...)
	p.DELETE("/products/:id", append(externalOp, a.require("products:write"), a.deleteProduct)...)

	// ============================================================
	// 订单管理路由（运营模式，租户级作用域）
	// ============================================================
	p.GET("/orders", append(externalOp, a.require("orders:read"), a.listOrders)...)
	p.POST("/orders", append(externalOp, a.require("orders:write"), a.createOrder)...)
	p.GET("/orders/:id", append(externalOp, a.require("orders:read"), a.getOrder)...)
	p.PUT("/orders/:id/:transition", append(externalOp, a.require("orders:write"), a.transitionOrder)...)
	p.GET("/orders/:id/export", append(externalOp, a.require("orders:read"), a.exportOrder)...)

	// ============================================================
	// 账户与支付管理路由（运营模式）
	// ============================================================
	p.GET("/accounts", append(externalOp, a.require("finance:read"), a.getAccount)...)
	p.POST("/accounts/recharge", append(externalOp, a.require("finance:write"), a.rechargeAccount)...)
	p.GET("/payments", append(externalOp, a.require("finance:read"), a.listPayments)...)
	p.POST("/payments", append(externalOp, a.require("finance:write"), a.createPayment)...)

	// ============================================================
	// 账单管理路由（运营模式）
	// ============================================================
	p.GET("/bills", append(externalOp, a.require("billing:read"), a.listBills)...)
	p.GET("/bills/:id", append(externalOp, a.require("billing:read"), a.getBill)...)
	p.GET("/bills/:id/export", append(externalOp, a.require("billing:read"), a.exportBill)...)
	p.POST("/bills/generate", append(externalOp, a.require("billing:write"), a.generateBills)...)

	// ============================================================
	// 发票管理路由（运营模式）
	// ============================================================
	p.GET("/invoices", append(externalOp, a.require("finance:read"), a.listInvoices)...)
	p.POST("/invoices", append(externalOp, a.require("finance:write"), a.createInvoice)...)
	p.PUT("/invoices/:id/:status", append(externalOp, a.require("finance:write"), a.updateInvoice)...)

	// ============================================================
	// 优惠券管理路由（运营模式）
	// ============================================================
	p.GET("/coupons", append(externalOp, a.require("marketing:read"), a.listCoupons)...)
	p.POST("/coupons", append(externalOp, a.require("marketing:write"), a.createCoupon)...)
	p.PUT("/coupons/:id", append(externalOp, a.require("marketing:write"), a.updateCoupon)...)
	p.DELETE("/coupons/:id", append(externalOp, a.require("marketing:write"), a.deleteCoupon)...)
	p.POST("/coupons/:code/redeem", append(externalOp, a.require("orders:write"), a.redeemCoupon)...)

	// ============================================================
	// 工单管理路由（两种模式通用）
	// ============================================================
	p.GET("/work-orders", a.require("work_orders:read"), a.listWorkOrders)
	p.POST("/work-orders", a.require("work_orders:write"), a.createWorkOrder)
	p.GET("/work-orders/:id", a.require("work_orders:read"), a.getWorkOrder)
	p.PUT("/work-orders/:id", a.require("work_orders:write"), a.updateWorkOrder)
	p.PUT("/work-orders/:id/:transition", a.require("work_orders:write"), a.transitionWorkOrder)

	// ============================================================
	// 运营看板路由（运营模式，管理员专用）
	// ============================================================
	p.GET("/admin/operations-dashboard", append(externalOp, a.require("operations:read"), a.getOperationsDashboard)...)

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
