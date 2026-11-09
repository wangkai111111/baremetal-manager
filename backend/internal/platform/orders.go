package platform

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ============================================================
// 订单管理处理器（运营模式）
// ============================================================
//
// 待注册路由（routes.go）：
// GET    /api/v1/projects/:projectId/orders                    → listOrders        → orders:read
// POST   /api/v1/projects/:projectId/orders                    → createOrder       → orders:write
// GET    /api/v1/projects/:projectId/orders/:id                → getOrder          → orders:read
// PUT    /api/v1/projects/:projectId/orders/:id/:transition    → transitionOrder   → orders:write
// GET    /api/v1/projects/:projectId/orders/:id/export         → exportOrder       → orders:read

// 订单状态流转规则
var orderTransitions = map[string]map[string]bool{
	"pending":    {"paid": true, "cancelled": true},
	"paid":       {"processing": true, "refunded": true},
	"processing": {"completed": true},
}

// 合法订单类型
var validOrderTypes = map[string]bool{"purchase": true, "renew": true, "upgrade": true, "refund": true}

// listOrders 返回当前租户下的订单列表
func (a *App) listOrders(c *gin.Context) {
	sc := contextScope(c)
	q := a.store.db.Table("orders").
		Where("tenant_id=? AND deleted_at IS NULL", sc.Tenant)
	// 按状态筛选
	if status := strings.TrimSpace(c.Query("status")); status != "" {
		q = q.Where("status=?", status)
	}
	// 按类型筛选
	if orderType := strings.TrimSpace(c.Query("type")); orderType != "" {
		q = q.Where("type=?", orderType)
	}
	// 按产品筛选
	if productID := number(c.Query("product_id")); productID > 0 {
		q = q.Where("product_id=?", productID)
	}
	var total int64
	if q.Count(&total).Error != nil {
		fail(c, 500, "订单查询失败")
		return
	}
	p, n := page(c)
	var items []map[string]any
	if e := q.Select("id, tenant_id, user_id, type, product_id, asset_id, amount, discount_amount, final_amount, status, paid_at, completed_at, created_at, updated_at").
		Order("id DESC").Limit(n).Offset((p - 1) * n).
		Find(&items).Error; e != nil {
		fail(c, 500, "订单查询失败")
		return
	}
	ok(c, gin.H{"items": items, "total": total, "page": p, "page_size": n})
}

// getOrder 获取单个订单详情
func (a *App) getOrder(c *gin.Context) {
	sc := contextScope(c)
	id := number(c.Param("id"))
	if id <= 0 {
		fail(c, 400, "无效的订单ID")
		return
	}
	var item map[string]any
	e := a.store.db.Table("orders").
		Where("id=? AND tenant_id=? AND deleted_at IS NULL", id, sc.Tenant).
		Take(&item).Error
	if e != nil {
		fail(c, 404, "订单不存在")
		return
	}
	ok(c, item)
}

// createOrder 创建新订单
func (a *App) createOrder(c *gin.Context) {
	sc := contextScope(c)
	u := current(c)
	var in struct {
		Type          string  `json:"type"`
		ProductID     *int64  `json:"product_id"`
		AssetID       *int64  `json:"asset_id"`
		Amount        float64 `json:"amount"`
		DiscountAmount float64 `json:"discount_amount"`
		CouponCode    string  `json:"coupon_code"`
	}
	if c.ShouldBindJSON(&in) != nil || !validOrderTypes[in.Type] {
		fail(c, 400, "请提供有效的订单类型（purchase/renew/upgrade/refund）")
		return
	}
	// 如果指定了产品，验证其存在并计算金额
	var productPricing float64
	if in.ProductID != nil && *in.ProductID > 0 {
		var product struct {
			ID          int64
			PricingJSON string `gorm:"column:pricing_json"`
			Status      string
		}
		e := a.store.db.Table("products").
			Select("id, pricing_json, status").
			Where("id=? AND tenant_id=? AND deleted_at IS NULL", *in.ProductID, sc.Tenant).
			Take(&product).Error
		if e != nil {
			fail(c, 404, "产品不存在")
			return
		}
		if product.Status != "active" {
			fail(c, 400, "产品当前不可购买")
			return
		}
		// 从产品定价 JSON 中提取基础价格
		if product.PricingJSON != "" {
			var pricing map[string]any
			if json.Unmarshal([]byte(product.PricingJSON), &pricing) == nil {
				if basePrice, ok := pricing["base_price"].(float64); ok {
					productPricing = basePrice
				}
			}
		}
	}
	// 计算金额：如果未传入 amount，则使用产品定价
	amount := in.Amount
	if amount == 0 && productPricing > 0 {
		amount = productPricing
	}
	discountAmount := in.DiscountAmount
	// 处理优惠券折扣
	if in.CouponCode != "" {
		discount, err := a.applyCoupon(sc.Tenant, in.CouponCode, amount)
		if err != nil {
			fail(c, 400, err.Error())
			return
		}
		discountAmount += discount
	}
	finalAmount := amount - discountAmount
	if finalAmount < 0 {
		finalAmount = 0
	}
	var id int64
	e := a.store.db.Transaction(func(tx *gorm.DB) error {
		row := map[string]any{
			"tenant_id":       sc.Tenant,
			"user_id":         u.ID,
			"type":            in.Type,
			"amount":          amount,
			"discount_amount": discountAmount,
			"final_amount":    finalAmount,
			"status":          "pending",
		}
		if in.ProductID != nil && *in.ProductID > 0 {
			row["product_id"] = *in.ProductID
		}
		if in.AssetID != nil && *in.AssetID > 0 {
			row["asset_id"] = *in.AssetID
		}
		if e := tx.Table("orders").Clauses(clause.Returning{Columns: []clause.Column{{Name: "id"}}}).Create(&row).Error; e != nil {
			return e
		}
		id = asInt(row["id"])
		return a.store.audit(tx, sc, "create", "orders", id)
	})
	if e != nil {
		fail(c, 500, "订单创建失败")
		return
	}
	ok(c, gin.H{"id": id, "amount": amount, "discount_amount": discountAmount, "final_amount": finalAmount})
}

// applyCoupon 验证并应用优惠券，返回折扣金额
func (a *App) applyCoupon(tenantID int64, code string, amount float64) (float64, error) {
	var coupon struct {
		ID          int64
		TenantID    *int64 `gorm:"column:tenant_id"`
		Type        string
		Value       float64
		MinAmount   float64  `gorm:"column:min_amount"`
		MaxDiscount *float64 `gorm:"column:max_discount"`
		ValidFrom   *time.Time `gorm:"column:valid_from"`
		ValidUntil  *time.Time `gorm:"column:valid_until"`
		UsageLimit  *int     `gorm:"column:usage_limit"`
		UsedCount   int      `gorm:"column:used_count"`
		Status      string
	}
	e := a.store.db.Table("coupons").
		Where("code=? AND status='active' AND deleted_at IS NULL", code).
		Take(&coupon).Error
	if e != nil {
		return 0, fmt.Errorf("优惠券无效")
	}
	now := time.Now()
	// 检查有效期
	if coupon.ValidFrom != nil && now.Before(*coupon.ValidFrom) {
		return 0, fmt.Errorf("优惠券尚未生效")
	}
	if coupon.ValidUntil != nil && now.After(*coupon.ValidUntil) {
		return 0, fmt.Errorf("优惠券已过期")
	}
	// 检查使用次数
	if coupon.UsageLimit != nil && coupon.UsedCount >= *coupon.UsageLimit {
		return 0, fmt.Errorf("优惠券已达到使用上限")
	}
	// 检查最低消费
	if amount < coupon.MinAmount {
		return 0, fmt.Errorf("订单金额未达到优惠券最低消费要求")
	}
	// 优惠券必须属于平台（tenant_id NULL）或当前租户
	if coupon.TenantID != nil && *coupon.TenantID != tenantID {
		return 0, fmt.Errorf("优惠券不适用于当前租户")
	}
	// 计算折扣
	var discount float64
	switch coupon.Type {
	case "fixed":
		discount = coupon.Value
	case "percent":
		discount = amount * coupon.Value / 100
		if coupon.MaxDiscount != nil && discount > *coupon.MaxDiscount {
			discount = *coupon.MaxDiscount
		}
	default:
		return 0, fmt.Errorf("优惠券类型无效")
	}
	// 增加使用次数
	a.store.db.Table("coupons").Where("id=?", coupon.ID).Update("used_count", gorm.Expr("used_count+1"))
	return discount, nil
}

// transitionOrder 处理订单状态流转
func (a *App) transitionOrder(c *gin.Context) {
	sc := contextScope(c)
	id := number(c.Param("id"))
	transition := strings.TrimSpace(c.Param("transition"))
	if id <= 0 {
		fail(c, 400, "无效的订单ID")
		return
	}
	if transition == "" {
		fail(c, 400, "请指定状态流转动作")
		return
	}
	// 查询当前订单
	var order struct {
		ID     int64
		Status string
	}
	e := a.store.db.Table("orders").
		Select("id, status").
		Where("id=? AND tenant_id=? AND deleted_at IS NULL", id, sc.Tenant).
		Take(&order).Error
	if e != nil {
		fail(c, 404, "订单不存在")
		return
	}
	// 校验状态流转合法性
	allowed, exists := orderTransitions[order.Status]
	if !exists || !allowed[transition] {
		fail(c, 400, fmt.Sprintf("不允许从 %s 流转到 %s", order.Status, transition))
		return
	}
	updates := map[string]any{"status": transition}
	now := time.Now()
	switch transition {
	case "paid":
		updates["paid_at"] = now
	case "completed":
		updates["completed_at"] = now
	case "cancelled", "refunded":
		// 如果已支付且退款，需要处理账户余额退回
		if transition == "refunded" {
			var orderDetail struct {
				FinalAmount float64 `gorm:"column:final_amount"`
				TenantID    int64   `gorm:"column:tenant_id"`
			}
			a.store.db.Table("orders").Select("final_amount, tenant_id").
				Where("id=?", id).Take(&orderDetail)
			// 退回金额到租户账户
			if orderDetail.FinalAmount > 0 {
				a.store.db.Table("accounts").
					Where("tenant_id=? AND deleted_at IS NULL", orderDetail.TenantID).
					Update("balance", gorm.Expr("balance+?", orderDetail.FinalAmount))
			}
		}
	}
	e = a.store.db.Transaction(func(tx *gorm.DB) error {
		res := tx.Table("orders").
			Where("id=? AND tenant_id=? AND status=? AND deleted_at IS NULL", id, sc.Tenant, order.Status).
			Updates(updates)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		return a.store.audit(tx, sc, "transition:"+transition, "orders", id)
	})
	if e != nil {
		fail(c, 500, "订单状态流转失败")
		return
	}
	ok(c, gin.H{"id": id, "status": transition})
}

// exportOrder 导出订单详情（JSON 格式）
func (a *App) exportOrder(c *gin.Context) {
	sc := contextScope(c)
	id := number(c.Param("id"))
	if id <= 0 {
		fail(c, 400, "无效的订单ID")
		return
	}
	var order map[string]any
	e := a.store.db.Table("orders").
		Where("id=? AND tenant_id=? AND deleted_at IS NULL", id, sc.Tenant).
		Take(&order).Error
	if e != nil {
		fail(c, 404, "订单不存在")
		return
	}
	// 附加产品信息
	if productID, ok := order["product_id"]; ok && productID != nil {
		var product map[string]any
		if e := a.store.db.Table("products").
			Select("id, name, spec_json, pricing_json, billing_cycle").
			Where("id=?", productID).
			Take(&product).Error; e == nil {
			order["product"] = product
		}
	}
	// 附加支付信息
	var payments []map[string]any
	a.store.db.Table("payments").
		Where("account_id IN (SELECT id FROM accounts WHERE tenant_id=?) AND deleted_at IS NULL", sc.Tenant).
		Find(&payments)
	order["payments"] = payments
	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=order_%d.json", id))
	c.Header("Content-Type", "application/json")
	ok(c, order)
}
