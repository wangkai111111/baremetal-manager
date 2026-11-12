package platform

// ============================================================
// 工单协作增强（两种模式通用）：客服工作台全量查询 + 指派 + 回复线程
// ============================================================
//
// 注册路由（routes.go，公共组，不受模式门禁限制）：
// PUT  /api/v1/projects/:projectId/work-orders/:id/assign    → assignWorkOrder         → work_orders:write（处理人须为 work_orders:manage 或 admin）
// GET  /api/v1/projects/:projectId/work-orders/:id/messages  → listWorkOrderMessages   → work_orders:read
// POST /api/v1/projects/:projectId/work-orders/:id/messages  → createWorkOrderMessage  → work_orders:write
//
// 可见性口径（与 listWorkOrders/getWorkOrder 统一）：
//   - 平台管理员，或持有 work_orders:manage 的客服/处理人：可见租户全量工单；
//   - 其余用户：仅可见自己提交或被指派给自己的工单。
// 工单消息表 work_order_messages 由 011_governance_phase56.sql 引入。

import (
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// workOrderViewer 返回工单可见性判定与上下文。
type workOrderViewer struct {
	scope  scope
	canAll bool
	db     *gorm.DB
}

// workOrderAccess 判定当前用户对工单的可见范围：平台管理员或 work_orders:manage 为全量。
func (a *App) workOrderAccess(c *gin.Context) workOrderViewer {
	sc := contextScope(c)
	return workOrderViewer{scope: sc, canAll: sc.Admin || a.store.allowed(sc, "work_orders:manage"), db: a.store.db}
}

// visible 判断某工单对该用户是否可见（提交人或处理人始终可见自己的单）。
func (v workOrderViewer) visible(userID int64, assignedTo *int64) bool {
	if v.canAll || userID == v.scope.User {
		return true
	}
	return assignedTo != nil && *assignedTo == v.scope.User
}

// workOrderSummary 是协作端点所需的最小工单快照。
type workOrderSummary struct {
	ID         int64
	TenantID   int64 `gorm:"column:tenant_id"`
	UserID     int64 `gorm:"column:user_id"`
	AssignedTo *int64
	Status     string
	Title      string
}

// load 读取工单关键列并做可见性校验，失败时已写出响应。
func (v workOrderViewer) load(c *gin.Context, id int64) (workOrderSummary, bool) {
	var row workOrderSummary
	if err := v.db.Table("work_orders").
		Select("id,tenant_id,user_id,assigned_to,status,title").
		Where("id=? AND tenant_id=? AND deleted_at IS NULL", id, v.scope.Tenant).
		Take(&row).Error; err != nil {
		fail(c, 404, "工单不存在")
		return row, false
	}
	if !v.visible(row.UserID, row.AssignedTo) {
		fail(c, 403, "无权查看此工单")
		return row, false
	}
	return row, true
}

// assignWorkOrder 指派或取消指派工单处理人（PUT /work-orders/:id/assign）。
// 仅平台管理员或持有 work_orders:manage 的客服可指派；被指派人必须是同租户启用用户。
func (a *App) assignWorkOrder(c *gin.Context) {
	viewer := a.workOrderAccess(c)
	sc := viewer.scope
	if !viewer.canAll {
		fail(c, 403, "仅平台管理员或客服角色可指派工单")
		return
	}
	id := number(c.Param("id"))
	if id <= 0 {
		fail(c, 400, "无效的工单ID")
		return
	}
	var in struct {
		AssignedTo *int64 `json:"assigned_to"`
		Note       string `json:"note"`
	}
	if c.ShouldBindJSON(&in) != nil || in.AssignedTo == nil {
		fail(c, 400, "必须提供 assigned_to（0 或 null 表示取消指派）")
		return
	}
	note := strings.TrimSpace(in.Note)
	if len(note) > 512 {
		fail(c, 400, "指派说明长度不能超过512字符")
		return
	}
	var workOrder struct {
		ID     int64
		UserID int64 `gorm:"column:user_id"`
		Status string
	}
	if err := a.store.db.Table("work_orders").Select("id,user_id,status").
		Where("id=? AND tenant_id=? AND deleted_at IS NULL", id, sc.Tenant).Take(&workOrder).Error; err != nil {
		fail(c, 404, "工单不存在")
		return
	}
	if workOrder.Status == "closed" {
		fail(c, 409, "已关闭的工单不可再指派")
		return
	}
	var assignedID *int64
	if in.AssignedTo != nil && *in.AssignedTo > 0 {
		var profile struct {
			ID       int64
			Username string
			Name     string
		}
		err := a.store.db.Table("users u").Select("u.id,u.username,u.name").
			Joins("JOIN tenant_memberships tm ON tm.user_id=u.id AND tm.tenant_id=? AND tm.deleted_at IS NULL", sc.Tenant).
			Where("u.id=? AND u.status='active' AND u.deleted_at IS NULL", *in.AssignedTo).Take(&profile).Error
		if err != nil {
			fail(c, 404, "指定的处理人不存在或不属于当前租户")
			return
		}
		assignedID = in.AssignedTo
	}
	meta := requestMeta(c, sc.User)
	e := a.store.db.Transaction(func(tx *gorm.DB) error {
		res := tx.Table("work_orders").Where("id=? AND tenant_id=? AND deleted_at IS NULL", id, sc.Tenant).
			Updates(map[string]any{"assigned_to": assignedID, "update_by": sc.User})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		// 指派同步落一条处理人消息，保证客服工作台的时间线完整。
		if assignedID != nil {
			var assignee struct{ Name, Username string }
			tx.Table("users").Select("name,username").Where("id=?", *assignedID).Take(&assignee)
			display := strings.TrimSpace(assignee.Name + " " + assignee.Username)
			message := "工单已指派给 " + display
			if note != "" {
				message += "：" + note
			}
			if err := tx.Table("work_order_messages").Clauses(clause.Returning{Columns: []clause.Column{{Name: "id"}}}).
				Create(map[string]any{"tenant_id": sc.Tenant, "work_order_id": id, "user_id": sc.User, "content": message, "is_support": true, "create_by": sc.User}).Error; err != nil {
				return err
			}
		}
		return a.store.recordAudit(tx, sc, meta, "assign", "work_orders", id, "success")
	})
	if e != nil {
		fail(c, 500, "工单指派失败")
		return
	}
	out := gin.H{"id": id, "assigned_to": nil, "assigned_at": nil}
	if assignedID != nil {
		out["assigned_to"] = *assignedID
		out["assigned_at"] = time.Now().UTC()
	}
	ok(c, out)
}

// listWorkOrderMessages 返回工单回复线程（升序，含指派系统消息）。
func (a *App) listWorkOrderMessages(c *gin.Context) {
	viewer := a.workOrderAccess(c)
	id := number(c.Param("id"))
	if id <= 0 {
		fail(c, 400, "无效的工单ID")
		return
	}
	if _, pass := viewer.load(c, id); !pass {
		return
	}
	q := a.store.db.Table("work_order_messages m").
		Joins("LEFT JOIN users u ON u.id=m.user_id").
		Where("m.work_order_id=? AND m.tenant_id=? AND m.deleted_at IS NULL", id, viewer.scope.Tenant)
	var total int64
	if err := q.Count(&total).Error; err != nil {
		fail(c, 500, "工单消息查询失败")
		return
	}
	p, n := page(c)
	var items []map[string]any
	if err := q.Select("m.id, m.work_order_id, m.user_id, m.content, m.is_support, m.created_at, m.updated_at, u.username AS author_username, u.name AS author_name").
		Order("m.id ASC").Limit(n).Offset((p - 1) * n).Scan(&items).Error; err != nil {
		fail(c, 500, "工单消息查询失败")
		return
	}
	ok(c, gin.H{"items": items, "total": total, "page": p, "page_size": n})
}

// createWorkOrderMessage 追加一条工单回复。提交人回复 is_support=false，
// 平台管理员/客服/处理人回复 is_support=true。回复不改变工单状态。
func (a *App) createWorkOrderMessage(c *gin.Context) {
	viewer := a.workOrderAccess(c)
	sc := viewer.scope
	id := number(c.Param("id"))
	if id <= 0 {
		fail(c, 400, "无效的工单ID")
		return
	}
	workOrder, pass := viewer.load(c, id)
	if !pass {
		return
	}
	var in struct {
		Content string `json:"content"`
	}
	if c.ShouldBindJSON(&in) != nil {
		fail(c, 400, "无效的请求体")
		return
	}
	content := strings.TrimSpace(in.Content)
	if content == "" {
		fail(c, 400, "回复内容不能为空")
		return
	}
	if len(content) > 8000 {
		fail(c, 400, "回复内容长度不能超过8000字符")
		return
	}
	support := sc.Admin || a.store.allowed(sc, "work_orders:manage") ||
		(workOrder.AssignedTo != nil && *workOrder.AssignedTo == sc.User)
	meta := requestMeta(c, sc.User)
	var messageID int64
	e := a.store.db.Transaction(func(tx *gorm.DB) error {
		row := map[string]any{
			"tenant_id":     sc.Tenant,
			"work_order_id": id,
			"user_id":       sc.User,
			"content":       content,
			"is_support":    support,
			"create_by":     sc.User,
		}
		if err := tx.Table("work_order_messages").Clauses(clause.Returning{Columns: []clause.Column{{Name: "id"}}}).Create(&row).Error; err != nil {
			return err
		}
		messageID = asInt(row["id"])
		// 回复仅追加时间线消息，不改变工单状态；状态流转仍由 :transition 端点负责。
		return a.store.recordAudit(tx, sc, meta, "reply", "work_orders", id, "success")
	})
	if e != nil {
		fail(c, 500, "工单回复失败")
		return
	}
	ok(c, gin.H{"id": messageID, "work_order_id": id, "is_support": support, "created_at": time.Now().UTC()})
}
