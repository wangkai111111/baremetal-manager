package platform

import (
	"errors"
	"fmt"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"math"
	"strings"
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

// listAssetDepreciation 通过所属资产限定租户与项目，折旧表本身没有 project_id。
func (a *App) listAssetDepreciation(c *gin.Context) {
	sc := contextScope(c)
	q := a.store.db.Table("asset_depreciation AS d").
		Joins("JOIN assets AS a ON a.id=d.asset_id AND a.tenant_id=d.tenant_id").
		Where("d.tenant_id=? AND a.project_id=? AND d.deleted_at IS NULL AND a.deleted_at IS NULL", sc.Tenant, sc.Project).Where(departmentAccessSQL(sc, "a.department_id", false))
	if search := strings.TrimSpace(c.Query("search")); search != "" {
		q = q.Where("a.name ILIKE ?", "%"+search+"%")
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		fail(c, 500, "depreciation query failed")
		return
	}
	p, n := page(c)
	items := make([]map[string]any, 0)
	if err := q.Select("d.*, a.name AS asset_name").Order("d.id DESC").Limit(n).Offset((p - 1) * n).Find(&items).Error; err != nil {
		fail(c, 500, "depreciation query failed")
		return
	}
	for _, item := range items {
		calculateDepreciation(item)
	}
	ok(c, gin.H{"items": items, "total": total, "page": p, "page_size": n})
}

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
	result := queryScope(a.store.db, resource{table: "assets"}, sc).Where("id=?", assetID).Count(&assetCount)
	if result.Error != nil {
		fail(c, 500, "资产查询失败")
		return
	}
	if assetCount == 0 {
		fail(c, 404, "资产不存在")
		return
	}
	// 查询折旧记录
	var dep map[string]any
	e := a.store.db.Table("asset_depreciation").
		Where("asset_id=? AND tenant_id=? AND deleted_at IS NULL", assetID, sc.Tenant).
		Take(&dep).Error
	if e != nil && !errors.Is(e, gorm.ErrRecordNotFound) {
		fail(c, 500, "折旧查询失败")
		return
	}
	if e != nil {
		// 无折旧记录时，返回空数据
		ok(c, gin.H{
			"asset_id":                 assetID,
			"has_depreciation":         false,
			"purchase_cost":            nil,
			"salvage_value":            nil,
			"useful_life_months":       nil,
			"monthly_depreciation":     nil,
			"accumulated_depreciation": nil,
			"net_value":                nil,
			"start_date":               nil,
			"status":                   nil,
		})
		return
	}
	calculateDepreciation(dep)
	dep["has_depreciation"] = true
	ok(c, dep)
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
	now := time.Now().UTC()
	// Deterministic calendar-month fraction, shared by list and detail. Use dates
	// only so the calculation never depends on the browser's local clock/timezone.
	months := float64(now.Year()-start.Year())*12 + float64(now.Month()-start.Month())
	months += float64(now.Day())/float64(daysInMonth(now)) - float64(start.Day())/float64(daysInMonth(start))
	return math.Max(months, 0)
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
	alertDeadline := time.Now().UTC().AddDate(0, 0, int(days)).Format("2006-01-02")
	// 查询在告警窗口内到保的资产
	var rows []map[string]any
	e := queryScope(a.store.db, resource{table: "assets"}, sc).
		Select(`id, name, serial_number, manufacturer, model, status, warranty_expiry, (warranty_expiry::date-CURRENT_DATE) AS days_remaining, (`+departmentAccessSQL(sc, "assets.department_id", true)+`) AS can_manage`).
		Where(`tenant_id=? AND project_id=? AND deleted_at IS NULL
			   AND warranty_expiry IS NOT NULL
			   AND warranty_expiry <= ?`,
			sc.Tenant, sc.Project, alertDeadline).
		Order("warranty_expiry ASC").
		Find(&rows).Error
	if e != nil {
		fail(c, 500, "维保告警查询失败")
		return
	}
	// 格式化输出
	type warrantyAlert struct {
		AssetID        int64  `json:"asset_id"`
		CanManage      bool   `json:"can_manage"`
		Name           string `json:"name"`
		SerialNumber   string `json:"serial_number"`
		Manufacturer   string `json:"manufacturer"`
		Model          string `json:"model"`
		Status         string `json:"status"`
		WarrantyExpiry string `json:"warranty_expiry"`
		DaysRemaining  int64  `json:"days_remaining"`
	}
	alerts := make([]warrantyAlert, 0, len(rows))
	for _, row := range rows {
		alerts = append(alerts, warrantyAlert{
			AssetID:        toInt64(row["id"]),
			CanManage:      row["can_manage"] == true,
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

func calculateDepreciation(dep map[string]any) {
	cost, salvage, months := toFloat64(dep["purchase_cost"]), toFloat64(dep["salvage_value"]), toFloat64(dep["useful_life_months"])
	base := math.Max(cost-salvage, 0)
	monthly := float64(0)
	if months > 0 {
		monthly = base / months
	}
	accumulated := math.Min(base, monthly*calculateUsedMonths(dep["start_date"]))
	// Use the unrounded monthly rate so rounding cannot accumulate over the life.
	dep["monthly_depreciation"] = math.Round(monthly*100) / 100
	dep["accumulated_depreciation"] = math.Round(accumulated*100) / 100
	dep["net_value"] = math.Round(math.Max(cost-accumulated, salvage)*100) / 100
	status := toString(dep["status"])
	if status != "scrapped" && status != "retired" {
		status = "active"
		if accumulated >= base {
			status = "completed"
		}
	}
	dep["status"] = status
}

type depreciationInput struct {
	PurchaseCost     *float64 `json:"purchase_cost"`
	SalvageValue     *float64 `json:"salvage_value"`
	UsefulLifeMonths int      `json:"useful_life_months"`
	StartDate        string   `json:"start_date"`
	Method           string   `json:"depreciation_method"`
	Status           string   `json:"status"`
}

func (in depreciationInput) validate() error {
	if in.PurchaseCost == nil || in.SalvageValue == nil {
		return errors.New("请填写资产原值和残值")
	}
	for _, v := range []float64{*in.PurchaseCost, *in.SalvageValue} {
		if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || v > 9999999999.99 || math.Abs(v*100-math.Round(v*100)) > 0.0001 {
			return errors.New("金额须为非负数，最多两位小数")
		}
	}
	if *in.SalvageValue > *in.PurchaseCost {
		return errors.New("残值不能大于资产原值")
	}
	if in.UsefulLifeMonths < 1 || in.UsefulLifeMonths > 1200 {
		return errors.New("折旧年限须为1至1200个月")
	}
	if _, err := time.Parse("2006-01-02", in.StartDate); err != nil {
		return errors.New("请输入有效的折旧起始日期")
	}
	if in.Method != "" && in.Method != "straight_line" {
		return errors.New("当前支持直线法折旧")
	}
	if in.Status != "" && in.Status != "active" && in.Status != "scrapped" {
		return errors.New("折旧状态无效，是否折完由系统自动计算")
	}
	return nil
}
func (a *App) saveAssetDepreciation(c *gin.Context) {
	sc := contextScope(c)
	id := number(c.Param("id"))
	var in depreciationInput
	if c.ShouldBindJSON(&in) != nil {
		fail(c, 400, "折旧参数无效")
		return
	}
	if err := in.validate(); err != nil {
		fail(c, 400, err.Error())
		return
	}
	err := a.store.db.WithContext(c.Request.Context()).Transaction(func(tx *gorm.DB) error {
		if err := lockDepartments(tx, sc); err != nil {
			return err
		}
		if !(&store{tx}).managesAsset(sc, id) {
			return errAccessDenied
		}
		var asset struct{ ID int64 }
		if err := queryScope(tx, resource{table: "assets"}, sc).Clauses(clause.Locking{Strength: "UPDATE"}).Where("id=?", id).Take(&asset).Error; err != nil {
			return err
		}
		row := map[string]any{"tenant_id": sc.Tenant, "asset_id": id, "purchase_cost": *in.PurchaseCost, "salvage_value": *in.SalvageValue, "useful_life_months": in.UsefulLifeMonths, "start_date": in.StartDate, "depreciation_method": "straight_line", "status": in.Status}
		if in.Status == "" {
			row["status"] = "active"
		}
		if err := tx.Table("asset_depreciation").Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "tenant_id"}, {Name: "asset_id"}}, TargetWhere: clause.Where{Exprs: []clause.Expression{clause.Expr{SQL: "deleted_at IS NULL"}}}, DoUpdates: clause.Assignments(map[string]any{"purchase_cost": *in.PurchaseCost, "salvage_value": *in.SalvageValue, "useful_life_months": in.UsefulLifeMonths, "start_date": in.StartDate, "depreciation_method": "straight_line", "status": row["status"], "monthly_depreciation": nil, "accumulated_depreciation": nil, "net_value": nil})}).Create(&row).Error; err != nil {
			return err
		}
		return a.store.audit(tx, sc, "depreciation:write", "assets", id)
	})
	if err != nil {
		if errors.Is(err, errAccessDenied) {
			fail(c, 403, err.Error())
		} else {
			fail(c, 409, "折旧保存失败")
		}
		return
	}
	a.getAssetDepreciation(c)
}

type lifecycleInput struct {
	PurchaseDate   *string `json:"purchase_date"`
	WarrantyExpiry *string `json:"warranty_expiry"`
	Vendor         string  `json:"vendor"`
}

func (in lifecycleInput) validate() error {
	for _, d := range []*string{in.PurchaseDate, in.WarrantyExpiry} {
		if d != nil {
			if _, e := time.Parse("2006-01-02", *d); e != nil {
				return errors.New("日期格式须为 YYYY-MM-DD；清空请传 null")
			}
		}
	}
	if len(strings.TrimSpace(in.Vendor)) > 255 {
		return errors.New("供应商名称过长")
	}
	if in.PurchaseDate != nil && in.WarrantyExpiry != nil && *in.WarrantyExpiry < *in.PurchaseDate {
		return errors.New("维保到期日期不能早于采购日期")
	}
	return nil
}
func (a *App) saveAssetLifecycle(c *gin.Context) {
	sc := contextScope(c)
	id := number(c.Param("id"))
	var in lifecycleInput
	if c.ShouldBindJSON(&in) != nil {
		fail(c, 400, "采购/维保信息无效")
		return
	}
	if err := in.validate(); err != nil {
		fail(c, 400, err.Error())
		return
	}
	err := a.store.db.WithContext(c.Request.Context()).Transaction(func(tx *gorm.DB) error {
		if err := lockDepartments(tx, sc); err != nil {
			return err
		}
		if !(&store{tx}).managesAsset(sc, id) {
			return errAccessDenied
		}
		r := queryScope(tx, resource{table: "assets"}, sc).Where("id=?", id).Updates(map[string]any{"purchase_date": in.PurchaseDate, "warranty_expiry": in.WarrantyExpiry, "vendor": strings.TrimSpace(in.Vendor), "update_by": sc.User})
		if r.Error != nil {
			return r.Error
		}
		if r.RowsAffected != 1 {
			return gorm.ErrRecordNotFound
		}
		return a.store.audit(tx, sc, "lifecycle:write", "assets", id)
	})
	if err != nil {
		if errors.Is(err, errAccessDenied) {
			fail(c, 403, err.Error())
		} else {
			fail(c, 409, "采购/维保信息保存失败")
		}
		return
	}
	ok(c, gin.H{"id": id})
}
