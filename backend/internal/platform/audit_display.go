package platform

import (
	"fmt"
	"sort"
	"strings"
)

// These labels are shared by the list, search and export. Identifiers remain
// available to API consumers, but are never used as a display-name fallback.
var auditActions = map[string]string{
	"initial-access:read": "查看初始登录信息",
	"create":              "创建", "update": "更新", "write": "保存", "delete": "删除",
	"submit": "提交任务", "cancel": "取消任务", "claim": "认领", "assign": "分配",
	"batch_assign": "批量分配", "reply": "回复", "settings:write": "修改系统设置",
	"iam:write": "修改账号或密钥", "iam:manage": "管理角色授权", "access:manage": "调整访问授权",
	"depreciation:write": "修改折旧配置", "lifecycle:write": "修改采购与维保信息",
	"transition:in_progress": "开始处理", "transition:resolved": "解决工单",
	"transition:closed": "关闭工单", "transition:open": "重新打开工单",
}

var auditTargetTypes = map[string]string{
	"assets": "服务器", "operations": "运维任务", "installations": "系统安装",
	"users": "用户", "roles": "角色", "memberships": "项目成员授权", "api_keys": "API 密钥",
	"departments": "部门", "work_orders": "工单", "system_settings": "系统设置",
	"datacenters": "机房", "tags": "标签", "asset_tags": "服务器标签",
	"networks": "子网", "ip_pools": "地址池", "images": "系统镜像",
	"image_templates": "镜像模板", "kickstart_templates": "自动安装模板",
	"partition_templates": "分区模板", "post_install_scripts": "安装后脚本",
	"install_plans": "安装方案", "install_plan_scripts": "安装方案脚本",
	"firmware_packages": "固件包", "firmware_baselines": "固件基线",
	"asset_monitor_configs": "服务器监控配置", "alert_rules": "告警规则",
	"alert_channels": "通知渠道", "alert_rule_channels": "告警通知关联",
	"maintenance_windows": "维护窗口", "maintenance_window_assets": "维护服务器",
	"alerts": "告警",
}

var auditOperationKinds = map[string]string{
	"power_on": "开机", "power_off": "关机", "power_cycle": "重启",
	"collect": "硬件采集", "install": "系统安装", "firmware_upgrade": "固件升级",
}

// All SQL identifiers and labels are internal constants, never request input.
func auditLabelSQL(field string, labels map[string]string, fallback string) string {
	keys := make([]string, 0, len(labels))
	for key := range labels {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var sql strings.Builder
	sql.WriteString("CASE " + field)
	for _, key := range keys {
		fmt.Fprintf(&sql, " WHEN '%s' THEN '%s'", key, labels[key])
	}
	sql.WriteString(" ELSE " + fallback + " END")
	return sql.String()
}

func auditUserSQL(id string) string {
	return `(SELECT ` + auditUserLabelSQL("u") + ` FROM users u WHERE u.id=` + id + `)`
}

func auditUserLabelSQL(alias string) string {
	return `concat_ws(' · ', NULLIF(NULLIF(` + alias + `.name,''), ` + alias + `.username), NULLIF(` + alias + `.username,''))`
}

func auditAssetSQL(alias string) string {
	return `concat_ws(' · ', NULLIF(` + alias + `.name,''), NULLIF(host(` + alias + `.management_ip),''),
 NULLIF(host(` + alias + `.bmc_address),''), CASE WHEN NULLIF(` + alias + `.serial_number,'') IS NOT NULL THEN 'SN: '||` + alias + `.serial_number END)`
}

// Soft-deleted rows deliberately remain resolvable for legacy audit evidence.
// Every resource lookup keeps its original tenant/project boundary.
func auditTargetSQL(alias string) string {
	id := `CASE WHEN ` + alias + `.target_id ~ '^[0-9]{1,18}$' THEN ` + alias + `.target_id::bigint END`
	var sql strings.Builder
	sql.WriteString("CASE " + alias + ".target_type")
	add := func(table, expression, boundary string) {
		fmt.Fprintf(&sql, " WHEN '%s' THEN (SELECT %s FROM %s r WHERE r.id=%s%s)", table, expression, table, id, boundary)
	}
	tenant := " AND r.tenant_id=" + alias + ".tenant_id"
	project := tenant + " AND r.project_id=" + alias + ".project_id"
	add("assets", auditAssetSQL("r"), project)
	add("users", auditUserLabelSQL("r"), "")
	for _, table := range []string{"departments", "roles"} {
		add(table, "r.name", tenant)
	}
	add("work_orders", "r.title", tenant)
	for _, table := range []string{"datacenters", "tags", "networks", "ip_pools", "images", "image_templates", "kickstart_templates", "partition_templates", "post_install_scripts", "install_plans", "firmware_packages", "firmware_baselines", "alert_rules", "alert_channels", "maintenance_windows", "api_keys"} {
		add(table, "r.name", project)
	}
	asset := `(SELECT ` + auditAssetSQL("ast") + ` FROM assets ast WHERE ast.id=r.asset_id AND ast.tenant_id=r.tenant_id AND ast.project_id=r.project_id)`
	add("operations", `concat_ws(' / ', `+asset+`, `+auditLabelSQL("r.kind", auditOperationKinds, "'运维操作'")+`, to_char(r.created_at AT TIME ZONE 'Asia/Shanghai','YYYY-MM-DD HH24:MI:SS'))`, project)
	add("installations", `concat_ws(' / ', `+asset+`, (SELECT p.name FROM install_plans p WHERE p.id=r.install_plan_id AND p.tenant_id=r.tenant_id AND p.project_id=r.project_id), to_char(r.created_at AT TIME ZONE 'Asia/Shanghai','YYYY-MM-DD HH24:MI:SS'))`, project)
	add("asset_monitor_configs", asset, project)
	add("maintenance_window_assets", asset, project)
	add("alerts", `concat_ws(' / ', `+asset+`, NULLIF(r.message,''))`, project)
	add("memberships", `concat_ws(' / ', `+auditUserSQL("r.user_id")+`, (SELECT role.name FROM roles role WHERE role.id=r.role_id AND role.tenant_id=r.tenant_id))`, project)
	add("asset_tags", `concat_ws(' / ', `+asset+`, (SELECT t.name FROM tags t WHERE t.id=r.tag_id AND t.tenant_id=r.tenant_id AND t.project_id=r.project_id))`, project)
	add("install_plan_scripts", `(SELECT p.name FROM install_plans p WHERE p.id=r.install_plan_id AND p.tenant_id=r.tenant_id AND p.project_id=r.project_id)`, project)
	add("alert_rule_channels", `concat_ws(' / ', (SELECT ar.name FROM alert_rules ar WHERE ar.id=r.rule_id AND ar.tenant_id=r.tenant_id AND ar.project_id=r.project_id), (SELECT ch.name FROM alert_channels ch WHERE ch.id=r.channel_id AND ch.tenant_id=r.tenant_id AND ch.project_id=r.project_id))`, project)
	// Only names are displayed; configured values (including SMTP secrets) are excluded.
	settings := map[string]string{
		"brand.platform_name": "平台名称", "brand.platform_tagline": "平台副标题", "brand.logo_url": "平台 Logo",
		"theme.nav_bg": "导航背景颜色", "theme.nav_text": "导航文字颜色", "app.footer_text": "页脚文字",
		"app.footer_visible": "页脚显示", "app.login_notice": "登录提示",
		"smtp.host": "邮件服务器", "smtp.port": "邮件服务端口", "smtp.username": "邮件登录账号",
		"smtp.password": "邮件登录密码", "smtp.from": "发件地址", "smtp.tls": "邮件 TLS",
	}
	sql.WriteString(" WHEN 'system_settings' THEN " + auditLabelSQL(alias+".target_id", settings, "'系统配置项'") + " ELSE NULL END")
	return sql.String()
}

func auditDisplaySQL(alias string) string {
	snapshot := alias + ".after_snapshot->'audit_display'"
	operator := `COALESCE(NULLIF(` + snapshot + `->>'operator_name',''), NULLIF(` + auditUserSQL(alias+".user_id") + `,''), '系统或历史操作人')`
	target := `COALESCE(NULLIF(` + snapshot + `->>'target_name',''), NULLIF((` + auditTargetSQL(alias) + `),''), '历史记录未保留对象名称')`
	project := `COALESCE(NULLIF(` + snapshot + `->>'project_name',''), (SELECT p.name FROM projects p WHERE p.id=` + alias + `.project_id AND p.tenant_id=` + alias + `.tenant_id), '历史项目')`
	return `jsonb_build_object('operator_name', ` + operator + `, 'target_name', ` + target + `,
 'project_name', ` + project + `, 'client_ip', host(` + alias + `.client_ip),
 'action_label', ` + auditLabelSQL(alias+".action", auditActions, "'其他操作'") + `,
 'target_type_label', ` + auditLabelSQL(alias+".target_type", auditTargetTypes, "'其他资源'") + `)`
}

func auditSearchSQL(alias string) string {
	// Search display values, without accidentally matching JSON field names.
	return `jsonb_path_query_array((` + auditDisplaySQL(alias) + `), '$.*')::text ILIKE ?`
}
