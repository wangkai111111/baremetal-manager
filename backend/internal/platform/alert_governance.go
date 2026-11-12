package platform

// ============================================================
// 告警治理（两种模式通用）：四级分级、屏蔽命中、认领、历史导出
// ============================================================
//
// 注册路由（routes.go）：
// GET  /api/v1/projects/:projectId/alerts/export          → exportAlerts      → alerts:read
// POST /api/v1/projects/:projectId/alerts/:id/:transition → alertTransition   → alerts:write（新增 claim）
// POST/GET/PUT/DELETE /api/v1/projects/:projectId/alert-shields → 通用资源处理器 → alerts:write / alerts:read
//
// 分级枚举（011_governance_phase56.sql 重建 alerts_severity_check / alert_rules_severity_check）：
//
//	info(0) < warning(1) < critical(2) < emergency(3)
//
// 存量三级数据原样可读，未出现过的 emergency 由规则、人工升级与阈值策略产生。
// 屏蔽语义：命中生效区间内的 alert_shields 规则时**不新建也不刷新**告警活动，
// 仅累计 alert_shields.hit_count/last_hit_at；恢复事件（resolve）不受屏蔽影响，
// 避免屏蔽到期后遗留无法关闭的历史活动。

import (
	"encoding/csv"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// 认领冲突的错误哨兵：映射为不同 HTTP 业务错误码，避免用字符串判定。
var (
	errClaimAlertMissing  = errors.New("alert not found")
	errClaimAlertResolved = errors.New("alert already resolved")
	errClaimAlertTaken    = errors.New("alert claimed by another user")
)

// severityRank 把分级映射为可比较的等级，未知值按最低级处理。
func severityRank(severity string) int {
	switch severity {
	case "warning":
		return 1
	case "critical":
		return 2
	case "emergency":
		return 3
	}
	return 0
}

// validSeverity 判定写入告警/规则的分级是否在 DDL CHECK 白名单内。
func validSeverity(severity string) bool {
	switch severity {
	case "info", "warning", "critical", "emergency":
		return true
	}
	return false
}

// alertShieldHitSQL 单语句完成「匹配一条生效屏蔽规则并累计命中」。
// metric 支持右通配前缀（如 bmc.*）；severity 为空表示不限级别，非空表示最低级别门槛。
const alertShieldHitSQL = `UPDATE alert_shields s SET hit_count=s.hit_count+1, last_hit_at=now()
WHERE s.id=(SELECT m.id FROM alert_shields m
 WHERE m.tenant_id=? AND m.project_id=? AND m.deleted_at IS NULL AND m.status='active'
 AND m.starts_at<=now() AND m.ends_at>now()
 AND (m.asset_id IS NULL OR m.asset_id=?)
 AND (m.metric IS NULL OR m.metric=? OR (right(m.metric,1)='*' AND ? LIKE left(m.metric,length(m.metric)-1)||'%'))
 AND (m.severity IS NULL OR (CASE m.severity WHEN 'info' THEN 0 WHEN 'warning' THEN 1 WHEN 'critical' THEN 2 ELSE 3 END)
  <= (CASE ? WHEN 'info' THEN 0 WHEN 'warning' THEN 1 WHEN 'critical' THEN 2 ELSE 3 END))
 ORDER BY m.id LIMIT 1)
RETURNING s.id`

// alertShieldHit 判定本次告警是否被屏蔽；命中时同步累计命中次数与最近命中时间。
func alertShieldHit(tx *gorm.DB, tenant, project, asset int64, metric, severity string) (bool, error) {
	var ids []int64
	if err := tx.Raw(alertShieldHitSQL, tenant, project, asset, metric, metric, severity).Scan(&ids).Error; err != nil {
		return false, err
	}
	return len(ids) > 0, nil
}

// claimAlert 认领告警（POST /alerts/:id/claim）：只写入 claimed_by/claimed_at，
// 不改变 status 状态机（firing/acknowledged/resolved 仍由 acknowledge、resolve 驱动）。
// 幂等口径：同一用户重复认领直接返回成功并标 already_claimed；
// 已被他人认领、已解决的告警返回 409；并发认领由 claimed_by IS NULL 的 CAS 保证只有一人成功。
func (a *App) claimAlert(c *gin.Context, sc scope) {
	id := number(c.Param("id"))
	if id <= 0 {
		fail(c, 400, "无效的告警ID")
		return
	}
	var in struct {
		Note string `json:"note"`
	}
	if c.Request.ContentLength > 0 {
		if c.ShouldBindJSON(&in) != nil {
			fail(c, 400, "无效的请求体")
			return
		}
	}
	note := strings.TrimSpace(in.Note)
	if len(note) > 512 {
		fail(c, 400, "认领备注长度不能超过512字符")
		return
	}
	type claimRow struct {
		ID        int64
		AssetID   int64 `gorm:"column:asset_id"`
		Status    string
		ClaimedBy *int64     `gorm:"column:claimed_by"`
		ClaimedAt *time.Time `gorm:"column:claimed_at"`
		Name      string     // 当前认领人展示名
	}
	meta := requestMeta(c, sc.User)
	var result claimRow
	e := a.store.db.Transaction(func(tx *gorm.DB) error {
		var alert claimRow
		err := queryScope(tx, resource{table: "alerts"}, sc).
			Select("alerts.id,alerts.asset_id,alerts.status,alerts.claimed_by,alerts.claimed_at,COALESCE(NULLIF(u.name,''),u.username) AS name").
			Joins("LEFT JOIN users u ON u.id=alerts.claimed_by").
			Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("alerts.id=?", id).Take(&alert).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return errClaimAlertMissing
		}
		if err != nil {
			return err
		}
		if alert.Status == "resolved" {
			return errClaimAlertResolved
		}
		if alert.ClaimedBy != nil {
			if *alert.ClaimedBy != sc.User {
				return errClaimAlertTaken
			}
			// 幂等：本人重复认领不写库、不重复产生事件。
			result = alert
			return nil
		}
		res := tx.Table("alerts").Where("id=? AND tenant_id=? AND project_id=? AND claimed_by IS NULL AND deleted_at IS NULL", id, sc.Tenant, sc.Project).
			Updates(map[string]any{"claimed_by": sc.User, "claimed_at": gorm.Expr("now()")})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected != 1 {
			return errClaimAlertTaken
		}
		message := "告警已被当前用户认领"
		if note != "" {
			message = message + "：" + note
		}
		if err := tx.Table("alert_events").Create(map[string]any{"tenant_id": sc.Tenant, "project_id": sc.Project, "alert_id": id, "event_type": "claimed", "message": fmt.Sprintf("Asset #%d: %s", alert.AssetID, message), "create_by": sc.User}).Error; err != nil {
			return err
		}
		if err := a.store.recordAudit(tx, sc, meta, "claim", "alerts", id, "success"); err != nil {
			return err
		}
		now := time.Now().UTC()
		alert.ClaimedBy, alert.ClaimedAt = &sc.User, &now
		result = alert
		return nil
	})
	if e != nil {
		switch {
		case errors.Is(e, errClaimAlertMissing):
			fail(c, 404, "告警不存在")
		case errors.Is(e, errClaimAlertResolved):
			fail(c, 409, "告警已解决，无需认领")
		case errors.Is(e, errClaimAlertTaken):
			fail(c, 409, "该告警已被其他用户认领")
		default:
			fail(c, 500, "告警认领失败")
		}
		return
	}
	ok(c, gin.H{"id": result.ID, "status": result.Status, "claimed_by": result.ClaimedBy,
		"claimed_at": result.ClaimedAt, "claimed_by_name": result.Name,
		"already_claimed": result.Name != ""})
}

// alertExportColumns 是 CSV 导出的列顺序。
var alertExportColumns = []string{"id", "asset_id", "asset_name", "dedup_key", "severity", "status", "message", "first_seen_at", "last_seen_at", "acknowledged_at", "claimed_at", "resolved_at", "tenant_id", "project_id"}

const alertExportSelect = `id, asset_id, severity, status, message, dedup_key, rule_id,
 first_seen_at, last_seen_at, acknowledged_at, acknowledged_by, claimed_at, claimed_by,
 resolved_at, resolved_by, created_at,
 (SELECT ast.name FROM assets ast WHERE ast.id=alerts.asset_id AND ast.tenant_id=alerts.tenant_id
  AND ast.project_id=alerts.project_id AND ast.deleted_at IS NULL) AS asset_name`

// alertExportQuery 组装告警历史筛选，作用域与通用列表一致（运营模式按已交付资产隔离）。
func (a *App) alertExportQuery(c *gin.Context, sc scope) *gorm.DB {
	q := queryScope(a.store.db.WithContext(c.Request.Context()), resource{table: "alerts"}, sc)
	if id := number(c.Query("asset_id")); id > 0 {
		q = q.Where("asset_id=?", id)
	}
	if severity := strings.TrimSpace(c.Query("severity")); severity != "" {
		q = q.Where("severity=?", severity)
	}
	if status := strings.TrimSpace(c.Query("status")); status != "" {
		q = q.Where("status=?", status)
	}
	if dedup := strings.TrimSpace(c.Query("dedup_key")); dedup != "" {
		q = q.Where("dedup_key=?", dedup)
	}
	if from := parseQueryTime(c.Query("from")); from != nil {
		q = q.Where("last_seen_at>=?", *from)
	}
	if to := parseQueryTime(c.Query("to")); to != nil {
		q = q.Where("first_seen_at<=?", *to)
	}
	if search := strings.TrimSpace(c.Query("search")); search != "" {
		q = q.Where("message ILIKE ?", "%"+search+"%")
	}
	return q
}

// exportAlerts 导出告警历史，支持 format=csv|json（默认 json），风格与审计/账单导出一致。
func (a *App) exportAlerts(c *gin.Context) {
	sc := contextScope(c)
	if !a.store.allowed(sc, "alerts:read") {
		fail(c, 403, "permission denied")
		return
	}
	var items []map[string]any
	if err := a.alertExportQuery(c, sc).Select(alertExportSelect).
		Order("first_seen_at DESC, id DESC").Limit(10000).Scan(&items).Error; err != nil {
		fail(c, 500, "alert export failed")
		return
	}
	stamp := time.Now().UTC().Format("20060102150405")
	if strings.EqualFold(strings.TrimSpace(c.Query("format")), "csv") {
		c.Header("Content-Disposition", "attachment; filename=alerts_"+stamp+".csv")
		c.Header("Content-Type", "text/csv; charset=utf-8")
		writer := csv.NewWriter(c.Writer)
		_ = writer.Write(alertExportColumns)
		for _, item := range items {
			record := make([]string, 0, len(alertExportColumns))
			for _, column := range alertExportColumns {
				record = append(record, exportCell(item[column]))
			}
			_ = writer.Write(record)
		}
		writer.Flush()
		return
	}
	c.Header("Content-Disposition", "attachment; filename=alerts_"+stamp+".json")
	c.Header("Content-Type", "application/json")
	ok(c, gin.H{"items": items, "total": len(items), "generated_at": time.Now().UTC()})
}
