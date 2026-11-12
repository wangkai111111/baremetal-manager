package migrations

import "gorm.io/gorm"

// Bootstrap 初始化组织、项目、权限与设置，不创建管理员或重置已有凭据。
func Bootstrap(db *gorm.DB) error {
	return db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SELECT pg_advisory_xact_lock(684219733)").Error; err != nil {
			return err
		}
		var count int64
		if err := tx.Table("tenants").Count(&count).Error; err != nil {
			return err
		}
		if count != 0 {
			return nil
		}
		var tenant, project, role int64
		if err := tx.Raw("INSERT INTO tenants(name,code) VALUES ('默认组织','default') RETURNING id").Scan(&tenant).Error; err != nil {
			return err
		}
		if err := tx.Raw("INSERT INTO projects(tenant_id,name,code) VALUES (?,'默认项目','default') RETURNING id", tenant).Scan(&project).Error; err != nil {
			return err
		}
		resources := []string{"assets", "bmc", "sol", "images", "tasks", "alerts", "network", "firmware", "iam", "audit", "settings", "projects", "datacenters", "tags", "install", "monitor", "work_orders", "products", "orders", "billing", "finance", "marketing", "departments"}
		for _, resource := range resources {
			for _, action := range []string{"read", "write", "operate", "access", "manage"} {
				code := resource + ":" + action
				if err := tx.Exec("INSERT INTO permissions(code,name,resource,action) VALUES (?,?,?,?) ON CONFLICT (code) WHERE deleted_at IS NULL DO NOTHING", code, code, resource, action).Error; err != nil {
					return err
				}
			}
		}
		for _, code := range []string{"manager", "operator", "viewer"} {
			if err := tx.Raw("INSERT INTO roles(tenant_id,name,code) VALUES (?,?,?) RETURNING id", tenant, code, code).Scan(&role).Error; err != nil {
				return err
			}
			actions := []string{"read"}
			if code == "manager" {
				actions = []string{"read", "write", "operate", "access", "manage"}
			} else if code == "operator" {
				actions = []string{"read", "operate", "access"}
			}
			if err := tx.Exec("INSERT INTO role_permissions(tenant_id,role_id,permission_id) SELECT ?,?,id FROM permissions WHERE deleted_at IS NULL AND action IN ?", tenant, role, actions).Error; err != nil {
				return err
			}
		}
		settings := map[string]string{"platform_name": "裸机运维平台", "sidebar_footer": "BMC · SOL · 基础设施管理", "login_notice": "", "logo_url": "", "nav_color": "#101820", "sidebar_color": "#ffffff"}
		for key, value := range settings {
			if err := tx.Exec("INSERT INTO system_settings(key,value) VALUES (?,to_jsonb(?::text)) ON CONFLICT (key) WHERE deleted_at IS NULL DO NOTHING", key, value).Error; err != nil {
				return err
			}
		}
		return nil
	})
}
