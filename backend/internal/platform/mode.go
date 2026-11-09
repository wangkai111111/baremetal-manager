package platform

import (
	"errors"
	"sync"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// 系统部署模式常量
const (
	ModeSelfUse          = "self_use"          // 自用模式
	ModeExternalOperation = "external_operation" // 运营模式
)

// GetSystemMode 从 system_modes 表读取当前系统部署模式。
// 返回模式字符串（"self_use"、"external_operation"）或空字符串（未初始化）。
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
// mode 参数必须是 "self_use" 或 "external_operation"。
func InitializeMode(db *gorm.DB, mode string) error {
	if mode != ModeSelfUse && mode != ModeExternalOperation {
		return errors.New("无效的系统模式，仅支持 self_use 或 external_operation")
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

// GetAvailableFeatures 根据系统模式返回可用功能列表。
func GetAvailableFeatures(mode string) []string {
	baseFeatures := []string{
		"assets",        // 资产管理
		"datacenters",   // 机房管理
		"racks",         // 机柜管理
		"networks",      // 网络管理
		"images",        // 镜像管理
		"alerts",        // 告警管理
		"sol",           // 串口访问
		"work_orders",   // 工单系统
		"audit_logs",    // 审计日志
	}
	switch mode {
	case ModeSelfUse:
		// 自用模式额外功能
		return append(baseFeatures,
			"departments",       // 部门管理
			"asset_depreciation", // 资产折旧
		)
	case ModeExternalOperation:
		// 运营模式额外功能
		return append(baseFeatures,
			"tenant_profiles", // 租户档案
			"products",        // 产品上架
			"orders",          // 订单管理
			"bills",           // 账单管理
			"accounts",        // 账户管理
			"payments",        // 支付管理
			"invoices",        // 发票管理
			"coupons",         // 优惠券管理
		)
	default:
		return baseFeatures
	}
}

// IsSetupComplete 检查系统是否已完成初始化设置（system_modes 表是否有记录）。
func IsSetupComplete(db *gorm.DB) (bool, error) {
	var count int64
	if err := db.Table("system_modes").Count(&count).Error; err != nil {
		return false, err
	}
	return count > 0, nil
}

// modeCache 缓存当前系统模式，避免每次请求都查询数据库
var modeCache struct {
	mu    sync.RWMutex
	value string
}

// getCachedSystemMode 从缓存或数据库获取当前系统部署模式
func (a *App) getCachedSystemMode() string {
	modeCache.mu.RLock()
	if modeCache.value != "" {
		defer modeCache.mu.RUnlock()
		return modeCache.value
	}
	modeCache.mu.RUnlock()

	modeCache.mu.Lock()
	defer modeCache.mu.Unlock()

	// 双重检查，防止并发重复查询
	if modeCache.value != "" {
		return modeCache.value
	}

	mode, err := GetSystemMode(a.store.db)
	if err != nil || mode == "" {
		// 默认自用模式
		modeCache.value = ModeSelfUse
	} else {
		modeCache.value = mode
	}
	return modeCache.value
}

// InvalidateModeCache 清除模式缓存（设置变更时调用）
func InvalidateModeCache() {
	modeCache.mu.Lock()
	modeCache.value = ""
	modeCache.mu.Unlock()
}

// requireModeMiddleware 返回模式检查中间件
// allowedModes: 允许访问的模式列表
func (a *App) requireModeMiddleware(allowedModes ...string) gin.HandlerFunc {
	modeSet := make(map[string]bool, len(allowedModes))
	for _, m := range allowedModes {
		modeSet[m] = true
	}
	return func(c *gin.Context) {
		currentMode := a.getCachedSystemMode()
		if !modeSet[currentMode] {
			fail(c, 404, "route not available in current mode")
			return
		}
		c.Next()
	}
}

// requireSelfUse 仅自用模式可访问
func (a *App) requireSelfUse() gin.HandlerFunc {
	return a.requireModeMiddleware(ModeSelfUse)
}

// requireExternalOperation 仅运营模式可访问
func (a *App) requireExternalOperation() gin.HandlerFunc {
	return a.requireModeMiddleware(ModeExternalOperation)
}

// getModeMiddleware 根据资源表名返回对应的模式检查中间件切片
func (a *App) getModeMiddleware(table string) []gin.HandlerFunc {
	mode := resourceMode(table)
	if mode == "" {
		// 两种模式都可访问，不需要额外中间件
		return []gin.HandlerFunc{}
	}
	return []gin.HandlerFunc{a.requireModeMiddleware(mode)}
}
