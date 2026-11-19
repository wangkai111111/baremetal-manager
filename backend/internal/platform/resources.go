package platform

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"strings"

	"baremetal-manager/backend/internal/service"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type resource struct {
	path, table, fields, required string
	readonly                      bool
}

// 注册项为固定白名单；路由参数永远不参与表名或列名拼接。
var resources = []resource{
	{"assets", "assets", "name serial_number manufacturer model datacenter_id department_id bmc_address bmc_protocol bmc_port bmc_tls_verify bmc_user bmc_secret management_ip mac_address status purchase_date warranty_expiry vendor", "name bmc_address bmc_user bmc_secret", false},
	{"datacenters", "datacenters", "name location contact", "name", false},
	{"tags", "tags", "name color", "name", false},
	{"asset-tags", "asset_tags", "asset_id tag_id", "asset_id tag_id", false},
	{"networks", "networks", "name subnet gateway vlan_id dns_servers dhcp_enabled", "name subnet", false},
	{"ip-pools", "ip_pools", "network_id name start_address end_address", "network_id name start_address end_address", false},
	{"images", "images", "name os_type version architecture", "name os_type version architecture", false},
	{"image-templates", "image_templates", "name image_id config", "name image_id", false},
	{"kickstart-templates", "kickstart_templates", "name format content revision", "name format content", false},
	{"partition-templates", "partition_templates", "name layout revision", "name layout", false},
	{"post-install-scripts", "post_install_scripts", "name interpreter content sha256 timeout_seconds revision", "name interpreter content sha256", false},
	{"install-plans", "install_plans", "name image_template_id kickstart_template_id partition_template_id network_id config", "name image_template_id kickstart_template_id partition_template_id", false},
	{"install-plan-scripts", "install_plan_scripts", "install_plan_id script_id sequence", "install_plan_id script_id sequence", false},
	{"firmware-packages", "firmware_packages", "name vendor component version compatible_models object_key size_bytes sha256", "name vendor component version compatible_models object_key size_bytes sha256", false},
	{"firmware-baselines", "firmware_baselines", "name component firmware_package_id", "name component firmware_package_id", false},
	{"monitor-configs", "asset_monitor_configs", "asset_id enabled interval_seconds timeout_seconds metrics", "asset_id", false},
	{"alert-rules", "alert_rules", "name metric operator threshold duration_seconds severity enabled selector", "name metric operator severity", false},
	{"alert-channels", "alert_channels", "name kind enabled", "name kind", false},
	{"alert-rule-channels", "alert_rule_channels", "rule_id channel_id", "rule_id channel_id", false},
	{"maintenance-windows", "maintenance_windows", "name starts_at ends_at reason", "name starts_at ends_at reason", false},
	{"maintenance-window-assets", "maintenance_window_assets", "window_id asset_id", "window_id asset_id", false},
	{"hardware", "asset_hardwares", "", "", true}, {"firmware-inventory", "firmware_inventories", "", "", true},
	{"tasks", "operations", "", "", true}, {"operations", "operations", "", "", true}, {"operation-logs", "operation_logs", "", "", true},
	{"installations", "installations", "", "", true}, {"installation-events", "installation_events", "", "", true},
	{"firmware-upgrades", "firmware_upgrades", "", "", true}, {"sensor-records", "sensor_records", "", "", true},
	{"alerts", "alerts", "", "", true}, {"alert-events", "alert_events", "", "", true}, {"notification-deliveries", "notification_deliveries", "", "", true},
	{"discovery-candidates", "discovery_candidates", "", "", true}, {"sol-sessions", "sol_sessions", "", "", true}, {"audit", "audits", "", "", true},

	// 以下资源已迁移至自定义处理器（租户级作用域），不再使用通用 CRUD：
	// departments, work_orders
	// 自定义处理器位于同目录下对应的 .go 文件中
}
var references = map[string]string{"department_id": "departments", "datacenter_id": "datacenters", "asset_id": "assets", "tag_id": "tags", "network_id": "networks", "pool_id": "ip_pools", "image_id": "images", "image_template_id": "image_templates", "kickstart_template_id": "kickstart_templates", "partition_template_id": "partition_templates", "install_plan_id": "install_plans", "script_id": "post_install_scripts", "firmware_package_id": "firmware_packages", "rule_id": "alert_rules", "channel_id": "alert_channels", "window_id": "maintenance_windows"}
var jsonFields = map[string]bool{"config": true, "layout": true, "compatible_models": true, "metrics": true, "selector": true, "detail_json": true}
var hidden = " - 'bmc_secret_ciphertext' - 'bmc_secret_key_id' - 'password_hash' - 'secret_ciphertext' - 'secret_key_id' - 'test_claim_until' - 'callback_token_hash' - 'config_snapshot' - 'redfish_snapshot' - 'snapshot' - 'payload' - 'result' - 'before_snapshot' - 'after_snapshot' - 'subscription_state' - 'lease_end' - 'unsubscribed_at'"

// queryScope enforces organization and project isolation for every resource.
func queryScope(db *gorm.DB, r resource, sc scope) *gorm.DB {
	q := db.Table(r.table).Where("tenant_id=? AND project_id=? AND deleted_at IS NULL", sc.Tenant, sc.Project)
	// Automatic polling is internal queue work, not a user operation or task history.
	switch r.table {
	case "operations":
		q = q.Where("NOT (automatic AND kind='collect')")
	case "operation_logs":
		q = q.Where(`EXISTS (SELECT 1 FROM operations visible_op WHERE visible_op.id=operation_logs.operation_id
          AND visible_op.tenant_id=operation_logs.tenant_id AND visible_op.project_id=operation_logs.project_id
          AND NOT (visible_op.automatic AND visible_op.kind='collect'))`)
	}
	return filterAssetResource(q, r.table, sc)
}
func resourceDocumentSQL(r resource) string {
	doc := "(to_jsonb(" + r.table + ")" + hidden + ")"
	if display := resourceDisplaySQL(r); display != "" {
		doc += " || " + display
	}
	if r.table == "audits" {
		doc += " || " + auditDisplaySQL("audits")
	}
	if r.table == "alert_rules" {
		doc += ` || jsonb_build_object('channel_ids',COALESCE((SELECT jsonb_agg(rc.channel_id ORDER BY rc.channel_id) FROM alert_rule_channels rc WHERE rc.rule_id=alert_rules.id AND rc.tenant_id=alert_rules.tenant_id AND rc.project_id=alert_rules.project_id AND rc.deleted_at IS NULL),'[]'::jsonb))`
	}
	if r.table == "alert_channels" {
		doc += ` || jsonb_build_object('has_secret',COALESCE(secret_key_id,'')<>'')`
	}
	if r.table == "operations" {
		doc += ` || jsonb_build_object(
 'operator_name', ` + auditUserSQL("operations.user_id") + `,
 'client_ip', host(operations.client_ip),
 'bmc_ip', (SELECT host(ast.bmc_address) FROM assets ast
  WHERE ast.tenant_id=operations.tenant_id AND ast.project_id=operations.project_id
   AND ast.id=operations.asset_id AND ast.deleted_at IS NULL),
 'installation_stage', (SELECT inst.status FROM installations inst
  WHERE inst.tenant_id=operations.tenant_id AND inst.project_id=operations.project_id
   AND inst.operation_id=operations.id AND inst.asset_id=operations.asset_id
   AND operations.kind='install' AND inst.deleted_at IS NULL),
 'installation_step', (SELECT s->>'title' FROM installations inst CROSS JOIN LATERAL jsonb_array_elements(inst.progress) s WHERE inst.operation_id=operations.id AND inst.asset_id=operations.asset_id AND inst.tenant_id=operations.tenant_id AND inst.project_id=operations.project_id AND inst.deleted_at IS NULL AND s->>'status'='running' LIMIT 1))`
	}
	return "(" + doc + ")::text AS doc"
}

func decodeRows(db *gorm.DB, r resource) ([]map[string]any, error) {
	var rows []struct{ Doc string }
	e := db.Select(resourceDocumentSQL(r)).Scan(&rows).Error
	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		var m map[string]any
		if json.Unmarshal([]byte(row.Doc), &m) != nil {
			return nil, errors.New("invalid database response")
		}
		out = append(out, m)
	}
	return out, e
}
func page(c *gin.Context) (int, int) {
	p := int(number(c.DefaultQuery("page", "1")))
	n := int(number(c.DefaultQuery("page_size", "20")))
	if p < 1 {
		p = 1
	}
	if p > 1000000 {
		p = 1000000
	}
	if n < 1 {
		n = 20
	}
	if n > 200 {
		n = 200
	}
	return p, n
}

// 资产显示状态由当前运维任务与电源状态推导。
const assetDisplayStatusSQL = `COALESCE((
 SELECT CASE WHEN op.status IN ('pending','retry_wait') THEN 'queued'
  WHEN op.kind='power_on' THEN 'starting'
  WHEN op.kind='power_off' THEN 'stopping'
  WHEN op.kind='power_cycle' THEN 'restarting'
  WHEN op.kind='collect' AND assets.collected_at IS NULL THEN 'managing'
  WHEN op.kind='collect' THEN 'collecting'
  WHEN op.kind='install' THEN 'reinstalling'
  WHEN op.kind='firmware_upgrade' THEN 'upgrading' END
 FROM operations op
 WHERE op.tenant_id=assets.tenant_id AND op.project_id=assets.project_id
  AND op.asset_id=assets.id AND op.deleted_at IS NULL
  AND op.status IN ('running','pending','retry_wait')
  AND op.kind IN ('power_on','power_off','power_cycle','collect','install','firmware_upgrade')
 ORDER BY CASE WHEN op.status='running' THEN 0 ELSE 1 END, op.created_at DESC, op.id DESC LIMIT 1
), CASE
 WHEN assets.status IN ('maintenance','retired') THEN assets.status
 WHEN assets.collected_at IS NULL AND COALESCE(assets.collection_error,'')<>'' THEN 'error'
 WHEN (SELECT op.status='unknown' AND op.created_at>COALESCE(assets.collected_at,assets.created_at)
  FROM operations op
  WHERE op.tenant_id=assets.tenant_id AND op.project_id=assets.project_id
   AND op.asset_id=assets.id AND op.deleted_at IS NULL
   AND op.kind IN ('power_on','power_off','power_cycle')
  ORDER BY op.created_at DESC, op.id DESC LIMIT 1) THEN 'unknown'
 WHEN assets.power_state='off' THEN 'stopped'
 WHEN assets.power_state='on' THEN 'running'
 WHEN assets.collected_at IS NULL AND assets.status='unknown' THEN 'pending_management'
 WHEN assets.status='offline' THEN 'offline'
 ELSE 'unknown' END)`

func assetSearchCondition(field string) (string, error) {
	fields := []string{"id", "name", "bmc_address", "management_ip", "serial_number", "manufacturer", "model", "datacenter"}
	conditions := make([]string, 0, len(fields))
	for _, key := range fields {
		condition := "assets." + key + " ILIKE ?"
		switch key {
		case "id":
			condition = "assets.id::text ILIKE ?"
		case "bmc_address", "management_ip":
			condition = "host(assets." + key + ") ILIKE ?"
		case "datacenter":
			condition = `EXISTS (SELECT 1 FROM datacenters dc WHERE dc.id=assets.datacenter_id
    AND dc.tenant_id=assets.tenant_id AND dc.project_id=assets.project_id
    AND dc.deleted_at IS NULL AND dc.name ILIKE ?)`
		}
		if field == key {
			return condition, nil
		}
		conditions = append(conditions, condition)
	}
	if field == "auto" {
		return "(" + strings.Join(conditions, " OR ") + ")", nil
	}
	return "", errors.New("invalid search_field")
}

func assetListQuery(q *gorm.DB, condition, search, tags, status string, datacenterID int64) *gorm.DB {
	if search = strings.TrimSpace(search); search != "" {
		args := make([]any, strings.Count(condition, "?"))
		for i := range args {
			args[i] = "%" + search + "%"
		}
		q = q.Where(condition, args...)
	}
	if datacenterID > 0 {
		q = q.Where("assets.datacenter_id=?", datacenterID)
	}
	names := []string{}
	for _, name := range strings.Split(tags, ",") {
		if name = strings.TrimSpace(name); name != "" {
			names = append(names, name)
		}
	}
	if len(names) > 0 {
		q = q.Where(`EXISTS (SELECT 1 FROM asset_tags at JOIN tags t ON t.id=at.tag_id
   AND t.tenant_id=at.tenant_id AND t.project_id=at.project_id AND t.deleted_at IS NULL
   WHERE at.asset_id=assets.id AND at.tenant_id=assets.tenant_id
    AND at.project_id=assets.project_id AND at.deleted_at IS NULL AND t.name IN ?)`, names)
	}
	base := q.Select("assets.*, " + assetDisplayStatusSQL + " AS display_status")
	q = q.Session(&gorm.Session{NewDB: true}).Table("(?) AS assets", base)
	if status = strings.TrimSpace(status); status != "" {
		q = q.Where("assets.display_status=?", status)
	}
	return q
}

func (a *App) listResource(r resource) gin.HandlerFunc {
	return func(c *gin.Context) {
		sc := contextScope(c)
		q := queryScope(a.store.db.WithContext(c.Request.Context()), r, sc)
		if network := number(c.Query("network_id")); network > 0 && r.table == "ip_pools" {
			q = q.Where("network_id=?", network)
		}
		if r.table == "images" && c.Query("visibility") == "private" {
			q = q.Where(`NOT EXISTS (SELECT 1 FROM image_download_jobs AS job WHERE job.image_id=images.id AND job.tenant_id=images.tenant_id AND job.project_id=images.project_id)`)
		}
		if id := number(c.Query("asset_id")); id > 0 && (strings.Contains(r.fields, "asset_id") || r.table == "sensor_records" || r.table == "operations" || r.table == "alerts" || r.table == "asset_hardwares" || r.table == "firmware_inventories" || r.table == "sol_sessions") {
			q = q.Where("asset_id=?", id)
		}
		if r.table == "assets" {
			if c.Query("manageable") == "true" {
				q = q.Where(departmentAccessSQL(sc, "assets.department_id", true))
			}
			condition, err := assetSearchCondition(c.DefaultQuery("search_field", "auto"))
			if err != nil {
				fail(c, 400, err.Error())
				return
			}
			q = assetListQuery(q, condition, c.Query("search"), c.Query("tags"), c.Query("status"), number(c.Query("datacenter_id")))
		} else if search := strings.TrimSpace(c.Query("search")); search != "" && strings.Contains(" "+r.fields+" ", " name ") {
			q = q.Where("name ILIKE ?", "%"+search+"%")
		}
		// Filter only known columns; user input is always bound as a value.
		if r.table != "assets" {
			if r.table == "alerts" {
				switch c.Query("resolved") {
				case "false":
					q = q.Where("status <> 'resolved'")
				case "true":
					q = q.Where("status = 'resolved'")
				}
			}
			if status := strings.TrimSpace(c.Query("status")); status != "" && (r.table == "operations" || r.table == "alerts" || r.table == "images") {
				q = q.Where("status=?", status)
			}
			if search := strings.TrimSpace(c.Query("search")); search != "" {
				switch r.table {
				case "operations":
					q = q.Where("concat_ws(' ',kind,status,error,host(operations.client_ip),"+auditUserSQL("operations.user_id")+",(SELECT "+auditAssetSQL("rel")+" FROM assets rel WHERE rel.id=operations.asset_id AND rel.tenant_id=operations.tenant_id AND rel.project_id=operations.project_id)) ILIKE ?", "%"+search+"%")
				case "alerts":
					q = q.Where("concat_ws(' ',message,(SELECT "+auditAssetSQL("rel")+" FROM assets rel WHERE rel.id=alerts.asset_id AND rel.tenant_id=alerts.tenant_id AND rel.project_id=alerts.project_id)) ILIKE ?", "%"+search+"%")
				case "audits":
					q = q.Where(auditSearchSQL("audits"), "%"+search+"%")
				}
			}
		}
		var total int64
		if q.Count(&total).Error != nil {
			fail(c, 500, "resource query failed")
			return
		}
		p, n := page(c)
		items, e := decodeRows(q.Order("id DESC").Limit(n).Offset((p-1)*n), r)
		if e != nil {
			fail(c, 500, "resource query failed")
			return
		}
		if r.table == "assets" {
			managed := []int64{}
			pageIDs := []int64{}
			for _, item := range items {
				pageIDs = append(pageIDs, asInt(item["id"]))
			}
			if err := queryScope(a.store.db, resource{table: "assets"}, sc).Where("id IN ?", pageIDs).Where(departmentAccessSQL(sc, "assets.department_id", true)).Pluck("id", &managed).Error; err != nil {
				fail(c, 500, "资产权限查询失败")
				return
			}
			lookup := map[int64]bool{}
			for _, id := range managed {
				lookup[id] = true
			}
			for _, item := range items {
				item["can_manage"] = lookup[asInt(item["id"])]
			}
		}
		ok(c, gin.H{"items": items, "total": total, "page": p, "page_size": n})
	}
}
func (a *App) getResource(r resource) gin.HandlerFunc {
	return func(c *gin.Context) {
		items, e := decodeRows(queryScope(a.store.db, r, contextScope(c)).Where("id=?", number(c.Param("id"))), r)
		if e != nil {
			fail(c, 500, "resource query failed")
			return
		}
		if len(items) == 0 {
			fail(c, 404, "resource not found")
			return
		}
		if r.table == "assets" {
			items[0]["can_manage"] = a.store.managesAsset(contextScope(c), number(c.Param("id")))
		}
		ok(c, items[0])
	}
}
func sensitive(v any) bool {
	switch x := v.(type) {
	case map[string]any:
		for k, v := range x {
			l := strings.ToLower(k)
			if strings.Contains(l, "password") || strings.Contains(l, "secret") || strings.Contains(l, "token") || strings.Contains(l, "credential") {
				return true
			}
			if sensitive(v) {
				return true
			}
		}
	case []any:
		for _, v := range x {
			if sensitive(v) {
				return true
			}
		}
	}
	return false
}
func inputResource(c *gin.Context, r resource, create bool) (map[string]any, error) {
	var in map[string]any
	c.Request.Body = &limitedBody{ReadCloser: c.Request.Body, remaining: 1 << 20}
	if c.ShouldBindJSON(&in) != nil || len(in) == 0 {
		return nil, errors.New("invalid request body")
	}
	allowed := map[string]bool{}
	for _, k := range strings.Fields(r.fields) {
		allowed[k] = true
	}
	for k, v := range in {
		if !allowed[k] {
			return nil, fmt.Errorf("unknown field: %s", k)
		}
		if str, ok := v.(string); ok && len(str) > 262144 {
			return nil, errors.New("field is too long")
		}
		if k != "bmc_secret" && sensitive(v) {
			return nil, errors.New("plaintext credentials are not allowed")
		}
		if strings.HasSuffix(k, "_id") && v != nil {
			f, ok := v.(float64)
			if !ok || f <= 0 || f != float64(int64(f)) {
				return nil, errors.New("invalid relation identifier")
			}
		}
		if k == "bmc_address" || k == "management_ip" {
			if v != nil {
				str, ok := v.(string)
				if !ok {
					return nil, errors.New("invalid IP address")
				}
				ip, e := netip.ParseAddr(str)
				if e != nil || ip.IsLoopback() || ip.IsUnspecified() || ip.IsMulticast() {
					return nil, errors.New("invalid IP address")
				}
			}
		}
		if k == "mac_address" && v != nil {
			mac, ok := v.(string)
			if !ok {
				return nil, errors.New("装机网卡 MAC 地址无效")
			}
			if _, err := installationTarget("", "", mac); err != nil {
				return nil, err
			}
		}

		if k == "name" {
			s, ok := v.(string)
			if !ok || len(strings.TrimSpace(s)) == 0 || len(s) > 255 {
				return nil, errors.New("invalid name")
			}
		}
		if jsonFields[k] {
			b, e := json.Marshal(v)
			if e != nil {
				return nil, e
			}
			in[k] = gorm.Expr("?::jsonb", string(b))
		}
	}
	if err := normalizeNetworkInput(r.table, in); err != nil {
		return nil, err
	}
	if create {
		for _, k := range strings.Fields(r.required) {
			v, exists := in[k]
			if !exists || v == nil || v == "" {
				return nil, fmt.Errorf("required field: %s", k)
			}
		}
	}
	if r.table == "assets" {
		life := lifecycleInput{}
		if v, ok := in["purchase_date"]; ok && v != nil {
			d, ok := v.(string)
			if !ok {
				return nil, errors.New("无效采购日期")
			}
			life.PurchaseDate = &d
		}
		if v, ok := in["warranty_expiry"]; ok && v != nil {
			d, ok := v.(string)
			if !ok {
				return nil, errors.New("无效维保日期")
			}
			life.WarrantyExpiry = &d
		}
		if v, ok := in["vendor"]; ok && v != nil {
			d, ok := v.(string)
			if !ok {
				return nil, errors.New("无效供应商")
			}
			life.Vendor = d
		}
		if err := life.validate(); err != nil {
			return nil, err
		}
	}
	if v, exists := in["bmc_secret"]; exists {
		str, ok := v.(string)
		if !ok || str == "" || len(str) > 4096 {
			return nil, errors.New("invalid BMC credential")
		}
		cipher, e := service.EncryptCredential(str)
		if e != nil {
			return nil, errors.New("credential encryption failed")
		}
		delete(in, "bmc_secret")
		in["bmc_secret_ciphertext"] = []byte(cipher)
		in["bmc_secret_key_id"] = "v1"
	}
	return in, nil
}
func (a *App) writeResource(r resource, create bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		in, e := inputResource(c, r, create)
		if e != nil {
			fail(c, 400, e.Error())
			return
		}
		sc := contextScope(c)
		if !create {
			if err := a.guardPXENetworkChange(c.Request.Context(), sc, r, number(c.Param("id")), in, false); err != nil {
				fail(c, 409, err.Error())
				return
			}
		}
		var id int64
		e = a.store.saveResource(c.Request.Context(), r, sc, number(c.Param("id")), in, create, &id)
		if errors.Is(e, errDepartmentMissing) {
			fail(c, 409, "所选部门不存在或已删除，请刷新部门选项")
			return
		}
		if errors.Is(e, errTestVMLabRequired) {
			fail(c, 409, e.Error())
			return
		}
		if text, ok := networkValidationMessage(e); ok {
			fail(c, 409, text)
			return
		}
		if e != nil {
			fail(c, 409, "resource validation or relation conflict")
			return
		}
		ok(c, gin.H{"id": id})
	}
}
func (a *App) deleteResource(r resource) gin.HandlerFunc {
	return func(c *gin.Context) {
		sc := contextScope(c)
		if err := a.guardPXENetworkChange(c.Request.Context(), sc, r, number(c.Param("id")), nil, true); err != nil {
			fail(c, 409, "网络删除被阻止："+err.Error())
			return
		}
		e := a.store.deleteResource(c.Request.Context(), r, sc, number(c.Param("id")))
		if errors.Is(e, errTestVMLabRequired) {
			fail(c, 409, e.Error())
			return
		}
		if e != nil {
			if r.table == "alert_channels" {
				fail(c, 409, "渠道不存在、正在测试或仍被告警规则引用；请先解除关联后重试")
				return
			}
			fail(c, 409, "resource is missing, busy, or still referenced")
			return
		}
		ok(c, nil)
	}
}
func (s *store) audit(tx *gorm.DB, sc scope, action, table string, id int64) error {
	return s.recordAudit(tx, sc, sc.Audit, action, table, id, "success")
}
