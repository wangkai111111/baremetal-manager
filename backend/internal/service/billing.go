package service

import (
	"encoding/json"
	"fmt"
	"time"

	"gorm.io/gorm"
)

// BillingEngine 处理实时计量与周期性账单生成
type BillingEngine struct {
	db *gorm.DB
}

// NewBillingEngine 创建计费引擎实例
func NewBillingEngine(db *gorm.DB) *BillingEngine {
	return &BillingEngine{db: db}
}

// UsageRecord 用量记录
type UsageRecord struct {
	AssetID   int64   `json:"asset_id"`
	Hours     float64 `json:"hours"`
	UnitPrice float64 `json:"unit_price"`
	Total     float64 `json:"total"`
}

// CalculateUsage 计算指定资产在时间范围内的使用量及费用
func (e *BillingEngine) CalculateUsage(assetID int64, startTime, endTime time.Time) (*UsageRecord, error) {
	if !endTime.After(startTime) {
		return nil, fmt.Errorf("结束时间必须晚于开始时间")
	}
	// 查找资产关联的产品定价
	var product struct {
		ID          int64
		PricingJSON string `gorm:"column:pricing_json"`
		BillingCycle string `gorm:"column:billing_cycle"`
	}
	err := e.db.Table("products p").
		Select("p.id, p.pricing_json, p.billing_cycle").
		Joins("JOIN orders o ON o.product_id=p.id").
		Where("o.asset_id=? AND o.deleted_at IS NULL AND p.deleted_at IS NULL", assetID).
		Order("o.id DESC").
		Limit(1).
		Take(&product).Error
	if err != nil {
		return nil, fmt.Errorf("未找到资产关联的产品定价信息")
	}
	// 解析定价 JSON
	var pricing map[string]any
	if product.PricingJSON != "" {
		if err := json.Unmarshal([]byte(product.PricingJSON), &pricing); err != nil {
			return nil, fmt.Errorf("产品定价信息格式无效")
		}
	}
	// 获取单价
	unitPrice := 0.0
	if pricing != nil {
		switch product.BillingCycle {
		case "hourly":
			if v, ok := pricing["hourly_price"].(float64); ok {
				unitPrice = v
			}
		case "daily":
			if v, ok := pricing["daily_price"].(float64); ok {
				unitPrice = v / 24
			}
		case "monthly":
			if v, ok := pricing["monthly_price"].(float64); ok {
				unitPrice = v / (30 * 24)
			}
		case "yearly":
			if v, ok := pricing["yearly_price"].(float64); ok {
				unitPrice = v / (365 * 24)
			}
		}
		if unitPrice == 0 {
			if v, ok := pricing["base_price"].(float64); ok {
				unitPrice = v
			}
		}
	}
	// 计算使用时长（小时）
	hours := endTime.Sub(startTime).Hours()
	total := hours * unitPrice
	return &UsageRecord{
		AssetID:   assetID,
		Hours:     hours,
		UnitPrice: unitPrice,
		Total:     total,
	}, nil
}

// PeriodBillResult 周期账单生成结果
type PeriodBillResult struct {
	TenantID    int64         `json:"tenant_id"`
	PeriodStart time.Time     `json:"period_start"`
	PeriodEnd   time.Time     `json:"period_end"`
	TotalAmount float64       `json:"total_amount"`
	UsageItems  []UsageRecord `json:"usage_items"`
	BillID      int64         `json:"bill_id"`
}

// GeneratePeriodBills 为指定租户在周期内生成账单
func (e *BillingEngine) GeneratePeriodBills(tenantID int64, periodStart, periodEnd time.Time) (*PeriodBillResult, error) {
	if !periodEnd.After(periodStart) {
		return nil, fmt.Errorf("账单周期结束时间必须晚于开始时间")
	}
	// 查询该租户在周期内有订单的资产
	var assetIDs []int64
	err := e.db.Table("orders").
		Where("tenant_id=? AND status IN ('paid','processing','completed') AND deleted_at IS NULL", tenantID).
		Where("asset_id IS NOT NULL").
		Distinct("asset_id").
		Pluck("asset_id", &assetIDs).Error
	if err != nil {
		return nil, fmt.Errorf("查询租户资产失败: %w", err)
	}
	var usageItems []UsageRecord
	totalAmount := 0.0
	for _, assetID := range assetIDs {
		record, err := e.CalculateUsage(assetID, periodStart, periodEnd)
		if err != nil {
			continue // 跳过无定价的资产
		}
		usageItems = append(usageItems, *record)
		totalAmount += record.Total
	}
	// 如果无用量数据，使用订单直接汇总
	if len(usageItems) == 0 {
		err = e.db.Table("orders").
			Where("tenant_id=? AND status IN ('paid','completed') AND paid_at >= ? AND paid_at < ? AND deleted_at IS NULL",
				tenantID, periodStart, periodEnd).
			Select("COALESCE(SUM(final_amount),0)").
			Scan(&totalAmount).Error
		if err != nil {
			return nil, fmt.Errorf("汇总订单金额失败: %w", err)
		}
	}
	if totalAmount <= 0 {
		return nil, fmt.Errorf("该周期内无需计费")
	}
	// 构建账单明细
	detail := map[string]any{
		"period_start": periodStart,
		"period_end":   periodEnd,
		"usage_items":  usageItems,
		"generated_at": time.Now(),
	}
	detailJSON, _ := json.Marshal(detail)
	// 创建账单记录
	var billID int64
	err = e.db.Transaction(func(tx *gorm.DB) error {
		row := map[string]any{
			"tenant_id":    tenantID,
			"period_start": periodStart,
			"period_end":   periodEnd,
			"total_amount": totalAmount,
			"status":       "unpaid",
			"detail_json":  gorm.Expr("?::jsonb", string(detailJSON)),
			"due_date":     periodEnd.Add(30 * 24 * time.Hour), // 30天付款期
		}
		if err := tx.Table("bills").Create(&row).Error; err != nil {
			return err
		}
		billID = toInt64(row["id"])
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("创建账单失败: %w", err)
	}
	return &PeriodBillResult{
		TenantID:    tenantID,
		PeriodStart: periodStart,
		PeriodEnd:   periodEnd,
		TotalAmount: totalAmount,
		UsageItems:  usageItems,
		BillID:      billID,
	}, nil
}

// DeductBalance 从账户余额中扣除指定金额，余额不足时返回错误
func (e *BillingEngine) DeductBalance(accountID int64, amount float64) error {
	if amount <= 0 {
		return fmt.Errorf("扣款金额必须大于0")
	}
	return e.db.Transaction(func(tx *gorm.DB) error {
		var account struct {
			ID      int64
			Balance float64
		}
		err := tx.Table("accounts").
			Where("id=? AND deleted_at IS NULL", accountID).
			Take(&account).Error
		if err != nil {
			return fmt.Errorf("账户不存在")
		}
		availableBalance := account.Balance - account.Balance // 可用余额 = 余额 - 冻结
		// 重新查询可用余额
		err = tx.Table("accounts").
			Select("balance - frozen_amount AS available").
			Where("id=? AND deleted_at IS NULL", accountID).
			Scan(&availableBalance).Error
		if err != nil {
			return fmt.Errorf("查询可用余额失败")
		}
		if availableBalance < amount {
			return fmt.Errorf("账户余额不足，可用余额: %.2f，需扣款: %.2f", availableBalance, amount)
		}
		// 扣款
		result := tx.Table("accounts").
			Where("id=? AND (balance - frozen_amount) >= ? AND deleted_at IS NULL", accountID, amount).
			Update("balance", gorm.Expr("balance-?", amount))
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return fmt.Errorf("扣款失败，余额不足或账户已冻结")
		}
		return nil
	})
}

// SuspendResult 暂停结果
type SuspendResult struct {
	CheckedCount  int     `json:"checked_count"`
	SuspendedCount int    `json:"suspended_count"`
	FailedDeductions []string `json:"failed_deductions"`
}

// CheckExpiryAndSuspend 检查过期资源并自动暂停
func (e *BillingEngine) CheckExpiryAndSuspend() (*SuspendResult, error) {
	result := &SuspendResult{}
	// 查找已过期但未付款的账单
	var expiredBills []struct {
		ID       int64
		TenantID int64 `gorm:"column:tenant_id"`
	}
	err := e.db.Table("bills").
		Where("status='unpaid' AND due_date < now() AND deleted_at IS NULL").
		Find(&expiredBills).Error
	if err != nil {
		return nil, fmt.Errorf("查询过期账单失败: %w", err)
	}
	result.CheckedCount = len(expiredBills)
	for _, bill := range expiredBills {
		// 标记为逾期
		e.db.Table("bills").Where("id=?", bill.ID).Update("status", "overdue")
		// 暂停该租户下所有处理中的订单关联资产
		var orderIDs []int64
		e.db.Table("orders").
			Where("tenant_id=? AND status='processing' AND deleted_at IS NULL", bill.TenantID).
			Pluck("id", &orderIDs)
		if len(orderIDs) > 0 {
			// 更新订单状态为暂停
			e.db.Table("orders").
				Where("id IN ?", orderIDs).
				Update("status", "cancelled")
			result.SuspendedCount++
		}
	}
	return result, nil
}

// CalculateOrderAmount 根据产品定价、时长和计费周期计算订单金额
func (e *BillingEngine) CalculateOrderAmount(productID int64, duration int, billingCycle string) (float64, error) {
	if duration <= 0 {
		return 0, fmt.Errorf("购买时长必须大于0")
	}
	var product struct {
		PricingJSON  string `gorm:"column:pricing_json"`
		BillingCycle string `gorm:"column:billing_cycle"`
		Status       string
	}
	err := e.db.Table("products").
		Where("id=? AND deleted_at IS NULL", productID).
		Take(&product).Error
	if err != nil {
		return 0, fmt.Errorf("产品不存在")
	}
	if product.Status != "active" {
		return 0, fmt.Errorf("产品当前不可购买")
	}
	var pricing map[string]any
	if err := json.Unmarshal([]byte(product.PricingJSON), &pricing); err != nil {
		return 0, fmt.Errorf("产品定价信息格式无效")
	}
	// 使用指定的计费周期或产品默认周期
	cycle := billingCycle
	if cycle == "" {
		cycle = product.BillingCycle
	}
	// 获取对应周期的单价
	priceKey := cycle + "_price"
	basePrice, ok := pricing[priceKey].(float64)
	if !ok || basePrice <= 0 {
		// 回退到基础价格
		if bp, exists := pricing["base_price"].(float64); exists && bp > 0 {
			basePrice = bp
		} else {
			return 0, fmt.Errorf("未找到有效的产品定价")
		}
	}
	// 计算总价
	total := basePrice * float64(duration)
	// 检查是否有折扣
	if discount, ok := pricing["discount"].(float64); ok && discount > 0 && discount <= 1 {
		total *= discount
	}
	return total, nil
}

// toInt64 安全地将任意数值类型转换为 int64
func toInt64(v any) int64 {
	if v == nil {
		return 0
	}
	switch x := v.(type) {
	case int64:
		return x
	case int:
		return int64(x)
	case float64:
		return int64(x)
	case int32:
		return int64(x)
	default:
		return 0
	}
}
