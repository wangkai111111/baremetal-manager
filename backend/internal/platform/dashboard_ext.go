package platform

// ============================================================
// B3 资源大盘与指标趋势（两种模式通用）
// ============================================================
//
// 注册路由（routes.go）：
// GET /api/v1/projects/:projectId/dashboard/resources     → resourceDashboard → assets:read
// GET /api/v1/projects/:projectId/dashboard/metrics/trend → metricsTrend      → monitor:read
//
// 数据来源与诚实口径：
//   - 容量（CPU 线程数、内存 GiB）来自 assets.redfish_snapshot，即最近一次采集覆盖写入的快照。
//     平台不保存容量历史，因此大盘只呈现“当前值”，不提供容量趋势曲线。
//   - 传感器时序来自 sensor_records。**该表在本平台此前的运行链路中从未被写入**，
//     自本次改动起，每次 Redfish 采集成功后由 finishCollection → recordSensorSamples
//     落一批 quality='good' 的样本（温度 / 转速 / 功率 / 电压）。
//     所以趋势接口对写入点上线之前的时间区间必然返回空点集，响应 note 会明确说明，
//     不会用快照值或插值伪造历史。
//   - 作用域沿用 queryScope：自用模式按租户+项目；运营模式非平台管理员按已交付订单资产隔离。
//   - 传感器历史按保留期物理清理（默认 90 天，SENSOR_RETENTION_DAYS 可覆盖，0 表示关闭），
//     这是唯一不走软删除约定的表：软删除标记本身会持续膨胀，且趋势查询仍需额外过滤。

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// maxSensorSamplesPerCollection 限制单次采集写入的样本行数，避免异常 BMC 传感器清单拖垮采集事务。
const maxSensorSamplesPerCollection = 200

// sensorMetricFromReading 把 Redfish 读数单位与传感器类别归一为稳定指标名。
// 返回空字符串表示该读数不纳入历史，避免把未知语义写进时序表。
func sensorMetricFromReading(kind, name, unit string) (string, string) {
	k := strings.ToLower(strings.TrimSpace(kind))
	n := strings.ToLower(strings.TrimSpace(name))
	u := strings.ToLower(strings.TrimSpace(unit))
	switch {
	case strings.Contains(u, "rpm"), k == "fan":
		return "fan_rpm", "RPM"
	case strings.Contains(u, "°c"), strings.Contains(u, "celsius"), k == "thermal", k == "temperature", strings.Contains(n, "temp"):
		return "temperature_celsius", "°C"
	case u == "w", strings.Contains(u, "watt"), k == "power", k == "power_supply", strings.Contains(n, "power"):
		return "power_watts", "W"
	case u == "v", strings.Contains(u, "volt"), k == "voltage":
		return "voltage_volts", "V"
	}
	return "", ""
}

// recordSensorSamples 在采集成功后写入传感器历史。
// 解析失败一律按“无样本”处理且不阻断采集收尾：历史写入是旁路能力，不得影响资产状态机。
func recordSensorSamples(tx *gorm.DB, op operation, snapshot json.RawMessage) error {
	if len(snapshot) == 0 {
		return nil
	}
	summary, err := parseComputerSystemSummary(snapshot)
	if err != nil {
		return nil
	}
	sampled := time.Now()
	rows := make([]map[string]any, 0, 64)
	seen := map[string]bool{}
	for _, component := range summary.Components["sensors"] {
		metric, unit := sensorMetricFromReading(component.Kind, component.Name, component.Units)
		if metric == "" {
			continue
		}
		value := component.Reading
		// hardwareComponent.Reading 带 omitempty，0 读数无法与“没有读数”区分，
		// 因此只接受非零读数，或明确带读数单位的读数，杜绝把缺省 0 当作真实采样。
		if value == 0 && strings.TrimSpace(component.Units) == "" {
			continue
		}
		if math.IsNaN(value) || math.IsInf(value, 0) {
			continue
		}
		key := strings.TrimSpace(component.ID)
		if key == "" {
			key = strings.TrimSpace(component.Kind) + ":" + strings.TrimSpace(component.Name)
		}
		if key == "" {
			continue
		}
		if len(key) > 128 {
			key = key[:128]
		}
		// 同一资产同一传感器在一次采集内只保留一条。
		if seen[metric+"|"+key] {
			continue
		}
		seen[metric+"|"+key] = true
		rows = append(rows, map[string]any{
			"tenant_id":  op.TenantID,
			"project_id": op.ProjectID,
			"asset_id":   op.AssetID,
			"sensor_key": key,
			"metric":     metric,
			"value":      value,
			"unit":       unit,
			"quality":    "good",
			"sampled_at": sampled,
		})
		if len(rows) >= maxSensorSamplesPerCollection {
			break
		}
	}
	if len(rows) == 0 {
		return nil
	}
	// 嵌套事务在 GORM 里对应 SAVEPOINT：批量写入失败只回滚样本本身，
	// 不会使采集收尾所在的 PostgreSQL 事务进入 aborted 状态。
	if err := tx.Transaction(func(savepoint *gorm.DB) error {
		return savepoint.Table("sensor_records").Create(rows).Error
	}); err != nil {
		log.Printf("sensor history write skipped: tenant_id=%d project_id=%d asset_id=%d rows=%d", op.TenantID, op.ProjectID, op.AssetID, len(rows))
	}
	return nil
}

// sensorRetentionDays 返回传感器历史保留天数，默认 90 天；SENSOR_RETENTION_DAYS=0 关闭清理。
func sensorRetentionDays() int {
	raw := strings.TrimSpace(os.Getenv("SENSOR_RETENTION_DAYS"))
	if raw == "" {
		return 90
	}
	days, err := strconv.Atoi(raw)
	if err != nil || days < 0 {
		return 90
	}
	return days
}

// lastSensorPurge 记录最近一次清理时间，保证清理每小时最多执行一次。
var lastSensorPurge atomic.Int64

// purgeSensorHistory 分批清理过期传感器历史，由 Scheduler 每小时触发一次。
func (a *App) purgeSensorHistory() {
	days := sensorRetentionDays()
	if days <= 0 {
		return
	}
	now := time.Now().Unix()
	if now-lastSensorPurge.Load() < 3600 {
		return
	}
	lastSensorPurge.Store(now)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	a.store.db.WithContext(ctx).Exec(`DELETE FROM sensor_records WHERE id IN
 (SELECT id FROM sensor_records WHERE sampled_at < now() - make_interval(days => ?) ORDER BY id LIMIT 20000)`, days)
}

// dashboardTotals 是资源大盘的总量口径。
type dashboardTotals struct {
	Assets          int64   `json:"assets"`
	PowerOn         int64   `json:"power_on"`
	PowerOff        int64   `json:"power_off"`
	PowerUnknown    int64   `json:"power_unknown"`
	HealthOK        int64   `json:"health_ok"`
	HealthWarning   int64   `json:"health_warning"`
	HealthCritical  int64   `json:"health_critical"`
	HealthUnknown   int64   `json:"health_unknown"`
	Collected       int64   `json:"collected"`
	CollectFailed   int64   `json:"collect_failed"`
	Stale           int64   `json:"stale"`
	Retired         int64   `json:"retired"`
	Maintenance     int64   `json:"maintenance"`
	ReclaimPending  int64   `json:"reclaim_pending"`
	MonitoringOn    int64   `json:"monitoring_enabled"`
	CpuThreads      int64   `json:"cpu_threads"`
	MemoryGiB       float64 `json:"memory_gib"`
	ActiveWindows   int64   `json:"active_maintenance_windows"`
}

// numericSelect 把 JSONB 文本安全转成 numeric：只有纯数字文本才参与聚合。
// 表达式全部来自下方固定白名单，不含任何用户输入。
func numericSelect(expression string) string {
	return `COALESCE(CASE WHEN (` + expression + `)::text ~ '^-?[0-9]+(\.[0-9]+)?$' THEN (` + expression + `)::text::numeric ELSE 0 END,0)`
}

const cpuThreadsExpr = `redfish_snapshot->'ProcessorSummary'->>'Count'`
const memoryGiBExpr = `redfish_snapshot->'MemorySummary'->>'TotalSystemMemoryGiB'`

var dashboardTotalsSelect = `COUNT(*) AS assets,
 COUNT(*) FILTER (WHERE assets.power_state='on') AS power_on,
 COUNT(*) FILTER (WHERE assets.power_state='off') AS power_off,
 COUNT(*) FILTER (WHERE assets.power_state IS NULL OR assets.power_state NOT IN ('on','off')) AS power_unknown,
 COUNT(*) FILTER (WHERE assets.monitor_health='ok') AS health_ok,
 COUNT(*) FILTER (WHERE assets.monitor_health='warning') AS health_warning,
 COUNT(*) FILTER (WHERE assets.monitor_health='critical') AS health_critical,
 COUNT(*) FILTER (WHERE assets.monitor_health IS NULL OR assets.monitor_health NOT IN ('ok','warning','critical')) AS health_unknown,
 COUNT(*) FILTER (WHERE assets.collected_at IS NOT NULL) AS collected,
 COUNT(*) FILTER (WHERE COALESCE(assets.collection_error,'')<>'') AS collect_failed,
 COUNT(*) FILTER (WHERE assets.collected_at IS NULL OR assets.collected_at < now() - interval '30 minutes') AS stale,
 COUNT(*) FILTER (WHERE assets.status='retired') AS retired,
 COUNT(*) FILTER (WHERE assets.status='maintenance') AS maintenance,
 COUNT(*) FILTER (WHERE COALESCE(assets.subscription_state,'legacy')='reclaim_pending') AS reclaim_pending,
 COUNT(*) FILTER (WHERE EXISTS (SELECT 1 FROM asset_monitor_configs mc WHERE mc.tenant_id=assets.tenant_id
   AND mc.project_id=assets.project_id AND mc.asset_id=assets.id AND mc.deleted_at IS NULL AND mc.enabled)) AS monitoring_enabled,
 COALESCE(SUM(` + numericSelect(cpuThreadsExpr) + `),0)::bigint AS cpu_threads,
 COALESCE(SUM(` + numericSelect(memoryGiBExpr) + `),0)::float8 AS memory_gib`

// resourceDashboard 返回项目级资源大盘：容量、健康、告警、任务与指标覆盖度。
func (a *App) resourceDashboard(c *gin.Context) {
	sc := contextScope(c)
	db := a.store.db.WithContext(c.Request.Context())
	var totals dashboardTotals
	if err := queryScope(db, resource{table: "assets"}, sc).Select(dashboardTotalsSelect).Scan(&totals).Error; err != nil {
		fail(c, 500, "resource dashboard unavailable")
		return
	}
	if err := queryScope(db, resource{table: "maintenance_windows"}, sc).
		Where("starts_at<=now() AND ends_at>now()").Count(&totals.ActiveWindows).Error; err != nil {
		fail(c, 500, "resource dashboard unavailable")
		return
	}
	byDatacenter, err := a.dashboardByDatacenter(sc)
	if err != nil {
		fail(c, 500, "resource dashboard unavailable")
		return
	}
	alerts, openAlerts, unclaimed := a.dashboardAlerts(sc)
	tasks, failedTasks := a.dashboardTasks(sc)
	ok(c, gin.H{
		"generated_at":  time.Now().UTC(),
		"mode":          currentSystemMode(db),
		"scope":         gin.H{"tenant_id": sc.Tenant, "project_id": sc.Project},
		"totals":        totals,
		"by_datacenter": byDatacenter,
		"alerts": gin.H{
			"open":               openAlerts,
			"unclaimed":          unclaimed,
			"by_severity":        alerts,
			"active_shields":     a.dashboardActiveShields(sc),
			"failed_tasks_24h":   failedTasks,
		},
		"tasks":   gin.H{"by_status_24h": tasks},
		"metrics": a.dashboardMetrics(sc),
		"note": "容量取自最近一次 Redfish 采集快照，无历史序列；传感器趋势样本自本次写入点上线后开始积累，" +
			"上线前的时间区间返回空点集。",
	})
}

// dashboardByDatacenter 按机房聚合资产与健康分布。
func (a *App) dashboardByDatacenter(sc scope) ([]gin.H, error) {
	var rows []struct {
		DatacenterID   *int64  `gorm:"column:datacenter_id"`
		DatacenterName string  `gorm:"column:datacenter_name"`
		Total          int64   `gorm:"column:total"`
		PowerOn        int64   `gorm:"column:power_on"`
		Warning        int64   `gorm:"column:warning"`
		MemoryGiB      float64 `gorm:"column:memory_gib"`
	}
	// 机房名用相关子查询，不能 JOIN datacenters：queryScope 的隔离条件使用不带限
	// 定符的 tenant_id/project_id/deleted_at，多表 JOIN 会触发 ambiguous column。
	err := queryScope(a.store.db, resource{table: "assets"}, sc).
		Select(`assets.datacenter_id,
 COALESCE((SELECT dc.name FROM datacenters dc WHERE dc.id=assets.datacenter_id
   AND dc.tenant_id=assets.tenant_id AND dc.project_id=assets.project_id AND dc.deleted_at IS NULL),'未分配') AS datacenter_name,
 COUNT(*) AS total,
 COUNT(*) FILTER (WHERE assets.power_state='on') AS power_on,
 COUNT(*) FILTER (WHERE assets.monitor_health IN ('warning','critical')) AS warning,
 COALESCE(SUM(` + numericSelect("assets."+memoryGiBExpr) + `),0)::float8 AS memory_gib`).
		Group("1, 2").Order("total DESC, assets.datacenter_id").Limit(100).Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make([]gin.H, 0, len(rows))
	for _, row := range rows {
		out = append(out, gin.H{"datacenter_id": row.DatacenterID, "datacenter_name": row.DatacenterName,
			"total": row.Total, "power_on": row.PowerOn, "warning": row.Warning, "memory_gib": row.MemoryGiB})
	}
	return out, nil
}

// dashboardAlerts 统计活动告警的分级分布、活动总数与未认领数。
func (a *App) dashboardAlerts(sc scope) ([]gin.H, int64, int64) {
	var rows []struct {
		Severity string `gorm:"column:severity"`
		Count    int64  `gorm:"column:count"`
	}
	_ = queryScope(a.store.db, resource{table: "alerts"}, sc).
		Select("severity, COUNT(*) AS count").Where("status IN ('firing','acknowledged')").
		Group("severity").Scan(&rows).Error
	out := make([]gin.H, 0, len(rows))
	open := int64(0)
	for _, row := range rows {
		out = append(out, gin.H{"severity": row.Severity, "count": row.Count})
		open += row.Count
	}
	sort.Slice(out, func(i, j int) bool {
		left, _ := out[i]["severity"].(string)
		right, _ := out[j]["severity"].(string)
		return severityRank(left) > severityRank(right)
	})
	var unclaimed struct{ Count int64 }
	_ = queryScope(a.store.db, resource{table: "alerts"}, sc).
		Select("COUNT(*) AS count").Where("status='firing' AND claimed_by IS NULL").Scan(&unclaimed).Error
	return out, open, unclaimed.Count
}

// dashboardTasks 统计最近 24 小时操作任务的状态分布与失败数。
func (a *App) dashboardTasks(sc scope) ([]gin.H, int64) {
	var rows []struct {
		Status string `gorm:"column:status"`
		Count  int64  `gorm:"column:count"`
	}
	_ = queryScope(a.store.db, resource{table: "operations"}, sc).
		Select("status, COUNT(*) AS count").Where("created_at >= now() - interval '24 hours'").
		Group("status").Scan(&rows).Error
	out := make([]gin.H, 0, len(rows))
	failed := int64(0)
	for _, row := range rows {
		out = append(out, gin.H{"status": row.Status, "count": row.Count})
		switch row.Status {
		case "failed", "timeout", "unknown":
			failed += row.Count
		}
	}
	return out, failed
}

// dashboardActiveShields 返回当前生效中的告警屏蔽规则数量。
func (a *App) dashboardActiveShields(sc scope) int64 {
	var count int64
	_ = queryScope(a.store.db, resource{table: "alert_shields"}, sc).
		Where("status='active' AND starts_at<=now() AND ends_at>now()").Count(&count).Error
	return count
}

// dashboardMetrics 返回近 7 天传感器指标覆盖度，供趋势页选择指标。
func (a *App) dashboardMetrics(sc scope) []gin.H {
	var rows []struct {
		Metric      string     `gorm:"column:metric"`
		Unit        *string    `gorm:"column:unit"`
		Assets      int64      `gorm:"column:assets"`
		Samples     int64      `gorm:"column:samples"`
		AvgValue    *float64   `gorm:"column:avg_value"`
		LastSampled *time.Time `gorm:"column:last_sampled_at"`
	}
	_ = queryScope(a.store.db, resource{table: "sensor_records"}, sc).
		Select(`metric, MAX(unit) AS unit, COUNT(DISTINCT asset_id) AS assets, COUNT(*) AS samples,
 AVG(value) AS avg_value, MAX(sampled_at) AS last_sampled_at`).
		Where("quality='good' AND sampled_at >= now() - interval '7 days'").
		Group("metric").Order("samples DESC, metric").Limit(50).Scan(&rows).Error
	out := make([]gin.H, 0, len(rows))
	for _, row := range rows {
		out = append(out, gin.H{"metric": row.Metric, "unit": row.Unit, "assets": row.Assets,
			"samples": row.Samples, "avg_value": row.AvgValue, "last_sampled_at": row.LastSampled})
	}
	return out
}

// metricRanges 是趋势接口支持的时间窗与默认分桶（秒）。
var metricRanges = map[string]struct {
	window time.Duration
	bucket int
}{
	"1h":  {time.Hour, 60},
	"6h":  {6 * time.Hour, 300},
	"24h": {24 * time.Hour, 900},
	"7d":  {7 * 24 * time.Hour, 3600},
	"30d": {30 * 24 * time.Hour, 21600},
}

// metricBuckets 是允许的分桶粒度白名单（秒），避免任意 interval 表达式进入 SQL。
var metricBuckets = map[string]int{"1m": 60, "5m": 300, "15m": 900, "30m": 1800, "1h": 3600, "6h": 21600, "12h": 43200, "1d": 86400}

// trendNoDataNote 在空结果时如实说明历史样本缺失原因。
const trendNoDataNote = "所选区间内没有传感器样本：sensor_records 的写入点自本次版本上线，历史区间不会用快照回填。"

// trendRow 是分桶聚合行。
type trendRow struct {
	Metric   string    `gorm:"column:metric"`
	AssetID  int64     `gorm:"column:asset_id"`
	BucketAt time.Time `gorm:"column:bucket_start"`
	Samples  int64     `gorm:"column:samples"`
	Assets   int64     `gorm:"column:assets"`
	AvgValue *float64  `gorm:"column:avg_value"`
	MinValue *float64  `gorm:"column:min_value"`
	MaxValue *float64  `gorm:"column:max_value"`
	Unit     *string   `gorm:"column:unit"`
}

// metricsTrend 返回一个或多个指标的时间分桶聚合序列。
// GET /dashboard/metrics/trend?metric=power_watts,temperature_celsius&range=24h
//
//	&from=&to=&bucket=15m&asset_id=&group_by=asset
func (a *App) metricsTrend(c *gin.Context) {
	sc := contextScope(c)
	requested := strings.TrimSpace(c.Query("metric"))
	if requested == "" {
		fail(c, 400, "必须指定 metric，取值见 /dashboard/resources 返回的 metrics 字段")
		return
	}
	metrics := make([]string, 0, 5)
	for _, item := range strings.Split(requested, ",") {
		item = strings.ToLower(strings.TrimSpace(item))
		if item == "" {
			continue
		}
		if !validMetricName(item) {
			fail(c, 400, "无效的指标名："+item)
			return
		}
		duplicate := false
		for _, known := range metrics {
			if known == item {
				duplicate = true
			}
		}
		if !duplicate {
			metrics = append(metrics, item)
		}
	}
	if len(metrics) == 0 || len(metrics) > 5 {
		fail(c, 400, "指标数量须在 1-5 之间")
		return
	}
	to := time.Now().UTC()
	if parsed := parseQueryTime(c.Query("to")); parsed != nil {
		to = *parsed
	}
	from, bucket := to.Add(-24*time.Hour), 900
	if preset, known := metricRanges[strings.ToLower(strings.TrimSpace(c.DefaultQuery("range", "24h")))]; known {
		from, bucket = to.Add(-preset.window), preset.bucket
	}
	if parsed := parseQueryTime(c.Query("from")); parsed != nil {
		from = *parsed
	}
	rawBucket := strings.ToLower(strings.TrimSpace(c.Query("bucket")))
	if rawBucket != "" {
		value, known := metricBuckets[rawBucket]
		if !known {
			fail(c, 400, "无效的分桶粒度，仅支持 1m/5m/15m/30m/1h/6h/12h/1d")
			return
		}
		bucket = value
	}
	span := to.Sub(from)
	if span <= 0 || span > 120*24*time.Hour {
		fail(c, 400, "时间区间无效，最长支持 120 天")
		return
	}
	if int64(span/time.Second)/int64(bucket) > 2000 {
		fail(c, 400, "分桶过多，请放大 bucket 或缩短时间区间")
		return
	}
	perAsset := strings.EqualFold(strings.TrimSpace(c.Query("group_by")), "asset")
	var rows []trendRow
	if err := a.trendQuery(sc, metrics, number(c.Query("asset_id")), from, to, bucket, perAsset).
		Order("metric, asset_id, bucket_start").Limit(5000).Scan(&rows).Error; err != nil {
		fail(c, 500, "指标趋势查询失败")
		return
	}
	out := gin.H{
		"metrics": metrics, "bucket_seconds": bucket, "from": from, "to": to,
		"point_count": len(rows),
	}
	if raw := strings.ToLower(strings.TrimSpace(c.Query("range"))); raw != "" {
		if _, known := metricRanges[raw]; known {
			out["range"] = raw
		}
	}
	names := map[int64]string{}
	if perAsset {
		names = a.trendAssetNames(sc, rows)
		grouped := map[string][]gin.H{}
		for _, row := range rows {
			grouped[row.Metric] = append(grouped[row.Metric], gin.H{
				"asset_id": row.AssetID, "asset_name": names[row.AssetID], "bucket_start": row.BucketAt,
				"samples": row.Samples, "avg_value": row.AvgValue, "min_value": row.MinValue,
				"max_value": row.MaxValue, "unit": row.Unit,
			})
		}
		series := make([]gin.H, 0, len(metrics))
		for _, metric := range metrics {
			points := grouped[metric]
			if points == nil {
				points = []gin.H{}
			}
			series = append(series, gin.H{"metric": metric, "points": points})
		}
		out["group_by"] = "asset"
		out["series"] = series
	} else {
		points := make([]gin.H, 0, len(rows))
		for _, row := range rows {
			item := gin.H{"metric": row.Metric, "bucket_start": row.BucketAt, "samples": row.Samples,
				"assets": row.Assets, "avg_value": row.AvgValue, "min_value": row.MinValue,
				"max_value": row.MaxValue, "unit": row.Unit}
			if row.AssetID > 0 {
				item["asset_id"] = row.AssetID
				item["asset_name"] = names[row.AssetID]
			}
			points = append(points, item)
		}
		out["group_by"] = "metric"
		out["points"] = points
	}
	if len(rows) == 0 {
		out["note"] = trendNoDataNote
	}
	ok(c, out)
}

// trendAssetNames 批量解析趋势序列涉及的资产名，避免在聚合 SQL 里做相关子查询。
func (a *App) trendAssetNames(sc scope, rows []trendRow) map[int64]string {
	ids := make([]int64, 0, len(rows))
	seen := map[int64]bool{}
	for _, row := range rows {
		if row.AssetID > 0 && !seen[row.AssetID] {
			seen[row.AssetID] = true
			ids = append(ids, row.AssetID)
		}
	}
	names := map[int64]string{}
	if len(ids) == 0 {
		return names
	}
	var assets []struct {
		ID   int64
		Name string
	}
	_ = queryScope(a.store.db, resource{table: "assets"}, sc).Select("assets.id,assets.name").
		Where("assets.id IN ?", ids).Limit(len(ids)).Scan(&assets).Error
	for _, asset := range assets {
		names[asset.ID] = asset.Name
	}
	for _, id := range ids {
		if _, known := names[id]; !known {
			names[id] = fmt.Sprintf("#%d", id)
		}
	}
	return names
}

// trendQuery 组装分桶聚合查询。bucket 秒数来自白名单，作为绑定参数下发；
// 非按资产分组时 asset_id 恒为 0，让上层复用同一个结果结构。
func (a *App) trendQuery(sc scope, metrics []string, assetID int64, from, to time.Time, bucket int, perAsset bool) *gorm.DB {
	q := queryScope(a.store.db, resource{table: "sensor_records"}, sc).
		Where("sensor_records.quality='good' AND sensor_records.metric IN ?", metrics).
		Where("sensor_records.sampled_at>=? AND sensor_records.sampled_at<?", from, to)
	if assetID > 0 {
		q = q.Where("sensor_records.asset_id=?", assetID)
	}
	bucketExpr := "to_timestamp(floor(extract(epoch FROM sensor_records.sampled_at)/?)*?) AT TIME ZONE 'UTC'"
	if perAsset {
		return q.Select(bucketExpr+" AS bucket_start, sensor_records.asset_id, sensor_records.metric, "+
			"MAX(sensor_records.unit) AS unit, COUNT(*) AS samples, 1 AS assets, "+
			"AVG(sensor_records.value) AS avg_value, MIN(sensor_records.value) AS min_value, MAX(sensor_records.value) AS max_value",
			bucket, bucket).Group("1, sensor_records.asset_id, sensor_records.metric")
	}
	return q.Select(bucketExpr+" AS bucket_start, 0 AS asset_id, sensor_records.metric, "+
		"MAX(sensor_records.unit) AS unit, COUNT(*) AS samples, COUNT(DISTINCT sensor_records.asset_id) AS assets, "+
		"AVG(sensor_records.value) AS avg_value, MIN(sensor_records.value) AS min_value, MAX(sensor_records.value) AS max_value",
		bucket, bucket).Group("1, asset_id, sensor_records.metric")
}

// validMetricName 只允许已知指标名与 [a-z0-9_] 组合，杜绝把用户输入放到 SQL 标识符位置。
func validMetricName(metric string) bool {
	if len(metric) == 0 || len(metric) > 64 {
		return false
	}
	for _, r := range metric {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' {
			continue
		}
		return false
	}
	return true
}
