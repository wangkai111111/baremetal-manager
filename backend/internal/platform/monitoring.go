package platform

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func enqueueAutomaticCollection(tx *gorm.DB, sc scope, asset int64) error {
	key := uuid.NewString()
	row := map[string]any{"tenant_id": sc.Tenant, "project_id": sc.Project, "asset_id": asset, "kind": "collect", "idempotency_key": key, "automatic": true, "timeout_seconds": 100}
	if err := tx.Table("operations").Clauses(clause.Returning{Columns: []clause.Column{{Name: "id"}}}).Create(&row).Error; err != nil {
		return err
	}
	if err := tx.Table("task_outbox").Create(map[string]any{"tenant_id": sc.Tenant, "project_id": sc.Project, "operation_id": asInt(row["id"]), "event_key": key, "event_type": "operation.execute"}).Error; err != nil {
		return err
	}
	return tx.Table("assets").Where("id=? AND tenant_id=? AND project_id=?", asset, sc.Tenant, sc.Project).Update("monitor_next_at", gorm.Expr("now()+interval '5 minutes'")).Error
}

const monitoredAssetSQL = `a.deleted_at IS NULL AND a.status <> 'retired' AND a.bmc_protocol='redfish'
 AND EXISTS (SELECT 1 FROM projects p JOIN tenants t ON t.id=p.tenant_id
 WHERE p.id=a.project_id AND p.tenant_id=a.tenant_id AND p.deleted_at IS NULL AND p.status='active'
 AND t.deleted_at IS NULL AND t.status='active')
 AND NOT EXISTS (SELECT 1 FROM asset_monitor_configs mc WHERE mc.tenant_id=a.tenant_id
 AND mc.project_id=a.project_id AND mc.asset_id=a.id AND mc.deleted_at IS NULL AND NOT mc.enabled)`

func (s *store) scheduleCollections(ctx context.Context) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var assets []struct{ ID, TenantID, ProjectID int64 }
		if err := tx.Raw(`SELECT a.id,a.tenant_id,a.project_id FROM assets a
 WHERE a.monitor_next_at<=now() AND ` + monitoredAssetSQL + `
 AND NOT EXISTS (SELECT 1 FROM operations op WHERE op.tenant_id=a.tenant_id AND op.project_id=a.project_id
 AND op.asset_id=a.id AND op.deleted_at IS NULL AND op.kind='collect' AND op.status IN ('pending','running','retry_wait'))
 ORDER BY a.monitor_next_at,a.id LIMIT 120 FOR UPDATE OF a SKIP LOCKED`).Scan(&assets).Error; err != nil {
			return err
		}
		for _, a := range assets {
			if err := enqueueAutomaticCollection(tx, scope{Tenant: a.TenantID, Project: a.ProjectID}, a.ID); err != nil {
				return err
			}
		}
		return nil
	})
}

func monitorHealth(snapshot json.RawMessage) string {
	var report struct {
		Status *struct{ Health, HealthRollup string }
	}
	if json.Unmarshal(snapshot, &report) != nil || report.Status == nil {
		return "unknown"
	}
	health, rollup := strings.ToLower(strings.TrimSpace(report.Status.Health)), strings.ToLower(strings.TrimSpace(report.Status.HealthRollup))
	for _, state := range []string{"critical", "warning"} {
		if health == state || rollup == state {
			return state
		}
	}
	if (health == "ok" || rollup == "ok") && (health == "" || health == "ok") && (rollup == "" || rollup == "ok") {
		return "ok"
	}
	return "unknown"
}

func finishCollection(tx *gorm.DB, op operation, status, message string, snapshot json.RawMessage) error {
	updates := map[string]any{"collection_error": message}
	if status == "succeeded" {
		var err error
		updates, err = collectionAssetUpdates(snapshot)
		if err != nil {
			return err
		}
	}
	res := tx.Table("assets").Where("id=? AND tenant_id=? AND project_id=? AND deleted_at IS NULL AND status<>'retired' AND bmc_protocol='redfish'", op.AssetID, op.TenantID, op.ProjectID).Updates(updates)
	if res.Error != nil || res.RowsAffected == 0 {
		return res.Error
	}
	// 采集成功后写入传感器历史（B3 趋势数据源）；旁路失败不阻断告警状态机。
	if status == "succeeded" {
		if err := recordSensorSamples(tx, op, snapshot); err != nil {
			return err
		}
	}
	return applyMonitorResult(tx, op, status == "succeeded", snapshot)
}

func applyMonitorResult(tx *gorm.DB, op operation, success bool, snapshot json.RawMessage) error {
	// Serializes state transitions against scheduling and other asset writes.
	var asset struct{ MonitorConsecutiveFailures int }
	q := tx.Table("assets").Where("id=? AND tenant_id=? AND project_id=? AND deleted_at IS NULL AND status<>'retired' AND bmc_protocol='redfish'", op.AssetID, op.TenantID, op.ProjectID)
	if err := q.Session(&gorm.Session{}).Select("monitor_consecutive_failures").Clauses(clause.Locking{Strength: "UPDATE"}).Take(&asset).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		return err
	}
	health, failures := monitorResult(asset.MonitorConsecutiveFailures, success, snapshot)
	if err := q.Updates(map[string]any{"monitor_next_at": gorm.Expr("now()+interval '5 minutes'"), "monitor_last_attempt_at": gorm.Expr("now()"), "monitor_health": health, "monitor_consecutive_failures": failures}).Error; err != nil {
		return err
	}
	if success || failures >= 2 {
		if err := monitorAlert(tx, op, "bmc.collection_failed", "warning", failures, success); err != nil {
			return err
		}
	}
	if success && health != "unknown" {
		return monitorAlert(tx, op, "bmc.health", health, healthLevel(health), health == "ok")
	}
	return nil
}

func monitorResult(failures int, success bool, snapshot json.RawMessage) (string, int) {
	if success {
		return monitorHealth(snapshot), 0
	}
	if failures < 2147483647 {
		failures++
	}
	return "unknown", failures
}

func healthLevel(health string) int {
	switch health {
	case "warning":
		return 1
	case "critical":
		return 2
	}
	return 0
}

func monitorAlert(tx *gorm.DB, op operation, metric, severity string, value int, resolve bool) error {
	// 屏蔽命中时不新建也不刷新活动告警；恢复路径必须放行，否则屏蔽期内产生的历史无法关闭。
	if !resolve {
		shielded, err := alertShieldHit(tx, op.TenantID, op.ProjectID, op.AssetID, metric, severity)
		if err != nil {
			return err
		}
		if shielded {
			return nil
		}
	}
	var current struct {
		ID       int64
		Severity string
	}
	err := tx.Table("alerts").Select("id,severity").Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND project_id=? AND asset_id=? AND dedup_key=? AND deleted_at IS NULL AND status IN ('firing','acknowledged')", op.TenantID, op.ProjectID, op.AssetID, metric).Take(&current).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	if resolve && current.ID == 0 {
		return nil
	}
	message := "Redfish hardware health: " + severity
	if metric == "bmc.collection_failed" {
		message = "Redfish collection failed consecutively"
	}
	eventType := "fired"
	switch {
	case resolve:
		eventType, message = "resolved", metric+" recovered"
		if err := tx.Table("alerts").Where("id=?", current.ID).Updates(map[string]any{"status": "resolved", "resolved_at": gorm.Expr("now()")}).Error; err != nil {
			return err
		}
	case current.ID == 0:
		row := map[string]any{"tenant_id": op.TenantID, "project_id": op.ProjectID, "asset_id": op.AssetID, "dedup_key": metric, "severity": severity, "message": message}
		if err := tx.Table("alerts").Clauses(clause.Returning{Columns: []clause.Column{{Name: "id"}}}).Create(&row).Error; err != nil {
			return err
		}
		current.ID = asInt(row["id"])
	default:
		updates := map[string]any{"last_seen_at": gorm.Expr("now()")}
		escalated := severityRank(severity) > severityRank(current.Severity)
		if escalated {
			updates["severity"], updates["message"] = severity, message
		}
		// An active health incident stays one incident even if its severity changes.
		return tx.Table("alerts").Where("id=?", current.ID).Updates(updates).Error
	}
	event := map[string]any{"tenant_id": op.TenantID, "project_id": op.ProjectID, "alert_id": current.ID, "event_type": eventType, "message": fmt.Sprintf("Asset #%d: %s", op.AssetID, message)}
	if err := tx.Table("alert_events").Clauses(clause.Returning{Columns: []clause.Column{{Name: "id"}}}).Create(&event).Error; err != nil {
		return err
	}
	if resolve {
		return tx.Exec(`INSERT INTO notification_deliveries (tenant_id,project_id,alert_event_id,channel_id)
 SELECT DISTINCT d.tenant_id,d.project_id,?::bigint,d.channel_id FROM notification_deliveries d
 JOIN alert_events e ON e.id=d.alert_event_id AND e.tenant_id=d.tenant_id AND e.project_id=d.project_id
 JOIN alert_channels c ON c.id=d.channel_id AND c.tenant_id=d.tenant_id AND c.project_id=d.project_id AND c.enabled AND c.deleted_at IS NULL
 WHERE e.alert_id=? AND e.tenant_id=? AND e.project_id=? AND d.deleted_at IS NULL ON CONFLICT DO NOTHING`, asInt(event["id"]), current.ID, op.TenantID, op.ProjectID).Error
	}
	return queueMonitorDeliveries(tx, op, asInt(event["id"]), metric, severity, value)
}

func queueMonitorDeliveries(tx *gorm.DB, op operation, eventID int64, metric, severity string, value int) error {
	// Rules route the built-in alerts; they don't change the collection/health thresholds.
	return tx.Exec(`INSERT INTO notification_deliveries (tenant_id,project_id,alert_event_id,channel_id)
 SELECT DISTINCT r.tenant_id,r.project_id,?::bigint,c.id FROM alert_rules r
 JOIN alert_rule_channels rc ON rc.tenant_id=r.tenant_id AND rc.project_id=r.project_id AND rc.rule_id=r.id AND rc.deleted_at IS NULL
 JOIN alert_channels c ON c.tenant_id=r.tenant_id AND c.project_id=r.project_id AND c.id=rc.channel_id AND c.deleted_at IS NULL AND c.enabled
 CROSS JOIN (SELECT ?::double precision AS v) sample
 WHERE r.tenant_id=? AND r.project_id=? AND r.deleted_at IS NULL AND r.enabled AND r.metric=? AND r.duration_seconds=0
 -- 规则的 severity 是最低级别门槛：规则级别 <= 告警级别即命中（四级 info<warning<critical<emergency）。
 AND (CASE r.severity WHEN 'info' THEN 0 WHEN 'warning' THEN 1 WHEN 'critical' THEN 2 ELSE 3 END)
  <= (CASE ? WHEN 'info' THEN 0 WHEN 'warning' THEN 1 WHEN 'critical' THEN 2 WHEN 'emergency' THEN 3 ELSE 1 END)
 AND CASE r.operator WHEN 'collection_failed' THEN r.metric='bmc.collection_failed'
 WHEN 'eq' THEN v=r.threshold WHEN 'ne' THEN v<>r.threshold WHEN 'gt' THEN v>r.threshold
 WHEN 'gte' THEN v>=r.threshold WHEN 'lt' THEN v<r.threshold WHEN 'lte' THEN v<=r.threshold ELSE false END
 AND (r.selector='{}'::jsonb OR r.selector = jsonb_build_object('tags',r.selector->'tags')
 AND jsonb_typeof(r.selector->'tags')='array' AND (SELECT COALESCE(jsonb_agg(t.name),'[]'::jsonb) FROM asset_tags at
 JOIN tags t ON t.tenant_id=at.tenant_id AND t.project_id=at.project_id AND t.id=at.tag_id AND t.deleted_at IS NULL
 WHERE at.tenant_id=r.tenant_id AND at.project_id=r.project_id AND at.asset_id=? AND at.deleted_at IS NULL) @> (r.selector->'tags'))
 ON CONFLICT DO NOTHING`, eventID, value, op.TenantID, op.ProjectID, metric, severity, op.AssetID).Error
}
