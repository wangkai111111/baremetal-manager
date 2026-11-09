package platform

import (
	"time"

	"github.com/gin-gonic/gin"
)

// ============================================================
// 运营看板处理器（运营模式）
// ============================================================
//
// 待注册路由（routes.go）：
// GET /api/v1/projects/:projectId/admin/operations-dashboard → getOperationsDashboard → operations:read

// getOperationsDashboard 返回运营看板聚合统计数据
// 仅运营模式可访问，需要管理员权限
func (a *App) getOperationsDashboard(c *gin.Context) {
	sc := contextScope(c)
	if !sc.Admin {
		fail(c, 403, "需要管理员权限")
		return
	}
	// 验证系统模式为运营模式
	currentMode := a.getCachedSystemMode()
	if currentMode != ModeExternalOperation {
		fail(c, 400, "运营看板仅在运营模式下可用")
		return
	}
	tenantID := sc.Tenant
	// 并行查询各项统计数据
	// 1. 收入趋势（最近30天每日收入）
	revenueTrend := a.calculateRevenueTrend(tenantID, 30)
	// 2. 订单统计
	orderStats := a.calculateOrderStats(tenantID)
	// 3. 用户增长统计
	userGrowth := a.calculateUserGrowth(tenantID)
	// 4. 资源利用率
	resourceUtilization := a.calculateResourceUtilization(sc)
	// 5. 账户总览
	accountOverview := a.calculateAccountOverview(tenantID)
	// 6. 待处理工单
	pendingWorkOrders := a.calculatePendingWorkOrders(tenantID)
	ok(c, gin.H{
		"revenue_trend":        revenueTrend,
		"order_stats":          orderStats,
		"user_growth":          userGrowth,
		"resource_utilization": resourceUtilization,
		"account_overview":     accountOverview,
		"pending_work_orders":  pendingWorkOrders,
		"generated_at":         time.Now(),
	})
}

// dailyRevenue 每日收入数据点
type dailyRevenue struct {
	Date   string  `json:"date"`
	Amount float64 `json:"amount"`
	Count  int64   `json:"count"`
}

// calculateRevenueTrend 计算最近N天的收入趋势
func (a *App) calculateRevenueTrend(tenantID int64, days int) []dailyRevenue {
	var results []dailyRevenue
	now := time.Now()
	startDate := now.AddDate(0, 0, -days)
	a.store.db.Raw(`
		SELECT DATE(paid_at) AS date, 
		       COALESCE(SUM(final_amount), 0) AS amount,
		       COUNT(*) AS count
		FROM orders 
		WHERE tenant_id = ? 
		  AND status IN ('paid', 'completed')
		  AND paid_at >= ?
		  AND deleted_at IS NULL
		GROUP BY DATE(paid_at)
		ORDER BY date ASC
	`, tenantID, startDate).Scan(&results)
	// 填充无数据日期
	if len(results) == 0 {
		results = make([]dailyRevenue, 0)
	}
	return results
}

// orderStatsResult 订单统计结果
type orderStatsResult struct {
	TotalOrders    int64   `json:"total_orders"`
	PendingOrders  int64   `json:"pending_orders"`
	CompletedOrders int64  `json:"completed_orders"`
	TotalRevenue   float64 `json:"total_revenue"`
	ConversionRate float64 `json:"conversion_rate"`
}

// calculateOrderStats 计算订单统计数据
func (a *App) calculateOrderStats(tenantID int64) orderStatsResult {
	var stats orderStatsResult
	// 总订单数
	a.store.db.Table("orders").
		Where("tenant_id=? AND deleted_at IS NULL", tenantID).
		Count(&stats.TotalOrders)
	// 待处理订单
	a.store.db.Table("orders").
		Where("tenant_id=? AND status='pending' AND deleted_at IS NULL", tenantID).
		Count(&stats.PendingOrders)
	// 已完成订单
	a.store.db.Table("orders").
		Where("tenant_id=? AND status='completed' AND deleted_at IS NULL", tenantID).
		Count(&stats.CompletedOrders)
	// 总收入
	a.store.db.Table("orders").
		Where("tenant_id=? AND status IN ('paid','completed') AND deleted_at IS NULL", tenantID).
		Select("COALESCE(SUM(final_amount),0)").
		Scan(&stats.TotalRevenue)
	// 转化率
	if stats.TotalOrders > 0 {
		stats.ConversionRate = float64(stats.CompletedOrders) / float64(stats.TotalOrders) * 100
	}
	return stats
}

// userGrowthResult 用户增长结果
type userGrowthResult struct {
	TotalUsers     int64 `json:"total_users"`
	ActiveUsers    int64 `json:"active_users"`
	NewUsersToday  int64 `json:"new_users_today"`
	NewUsersWeek   int64 `json:"new_users_week"`
	NewUsersMonth  int64 `json:"new_users_month"`
}

// calculateUserGrowth 计算用户增长统计
func (a *App) calculateUserGrowth(tenantID int64) userGrowthResult {
	var result userGrowthResult
	now := time.Now()
	// 总用户数
	a.store.db.Table("users u").
		Joins("JOIN tenant_memberships tm ON tm.user_id=u.id AND tm.tenant_id=? AND tm.deleted_at IS NULL", tenantID).
		Where("u.deleted_at IS NULL").
		Count(&result.TotalUsers)
	// 活跃用户数（最近7天登录过）
	weekAgo := now.AddDate(0, 0, -7)
	a.store.db.Table("users u").
		Joins("JOIN tenant_memberships tm ON tm.user_id=u.id AND tm.tenant_id=? AND tm.deleted_at IS NULL", tenantID).
		Where("u.deleted_at IS NULL AND u.last_login_at >= ?", weekAgo).
		Count(&result.ActiveUsers)
	// 今日新增
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	a.store.db.Table("users u").
		Joins("JOIN tenant_memberships tm ON tm.user_id=u.id AND tm.tenant_id=? AND tm.deleted_at IS NULL", tenantID).
		Where("u.deleted_at IS NULL AND u.created_at >= ?", todayStart).
		Count(&result.NewUsersToday)
	// 本周新增
	weekStart := now.AddDate(0, 0, -7)
	a.store.db.Table("users u").
		Joins("JOIN tenant_memberships tm ON tm.user_id=u.id AND tm.tenant_id=? AND tm.deleted_at IS NULL", tenantID).
		Where("u.deleted_at IS NULL AND u.created_at >= ?", weekStart).
		Count(&result.NewUsersWeek)
	// 本月新增
	monthStart := now.AddDate(0, -1, 0)
	a.store.db.Table("users u").
		Joins("JOIN tenant_memberships tm ON tm.user_id=u.id AND tm.tenant_id=? AND tm.deleted_at IS NULL", tenantID).
		Where("u.deleted_at IS NULL AND u.created_at >= ?", monthStart).
		Count(&result.NewUsersMonth)
	return result
}

// resourceUtilResult 资源利用率
type resourceUtilResult struct {
	TotalAssets    int64   `json:"total_assets"`
	OnlineAssets   int64   `json:"online_assets"`
	OfflineAssets  int64   `json:"offline_assets"`
	UtilizationRate float64 `json:"utilization_rate"`
}

// calculateResourceUtilization 计算资源利用率
func (a *App) calculateResourceUtilization(sc scope) resourceUtilResult {
	var result resourceUtilResult
	// 总资产数
	a.store.db.Table("assets").
		Where("project_id=? AND deleted_at IS NULL", sc.Project).
		Count(&result.TotalAssets)
	// 在线资产
	a.store.db.Table("assets").
		Where("project_id=? AND status='online' AND deleted_at IS NULL", sc.Project).
		Count(&result.OnlineAssets)
	result.OfflineAssets = result.TotalAssets - result.OnlineAssets
	if result.TotalAssets > 0 {
		result.UtilizationRate = float64(result.OnlineAssets) / float64(result.TotalAssets) * 100
	}
	return result
}

// accountOverviewResult 账户总览
type accountOverviewResult struct {
	Balance      float64 `json:"balance"`
	FrozenAmount float64 `json:"frozen_amount"`
	CreditLimit  float64 `json:"credit_limit"`
	TotalPayments float64 `json:"total_payments"`
}

// calculateAccountOverview 计算账户总览
func (a *App) calculateAccountOverview(tenantID int64) accountOverviewResult {
	var result accountOverviewResult
	var account struct {
		Balance      float64 `gorm:"column:balance"`
		FrozenAmount float64 `gorm:"column:frozen_amount"`
		CreditLimit  float64 `gorm:"column:credit_limit"`
	}
	err := a.store.db.Table("accounts").
		Where("tenant_id=? AND deleted_at IS NULL", tenantID).
		Take(&account).Error
	if err == nil {
		result.Balance = account.Balance
		result.FrozenAmount = account.FrozenAmount
		result.CreditLimit = account.CreditLimit
	}
	// 总支付金额
	a.store.db.Table("payments p").
		Joins("JOIN accounts a ON a.id=p.account_id AND a.tenant_id=?", tenantID).
		Where("p.status='success' AND p.deleted_at IS NULL").
		Select("COALESCE(SUM(p.amount),0)").
		Scan(&result.TotalPayments)
	return result
}

// calculatePendingWorkOrders 计算待处理工单数
func (a *App) calculatePendingWorkOrders(tenantID int64) map[string]int64 {
	result := map[string]int64{
		"open":        0,
		"in_progress": 0,
		"total":       0,
	}
	var openCount, inProgressCount int64
	a.store.db.Table("work_orders").
		Where("tenant_id=? AND status='open' AND deleted_at IS NULL", tenantID).
		Count(&openCount)
	a.store.db.Table("work_orders").
		Where("tenant_id=? AND status='in_progress' AND deleted_at IS NULL", tenantID).
		Count(&inProgressCount)
	result["open"] = openCount
	result["in_progress"] = inProgressCount
	result["total"] = openCount + inProgressCount
	return result
}
