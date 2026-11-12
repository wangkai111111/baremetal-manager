package platform

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"time"

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
	{"assets", "assets", "name serial_number manufacturer model datacenter_id bmc_address bmc_protocol bmc_port bmc_tls_verify bmc_user bmc_secret management_ip mac_address status purchase_date warranty_expiry vendor", "name bmc_address bmc_user bmc_secret", false},
	{"datacenters", "datacenters", "name location contact", "name", false},
	{"tags", "tags", "name color", "name", false},
	{"asset-tags", "asset_tags", "asset_id tag_id", "asset_id tag_id", false},
	{"networks", "networks", "name subnet gateway vlan_id dhcp_enabled", "name subnet", false},
	{"ip-pools", "ip_pools", "network_id name start_address end_address", "network_id name start_address end_address", false},
	{"ip-allocations", "ip_allocations", "network_id pool_id asset_id address mac_address status", "network_id address", false},
	{"pxe-configs", "pxe_configs", "network_id name boot_mode next_server boot_file enabled config", "network_id name boot_mode next_server boot_file", false},
	{"images", "images", "name object_key size_bytes sha256 os_type version architecture", "name object_key size_bytes sha256 os_type version architecture", false},
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
	// departments, products, orders, bills, accounts, payments, invoices, coupons, work_orders
	// 自定义处理器位于同目录下对应的 .go 文件中
}
var references = map[string]string{"datacenter_id": "datacenters", "asset_id": "assets", "tag_id": "tags", "network_id": "networks", "pool_id": "ip_pools", "image_id": "images", "image_template_id": "image_templates", "kickstart_template_id": "kickstart_templates", "partition_template_id": "partition_templates", "install_plan_id": "install_plans", "script_id": "post_install_scripts", "firmware_package_id": "firmware_packages", "rule_id": "alert_rules", "channel_id": "alert_channels", "window_id": "maintenance_windows"}
var jsonFields = map[string]bool{"config": true, "layout": true, "compatible_models": true, "metrics": true, "selector": true, "spec_json": true, "pricing_json": true, "detail_json": true}
var hidden = " - 'bmc_secret_ciphertext' - 'bmc_secret_key_id' - 'password_hash' - 'secret_ciphertext' - 'secret_key_id' - 'test_claim_until' - 'callback_token_hash' - 'config_snapshot' - 'redfish_snapshot' - 'snapshot' - 'payload' - 'result' - 'before_snapshot' - 'after_snapshot'"

func queryScope(db *gorm.DB, r resource, sc scope) *gorm.DB {
	q := db.Table(r.table).Where("tenant_id=? AND project_id=? AND deleted_at IS NULL", sc.Tenant, sc.Project)
	if sc.Admin {
		return q
	}
	// 数据隔离唯一权威源：GetSystemMode()（system_modes 表）；
	// 仅运营模式（external_operation）需要按已交付购买订单过滤，
	// 自用模式（self_use）或未初始化时不做隔离。
	mode := currentSystemMode(db)
	if mode != ModeExternalOperation {
		return q
	}
	var ownership *gorm.DB
	switch r.table {
	case "assets":
		return q.Where("COALESCE(assets.subscription_state,'legacy') <> 'reclaim_pending'").Where("EXISTS (SELECT 1 FROM orders WHERE orders.asset_id=assets.id AND orders.tenant_id=? AND orders.type='purchase' AND orders.status='completed' AND orders.deleted_at IS NULL)", sc.Tenant)
	case "asset_tags", "ip_allocations", "asset_monitor_configs", "maintenance_window_assets", "asset_hardwares", "firmware_inventories", "operations", "installations", "firmware_upgrades", "sensor_records", "alerts", "sol_sessions":
		ownership = queryScope(db.Session(&gorm.Session{NewDB: true}), resource{table: "assets"}, sc).Select("id")
	case "operation_logs":
		ownership = queryScope(db.Session(&gorm.Session{NewDB: true}), resource{table: "operations"}, sc).Select("id")
	case "installation_events":
		ownership = queryScope(db.Session(&gorm.Session{NewDB: true}), resource{table: "installations"}, sc).Select("id")
	case "alert_events":
		ownership = queryScope(db.Session(&gorm.Session{NewDB: true}), resource{table: "alerts"}, sc).Select("id")
	case "notification_deliveries":
		ownership = queryScope(db.Session(&gorm.Session{NewDB: true}), resource{table: "alert_events"}, sc).Select("id")
	default:
		return q
	}
	field := "asset_id"
	switch r.table {
	case "operation_logs":
		field = "operation_id"
	case "installation_events":
		field = "installation_id"
	case "alert_events":
		field = "alert_id"
	case "notification_deliveries":
		field = "alert_event_id"
	}
	return q.Where(field+" IN (?)", ownership)
}
func decodeRows(db *gorm.DB, r resource) ([]map[string]any, error) {
	var rows []struct{ Doc string }
	e := db.Select("(to_jsonb(" + r.table + ")" + hidden + ")::text AS doc").Scan(&rows).Error
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

// 订单按租户隔离，asset_id 将交付关系关联到当前项目的资产。
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
 WHEN assets.power_state='on' AND EXISTS (SELECT 1 FROM orders
  WHERE orders.tenant_id=assets.tenant_id AND orders.asset_id=assets.id
   AND orders.type='purchase' AND orders.status='completed' AND orders.deleted_at IS NULL) THEN 'in_use'
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
		if id := number(c.Query("asset_id")); id > 0 && (strings.Contains(r.fields, "asset_id") || r.table == "sensor_records" || r.table == "operations" || r.table == "alerts" || r.table == "asset_hardwares" || r.table == "firmware_inventories" || r.table == "sol_sessions") {
			q = q.Where("asset_id=?", id)
		}
		if r.table == "assets" {
			condition, err := assetSearchCondition(c.DefaultQuery("search_field", "auto"))
			if err != nil {
				fail(c, 400, err.Error())
				return
			}
			q = assetListQuery(q, condition, c.Query("search"), c.Query("tags"), c.Query("status"), number(c.Query("datacenter_id")))
		} else if search := strings.TrimSpace(c.Query("search")); search != "" && strings.Contains(" "+r.fields+" ", " name ") {
			q = q.Where("name ILIKE ?", "%"+search+"%")
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
	if create {
		for _, k := range strings.Fields(r.required) {
			v, exists := in[k]
			if !exists || v == nil || v == "" {
				return nil, fmt.Errorf("required field: %s", k)
			}
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
		if create && r.table == "assets" && !contextScope(c).Admin && currentSystemMode(a.store.db) == ModeExternalOperation {
			fail(c, 403, errAssetProvisioningForbidden.Error())
			return
		}
		in, e := inputResource(c, r, create)
		if e != nil {
			fail(c, 400, e.Error())
			return
		}
		sc := contextScope(c)
		var id int64
		e = a.store.saveResource(c.Request.Context(), r, sc, number(c.Param("id")), in, create, &id)
		if errors.Is(e, errAssetProvisioningForbidden) {
			fail(c, 403, e.Error())
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
		e := a.store.deleteResource(c.Request.Context(), r, sc, number(c.Param("id")))
		if errors.Is(e, errAssetProvisioningForbidden) {
			fail(c, 403, e.Error())
			return
		}
		if e != nil {
			fail(c, 409, "resource is missing, busy, or still referenced")
			return
		}
		ok(c, nil)
	}
}
func (s *store) audit(tx *gorm.DB, sc scope, action, table string, id int64) error {
	return tx.Table("audits").Create(map[string]any{"tenant_id": sc.Tenant, "project_id": sc.Project, "user_id": sc.User, "request_id": fmt.Sprintf("%d-%d", time.Now().UnixNano(), sc.User), "action": action, "target_type": table, "target_id": fmt.Sprint(id), "outcome": "success"}).Error
}
