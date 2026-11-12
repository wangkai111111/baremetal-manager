package platform

import (
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ============================================================
// 优惠券管理处理器（运营模式）
// ============================================================
//
// 待注册路由（routes.go）：
// GET    /api/v1/projects/:projectId/coupons                 → listCoupons       → marketing:read
// POST   /api/v1/projects/:projectId/coupons                 → createCoupon      → marketing:write
// PUT    /api/v1/projects/:projectId/coupons/:id             → updateCoupon      → marketing:write
// DELETE /api/v1/projects/:projectId/coupons/:id             → deleteCoupon      → marketing:write
// POST   /api/v1/projects/:projectId/coupons/:code/redeem   → redeemCoupon      → orders:write

// listCoupons 查询优惠券列表
// 支持查看平台级优惠券（tenant_id IS NULL）和当前租户的优惠券
func (a *App) listCoupons(c *gin.Context) {
	sc := contextScope(c)
	q := a.store.db.Table("coupons").
		Where("deleted_at IS NULL").
		Where("(tenant_id IS NULL OR tenant_id=?)", sc.Tenant)
	// 按状态筛选
	if status := strings.TrimSpace(c.Query("status")); status != "" {
		q = q.Where("status=?", status)
	}
	// 按类型筛选
	if couponType := strings.TrimSpace(c.Query("type")); couponType != "" {
		q = q.Where("type=?", couponType)
	}
	// 搜索优惠券码或名称
	if search := strings.TrimSpace(c.Query("search")); search != "" {
		q = q.Where("(code ILIKE ? OR name ILIKE ?)", "%"+search+"%", "%"+search+"%")
	}
	var total int64
	if q.Count(&total).Error != nil {
		fail(c, 500, "优惠券查询失败")
		return
	}
	p, n := page(c)
	var items []map[string]any
	if e := q.Select("id, tenant_id, code, name, type, value, min_amount, max_discount, valid_from, valid_until, usage_limit, used_count, status, created_at, updated_at").
		Order("id DESC").Limit(n).Offset((p - 1) * n).
		Find(&items).Error; e != nil {
		fail(c, 500, "优惠券查询失败")
		return
	}
	ok(c, gin.H{"items": items, "total": total, "page": p, "page_size": n})
}

// createCoupon 创建优惠券
func (a *App) createCoupon(c *gin.Context) {
	sc := contextScope(c)
	var in struct {
		Code        string   `json:"code"`
		Name        string   `json:"name"`
		Type        string   `json:"type"`
		Value       float64  `json:"value"`
		MinAmount   float64  `json:"min_amount"`
		MaxDiscount *float64 `json:"max_discount"`
		ValidFrom   string   `json:"valid_from"`
		ValidUntil  string   `json:"valid_until"`
		UsageLimit  *int     `json:"usage_limit"`
		TenantID    *int64   `json:"tenant_id"` // nil 表示平台级优惠券
	}
	if c.ShouldBindJSON(&in) != nil || strings.TrimSpace(in.Code) == "" || strings.TrimSpace(in.Name) == "" {
		fail(c, 400, "优惠券代码和名称为必填项")
		return
	}
	if len(in.Code) > 64 {
		fail(c, 400, "优惠券代码过长")
		return
	}
	// 校验类型
	validTypes := map[string]bool{"fixed": true, "percent": true}
	if !validTypes[in.Type] {
		fail(c, 400, "无效的优惠类型，仅支持 fixed/percent")
		return
	}
	if in.Value <= 0 {
		fail(c, 400, "优惠面值必须大于0")
		return
	}
	if in.Type == "percent" && in.Value > 100 {
		fail(c, 400, "百分比折扣不能超过100")
		return
	}
	// 解析有效期
	var validFrom, validUntil *time.Time
	if in.ValidFrom != "" {
		t, err := time.Parse(time.RFC3339, in.ValidFrom)
		if err != nil {
			fail(c, 400, "valid_from 格式无效，请使用 RFC3339 格式")
			return
		}
		validFrom = &t
	}
	if in.ValidUntil != "" {
		t, err := time.Parse(time.RFC3339, in.ValidUntil)
		if err != nil {
			fail(c, 400, "valid_until 格式无效，请使用 RFC3339 格式")
			return
		}
		validUntil = &t
	}
	// 检查代码唯一性
	var existing int64
	a.store.db.Table("coupons").Where("code=? AND deleted_at IS NULL", in.Code).Count(&existing)
	if existing > 0 {
		fail(c, 409, "优惠券代码已存在")
		return
	}
	var id int64
	e := a.store.db.Transaction(func(tx *gorm.DB) error {
		row := map[string]any{
			"code":       strings.TrimSpace(in.Code),
			"name":       strings.TrimSpace(in.Name),
			"type":       in.Type,
			"value":      in.Value,
			"min_amount": in.MinAmount,
			"status":     "active",
			"used_count": 0,
		}
		if in.MaxDiscount != nil {
			row["max_discount"] = *in.MaxDiscount
		}
		if validFrom != nil {
			row["valid_from"] = validFrom
		}
		if validUntil != nil {
			row["valid_until"] = validUntil
		}
		if in.UsageLimit != nil {
			row["usage_limit"] = *in.UsageLimit
		}
		// 设置租户：nil 表示平台级优惠券
		if in.TenantID != nil {
			row["tenant_id"] = *in.TenantID
		} else {
			row["tenant_id"] = sc.Tenant
		}
		if e := tx.Table("coupons").Clauses(clause.Returning{Columns: []clause.Column{{Name: "id"}}}).Create(&row).Error; e != nil {
			return e
		}
		id = asInt(row["id"])
		return a.store.audit(tx, sc, "create", "coupons", id)
	})
	if e != nil {
		if strings.Contains(e.Error(), "coupons_code_key") || strings.Contains(e.Error(), "unique") {
			fail(c, 409, "优惠券代码已存在")
			return
		}
		fail(c, 500, "优惠券创建失败")
		return
	}
	ok(c, gin.H{"id": id})
}

// updateCoupon 更新优惠券信息
func (a *App) updateCoupon(c *gin.Context) {
	sc := contextScope(c)
	id := number(c.Param("id"))
	if id <= 0 {
		fail(c, 400, "无效的优惠券ID")
		return
	}
	// 验证优惠券存在
	var existing struct {
		ID       int64
		TenantID *int64 `gorm:"column:tenant_id"`
	}
	e := a.store.db.Table("coupons").
		Select("id, tenant_id").
		Where("id=? AND deleted_at IS NULL", id).
		Take(&existing).Error
	if e != nil {
		fail(c, 404, "优惠券不存在")
		return
	}
	// 只能修改当前租户的优惠券或平台级优惠券
	if existing.TenantID != nil && *existing.TenantID != sc.Tenant {
		fail(c, 403, "无权修改其他租户的优惠券")
		return
	}
	var in struct {
		Name        *string  `json:"name"`
		Value       *float64 `json:"value"`
		MinAmount   *float64 `json:"min_amount"`
		MaxDiscount *float64 `json:"max_discount"`
		ValidFrom   *string  `json:"valid_from"`
		ValidUntil  *string  `json:"valid_until"`
		UsageLimit  *int     `json:"usage_limit"`
		Status      *string  `json:"status"`
	}
	if c.ShouldBindJSON(&in) != nil {
		fail(c, 400, "无效的请求体")
		return
	}
	updates := map[string]any{}
	if in.Name != nil {
		trimmed := strings.TrimSpace(*in.Name)
		if trimmed == "" {
			fail(c, 400, "优惠券名称不能为空")
			return
		}
		updates["name"] = trimmed
	}
	if in.Value != nil {
		if *in.Value <= 0 {
			fail(c, 400, "优惠面值必须大于0")
			return
		}
		updates["value"] = *in.Value
	}
	if in.MinAmount != nil {
		if *in.MinAmount < 0 {
			fail(c, 400, "最低消费金额不能为负数")
			return
		}
		updates["min_amount"] = *in.MinAmount
	}
	if in.MaxDiscount != nil {
		updates["max_discount"] = *in.MaxDiscount
	}
	if in.ValidFrom != nil {
		if *in.ValidFrom == "" {
			updates["valid_from"] = nil
		} else {
			t, err := time.Parse(time.RFC3339, *in.ValidFrom)
			if err != nil {
				fail(c, 400, "valid_from 格式无效")
				return
			}
			updates["valid_from"] = t
		}
	}
	if in.ValidUntil != nil {
		if *in.ValidUntil == "" {
			updates["valid_until"] = nil
		} else {
			t, err := time.Parse(time.RFC3339, *in.ValidUntil)
			if err != nil {
				fail(c, 400, "valid_until 格式无效")
				return
			}
			updates["valid_until"] = t
		}
	}
	if in.UsageLimit != nil {
		updates["usage_limit"] = *in.UsageLimit
	}
	if in.Status != nil {
		validStatuses := map[string]bool{"active": true, "inactive": true, "expired": true}
		if !validStatuses[*in.Status] {
			fail(c, 400, "无效的优惠券状态")
			return
		}
		updates["status"] = *in.Status
	}
	if len(updates) == 0 {
		fail(c, 400, "无更新内容")
		return
	}
	e = a.store.db.Transaction(func(tx *gorm.DB) error {
		res := tx.Table("coupons").
			Where("id=? AND deleted_at IS NULL", id).
			Updates(updates)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		return a.store.audit(tx, sc, "update", "coupons", id)
	})
	if e != nil {
		fail(c, 500, "优惠券更新失败")
		return
	}
	ok(c, nil)
}

// deleteCoupon 软删除优惠券
func (a *App) deleteCoupon(c *gin.Context) {
	sc := contextScope(c)
	id := number(c.Param("id"))
	if id <= 0 {
		fail(c, 400, "无效的优惠券ID")
		return
	}
	// 验证优惠券存在且属于当前租户
	var existing struct {
		ID       int64
		TenantID *int64 `gorm:"column:tenant_id"`
	}
	e := a.store.db.Table("coupons").
		Select("id, tenant_id").
		Where("id=? AND deleted_at IS NULL", id).
		Take(&existing).Error
	if e != nil {
		fail(c, 404, "优惠券不存在")
		return
	}
	if existing.TenantID != nil && *existing.TenantID != sc.Tenant {
		fail(c, 403, "无权删除其他租户的优惠券")
		return
	}
	e = a.store.db.Transaction(func(tx *gorm.DB) error {
		res := tx.Table("coupons").
			Where("id=? AND deleted_at IS NULL", id).
			Updates(map[string]any{"deleted_at": time.Now()})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		return a.store.audit(tx, sc, "delete", "coupons", id)
	})
	if e != nil {
		fail(c, 500, "优惠券删除失败")
		return
	}
	ok(c, nil)
}

// redeemCoupon 兑换优惠券（将优惠券与当前用户关联，返回优惠券信息）
func (a *App) redeemCoupon(c *gin.Context) {
	sc := contextScope(c)
	code := strings.TrimSpace(c.Param("code"))
	if code == "" {
		fail(c, 400, "请提供优惠券代码")
		return
	}
	// 查找优惠券
	var coupon struct {
		ID         int64
		TenantID   *int64 `gorm:"column:tenant_id"`
		Code       string
		Name       string
		Type       string
		Value      float64
		MinAmount  float64    `gorm:"column:min_amount"`
		ValidFrom  *time.Time `gorm:"column:valid_from"`
		ValidUntil *time.Time `gorm:"column:valid_until"`
		UsageLimit *int       `gorm:"column:usage_limit"`
		UsedCount  int        `gorm:"column:used_count"`
		Status     string
	}
	e := a.store.db.Table("coupons").
		Where("code=? AND status='active' AND deleted_at IS NULL", code).
		Take(&coupon).Error
	if e != nil {
		fail(c, 404, "优惠券不存在或已失效")
		return
	}
	now := time.Now()
	// 检查有效期
	if coupon.ValidFrom != nil && now.Before(*coupon.ValidFrom) {
		fail(c, 400, "优惠券尚未生效")
		return
	}
	if coupon.ValidUntil != nil && now.After(*coupon.ValidUntil) {
		// 自动标记过期
		a.store.db.Table("coupons").Where("id=?", coupon.ID).Update("status", "expired")
		fail(c, 400, "优惠券已过期")
		return
	}
	// 检查使用次数
	if coupon.UsageLimit != nil && coupon.UsedCount >= *coupon.UsageLimit {
		fail(c, 400, "优惠券已达到使用上限")
		return
	}
	// 优惠券必须属于平台（tenant_id NULL）或当前租户
	if coupon.TenantID != nil && *coupon.TenantID != sc.Tenant {
		fail(c, 400, "优惠券不适用于当前租户")
		return
	}
	// 增加使用次数
	e = a.store.db.Table("coupons").
		Where("id=? AND status='active' AND deleted_at IS NULL", coupon.ID).
		Update("used_count", gorm.Expr("used_count+1")).Error
	if e != nil {
		fail(c, 500, "优惠券兑换失败")
		return
	}
	ok(c, gin.H{
		"id":          coupon.ID,
		"code":        coupon.Code,
		"name":        coupon.Name,
		"type":        coupon.Type,
		"value":       coupon.Value,
		"min_amount":  coupon.MinAmount,
		"redeemed_at": now,
	})
}
