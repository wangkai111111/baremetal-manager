// Package seeds 提供数据库种子数据初始化功能，确保系统启动时具备默认配置。
package seeds

import (
	"log"

	"baremetal-manager/backend/internal/model"
	"gorm.io/gorm"
)

// SeedData 初始化种子数据：默认系统设置项。
// 使用 FirstOrCreate 保证幂等，重复执行不会产生重复数据。
func SeedData(db *gorm.DB) error {
	// 1. 初始化默认系统设置
	settings := []model.SystemSetting{
		{Key: "bmc.timeout", Value: "12", Description: "BMC 操作超时时间（秒）", Category: "bmc"},
		{Key: "bmc.max_concurrent", Value: "10", Description: "BMC 最大并发数", Category: "bmc"},
		{Key: "sensor.interval", Value: "300", Description: "传感器采集间隔（秒）", Category: "monitor"},
		{Key: "alert.deduplicate_window", Value: "300", Description: "告警去重窗口（秒）", Category: "alert"},
		{Key: "alert.default_cpu_temp", Value: "85", Description: "默认 CPU 温度告警阈值", Category: "alert"},
		{Key: "alert.default_fan_speed", Value: "1000", Description: "默认风扇转速告警阈值", Category: "alert"},
		{Key: "sol.session_timeout", Value: "15", Description: "SOL 会话超时（分钟）", Category: "sol"},
		{Key: "sol.log_enabled", Value: "true", Description: "SOL 日志记录开关", Category: "sol"},
		// 平台品牌默认设置
		{Key: "brand.platform_name", Value: "METALOPS", Description: "平台名称", Category: "brand"},
		{Key: "brand.platform_tagline", Value: "BAREMETAL CONTROL PLANE", Description: "平台副标题", Category: "brand"},
		{Key: "brand.logo_url", Value: "", Description: "平台 Logo（图片 URL 或 Base64 数据）", Category: "brand"},
		{Key: "theme.nav_bg", Value: "#101e2b", Description: "导航栏背景色", Category: "theme"},
		{Key: "theme.nav_text", Value: "#ffffff", Description: "导航栏文字颜色", Category: "theme"},
		{Key: "app.footer_text", Value: "控制平面在线", Description: "侧边栏底部文案", Category: "app"},
		{Key: "app.footer_visible", Value: "true", Description: "是否显示侧边栏底部状态", Category: "app"},
	}
	for i := range settings {
		s := &settings[i]
		var count int64
		db.Model(&model.SystemSetting{}).Where("\"key\" = ?", s.Key).Count(&count)
		if count == 0 {
			if err := db.Create(s).Error; err != nil {
				log.Printf("[种子数据] 创建系统设置 %s 失败: %v", s.Key, err)
			}
		}
	}

	// 2. 角色权限已在 repository.RolePermissions 中定义，无需入库
	log.Println("[种子数据] 初始化完成")
	return nil
}
