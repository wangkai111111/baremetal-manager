package migrations

import (
	"fmt"
	"os"
	"strings"

	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// GrantApplicationRole 使用独立非 owner 账户运行应用，并禁止改写审计证据。
func GrantApplicationRole(db *gorm.DB) error {
	name, password := os.Getenv("DATABASE_APP_USER"), os.Getenv("DATABASE_APP_PASSWORD")
	if name == "" && password == "" {
		return nil
	}
	if name == "" || len(password) < 16 {
		return fmt.Errorf("DATABASE_APP_USER and DATABASE_APP_PASSWORD of at least 16 bytes are required")
	}
	quoteID := func(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }
	quoteString := func(s string) string { return `'` + strings.ReplaceAll(s, `'`, `''`) + `'` }
	return db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SELECT pg_advisory_xact_lock(684219734)").Error; err != nil {
			return err
		}
		var exists bool
		if err := tx.Raw("SELECT EXISTS(SELECT 1 FROM pg_roles WHERE rolname=?)", name).Scan(&exists).Error; err != nil {
			return err
		}
		var current string
		if err := tx.Raw("SELECT current_user").Scan(&current).Error; err != nil {
			return err
		}
		if current == name {
			return fmt.Errorf("application role must differ from migration owner")
		}
		role := quoteID(name)
		if !exists {
			// 角色 DDL 不能绑定密码参数，禁止 SQL 日志泄露临时凭据。
			if err := tx.Session(&gorm.Session{Logger: logger.Default.LogMode(logger.Silent)}).Exec("CREATE ROLE " + role + " LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT PASSWORD " + quoteString(password)).Error; err != nil {
				return err
			}
		} else {
			var unsafe bool
			if err := tx.Raw("SELECT rolsuper OR rolcreatedb OR rolcreaterole OR rolbypassrls FROM pg_roles WHERE rolname=?", name).Scan(&unsafe).Error; err != nil {
				return err
			}
			if unsafe {
				return fmt.Errorf("application role has elevated database privileges")
			}
		}
		for _, sql := range []string{
			"REVOKE CREATE ON SCHEMA public FROM PUBLIC",
			"GRANT USAGE ON SCHEMA public TO " + role,
			"GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO " + role,
			"GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA public TO " + role,
			"REVOKE UPDATE, DELETE, TRUNCATE ON audits FROM " + role,
			"REVOKE INSERT, UPDATE, DELETE, TRUNCATE ON schema_migrations FROM " + role,
		} {
			if err := tx.Exec(sql).Error; err != nil {
				return err
			}
		}
		return nil
	})
}
