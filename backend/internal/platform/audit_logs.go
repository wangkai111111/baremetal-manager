package platform

// 唯一权威审计证据表是 003 引入的 audits（应用账户仅可追加，见 migrations/grants.go）：
// 行为用户、请求标识、动作、对象类型/标识、结果 outcome、客户端 IP 与 User-Agent 均已落库。
// 历史实现只写入最小字段，来源信息缺失；本文件补齐带来源信息的写入路径 recordAudit，
// 所有治理类端点（租户审核、风控处置、批量任务、告警屏蔽/认领）统一经该路径留痕。

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/netip"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// auditMeta 承载一次请求的来源信息，用于审计留痕。
type auditMeta struct {
	RequestID string
	ClientIP  string
	UserAgent string
	Audience  []auditAudienceEntry
}

// requestMeta 从请求上下文提取来源信息。client_ip 仅在可解析为合法 IP 时写入，
// 避免伪造的 X-Forwarded-For 触发 inet 列类型错误而使业务事务回滚。
func requestMeta(c *gin.Context, actor int64) auditMeta {
	ip := strings.TrimSpace(c.ClientIP())
	if addr, err := netip.ParseAddr(ip); err != nil {
		ip = ""
	} else {
		ip = addr.Unmap().String()
	}
	ua := strings.TrimSpace(c.GetHeader("User-Agent"))
	if len(ua) > 512 {
		ua = ua[:512]
	}
	return auditMeta{RequestID: fmt.Sprintf("%d-%d", time.Now().UnixNano(), actor), ClientIP: ip, UserAgent: ua}
}

// recordAudit 在给定事务内追加一条带来源信息的审计证据。
// outcome 取值沿用 audits DDL CHECK：success|denied|failed。
func (s *store) recordAudit(tx *gorm.DB, sc scope, meta auditMeta, action, targetType string, targetID any, outcome string) error {
	return s.recordAuditNote(tx, sc, meta, action, targetType, targetID, outcome, nil)
}

// recordAuditNote 在标准审计字段之外附加一段处置详情，写入 after_snapshot 供复核。
// 详情必须是已脱敏的结构化数据，不得包含凭据、密钥或明文个人信息。
func (s *store) recordAuditNote(tx *gorm.DB, sc scope, meta auditMeta, action, targetType string, targetID any, outcome string, note any) error {
	row := auditRow(sc, meta, action, targetType, targetID, outcome)
	// Store only the display allowlist, never resource configuration or secrets.
	// Capturing at write time also preserves names after renames and deletion.
	var display string
	if err := tx.Raw(`SELECT (`+auditDisplaySQL("e")+`)::text FROM
 (SELECT ?::bigint AS tenant_id, ?::bigint AS project_id, ?::bigint AS user_id,
 ?::text AS action, ?::text AS target_type, ?::text AS target_id,
 NULL::jsonb AS after_snapshot, NULL::inet AS client_ip) e`,
		nullableID(sc.Tenant), nullableID(sc.Project), nullableID(sc.User), action, targetType, fmt.Sprint(targetID)).Scan(&display).Error; err != nil {
		return err
	}
	snapshot := map[string]any{"audit_display": json.RawMessage(display)}
	audience := meta.Audience
	var err error
	if audience == nil {
		audience, err = auditAudience(tx, sc, action, targetType, targetID)
	}
	if err != nil {
		return err
	}
	snapshot["audience"] = audience
	if note != nil {
		snapshot["note"] = note
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	row["after_snapshot"] = gorm.Expr("?::jsonb", string(encoded))
	return tx.Table("audits").Create(row).Error
}

// auditRow 组装 audits 行；client_ip 仅在合法 IP 时写入，避免 inet 类型转换失败回滚业务事务。
func auditRow(sc scope, meta auditMeta, action, targetType string, targetID any, outcome string) map[string]any {
	if meta.RequestID == "" {
		meta.RequestID = sc.Audit.RequestID
	}
	if meta.RequestID == "" {
		meta.RequestID = fmt.Sprintf("%d-%d", time.Now().UnixNano(), sc.User)
	}
	if meta.ClientIP == "" {
		meta.ClientIP = sc.Audit.ClientIP
	}
	if meta.UserAgent == "" {
		meta.UserAgent = sc.Audit.UserAgent
	}
	if outcome != "success" && outcome != "denied" && outcome != "failed" {
		outcome = "success"
	}
	row := map[string]any{
		"tenant_id":   nullableID(sc.Tenant),
		"project_id":  nullableID(sc.Project),
		"user_id":     nullableID(sc.User),
		"request_id":  meta.RequestID,
		"action":      action,
		"target_type": targetType,
		"target_id":   strings.TrimSpace(fmt.Sprint(targetID)),
		"outcome":     outcome,
		"user_agent":  meta.UserAgent,
	}
	if ip, err := netip.ParseAddr(meta.ClientIP); err == nil {
		row["client_ip"] = ip.Unmap().String()
	}
	return row
}

// auditQuery 按筛选条件构造审计查询。非平台管理员只能看到本项目作用域内的记录。
func (a *App) auditQuery(c *gin.Context, sc scope) *gorm.DB {
	q := a.store.db.WithContext(c.Request.Context()).Table("audits").Where("audits.deleted_at IS NULL")
	if sc.Project > 0 {
		q = q.Where("audits.project_id=? OR audits.project_id IS NULL", sc.Project)
	}
	if !sc.Admin {
		q = q.Where(auditManagedSQL(sc))
	}
	if department := number(c.Query("department_id")); department > 0 {
		q = q.Where(`EXISTS(SELECT 1 FROM jsonb_array_elements(COALESCE(audits.after_snapshot->'audience','[]'::jsonb)) au WHERE (au->>'tenant_id')::bigint=? AND (au->>'department_id')::bigint IN (WITH RECURSIVE subtree(id) AS (SELECT id FROM departments WHERE id=? AND tenant_id=? UNION SELECT d.id FROM departments d JOIN subtree s ON d.parent_id=s.id WHERE d.tenant_id=?) SELECT id FROM subtree))`, sc.Tenant, department, sc.Tenant, sc.Tenant)
	}
	if operator := strings.TrimSpace(c.Query("operator")); operator != "" {
		if id := number(operator); id > 0 {
			q = q.Where("audits.user_id=?", id)
		} else {
			q = q.Where("("+auditUserSQL("audits.user_id")+" ILIKE ?)", "%"+operator+"%")
		}
	}
	if ip := strings.TrimSpace(c.Query("ip")); ip != "" {
		q = q.Where("host(audits.client_ip) ILIKE ?", "%"+ip+"%")
	}
	if action := strings.TrimSpace(c.Query("action")); action != "" {
		q = q.Where("audits.action ILIKE ?", "%"+action+"%")
	}
	if result := strings.TrimSpace(c.Query("result")); result != "" {
		q = q.Where("audits.outcome=?", result)
	}
	if target := strings.TrimSpace(c.Query("target_type")); target != "" {
		q = q.Where("audits.target_type=?", target)
	}
	if from := parseQueryTime(c.Query("from")); from != nil {
		q = q.Where("audits.occurred_at>=?", *from)
	}
	if to := parseQueryTime(c.Query("to")); to != nil {
		q = q.Where("audits.occurred_at<=?", *to)
	}
	if search := strings.TrimSpace(c.Query("search")); search != "" {
		like := "%" + search + "%"
		q = q.Where(`jsonb_path_query_array((`+scopedAuditDisplaySQL(sc)+`), '$.*')::text ILIKE ?`, like)
	}
	return q
}

// parseQueryTime 解析 RFC3339 或 yyyy-mm-dd 日期参数，返回 UTC 时间指针。
func parseQueryTime(raw string) *time.Time {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		utc := t.UTC()
		return &utc
	}
	if t, err := time.Parse("2006-01-02", raw); err == nil {
		utc := t.UTC()
		return &utc
	}
	return nil
}

// listAuditLogs 返回多维筛选后的审计记录分页列表。
func (a *App) listAuditLogs(c *gin.Context) {
	sc := contextScope(c)
	if !a.store.allowed(sc, "audit:read") {
		fail(c, 403, "permission denied")
		return
	}
	q := a.auditQuery(c, sc)
	var total int64
	if err := q.Count(&total).Error; err != nil {
		fail(c, 500, "audit query failed")
		return
	}
	p, n := page(c)
	items, err := decodeAuditRows(q.Order("audits.id DESC").Limit(n).Offset((p-1)*n), sc)
	if err != nil {
		fail(c, 500, "audit query failed")
		return
	}
	ok(c, gin.H{"items": items, "total": total, "page": p, "page_size": n})
}

// auditExportColumns 是 CSV 导出的列顺序，JSON 导出复用查询结果字段。
var auditExportColumns = []string{"occurred_at", "operator_name", "action_label", "target_type_label", "target_name", "project_name", "scope_names", "description", "outcome", "client_ip"}

// exportAuditLogs 导出审计记录，支持 format=csv|json（默认 json），风格与
// orders/bills 的单对象导出一致：设置 Content-Disposition 附件头后直接输出。
func (a *App) exportAuditLogs(c *gin.Context) {
	sc := contextScope(c)
	if !a.store.allowed(sc, "audit:read") {
		fail(c, 403, "permission denied")
		return
	}
	items, err := decodeAuditRows(a.auditQuery(c, sc).Order("audits.id DESC").Limit(10000), sc)
	if err != nil {
		fail(c, 500, "audit export failed")
		return
	}
	for i, item := range items {
		readable := make(map[string]any, len(auditExportColumns))
		for _, column := range auditExportColumns {
			readable[column] = item[column]
		}
		items[i] = readable
	}
	stamp := time.Now().UTC().Format("20060102150405")
	if strings.EqualFold(strings.TrimSpace(c.Query("format")), "csv") {
		c.Header("Content-Disposition", "attachment; filename=audit_logs_"+stamp+".csv")
		c.Header("Content-Type", "text/csv; charset=utf-8")
		writer := csv.NewWriter(c.Writer)
		headers := make([]string, 0, len(auditExportColumns))
		for _, column := range auditExportColumns {
			headers = append(headers, auditExportLabels[column])
		}
		_ = writer.Write(headers)
		for _, item := range items {
			record := make([]string, 0, len(auditExportColumns))
			for _, column := range auditExportColumns {
				value := exportCell(item[column])
				if column == "outcome" {
					value = map[string]string{"success": "成功", "failed": "失败", "denied": "拒绝"}[value]
				}
				record = append(record, auditCSVCell(value))
			}
			_ = writer.Write(record)
		}
		writer.Flush()
		return
	}
	c.Header("Content-Disposition", "attachment; filename=audit_logs_"+stamp+".json")
	c.Header("Content-Type", "application/json")
	ok(c, gin.H{"items": items, "total": len(items), "generated_at": time.Now().UTC()})
}

// exportCell 将查询结果标量转换为 CSV 单元格文本。
func exportCell(value any) string {
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		return v
	case bool:
		return strconv.FormatBool(v)
	case time.Time:
		return v.UTC().Format(time.RFC3339)
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	default:
		return fmt.Sprint(v)
	}
}

var auditExportLabels = map[string]string{"occurred_at": "发生时间", "operator_name": "操作人", "action_label": "操作", "target_type_label": "对象类型", "target_name": "操作对象", "project_name": "所属项目", "scope_names": "组织 / 部门范围", "description": "说明", "outcome": "结果", "client_ip": "操作 IP"}

func auditCSVCell(value string) string {
	// Spreadsheet clients must display untrusted names as text, never formulas.
	trimmed := strings.TrimLeftFunc(value, unicode.IsSpace)
	if len(trimmed) > 0 && strings.ContainsRune("=+-@", rune(trimmed[0])) {
		return "'" + value
	}
	return value
}
