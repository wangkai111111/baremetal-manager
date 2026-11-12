package platform

// ============================================================
// B4 任务中心详情与日志（两种模式通用）
// ============================================================
//
// 注册路由（routes.go）：
// GET /api/v1/projects/:projectId/tasks          → listTasks     → tasks:read（沿用通用资源查询，行为与迁移前一致）
// GET /api/v1/projects/:projectId/tasks/:id      → getTask       → tasks:read
// GET /api/v1/projects/:projectId/tasks/:id/logs → listTaskLogs  → tasks:read
//
// 说明：operations 表是唯一任务权威源（task_outbox 只是投递通道，不对前端暴露）。
// 通用列表返回的是裁剪掉 payload/result 的 operations 行；本文件的详情接口在此之上补充
// 资产快照、日志条数与最近日志，日志接口按 sequence/id 升序返回执行流水。
// 幂等键与 result 属于内部字段，一律不出现在响应里。

import (
	"encoding/json"
	"errors"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// taskResource 是 tasks 列表沿用的通用资源定义（与迁移前 resources 白名单同构）。
var taskResource = resource{table: "operations"}

// listTasks 复用通用资源查询，保证既有前端契约不变（items/total/page/page_size）。
func (a *App) listTasks(c *gin.Context) { a.listResource(taskResource)(c) }

// getTask 返回单个任务详情：操作主体 + 资产快照 + 日志统计。
func (a *App) getTask(c *gin.Context) {
	sc := contextScope(c)
	id := number(c.Param("id"))
	if id <= 0 {
		fail(c, 400, "无效的任务ID")
		return
	}
	var item map[string]any
	db := a.store.db.WithContext(c.Request.Context())
	// 资产信息用相关子查询而非 JOIN：queryScope 的隔离条件是不带限定符的
	// tenant_id/project_id/deleted_at，一旦 JOIN assets 就会触发 ambiguous column。
	err := queryScope(db, resource{table: "operations"}, sc).
		Select(`operations.id, operations.tenant_id, operations.project_id, operations.asset_id,
 operations.user_id, operations.kind, operations.status, operations.attempts, operations.max_attempts,
 operations.timeout_seconds, operations.automatic, operations.worker_id, operations.started_at,
 operations.completed_at, operations.created_at, operations.updated_at, operations.error,
 (SELECT jsonb_build_object('id',ast.id,'name',ast.name,'status',ast.status,'power_state',ast.power_state,
    'monitor_health',ast.monitor_health,'bmc_protocol',ast.bmc_protocol)
  FROM assets ast WHERE ast.id=operations.asset_id AND ast.tenant_id=operations.tenant_id
   AND ast.project_id=operations.project_id AND ast.deleted_at IS NULL)::text AS asset,
 (SELECT COUNT(*) FROM operation_logs l WHERE l.operation_id=operations.id AND l.deleted_at IS NULL) AS log_count`).
		Where("operations.id=?", id).Take(&item).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		fail(c, 404, "任务不存在")
		return
	}
	if err != nil {
		fail(c, 500, "任务查询失败")
		return
	}
	// asset 以 jsonb 文本返回，解析失败时保留原值，不影响主详情。
	var assetJSON []byte
	switch asset := item["asset"].(type) {
	case string:
		assetJSON = []byte(asset)
	case []byte:
		assetJSON = asset
	}
	var asset any
	if json.Unmarshal(assetJSON, &asset) == nil {
		item["asset"] = asset
	}
	// 最近 5 条日志随详情返回，前端首屏无需再拉一次列表。
	logs, e := a.taskLogRows(sc, id, 5, 0)
	if e != nil {
		logs = []gin.H{}
	}
	if logs == nil {
		logs = []gin.H{}
	}
	item["recent_logs"] = logs
	ok(c, item)
}

// taskLogRows 读取任务日志（ascending by sequence,id）。
func (a *App) taskLogRows(sc scope, operationID int64, limit int, offset int) ([]gin.H, error) {
	var rows []struct {
		ID         int64       `gorm:"column:id"`
		Sequence   int64       `gorm:"column:sequence"`
		Level      string      `gorm:"column:level"`
		Message    string      `gorm:"column:message"`
		OccurredAt interface{} `gorm:"column:occurred_at"`
		CreateBy   *int64      `gorm:"column:create_by"`
	}
	err := queryScope(a.store.db, resource{table: "operation_logs"}, sc).
		Select("id, sequence, level, message, occurred_at, create_by").
		Where("operation_id=?", operationID).
		Order("sequence ASC, id ASC").Limit(limit).Offset(offset).Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make([]gin.H, 0, len(rows))
	for _, row := range rows {
		out = append(out, gin.H{"id": row.ID, "sequence": row.Sequence, "level": row.Level,
			"message": row.Message, "occurred_at": row.OccurredAt, "create_by": row.CreateBy})
	}
	return out, nil
}

// listTaskLogs 分页返回任务执行日志。
func (a *App) listTaskLogs(c *gin.Context) {
	sc := contextScope(c)
	id := number(c.Param("id"))
	if id <= 0 {
		fail(c, 400, "无效的任务ID")
		return
	}
	var exists int64
	if err := queryScope(a.store.db.WithContext(c.Request.Context()), resource{table: "operations"}, sc).
		Where("id=?", id).Count(&exists).Error; err != nil || exists == 0 {
		fail(c, 404, "任务不存在")
		return
	}
	var total int64
	if err := queryScope(a.store.db.WithContext(c.Request.Context()), resource{table: "operation_logs"}, sc).
		Where("operation_id=?", id).Count(&total).Error; err != nil {
		fail(c, 500, "任务日志查询失败")
		return
	}
	p, n := page(c)
	items, err := a.taskLogRows(sc, id, n, (p-1)*n)
	if err != nil {
		fail(c, 500, "任务日志查询失败")
		return
	}
	if items == nil {
		items = []gin.H{}
	}
	ok(c, gin.H{"items": items, "total": total, "page": p, "page_size": n, "task_id": id})
}
