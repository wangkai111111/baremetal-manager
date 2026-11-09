package migrations

import (
	"crypto/sha256"
	"fmt"
	"strings"

	"gorm.io/gorm"
)

const TargetVersion = "003_target_schema"

// Apply 在空库内执行目标迁移；旧数据库明确报错，不隐式破坏历史数据。
func Apply(db *gorm.DB) error {
	return db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SELECT pg_advisory_xact_lock(684219732)").Error; err != nil {
			return err
		}
		if err := tx.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (version text PRIMARY KEY, checksum text NOT NULL, applied_at timestamptz NOT NULL DEFAULT now())`).Error; err != nil {
			return err
		}
		var existing struct{ Checksum string }
		if err := tx.Raw("SELECT checksum FROM schema_migrations WHERE version = ?", TargetVersion).Scan(&existing).Error; err != nil {
			return err
		}
		checksum := fmt.Sprintf("%x", sha256.Sum256([]byte(TargetSQL)))
		if existing.Checksum != "" {
			if existing.Checksum != checksum {
				return fmt.Errorf("migration checksum mismatch: %s", TargetVersion)
			}
			return nil
		}
		var count int64
		if err := tx.Raw("SELECT count(*) FROM information_schema.tables WHERE table_schema='public' AND table_type='BASE TABLE' AND table_name <> 'schema_migrations'").Scan(&count).Error; err != nil {
			return err
		}
		if count != 0 {
			return fmt.Errorf("target schema requires an empty database; configure a new POSTGRES_DB and data volume")
		}
		// 外层事务统一保证结构与账本原子提交；仅剥除文件的事务边界。
		sql := strings.Replace(TargetSQL, "\nBEGIN;\n", "\n", 1)
		sql = strings.TrimSuffix(strings.TrimSpace(sql), "COMMIT;")
		if err := tx.Exec(sql).Error; err != nil {
			return err
		}
		return tx.Exec("INSERT INTO schema_migrations(version,checksum) VALUES (?,?)", TargetVersion, checksum).Error
	})
}

// Verify 要求应用启动前已完成显式迁移，并防止运行过期二进制。
func Verify(db *gorm.DB) error {
	var checksum string
	if err := db.Raw("SELECT checksum FROM schema_migrations WHERE version = ?", TargetVersion).Scan(&checksum).Error; err != nil {
		return fmt.Errorf("database is not migrated: %w", err)
	}
	expected := fmt.Sprintf("%x", sha256.Sum256([]byte(TargetSQL)))
	if checksum != expected {
		return fmt.Errorf("target migration is missing or checksum differs; run server migrate")
	}
	return nil
}
