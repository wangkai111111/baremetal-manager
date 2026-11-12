package migrations

import (
	"fmt"
	"os"
	"testing"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

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
	for _, table := range []string{"racks", "rack_labels"} {
		if db.Migrator().HasTable(table) {
			t.Fatalf("obsolete table %s remains", table)
		}
	}
	if !db.Migrator().HasColumn("assets", "datacenter_id") {
		t.Fatal("asset datacenter missing")
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
