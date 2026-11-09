package migrations

import (
	"crypto/sha256"
	"fmt"
	"strings"

	"gorm.io/gorm"
)

const TargetVersion = "004_mode_system"

// Apply 在空库内执行目标迁移；旧数据库明确报错，不隐式破坏历史数据。
// 依次应用 003（基础结构）与 004（模式与运营体系）。
func Apply(db *gorm.DB) error {
	return db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SELECT pg_advisory_xact_lock(684219732)").Error; err != nil {
			return err
		}
		if err := tx.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (version text PRIMARY KEY, checksum text NOT NULL, applied_at timestamptz NOT NULL DEFAULT now())`).Error; err != nil {
			return err
		}

		// ---- 003：目标空库基础结构 ----
		checksum003 := fmt.Sprintf("%x", sha256.Sum256([]byte(TargetSQL)))
		var existing003 struct{ Checksum string }
		if err := tx.Raw("SELECT checksum FROM schema_migrations WHERE version = ?", "003_target_schema").Scan(&existing003).Error; err != nil && err != gorm.ErrRecordNotFound {
			return err
		}
		if existing003.Checksum == "" {
			// 空库才能执行 003
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

		// ---- 004：模式与运营体系增量迁移 ----
		checksum004 := fmt.Sprintf("%x", sha256.Sum256([]byte(ModeSystemSQL)))
		var existing004 struct{ Checksum string }
		if err := tx.Raw("SELECT checksum FROM schema_migrations WHERE version = ?", TargetVersion).Scan(&existing004).Error; err != nil && err != gorm.ErrRecordNotFound {
			return err
		}
		if existing004.Checksum == "" {
			if err := tx.Exec(ModeSystemSQL).Error; err != nil {
				return err
			}
			if err := tx.Exec("INSERT INTO schema_migrations(version,checksum) VALUES (?,?)", TargetVersion, checksum004).Error; err != nil {
				return err
			}
		} else if existing004.Checksum != checksum004 {
			return fmt.Errorf("migration checksum mismatch: %s", TargetVersion)
		}

		return nil
	})
}

// Verify 要求应用启动前已完成显式迁移，并防止运行过期二进制。
func Verify(db *gorm.DB) error {
	// 校验 003 基础结构
	var checksum003 string
	if err := db.Raw("SELECT checksum FROM schema_migrations WHERE version = ?", "003_target_schema").Scan(&checksum003).Error; err != nil {
		return fmt.Errorf("database is not migrated: 003_target_schema missing: %w", err)
	}
	expected003 := fmt.Sprintf("%x", sha256.Sum256([]byte(TargetSQL)))
	if checksum003 != expected003 {
		return fmt.Errorf("003_target_schema checksum differs; run server migrate")
	}
	// 校验 004 增量迁移
	var checksum004 string
	if err := db.Raw("SELECT checksum FROM schema_migrations WHERE version = ?", TargetVersion).Scan(&checksum004).Error; err != nil {
		return fmt.Errorf("database is not migrated: %s missing: %w", TargetVersion, err)
	}
	expected004 := fmt.Sprintf("%x", sha256.Sum256([]byte(ModeSystemSQL)))
	if checksum004 != expected004 {
		return fmt.Errorf("%s checksum differs; run server migrate", TargetVersion)
	}
	return nil
}
