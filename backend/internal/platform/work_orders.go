package platform

import (
	"fmt"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ============================================================
// 工单管理处理器（两种模式通用）
// ============================================================
//
// 已注册路由（routes.go，两种模式通用的公共组，不受模式门禁限制）：
// GET    /api/v1/projects/:projectId/work-orders                    → listWorkOrders       → work_orders:read
// POST   /api/v1/projects/:projectId/work-orders                    → createWorkOrder      → work_orders:write
// GET    /api/v1/projects/:projectId/work-orders/:id                → getWorkOrder         → work_orders:read
// PUT    /api/v1/projects/:projectId/work-orders/:id                → updateWorkOrder      → work_orders:write
// PUT    /api/v1/projects/:projectId/work-orders/:id/:transition    → transitionWorkOrder  → work_orders:write
//
// 协作增强端点见 work_order_ext.go：
// PUT    /api/v1/projects/:projectId/work-orders/:id/assign    → assignWorkOrder        → work_orders:write + 客服/管理员
// GET    /api/v1/projects/:projectId/work-orders/:id/messages  → listWorkOrderMessages  → work_orders:read
// POST   /api/v1/projects/:projectId/work-orders/:id/messages  → createWorkOrderMessage → work_orders:write

// 工单状态流转规则
var workOrderTransitions = map[string]map[string]bool{
	"open":        {"in_progress": true, "closed": true},
	"in_progress": {"resolved": true, "closed": true},
	"resolved":    {"closed": true, "in_progress": true}, // 允许重新打开
}

// 合法工单类型
var validWorkOrderTypes = map[string]bool{"fault": true, "technical": true, "consultation": true, "other": true}

// 合法优先级
var validPriorities = map[string]bool{"low": true, "medium": true, "high": true, "urgent": true}

// listWorkOrders 查询工单列表。
// 可见范围：平台管理员或持有 work_orders:manage 的客服角色→租户全量；
// 其余用户→自己提交的 + 指派给自己的工单。
func (a *App) listWorkOrders(c *gin.Context) {
	sc := contextScope(c)
	viewer := a.workOrderAccess(c)
	q := a.store.db.Table("work_orders").
		Where("tenant_id=? AND deleted_at IS NULL", sc.Tenant)
	// 按状态筛选
	if status := strings.TrimSpace(c.Query("status")); status != "" {
		q = q.Where("status=?", status)
	}
	// 按类型筛选
	if woType := strings.TrimSpace(c.Query("type")); woType != "" {
		q = q.Where("type=?", woType)
	}
	// 按优先级筛选
	if priority := strings.TrimSpace(c.Query("priority")); priority != "" {
		q = q.Where("priority=?", priority)
	}
	// 按指派人筛选
	if assignedTo := number(c.Query("assigned_to")); assignedTo > 0 {
		q = q.Where("assigned_to=?", assignedTo)
	}
	// 按提交人筛选
	if userID := number(c.Query("user_id")); userID > 0 {
		// 非管理员且无客服权限时只能查看自己相关的工单
		if !viewer.canAll && userID != sc.User {
			fail(c, 403, "无权查看其他用户的工单")
			return
		}
		q = q.Where("user_id=?", userID)
	} else if !viewer.canAll {
		// 普通用户默认只看到自己提交或被指派的工单
		q = q.Where("(user_id=? OR assigned_to=?)", sc.User, sc.User)
	}
	// 搜索标题
	if search := strings.TrimSpace(c.Query("search")); search != "" {
		q = q.Where("(title ILIKE ? OR description ILIKE ?)", "%"+search+"%", "%"+search+"%")
	}
	var total int64
	if q.Count(&total).Error != nil {
		fail(c, 500, "工单查询失败")
		return
	}
	p, n := page(c)
	var items []map[string]any
	if e := q.Select("id, tenant_id, user_id, type, title, description, status, priority, assigned_to, resolved_at, closed_at, created_at, updated_at").
		Order("CASE priority WHEN 'urgent' THEN 0 WHEN 'high' THEN 1 WHEN 'medium' THEN 2 ELSE 3 END, id DESC").
		Limit(n).Offset((p - 1) * n).
		Find(&items).Error; e != nil {
		fail(c, 500, "工单查询失败")
		return
	}
	ok(c, gin.H{"items": items, "total": total, "page": p, "page_size": n})
}

// getWorkOrder 获取单个工单详情
func (a *App) getWorkOrder(c *gin.Context) {
	sc := contextScope(c)
	id := number(c.Param("id"))
	if id <= 0 {
		fail(c, 400, "无效的工单ID")
		return
	}
	var item map[string]any
	e := a.store.db.Table("work_orders").
		Where("id=? AND tenant_id=? AND deleted_at IS NULL", id, sc.Tenant).
		Take(&item).Error
	if e != nil {
		fail(c, 404, "工单不存在")
		return
	}
	// 可见性与列表保持一致：客服/管理员全量，其余限提交人与被指派人
	if !a.workOrderAccess(c).visible(toInt64(item["user_id"]), workOrderAssignee(item)) {
		fail(c, 403, "无权查看此工单")
		return
	}
	ok(c, item)
}

// workOrderAssignee 从通用查询结果中提取可空的指派人。
func workOrderAssignee(item map[string]any) *int64 {
	raw, present := item["assigned_to"]
	if !present || raw == nil {
		return nil
	}
	value := toInt64(raw)
	if value <= 0 {
		return nil
	}
	return &value
}

// createWorkOrder 创建新工单
func (a *App) createWorkOrder(c *gin.Context) {
	sc := contextScope(c)
	u := current(c)
	var in struct {
		Type        string `json:"type"`
		Title       string `json:"title"`
		Description string `json:"description"`
		Priority    string `json:"priority"`
		AssignedTo  *int64 `json:"assigned_to"`
	}
	if c.ShouldBindJSON(&in) != nil || !validWorkOrderTypes[in.Type] || strings.TrimSpace(in.Title) == "" {
		fail(c, 400, "请提供有效的工单类型和标题")
		return
	}
	if len(in.Title) > 256 {
		fail(c, 400, "工单标题过长")
		return
	}
	// 默认优先级
	if in.Priority == "" {
		in.Priority = "medium"
	}
	if !validPriorities[in.Priority] {
		fail(c, 400, "无效的优先级，仅支持 low/medium/high/urgent")
		return
	}
	// 如果指定了处理人，验证其存在性
	if in.AssignedTo != nil && *in.AssignedTo > 0 {
		var count int64
		a.store.db.Table("users").
			Where("id=? AND status='active' AND deleted_at IS NULL", *in.AssignedTo).
			Count(&count)
		if count == 0 {
			fail(c, 404, "指定的处理人不存在")
			return
		}
	}
	var id int64
	e := a.store.db.Transaction(func(tx *gorm.DB) error {
		row := map[string]any{
			"tenant_id":   sc.Tenant,
			"user_id":     u.ID,
			"type":        in.Type,
			"title":       strings.TrimSpace(in.Title),
			"description": in.Description,
			"priority":    in.Priority,
			"status":      "open",
		}
		if in.AssignedTo != nil && *in.AssignedTo > 0 {
			row["assigned_to"] = *in.AssignedTo
		}
		if e := tx.Table("work_orders").Clauses(clause.Returning{Columns: []clause.Column{{Name: "id"}}}).Create(&row).Error; e != nil {
			return e
		}
		id = asInt(row["id"])
		return a.store.audit(tx, sc, "create", "work_orders", id)
	})
	if e != nil {
		fail(c, 500, "工单创建失败")
		return
	}
	ok(c, gin.H{"id": id})
}

// updateWorkOrder 更新工单信息
func (a *App) updateWorkOrder(c *gin.Context) {
	sc := contextScope(c)
	viewer := a.workOrderAccess(c)
	id := number(c.Param("id"))
	if id <= 0 {
		fail(c, 400, "无效的工单ID")
		return
	}
	// 验证工单存在
	var existing struct {
		ID     int64
		Status string
		UserID int64 `gorm:"column:user_id"`
	}
	e := a.store.db.Table("work_orders").
		Select("id, status, user_id").
		Where("id=? AND tenant_id=? AND deleted_at IS NULL", id, sc.Tenant).
		Take(&existing).Error
	if e != nil {
		fail(c, 404, "工单不存在")
		return
	}
	// 无客服权限时只能修改自己的工单
	if !viewer.canAll && existing.UserID != sc.User {
		fail(c, 403, "无权修改此工单")
		return
	}
	var in struct {
		Title       *string `json:"title"`
		Description *string `json:"description"`
		Priority    *string `json:"priority"`
		AssignedTo  *int64  `json:"assigned_to"`
	}
	if c.ShouldBindJSON(&in) != nil {
		fail(c, 400, "无效的请求体")
		return
	}
	updates := map[string]any{}
	if in.Title != nil {
		trimmed := strings.TrimSpace(*in.Title)
		if trimmed == "" {
			fail(c, 400, "工单标题不能为空")
			return
		}
		if len(trimmed) > 256 {
			fail(c, 400, "工单标题过长")
			return
		}
		updates["title"] = trimmed
	}
	if in.Description != nil {
		updates["description"] = *in.Description
	}
	if in.Priority != nil {
		if !validPriorities[*in.Priority] {
			fail(c, 400, "无效的优先级")
			return
		}
		updates["priority"] = *in.Priority
	}
	if in.AssignedTo != nil {
		if *in.AssignedTo > 0 {
			var count int64
			a.store.db.Table("users").
				Where("id=? AND status='active' AND deleted_at IS NULL", *in.AssignedTo).
				Count(&count)
			if count == 0 {
				fail(c, 404, "指定的处理人不存在")
				return
			}
		}
		if *in.AssignedTo > 0 {
			updates["assigned_to"] = *in.AssignedTo
		} else {
			updates["assigned_to"] = nil
		}
	}
	if len(updates) == 0 {
		fail(c, 400, "无更新内容")
		return
	}
	e = a.store.db.Transaction(func(tx *gorm.DB) error {
		res := tx.Table("work_orders").
			Where("id=? AND tenant_id=? AND deleted_at IS NULL", id, sc.Tenant).
			Updates(updates)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		return a.store.audit(tx, sc, "update", "work_orders", id)
	})
	if e != nil {
		fail(c, 500, "工单更新失败")
		return
	}
	ok(c, nil)
}

// transitionWorkOrder 处理工单状态流转
func (a *App) transitionWorkOrder(c *gin.Context) {
	sc := contextScope(c)
	viewer := a.workOrderAccess(c)
	id := number(c.Param("id"))
	transition := strings.TrimSpace(c.Param("transition"))
	if id <= 0 {
		fail(c, 400, "无效的工单ID")
		return
	}
	if transition == "" {
		fail(c, 400, "请指定状态流转动作")
		return
	}
	// 查询当前工单
	var workOrder struct {
		ID     int64
		Status string
		UserID int64 `gorm:"column:user_id"`
	}
	e := a.store.db.Table("work_orders").
		Select("id, status, user_id").
		Where("id=? AND tenant_id=? AND deleted_at IS NULL", id, sc.Tenant).
		Take(&workOrder).Error
	if e != nil {
		fail(c, 404, "工单不存在")
		return
	}
	// 校验状态流转合法性
	allowed, exists := workOrderTransitions[workOrder.Status]
	if !exists || !allowed[transition] {
		fail(c, 400, fmt.Sprintf("不允许从 %s 流转到 %s", workOrder.Status, transition))
		return
	}
	// 状态流转权限：
	// - open → in_progress: 仅管理员或指派人可操作
	// - resolved → closed: 提交者可关闭
	// - in_progress → resolved: 指派人者或管理员可操作
	if !viewer.canAll {
		switch transition {
		case "in_progress":
			// 需要是指派人或管理员
			var wo struct {
				AssignedTo *int64 `gorm:"column:assigned_to"`
			}
			a.store.db.Table("work_orders").Select("assigned_to").Where("id=?", id).Take(&wo)
			if wo.AssignedTo == nil || *wo.AssignedTo != sc.User {
				fail(c, 403, "仅指派的处理人可开始处理工单")
				return
			}
		case "resolved":
			var wo struct {
				AssignedTo *int64 `gorm:"column:assigned_to"`
			}
			a.store.db.Table("work_orders").Select("assigned_to").Where("id=?", id).Take(&wo)
			if wo.AssignedTo == nil || *wo.AssignedTo != sc.User {
				fail(c, 403, "仅指派的处理人可解决工单")
				return
			}
		}
	}
	updates := map[string]any{"status": transition}
	now := time.Now()
	switch transition {
	case "resolved":
		updates["resolved_at"] = now
	case "closed":
		updates["closed_at"] = now
	}
	e = a.store.db.Transaction(func(tx *gorm.DB) error {
		res := tx.Table("work_orders").
			Where("id=? AND tenant_id=? AND status=? AND deleted_at IS NULL", id, sc.Tenant, workOrder.Status).
			Updates(updates)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		return a.store.audit(tx, sc, "transition:"+transition, "work_orders", id)
	})
	if e != nil {
		fail(c, 500, "工单状态流转失败")
		return
	}
	ok(c, gin.H{"id": id, "status": transition})
}
