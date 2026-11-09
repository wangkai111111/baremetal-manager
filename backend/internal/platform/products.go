package platform

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ============================================================
// 产品管理处理器（运营模式）
// ============================================================
//
// 待注册路由（routes.go）：
// GET    /api/v1/projects/:projectId/products          → listProducts      → products:read
// POST   /api/v1/projects/:projectId/products          → createProduct     → products:write
// GET    /api/v1/projects/:projectId/products/:id      → getProduct        → products:read
// PUT    /api/v1/projects/:projectId/products/:id      → updateProduct     → products:write
// DELETE /api/v1/projects/:projectId/products/:id      → deleteProduct     → products:write

// listProducts 返回当前租户下的产品列表（租户级作用域，非项目级）
func (a *App) listProducts(c *gin.Context) {
	sc := contextScope(c)
	q := a.store.db.Table("products").
		Where("tenant_id=? AND deleted_at IS NULL", sc.Tenant)
	// 按名称搜索
	if search := strings.TrimSpace(c.Query("search")); search != "" {
		q = q.Where("name ILIKE ?", "%"+search+"%")
	}
	// 按状态筛选
	if status := strings.TrimSpace(c.Query("status")); status != "" {
		q = q.Where("status=?", status)
	}
	// 按计费周期筛选
	if cycle := strings.TrimSpace(c.Query("billing_cycle")); cycle != "" {
		q = q.Where("billing_cycle=?", cycle)
	}
	var total int64
	if q.Count(&total).Error != nil {
		fail(c, 500, "产品查询失败")
		return
	}
	p, n := page(c)
	var items []map[string]any
	if e := q.Select("id, tenant_id, name, description, spec_json, pricing_json, billing_cycle, status, created_at, updated_at").
		Order("id DESC").Limit(n).Offset((p - 1) * n).
		Find(&items).Error; e != nil {
		fail(c, 500, "产品查询失败")
		return
	}
	ok(c, gin.H{"items": items, "total": total, "page": p, "page_size": n})
}

// getProduct 获取单个产品详情
func (a *App) getProduct(c *gin.Context) {
	sc := contextScope(c)
	id := number(c.Param("id"))
	if id <= 0 {
		fail(c, 400, "无效的产品ID")
		return
	}
	var item map[string]any
	e := a.store.db.Table("products").
		Where("id=? AND tenant_id=? AND deleted_at IS NULL", id, sc.Tenant).
		Take(&item).Error
	if e != nil {
		fail(c, 404, "产品不存在")
		return
	}
	ok(c, item)
}

// createProduct 创建新产品
func (a *App) createProduct(c *gin.Context) {
	sc := contextScope(c)
	var in struct {
		Name         string `json:"name"`
		Description  string `json:"description"`
		SpecJSON     any    `json:"spec_json"`
		PricingJSON  any    `json:"pricing_json"`
		BillingCycle string `json:"billing_cycle"`
		Status       string `json:"status"`
	}
	if c.ShouldBindJSON(&in) != nil || strings.TrimSpace(in.Name) == "" {
		fail(c, 400, "产品名称不能为空")
		return
	}
	if len(in.Name) > 128 {
		fail(c, 400, "产品名称过长")
		return
	}
	// 校验计费周期
	validCycles := map[string]bool{"hourly": true, "daily": true, "monthly": true, "yearly": true}
	if in.BillingCycle != "" && !validCycles[in.BillingCycle] {
		fail(c, 400, "无效的计费周期，仅支持 hourly/daily/monthly/yearly")
		return
	}
	// 校验状态
	if in.Status == "" {
		in.Status = "active"
	}
	validStatuses := map[string]bool{"active": true, "inactive": true, "sold_out": true}
	if !validStatuses[in.Status] {
		fail(c, 400, "无效的产品状态，仅支持 active/inactive/sold_out")
		return
	}
	// 序列化 JSON 字段
	specJSON := marshalJSONOrEmpty(in.SpecJSON)
	pricingJSON := marshalJSONOrEmpty(in.PricingJSON)

	var id int64
	e := a.store.db.Transaction(func(tx *gorm.DB) error {
		row := map[string]any{
			"tenant_id":     sc.Tenant,
			"name":          strings.TrimSpace(in.Name),
			"description":   in.Description,
			"billing_cycle": in.BillingCycle,
			"status":        in.Status,
		}
		if specJSON != "" {
			row["spec_json"] = gorm.Expr("?::jsonb", specJSON)
		}
		if pricingJSON != "" {
			row["pricing_json"] = gorm.Expr("?::jsonb", pricingJSON)
		}
		if e := tx.Table("products").Clauses(clause.Returning{Columns: []clause.Column{{Name: "id"}}}).Create(&row).Error; e != nil {
			return e
		}
		id = asInt(row["id"])
		return a.store.audit(tx, sc, "create", "products", id)
	})
	if e != nil {
		fail(c, 500, "产品创建失败")
		return
	}
	ok(c, gin.H{"id": id})
}

// updateProduct 更新产品信息
func (a *App) updateProduct(c *gin.Context) {
	sc := contextScope(c)
	id := number(c.Param("id"))
	if id <= 0 {
		fail(c, 400, "无效的产品ID")
		return
	}
	// 验证产品存在
	var existing struct{ ID int64 }
	e := a.store.db.Table("products").
		Select("id").
		Where("id=? AND tenant_id=? AND deleted_at IS NULL", id, sc.Tenant).
		Take(&existing).Error
	if e != nil {
		fail(c, 404, "产品不存在")
		return
	}
	var in struct {
		Name         *string `json:"name"`
		Description  *string `json:"description"`
		SpecJSON     any     `json:"spec_json"`
		PricingJSON  any     `json:"pricing_json"`
		BillingCycle *string `json:"billing_cycle"`
		Status       *string `json:"status"`
	}
	if c.ShouldBindJSON(&in) != nil {
		fail(c, 400, "无效的请求体")
		return
	}
	updates := map[string]any{}
	if in.Name != nil {
		trimmed := strings.TrimSpace(*in.Name)
		if trimmed == "" {
			fail(c, 400, "产品名称不能为空")
			return
		}
		if len(trimmed) > 128 {
			fail(c, 400, "产品名称过长")
			return
		}
		updates["name"] = trimmed
	}
	if in.Description != nil {
		updates["description"] = *in.Description
	}
	if in.SpecJSON != nil {
		s := marshalJSONOrEmpty(in.SpecJSON)
		if s != "" {
			updates["spec_json"] = gorm.Expr("?::jsonb", s)
		}
	}
	if in.PricingJSON != nil {
		s := marshalJSONOrEmpty(in.PricingJSON)
		if s != "" {
			updates["pricing_json"] = gorm.Expr("?::jsonb", s)
		}
	}
	if in.BillingCycle != nil {
		validCycles := map[string]bool{"hourly": true, "daily": true, "monthly": true, "yearly": true}
		if *in.BillingCycle != "" && !validCycles[*in.BillingCycle] {
			fail(c, 400, "无效的计费周期")
			return
		}
		updates["billing_cycle"] = *in.BillingCycle
	}
	if in.Status != nil {
		validStatuses := map[string]bool{"active": true, "inactive": true, "sold_out": true}
		if !validStatuses[*in.Status] {
			fail(c, 400, "无效的产品状态")
			return
		}
		updates["status"] = *in.Status
	}
	if len(updates) == 0 {
		fail(c, 400, "无更新内容")
		return
	}
	e = a.store.db.Transaction(func(tx *gorm.DB) error {
		res := tx.Table("products").
			Where("id=? AND tenant_id=? AND deleted_at IS NULL", id, sc.Tenant).
			Updates(updates)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		return a.store.audit(tx, sc, "update", "products", id)
	})
	if e != nil {
		fail(c, 500, "产品更新失败")
		return
	}
	ok(c, nil)
}

// deleteProduct 软删除产品
func (a *App) deleteProduct(c *gin.Context) {
	sc := contextScope(c)
	id := number(c.Param("id"))
	if id <= 0 {
		fail(c, 400, "无效的产品ID")
		return
	}
	// 检查是否有关联订单
	var orderCount int64
	a.store.db.Table("orders").
		Where("product_id=? AND deleted_at IS NULL", id).
		Count(&orderCount)
	if orderCount > 0 {
		fail(c, 409, "产品存在关联订单，无法删除")
		return
	}
	e := a.store.db.Transaction(func(tx *gorm.DB) error {
		res := tx.Table("products").
			Where("id=? AND tenant_id=? AND deleted_at IS NULL", id, sc.Tenant).
			Updates(map[string]any{"deleted_at": time.Now()})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		return a.store.audit(tx, sc, "delete", "products", id)
	})
	if e != nil {
		fail(c, 500, "产品删除失败")
		return
	}
	ok(c, nil)
}

// marshalJSONOrEmpty 将任意值序列化为 JSON 字符串，nil 返回空字符串
func marshalJSONOrEmpty(v any) string {
	if v == nil {
		return ""
	}
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}
