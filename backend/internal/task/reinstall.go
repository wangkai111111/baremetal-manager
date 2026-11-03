package task

import (
	"context"
	"fmt"

	"baremetal-manager/backend/internal/repository"
)

// ReinstallProcessor 装机任务处理器
type ReinstallProcessor struct {
	Store *repository.Store // 数据存储
}

// NewReinstallProcessor 创建装机任务处理器实例
func NewReinstallProcessor(store *repository.Store) *ReinstallProcessor {
	return &ReinstallProcessor{Store: store}
}

// Process 执行装机任务
// 流程：验证模板 → 获取镜像 → 配置 PXE 引导 → 触发安装 → 监控进度
func (p *ReinstallProcessor) Process(ctx context.Context, payload ReinstallAction) error {
	// TODO: 在 Task #6 中实现具体的装机逻辑
	// 1. 根据 TemplateID 查询 KickstartTemplate
	// 2. 根据 ImageID 查询 Image 信息
	// 3. 查询 Asset 获取 BMC 连接信息
	// 4. 配置 PXE 引导（通过 dnsmasq）
	// 5. 通过 BMC 设置启动设备为 PXE/CD-ROM
	// 6. 重启设备触发安装
	// 7. 监控安装进度并更新 Operation 状态
	return fmt.Errorf("装机功能尚未实现")
}

// ValidateTemplate 验证装机模板是否可用
func (p *ReinstallProcessor) ValidateTemplate(templateID uint) error {
	// TODO: 在 Task #6 中实现模板验证逻辑
	return nil
}

// MonitorProgress 监控装机进度
func (p *ReinstallProcessor) MonitorProgress(ctx context.Context, assetID uint) error {
	// TODO: 在 Task #6 中实现进度监控逻辑
	return nil
}
