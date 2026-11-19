package platform

import (
	"sort"
	"strings"
)

// Resolve display names inside the same scoped query. Consumers must never
// need unrestricted user/resource lists just to display a permitted record.
func resourceDisplaySQL(r resource) string {
	fields := strings.Fields(r.fields)
	switch r.table {
	case "assets":
		fields = append(fields, "project_id", "department_id", "datacenter_id")
	case "operations", "sol_sessions":
		fields = append(fields, "asset_id", "user_id")
	case "installations":
		fields = append(fields, "asset_id", "install_plan_id")
	case "firmware_upgrades":
		fields = append(fields, "asset_id", "firmware_package_id")
	case "alerts":
		fields = append(fields, "asset_id", "rule_id")
	}
	values := map[string]string{}
	for _, key := range fields {
		parent, exists := references[key]
		if key == "project_id" {
			parent, exists = "projects", true
		}
		if key == "user_id" {
			values["operator_name"] = auditUserSQL(r.table + ".user_id")
			continue
		}
		if !exists {
			continue
		}
		boundary := " AND rel.tenant_id=" + r.table + ".tenant_id"
		if parent != "departments" && parent != "projects" {
			boundary += " AND rel.project_id=" + r.table + ".project_id"
		}
		active := " AND rel.deleted_at IS NULL"
		if parent == "assets" && r.readonly {
			active = ""
		}
		lookup := " FROM " + parent + " rel WHERE rel.id=" + r.table + "." + key + boundary + active + ")"
		name := strings.TrimSuffix(key, "_id") + "_name"
		values[name] = "(SELECT rel.name" + lookup
		if parent == "assets" {
			values["asset_label"] = "(SELECT " + auditAssetSQL("rel") + lookup
		}
	}
	if len(values) == 0 {
		return ""
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys)*2)
	for _, key := range keys {
		parts = append(parts, "'"+key+"'", values[key])
	}
	return "jsonb_build_object(" + strings.Join(parts, ",") + ")"
}
