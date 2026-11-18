package platform

import (
	"errors"

	"gorm.io/gorm"
)

const ModeSelfUse = "self_use"

// GetSystemMode 读取历史初始化标记；平台仅支持内部管理。
// 返回模式字符串（"self_use"）或空字符串（未初始化）。
func GetSystemMode(db *gorm.DB) (string, error) {
	var mode string
	err := db.Table("system_modes").Select("mode").Order("id DESC").Limit(1).Scan(&mode).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "", nil
		}
		return "", err
	}
	return mode, nil
}

// InitializeMode 初始化系统部署模式，向 system_modes 表插入记录。
// mode 参数必须是 "self_use"。
func InitializeMode(db *gorm.DB, mode string) error {
	if mode != ModeSelfUse {
		return errors.New("无效的系统模式，仅支持 self_use")
	}
	// 检查是否已存在模式记录
	var count int64
	if err := db.Table("system_modes").Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return errors.New("系统模式已初始化，不可重复设置")
	}
	return db.Table("system_modes").Create(map[string]any{
		"mode":   mode,
		"locked": false,
	}).Error
}

// LockMode 锁定最近一次初始化的模式记录，锁定后部署模式不可再变更。
// 由 setupConfig 在 InitializeMode 成功后调用。
func LockMode(db *gorm.DB) error {
	return db.Exec(
		"UPDATE system_modes SET locked = true WHERE id = (SELECT MAX(id) FROM system_modes)",
	).Error
}

// IsModeLocked 检查系统模式是否已锁定（锁定后不可变更）。
func IsModeLocked(db *gorm.DB) (bool, error) {
	var locked bool
	err := db.Table("system_modes").Select("locked").Order("id DESC").Limit(1).Scan(&locked).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, nil
		}
		return false, err
	}
	return locked, nil
}

// IsSetupComplete 检查系统是否已完成初始化设置（system_modes 表是否有记录）。
func IsSetupComplete(db *gorm.DB) (bool, error) {
	var count int64
	if err := db.Table("system_modes").Count(&count).Error; err != nil {
		return false, err
	}
	return count > 0, nil
}
