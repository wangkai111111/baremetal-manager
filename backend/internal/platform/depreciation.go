package platform

import (
	"fmt"
	"time"

	"github.com/gin-gonic/gin"
)

// ============================================================
// 资产折旧与维保告警处理器（自用模式）
// ============================================================
//
// 待注册路由（routes.go）：
// GET /api/v1/projects/:projectId/assets/:id/depreciation    → getAssetDepreciation  → assets:read
// GET /api/v1/projects/:projectId/assets/warranty-alerts     → listWarrantyAlerts    → assets:read

// getAssetDepreciation 返回指定资产的折旧信息
// 使用直线法计算：月折旧额 = (原值 - 残值) / 使用月数
// 累计折旧 = 月折旧额 × 已使用月数
// 当前净值 = 原值 - 累计折旧
func (a *App) getAssetDepreciation(c *gin.Context) {
	sc := contextScope(c)
	assetID := number(c.Param("id"))
	if assetID <= 0 {
		fail(c, 400, "无效的资产ID")
		return
	}
	// 验证资产存在且属于当前项目
	var assetCount int64
	a.store.db.Table("assets").
		Where("id=? AND project_id=? AND deleted_at IS NULL", assetID, sc.Project).
		Count(&assetCount)
	if assetCount == 0 {
		fail(c, 404, "资产不存在")
		return
	}
	// 查询折旧记录
	var dep map[string]any
	e := a.store.db.Table("asset_depreciation").
		Where("asset_id=? AND tenant_id=? AND deleted_at IS NULL", assetID, sc.Tenant).
		Take(&dep).Error
	if e != nil {
		// 无折旧记录时，返回空数据
		ok(c, gin.H{
			"asset_id":               assetID,
			"has_depreciation":       false,
			"purchase_cost":          nil,
			"salvage_value":          nil,
			"useful_life_months":     nil,
			"monthly_depreciation":   nil,
			"accumulated_depreciation": nil,
			"net_value":              nil,
			"start_date":             nil,
			"status":                 nil,
		})
		return
	}
	// 动态计算当前累计折旧和净值
	purchaseCost := toFloat64(dep["purchase_cost"])
	salvageValue := toFloat64(dep["salvage_value"])
	usefulLifeMonths := toFloat64(dep["useful_life_months"])
	monthlyDepreciation := toFloat64(dep["monthly_depreciation"])
	startDate := dep["start_date"]

	// 如果月折旧额未设置，按直线法计算
	if monthlyDepreciation == 0 && usefulLifeMonths > 0 {
		monthlyDepreciation = (purchaseCost - salvageValue) / usefulLifeMonths
	}

	// 计算已使用月数
	usedMonths := calculateUsedMonths(startDate)
	accumulatedDepreciation := monthlyDepreciation * usedMonths
	// 累计折旧不超过原值减残值
	maxDepreciation := purchaseCost - salvageValue
	if accumulatedDepreciation > maxDepreciation && maxDepreciation > 0 {
		accumulatedDepreciation = maxDepreciation
	}
	netValue := purchaseCost - accumulatedDepreciation
	if netValue < salvageValue {
		netValue = salvageValue
	}

	ok(c, gin.H{
		"asset_id":               assetID,
		"has_depreciation":       true,
		"depreciation_method":    dep["depreciation_method"],
		"purchase_cost":          purchaseCost,
		"salvage_value":          salvageValue,
		"useful_life_months":     usefulLifeMonths,
		"monthly_depreciation":   monthlyDepreciation,
		"accumulated_depreciation": accumulatedDepreciation,
		"net_value":              netValue,
		"start_date":             startDate,
		"status":                 dep["status"],
		"updated_at":             dep["updated_at"],
	})
}

// calculateUsedMonths 计算从起始日期到当前的月数
func calculateUsedMonths(startDate any) float64 {
	if startDate == nil {
		return 0
	}
	var start time.Time
	switch v := startDate.(type) {
	case time.Time:
		start = v
	case string:
		parsed, err := time.Parse("2006-01-02", v)
		if err != nil {
			return 0
		}
		start = parsed
	default:
		return 0
	}
	now := time.Now()
	if start.After(now) {
		return 0
	}
	months := float64(now.Year()-start.Year())*12 + float64(now.Month()-start.Month())
	// 按天数比例修正
	dayRatio := float64(now.Day()) / float64(daysInMonth(now))
	months += dayRatio - float64(start.Day())/float64(daysInMonth(start))
	if months < 0 {
		return 0
	}
	return months
}

// daysInMonth 返回指定月份的总天数
func daysInMonth(t time.Time) int {
	return time.Date(t.Year(), t.Month()+1, 0, 0, 0, 0, 0, t.Location()).Day()
}

// toFloat64 将任意数值类型安全转换为 float64
func toFloat64(v any) float64 {
	if v == nil {
		return 0
	}
	switch x := v.(type) {
	case float64:
		return x
	case float32:
		return float64(x)
	case int64:
		return float64(x)
	case int:
		return float64(x)
	case int32:
		return float64(x)
	case string:
		var f float64
		fmt.Sscanf(v.(string), "%f", &f)
		return f
	default:
		return 0
	}
}

// listWarrantyAlerts 查询即将到保的资产列表
// 支持 ?days=N 参数自定义告警窗口（默认90天）
func (a *App) listWarrantyAlerts(c *gin.Context) {
	sc := contextScope(c)
	// 解析告警窗口天数
	days := number(c.DefaultQuery("days", "90"))
	if days <= 0 || days > 365 {
		days = 90
	}
	alertDeadline := time.Now().Add(time.Duration(days) * 24 * time.Hour)
	// 查询在告警窗口内到保的资产
	var rows []map[string]any
	e := a.store.db.Table("assets").
		Select(`id, name, serial_number, manufacturer, model, status, warranty_expiry,
				CEIL(EXTRACT(EPOCH FROM (warranty_expiry - NOW())) / 86400) AS days_remaining`).
		Where(`project_id=? AND deleted_at IS NULL
			   AND warranty_expiry IS NOT NULL
			   AND warranty_expiry <= ?
			   AND warranty_expiry > NOW()`,
			sc.Project, alertDeadline).
		Order("warranty_expiry ASC").
		Find(&rows).Error
	if e != nil {
		fail(c, 500, "维保告警查询失败")
		return
	}
	// 格式化输出
	type warrantyAlert struct {
		AssetID       int64  `json:"asset_id"`
		Name          string `json:"name"`
		SerialNumber  string `json:"serial_number"`
		Manufacturer  string `json:"manufacturer"`
		Model         string `json:"model"`
		Status        string `json:"status"`
		WarrantyExpiry string `json:"warranty_expiry"`
		DaysRemaining int64  `json:"days_remaining"`
	}
	alerts := make([]warrantyAlert, 0, len(rows))
	for _, row := range rows {
		alerts = append(alerts, warrantyAlert{
			AssetID:        toInt64(row["id"]),
			Name:           toString(row["name"]),
			SerialNumber:   toString(row["serial_number"]),
			Manufacturer:   toString(row["manufacturer"]),
			Model:          toString(row["model"]),
			Status:         toString(row["status"]),
			WarrantyExpiry: fmt.Sprintf("%v", row["warranty_expiry"]),
			DaysRemaining:  toInt64(row["days_remaining"]),
		})
	}
	ok(c, gin.H{
		"items":      alerts,
		"total":      len(alerts),
		"alert_days": days,
	})
}

// toString 将任意值安全转换为字符串
func toString(v any) string {
	if v == nil {
		return ""
	}
	switch x := v.(type) {
	case string:
		return x
	case []byte:
		return string(x)
	default:
		return fmt.Sprintf("%v", v)
	}
}
