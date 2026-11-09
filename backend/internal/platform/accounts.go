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
// 账户与支付管理处理器（运营模式）
// ============================================================
//
// 待注册路由（routes.go）：
// GET    /api/v1/projects/:projectId/accounts              → getAccount          → finance:read
// POST   /api/v1/projects/:projectId/accounts/recharge     → rechargeAccount     → finance:write
// GET    /api/v1/projects/:projectId/payments              → listPayments        → finance:read
// POST   /api/v1/projects/:projectId/payments              → createPayment       → finance:write
// GET    /api/v1/projects/:projectId/bills                 → listBills           → billing:read
// GET    /api/v1/projects/:projectId/bills/:id             → getBill             → billing:read
// GET    /api/v1/projects/:projectId/bills/:id/export      → exportBill          → billing:read
// POST   /api/v1/projects/:projectId/bills/generate        → generateBills       → billing:write
// GET    /api/v1/projects/:projectId/invoices              → listInvoices        → finance:read
// POST   /api/v1/projects/:projectId/invoices              → createInvoice       → finance:write
// PUT    /api/v1/projects/:projectId/invoices/:id/:status  → updateInvoice       → finance:write

// getAccount 获取当前租户的账户信息（一个租户一个账户）
func (a *App) getAccount(c *gin.Context) {
	sc := contextScope(c)
	var account map[string]any
	e := a.store.db.Table("accounts").
		Where("tenant_id=? AND deleted_at IS NULL", sc.Tenant).
		Take(&account).Error
	if e != nil {
		if e == gorm.ErrRecordNotFound {
			fail(c, 404, "账户不存在，请先进行充值")
			return
		}
		fail(c, 500, "账户查询失败")
		return
	}
	ok(c, account)
}

// rechargeAccount 账户充值（增加余额）
func (a *App) rechargeAccount(c *gin.Context) {
	sc := contextScope(c)
	var in struct {
		Amount float64 `json:"amount"`
		Method string  `json:"method"` // alipay/wechat/transfer
	}
	if c.ShouldBindJSON(&in) != nil || in.Amount <= 0 {
		fail(c, 400, "请提供有效的充值金额")
		return
	}
	validMethods := map[string]bool{"alipay": true, "wechat": true, "transfer": true}
	if in.Method != "" && !validMethods[in.Method] {
		fail(c, 400, "无效的支付方式，仅支持 alipay/wechat/transfer")
		return
	}
	method := in.Method
	if method == "" {
		method = "transfer"
	}
	e := a.store.db.Transaction(func(tx *gorm.DB) error {
		// 确保账户存在（不存在则自动创建）
		var account struct {
			ID       int64
			Balance  float64
			TenantID int64 `gorm:"column:tenant_id"`
		}
		err := tx.Table("accounts").
			Where("tenant_id=? AND deleted_at IS NULL", sc.Tenant).
			Take(&account).Error
		if err != nil {
			if err == gorm.ErrRecordNotFound {
				// 创建新账户
				row := map[string]any{
					"tenant_id":     sc.Tenant,
					"balance":       in.Amount,
					"frozen_amount": 0,
					"credit_limit":  0,
				}
				if e := tx.Table("accounts").Clauses(clause.Returning{Columns: []clause.Column{{Name: "id"}}}).Create(&row).Error; e != nil {
					return e
				}
				account.ID = asInt(row["id"])
			} else {
				return err
			}
		} else {
			// 增加余额
			if e := tx.Table("accounts").Where("id=? AND deleted_at IS NULL", account.ID).
				Update("balance", gorm.Expr("balance+?", in.Amount)).Error; e != nil {
				return e
			}
		}
		// 记录支付流水
		now := time.Now()
		paymentRow := map[string]any{
			"account_id": account.ID,
			"amount":     in.Amount,
			"method":     method,
			"status":     "success",
			"paid_at":    now,
		}
		if e := tx.Table("payments").Create(&paymentRow).Error; e != nil {
			return e
		}
		return a.store.audit(tx, sc, "recharge", "accounts", account.ID)
	})
	if e != nil {
		fail(c, 500, "充值失败")
		return
	}
	ok(c, gin.H{"message": "充值成功", "amount": in.Amount})
}

// listPayments 查询支付记录列表
func (a *App) listPayments(c *gin.Context) {
	sc := contextScope(c)
	// 先获取租户账户 ID
	var account struct{ ID int64 }
	e := a.store.db.Table("accounts").
		Select("id").
		Where("tenant_id=? AND deleted_at IS NULL", sc.Tenant).
		Take(&account).Error
	if e != nil {
		// 账户不存在，返回空列表
		ok(c, gin.H{"items": []any{}, "total": 0, "page": 1, "page_size": 20})
		return
	}
	q := a.store.db.Table("payments").
		Where("account_id=? AND deleted_at IS NULL", account.ID)
	// 按状态筛选
	if status := strings.TrimSpace(c.Query("status")); status != "" {
		q = q.Where("status=?", status)
	}
	// 按支付方式筛选
	if method := strings.TrimSpace(c.Query("method")); method != "" {
		q = q.Where("method=?", method)
	}
	var total int64
	if q.Count(&total).Error != nil {
		fail(c, 500, "支付记录查询失败")
		return
	}
	p, n := page(c)
	var items []map[string]any
	if e := q.Select("id, account_id, amount, method, status, transaction_id, paid_at, created_at, updated_at").
		Order("id DESC").Limit(n).Offset((p - 1) * n).
		Find(&items).Error; e != nil {
		fail(c, 500, "支付记录查询失败")
		return
	}
	ok(c, gin.H{"items": items, "total": total, "page": p, "page_size": n})
}

// createPayment 创建支付记录（用于线下支付登记）
func (a *App) createPayment(c *gin.Context) {
	sc := contextScope(c)
	var in struct {
		Amount        float64 `json:"amount"`
		Method        string  `json:"method"`
		TransactionID string  `json:"transaction_id"`
	}
	if c.ShouldBindJSON(&in) != nil || in.Amount <= 0 {
		fail(c, 400, "请提供有效的支付金额")
		return
	}
	validMethods := map[string]bool{"alipay": true, "wechat": true, "transfer": true}
	if !validMethods[in.Method] {
		fail(c, 400, "无效的支付方式")
		return
	}
	// 获取租户账户
	var account struct{ ID int64 }
	e := a.store.db.Table("accounts").
		Select("id").
		Where("tenant_id=? AND deleted_at IS NULL", sc.Tenant).
		Take(&account).Error
	if e != nil {
		fail(c, 404, "账户不存在")
		return
	}
	var id int64
	e = a.store.db.Transaction(func(tx *gorm.DB) error {
		row := map[string]any{
			"account_id": account.ID,
			"amount":     in.Amount,
			"method":     in.Method,
			"status":     "pending",
		}
		if in.TransactionID != "" {
			row["transaction_id"] = in.TransactionID
		}
		if e := tx.Table("payments").Clauses(clause.Returning{Columns: []clause.Column{{Name: "id"}}}).Create(&row).Error; e != nil {
			return e
		}
		id = asInt(row["id"])
		return a.store.audit(tx, sc, "create", "payments", id)
	})
	if e != nil {
		fail(c, 500, "支付记录创建失败")
		return
	}
	ok(c, gin.H{"id": id})
}

// ============================================================
// 账单管理
// ============================================================

// listBills 查询账单列表
func (a *App) listBills(c *gin.Context) {
	sc := contextScope(c)
	q := a.store.db.Table("bills").
		Where("tenant_id=? AND deleted_at IS NULL", sc.Tenant)
	// 按状态筛选
	if status := strings.TrimSpace(c.Query("status")); status != "" {
		q = q.Where("status=?", status)
	}
	var total int64
	if q.Count(&total).Error != nil {
		fail(c, 500, "账单查询失败")
		return
	}
	p, n := page(c)
	var items []map[string]any
	if e := q.Select("id, tenant_id, period_start, period_end, total_amount, status, due_date, created_at, updated_at").
		Order("id DESC").Limit(n).Offset((p - 1) * n).
		Find(&items).Error; e != nil {
		fail(c, 500, "账单查询失败")
		return
	}
	ok(c, gin.H{"items": items, "total": total, "page": p, "page_size": n})
}

// getBill 获取单个账单详情
func (a *App) getBill(c *gin.Context) {
	sc := contextScope(c)
	id := number(c.Param("id"))
	if id <= 0 {
		fail(c, 400, "无效的账单ID")
		return
	}
	var item map[string]any
	e := a.store.db.Table("bills").
		Where("id=? AND tenant_id=? AND deleted_at IS NULL", id, sc.Tenant).
		Take(&item).Error
	if e != nil {
		fail(c, 404, "账单不存在")
		return
	}
	ok(c, item)
}

// exportBill 导出账单详情（JSON 格式）
func (a *App) exportBill(c *gin.Context) {
	sc := contextScope(c)
	id := number(c.Param("id"))
	if id <= 0 {
		fail(c, 400, "无效的账单ID")
		return
	}
	var bill map[string]any
	e := a.store.db.Table("bills").
		Where("id=? AND tenant_id=? AND deleted_at IS NULL", id, sc.Tenant).
		Take(&bill).Error
	if e != nil {
		fail(c, 404, "账单不存在")
		return
	}
	// 解析明细 JSON
	if detailStr, ok := bill["detail_json"].(string); ok && detailStr != "" {
		var detail any
		if json.Unmarshal([]byte(detailStr), &detail) == nil {
			bill["detail"] = detail
		}
	}
	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=bill_%d.json", id))
	c.Header("Content-Type", "application/json")
	ok(c, bill)
}

// generateBills 管理员生成周期性账单
func (a *App) generateBills(c *gin.Context) {
	sc := contextScope(c)
	if !sc.Admin {
		fail(c, 403, "需要管理员权限")
		return
	}
	var in struct {
		PeriodStart string `json:"period_start"` // 格式: 2006-01-02T15:04:05Z
		PeriodEnd   string `json:"period_end"`
		TenantID    *int64 `json:"tenant_id"` // 可选：指定租户，不指定则为所有租户生成
	}
	if c.ShouldBindJSON(&in) != nil || in.PeriodStart == "" || in.PeriodEnd == "" {
		fail(c, 400, "请提供账单周期（period_start 和 period_end）")
		return
	}
	periodStart, err := time.Parse(time.RFC3339, in.PeriodStart)
	if err != nil {
		fail(c, 400, "period_start 格式无效，请使用 RFC3339 格式")
		return
	}
	periodEnd, err := time.Parse(time.RFC3339, in.PeriodEnd)
	if err != nil {
		fail(c, 400, "period_end 格式无效，请使用 RFC3339 格式")
		return
	}
	if !periodEnd.After(periodStart) {
		fail(c, 400, "period_end 必须晚于 period_start")
		return
	}
	// 查询需要生成账单的租户
	tenantQ := a.store.db.Table("accounts").Where("deleted_at IS NULL")
	if in.TenantID != nil && *in.TenantID > 0 {
		tenantQ = tenantQ.Where("tenant_id=?", *in.TenantID)
	}
	var accounts []struct {
		ID       int64
		TenantID int64 `gorm:"column:tenant_id"`
	}
	if e := tenantQ.Find(&accounts).Error; e != nil {
		fail(c, 500, "租户账户查询失败")
		return
	}
	generated := 0
	for _, acct := range accounts {
		// 计算该租户在周期内的订单总额
		var totalAmount float64
		a.store.db.Table("orders").
			Where("tenant_id=? AND status IN ('paid','completed') AND paid_at >= ? AND paid_at < ? AND deleted_at IS NULL",
				acct.TenantID, periodStart, periodEnd).
			Select("COALESCE(SUM(final_amount),0)").
			Scan(&totalAmount)
		if totalAmount <= 0 {
			continue
		}
		// 构建明细
		detail := map[string]any{
			"period_start": periodStart,
			"period_end":   periodEnd,
			"total_amount": totalAmount,
			"generated_at": time.Now(),
		}
		detailJSON, _ := json.Marshal(detail)
		// 计算到期日（周期结束后 30 天）
		dueDate := periodEnd.Add(30 * 24 * time.Hour)
		billRow := map[string]any{
			"tenant_id":    acct.TenantID,
			"period_start": periodStart,
			"period_end":   periodEnd,
			"total_amount": totalAmount,
			"status":       "unpaid",
			"detail_json":  gorm.Expr("?::jsonb", string(detailJSON)),
			"due_date":     dueDate,
		}
		if e := a.store.db.Table("bills").Create(&billRow).Error; e == nil {
			generated++
		}
	}
	ok(c, gin.H{
		"message":   fmt.Sprintf("成功生成 %d 张账单", generated),
		"generated": generated,
	})
}

// ============================================================
// 发票管理
// ============================================================

// listInvoices 查询发票列表
func (a *App) listInvoices(c *gin.Context) {
	sc := contextScope(c)
	q := a.store.db.Table("invoices").
		Where("tenant_id=? AND deleted_at IS NULL", sc.Tenant)
	// 按状态筛选
	if status := strings.TrimSpace(c.Query("status")); status != "" {
		q = q.Where("status=?", status)
	}
	var total int64
	if q.Count(&total).Error != nil {
		fail(c, 500, "发票查询失败")
		return
	}
	p, n := page(c)
	var items []map[string]any
	if e := q.Select("id, tenant_id, bill_id, type, title, tax_number, amount, status, issued_at, created_at, updated_at").
		Order("id DESC").Limit(n).Offset((p - 1) * n).
		Find(&items).Error; e != nil {
		fail(c, 500, "发票查询失败")
		return
	}
	ok(c, gin.H{"items": items, "total": total, "page": p, "page_size": n})
}

// createInvoice 创建发票申请
func (a *App) createInvoice(c *gin.Context) {
	sc := contextScope(c)
	var in struct {
		BillID     *int64  `json:"bill_id"`
		Type       string  `json:"type"`
		Title      string  `json:"title"`
		TaxNumber  string  `json:"tax_number"`
		Amount     float64 `json:"amount"`
	}
	if c.ShouldBindJSON(&in) != nil || strings.TrimSpace(in.Title) == "" || in.Amount <= 0 {
		fail(c, 400, "发票抬头和金额为必填项")
		return
	}
	validTypes := map[string]bool{"normal": true, "special": true}
	if in.Type != "" && !validTypes[in.Type] {
		fail(c, 400, "无效的发票类型，仅支持 normal/special")
		return
	}
	if in.Type == "" {
		in.Type = "normal"
	}
	// 如果关联了账单，验证其存在性
	if in.BillID != nil && *in.BillID > 0 {
		var bill struct{ ID int64 }
		e := a.store.db.Table("bills").
			Where("id=? AND tenant_id=? AND deleted_at IS NULL", *in.BillID, sc.Tenant).
			Take(&bill).Error
		if e != nil {
			fail(c, 404, "关联账单不存在")
			return
		}
	}
	var id int64
	e := a.store.db.Transaction(func(tx *gorm.DB) error {
		row := map[string]any{
			"tenant_id":  sc.Tenant,
			"type":       in.Type,
			"title":      strings.TrimSpace(in.Title),
			"tax_number": in.TaxNumber,
			"amount":     in.Amount,
			"status":     "requested",
		}
		if in.BillID != nil && *in.BillID > 0 {
			row["bill_id"] = *in.BillID
		}
		if e := tx.Table("invoices").Clauses(clause.Returning{Columns: []clause.Column{{Name: "id"}}}).Create(&row).Error; e != nil {
			return e
		}
		id = asInt(row["id"])
		return a.store.audit(tx, sc, "create", "invoices", id)
	})
	if e != nil {
		fail(c, 500, "发票申请创建失败")
		return
	}
	ok(c, gin.H{"id": id})
}

// updateInvoice 更新发票状态（开具/作废）
func (a *App) updateInvoice(c *gin.Context) {
	sc := contextScope(c)
	id := number(c.Param("id"))
	status := strings.TrimSpace(c.Param("status"))
	if id <= 0 {
		fail(c, 400, "无效的发票ID")
		return
	}
	// 校验目标状态
	validStatusTransitions := map[string]map[string]bool{
		"requested": {"issued": true, "cancelled": true},
		"issued":    {"cancelled": true},
	}
	var invoice struct {
		ID     int64
		Status string
	}
	e := a.store.db.Table("invoices").
		Select("id, status").
		Where("id=? AND tenant_id=? AND deleted_at IS NULL", id, sc.Tenant).
		Take(&invoice).Error
	if e != nil {
		fail(c, 404, "发票不存在")
		return
	}
	allowed, exists := validStatusTransitions[invoice.Status]
	if !exists || !allowed[status] {
		fail(c, 400, fmt.Sprintf("不允许从 %s 流转到 %s", invoice.Status, status))
		return
	}
	updates := map[string]any{"status": status}
	if status == "issued" {
		now := time.Now()
		updates["issued_at"] = now
	}
	e = a.store.db.Transaction(func(tx *gorm.DB) error {
		res := tx.Table("invoices").
			Where("id=? AND tenant_id=? AND deleted_at IS NULL", id, sc.Tenant).
			Updates(updates)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		return a.store.audit(tx, sc, "update:"+status, "invoices", id)
	})
	if e != nil {
		fail(c, 500, "发票状态更新失败")
		return
	}
	ok(c, gin.H{"id": id, "status": status})
}
