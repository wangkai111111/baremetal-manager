package migrations

import (
	"crypto/sha256"
	"fmt"
	"strings"

	"gorm.io/gorm"
)

const TargetVersion = "011_governance_phase56"

var incrementalMigrations = []struct{ version, sql string }{
	{"004_mode_system", ModeSystemSQL},
	{"005_asset_datacenter", AssetDatacenterSQL},
	{"006_bmc_tls_verify_default", BMCTLSVerifyDefaultSQL},
	{"007_redfish_monitoring", RedfishMonitoringSQL},
	{"008_asset_subscriptions", AssetSubscriptionsSQL},
	{"009_subscription_safety", SubscriptionSafetySQL},
	{"010_subscription_balance_payment", SubscriptionBalancePaymentSQL},
	{TargetVersion, GovernanceSQL},
}

// Apply 在空库内执行目标迁移；旧数据库明确报错，不隐式破坏历史数据。
// 先验证或初始化目标结构，再按顺序执行增量迁移。
func Apply(db *gorm.DB) error {
	return db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SELECT pg_advisory_xact_lock(684219732)").Error; err != nil {
			return err
		}
		if err := tx.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (version text PRIMARY KEY, checksum text NOT NULL, applied_at timestamptz NOT NULL DEFAULT now())`).Error; err != nil {
			return err
		}

		checksum003 := fmt.Sprintf("%x", sha256.Sum256([]byte(TargetSQL)))
		var existing003 struct{ Checksum string }
		if err := tx.Raw("SELECT checksum FROM schema_migrations WHERE version = ?", "003_target_schema").Scan(&existing003).Error; err != nil && err != gorm.ErrRecordNotFound {
			return err
		}
		if existing003.Checksum == "" {
			var count int64
			if err := tx.Raw("SELECT count(*) FROM information_schema.tables WHERE table_schema='public' AND table_type='BASE TABLE' AND table_name <> 'schema_migrations'").Scan(&count).Error; err != nil {
				return err
			}
			if count != 0 {
				return fmt.Errorf("target schema requires an empty database; configure a new POSTGRES_DB and data volume")
			}
			sql := strings.Replace(TargetSQL, "\nBEGIN;\n", "\n", 1)
			sql = strings.TrimSuffix(strings.TrimSpace(sql), "COMMIT;")
			if err := tx.Exec(sql).Error; err != nil {
				return err
			}
			if err := tx.Exec("INSERT INTO schema_migrations(version,checksum) VALUES (?,?)", "003_target_schema", checksum003).Error; err != nil {
				return err
			}
		} else if existing003.Checksum != checksum003 {
			return fmt.Errorf("migration checksum mismatch: 003_target_schema")
		}

		for _, migration := range incrementalMigrations {
			checksum := fmt.Sprintf("%x", sha256.Sum256([]byte(migration.sql)))
			var existing struct{ Checksum string }
			if err := tx.Raw("SELECT checksum FROM schema_migrations WHERE version = ?", migration.version).Scan(&existing).Error; err != nil && err != gorm.ErrRecordNotFound {
				return err
			}
			if existing.Checksum == "" {
				if err := tx.Exec(migration.sql).Error; err != nil {
					return err
				}
				if err := tx.Exec("INSERT INTO schema_migrations(version,checksum) VALUES (?,?)", migration.version, checksum).Error; err != nil {
					return err
				}
			} else if existing.Checksum != checksum {
				return fmt.Errorf("migration checksum mismatch: %s", migration.version)
			}
		}
		return nil
	})
}

// Verify 要求应用启动前已完成显式迁移，并防止运行过期二进制。
func Verify(db *gorm.DB) error {
	for _, migration := range append([]struct{ version, sql string }{{"003_target_schema", TargetSQL}}, incrementalMigrations...) {
		var checksum string
		if err := db.Raw("SELECT checksum FROM schema_migrations WHERE version = ?", migration.version).Scan(&checksum).Error; err != nil {
			return fmt.Errorf("database is not migrated: %s missing: %w", migration.version, err)
		}
		if checksum != fmt.Sprintf("%x", sha256.Sum256([]byte(migration.sql))) {
			return fmt.Errorf("%s checksum differs; run server migrate", migration.version)
		}
	}
	return nil
}
