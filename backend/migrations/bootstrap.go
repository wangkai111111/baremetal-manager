package migrations

import (
	"fmt"
	"os"
	"strings"

	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

// Bootstrap 仅初始化空平台，不重置已有管理员密码，不创建演示设备或虚假任务。
func Bootstrap(db *gorm.DB) error {
	return db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SELECT pg_advisory_xact_lock(684219733)").Error; err != nil {
			return err
		}
		var count int64
		if err := tx.Table("users").Count(&count).Error; err != nil {
			return err
		}
		if count != 0 {
			return nil
		}
		username, password := os.Getenv("BOOTSTRAP_ADMIN_USER"), os.Getenv("BOOTSTRAP_ADMIN_PASSWORD")
		if username == "" || len(password) < 12 || len(password) > 72 {
			return fmt.Errorf("bootstrap requires BOOTSTRAP_ADMIN_USER and a password of 12 to 72 bytes")
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
		if err != nil {
			return err
		}
		var user, tenant, project, role int64
		if err := tx.Raw("INSERT INTO users(username,password_hash,name,is_platform_admin) VALUES (?,?,?,true) RETURNING id", username, string(hash), username).Scan(&user).Error; err != nil {
			return err
		}
		if err := tx.Raw("INSERT INTO tenants(name,code,create_by,update_by) VALUES ('默认组织','default',?,?) RETURNING id", user, user).Scan(&tenant).Error; err != nil {
			return err
		}
		if err := tx.Exec("INSERT INTO tenant_memberships(tenant_id,user_id,create_by,update_by) VALUES (?,?,?,?)", tenant, user, user, user).Error; err != nil {
			return err
		}
		if err := tx.Raw("INSERT INTO projects(tenant_id,name,code,owner_id,create_by,update_by) VALUES (?,'默认项目','default',?,?,?) RETURNING id", tenant, user, user, user).Scan(&project).Error; err != nil {
			return err
		}
		resources := []string{"assets", "bmc", "sol", "images", "tasks", "alerts", "network", "firmware", "iam", "audit", "settings", "projects", "datacenters", "racks", "tags", "install", "monitor"}
		for _, resource := range resources {
			for _, action := range []string{"read", "write", "operate", "access", "manage"} {
				code := resource + ":" + action
				if err := tx.Exec("INSERT INTO permissions(code,name,resource,action,create_by,update_by) VALUES (?,?,?,?,?,?)", code, code, resource, action, user, user).Error; err != nil {
					return err
				}
			}
		}
		for _, code := range []string{"manager", "operator", "viewer"} {
			if err := tx.Raw("INSERT INTO roles(tenant_id,name,code,create_by,update_by) VALUES (?,?,?,?,?) RETURNING id", tenant, code, code, user, user).Scan(&role).Error; err != nil {
				return err
			}
			actions := []string{"read"}
			if code == "manager" {
				actions = []string{"read", "write", "operate", "access", "manage"}
			} else if code == "operator" {
				actions = []string{"read", "operate", "access"}
			}
			if err := tx.Exec("INSERT INTO role_permissions(tenant_id,role_id,permission_id,create_by,update_by) SELECT ?,?,id,?,? FROM permissions WHERE action IN ?", tenant, role, user, user, actions).Error; err != nil {
				return err
			}
			if code == "manager" {
				if err := tx.Exec("INSERT INTO memberships(tenant_id,project_id,user_id,role_id,create_by,update_by) VALUES (?,?,?,?,?,?)", tenant, project, user, role, user, user).Error; err != nil {
					return err
				}
			}
		}
		settings := map[string]string{"platform_name": "裸机运维平台", "sidebar_footer": "BMC · SOL · 基础设施管理", "login_notice": "仅限授权人员访问", "logo_url": "", "nav_color": "#101820", "sidebar_color": "#101820"}
		for key, value := range settings {
			if strings.TrimSpace(key) == "" {
				continue
			}
			if err := tx.Exec("INSERT INTO system_settings(key,value,create_by,update_by) VALUES (?,to_jsonb(?::text),?,?)", key, value, user, user).Error; err != nil {
				return err
			}
		}
		return nil
	})
}
