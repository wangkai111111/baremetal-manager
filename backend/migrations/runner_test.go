package migrations

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestOperationClientIPMigration(t *testing.T) {
	last := migrationByVersion(t, "015_order_configuration")
	if last.version != "015_order_configuration" || last.sql != OrderConfigurationSQL {
		t.Fatal("operation client IP migration not registered as target")
	}
	if !strings.Contains(OperationClientIPSQL, "ALTER TABLE operations ADD COLUMN IF NOT EXISTS client_ip inet;") {
		t.Fatal("missing nullable IP column")
	}
	if strings.Contains(OperationClientIPSQL, "DEFAULT") || strings.Contains(OperationClientIPSQL, "NOT NULL") || strings.Contains(OperationClientIPSQL, "UPDATE operations") {
		t.Fatal("historical IP must remain NULL")
	}
}

func TestImageDownloadSourcesMigration(t *testing.T) {
	last := migrationByVersion(t, "013_image_download_sources")
	previous := migrationByVersion(t, "012_image_download_jobs")
	if last.version != "013_image_download_sources" || last.sql != ImageDownloadSourcesSQL {
		t.Fatal("source migration registration changed")
	}
	if previous.version != "012_image_download_jobs" || previous.sql != ImageDownloadJobsSQL {
		t.Fatal("existing job migration registration changed")
	}
	for _, part := range []string{"ALTER TABLE image_download_jobs", "ADD COLUMN source text NOT NULL DEFAULT 'official'", "CHECK (source IN ('official', 'tuna'))"} {
		if !strings.Contains(ImageDownloadSourcesSQL, part) {
			t.Errorf("missing source migration constraint %q", part)
		}
	}
}

// 集成测试必须显式指向一次性空库，禁止自动连接工作数据库。
func TestTargetMigrationIntegration(t *testing.T) {
	dsn := os.Getenv("BMM_MIGRATION_TEST_DSN")
	if dsn == "" {
		t.Skip("BMM_MIGRATION_TEST_DSN must point to an empty disposable database")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	conn, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := Apply(db); err != nil {
		t.Fatal(err)
	}
	if err := Apply(db); err != nil {
		t.Fatalf("repeat migration: %v", err)
	}
	if err := Verify(db); err != nil {
		t.Fatal(err)
	}
	var obsolete int64
	if err := db.Raw(`SELECT count(*) FROM information_schema.columns WHERE table_schema='public' AND table_name='assets' AND column_name IN ('rack_id','rack_u_position','height_u')`).Scan(&obsolete).Error; err != nil || obsolete != 0 {
		t.Fatalf("obsolete asset columns: %d %v", obsolete, err)
	}
	for _, table := range []string{"racks", "rack_labels", "ip_allocations", "pxe_configs"} {
		if db.Migrator().HasTable(table) {
			t.Fatalf("obsolete table %s remains", table)
		}
	}
	if !db.Migrator().HasColumn("assets", "datacenter_id") {
		t.Fatal("asset datacenter missing")
	}
	var clientIP struct{ IsNullable, DataType string }
	if err := db.Raw(`SELECT is_nullable, data_type FROM information_schema.columns WHERE table_schema='public' AND table_name='operations' AND column_name='client_ip'`).Scan(&clientIP).Error; err != nil || clientIP.IsNullable != "YES" || clientIP.DataType != "inet" {
		t.Fatalf("operation client_ip: %#v %v", clientIP, err)
	}
	if err := db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("UPDATE schema_migrations SET checksum = 'invalid' WHERE version = ?", TargetVersion).Error; err != nil {
			return err
		}
		if err := Verify(tx); err == nil {
			t.Error("checksum corruption was accepted")
		}
		return fmt.Errorf("rollback checksum probe")
	}); err == nil {
		t.Fatal("probe must roll back")
	}
	if err := Bootstrap(db); err != nil {
		t.Fatal(err)
	}
	if err := Bootstrap(db); err != nil {
		t.Fatal(err)
	}
	var count int64
	db.Table("users").Count(&count)
	if count != 0 {
		t.Fatalf("bootstrap must not create users: %d", count)
	}
	db.Table("role_permissions").Count(&count)
	if count == 0 {
		t.Fatal("role permissions not seeded")
	}
	t.Setenv("DATABASE_APP_USER", "bmm_integration_app")
	t.Setenv("DATABASE_APP_PASSWORD", "integration-only-password-2026")
	if err := GrantApplicationRole(db); err != nil {
		t.Fatal(err)
	}
	if err := db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SET LOCAL ROLE bmm_integration_app").Error; err != nil {
			return err
		}
		if err := tx.Table("users").Count(&count).Error; err != nil {
			return err
		}
		var jobsAllowed bool
		if err := tx.Raw("SELECT has_table_privilege(current_user,'image_download_jobs','SELECT,INSERT,UPDATE,DELETE') AND has_sequence_privilege(current_user,'image_download_jobs_id_seq','USAGE,SELECT')").Scan(&jobsAllowed).Error; err != nil {
			return err
		}
		if !jobsAllowed {
			t.Error("application role cannot access image download jobs or their sequence")
		}
		var allowed bool
		if err := tx.Raw("SELECT has_table_privilege(current_user,'audits','UPDATE') OR has_table_privilege(current_user,'audits','DELETE') OR has_table_privilege(current_user,'audits','TRUNCATE')").Scan(&allowed).Error; err != nil {
			return err
		}
		if allowed {
			t.Error("application role can mutate audits")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// Verify upgrade from commercial configuration without removing historical tables.
func TestInternalManagementUpgrade(t *testing.T) {
	dsn := os.Getenv("BMM_MIGRATION_TEST_DSN")
	if dsn == "" {
		t.Skip("requires disposable migrated database")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	conn, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	tx := db.Begin()
	if tx.Error != nil {
		t.Fatal(tx.Error)
	}
	defer tx.Rollback()
	for _, statement := range []string{
		"ALTER TABLE system_modes DROP CONSTRAINT system_modes_internal_only",
		"INSERT INTO system_modes(mode,locked) VALUES ('external_operation',true)",
		"INSERT INTO system_settings(key,value) VALUES ('registration.enabled','true'),('billing.enabled','true'),('platform.operation_mode','\"external\"')",
		"INSERT INTO permissions(code,name,resource,action) VALUES ('orders:read','orders:read','orders','read')",
		"INSERT INTO role_permissions(tenant_id,role_id,permission_id) SELECT r.tenant_id,r.id,p.id FROM roles r CROSS JOIN permissions p WHERE p.code='orders:read' AND p.deleted_at IS NULL",
		"DELETE FROM schema_migrations WHERE version='016_internal_management'",
	} {
		if err := tx.Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := Apply(tx); err != nil {
		t.Fatal(err)
	}
	if err := Verify(tx); err != nil {
		t.Fatal(err)
	}
	var count int64
	for _, query := range []string{
		"SELECT count(*) FROM system_modes WHERE mode<>'self_use' OR NOT locked",
		"SELECT count(*) FROM system_settings WHERE deleted_at IS NULL AND (key='platform.operation_mode' OR key LIKE 'registration.%' OR key LIKE 'billing.%')",
		"SELECT count(*) FROM permissions WHERE resource='orders' AND deleted_at IS NULL",
		"SELECT count(*) FROM role_permissions rp JOIN permissions p ON p.id=rp.permission_id WHERE p.resource='orders' AND rp.deleted_at IS NULL",
	} {
		if err := tx.Raw(query).Scan(&count).Error; err != nil || count != 0 {
			t.Fatalf("upgrade invariant failed: %s: %d %v", query, count, err)
		}
	}
	if err := tx.Table("permissions").Where("resource='work_orders' AND deleted_at IS NULL").Count(&count).Error; err != nil || count == 0 {
		t.Fatalf("work orders permissions lost: %v", err)
	}
	for _, table := range []string{"orders", "payments", "assets", "departments", "work_orders"} {
		if !tx.Migrator().HasTable(table) {
			t.Fatalf("historical/shared table removed: %s", table)
		}
	}
}

func migrationByVersion(t *testing.T, version string) struct{ version, sql string } {
	t.Helper()
	for _, m := range incrementalMigrations {
		if m.version == version {
			return m
		}
	}
	t.Fatalf("migration missing: %s", version)
	return struct{ version, sql string }{}
}
