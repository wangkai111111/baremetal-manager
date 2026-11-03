package task

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hibiken/asynq"
)

// NewPowerTask 创建电源操作任务
func NewPowerTask(payload PowerAction) (*asynq.Task, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("序列化电源操作参数失败: %w", err)
	}
	taskType := TypePowerOn
	switch payload.Action {
	case "off":
		taskType = TypePowerOff
	case "restart":
		taskType = TypePowerRestart
	}
	return asynq.NewTask(taskType, data), nil
}

// NewInspectTask 创建硬件检查任务
func NewInspectTask(payload InspectAction) (*asynq.Task, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("序列化硬件检查参数失败: %w", err)
	}
	return asynq.NewTask(TypeInspect, data), nil
}

// NewReinstallTask 创建装机任务
func NewReinstallTask(payload ReinstallAction) (*asynq.Task, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("序列化装机参数失败: %w", err)
	}
	return asynq.NewTask(TypeReinstall, data), nil
}

// NewFirmwareTask 创建固件升级任务
func NewFirmwareTask(payload FirmwareUpgradeAction) (*asynq.Task, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("序列化固件升级参数失败: %w", err)
	}
	return asynq.NewTask(TypeFirmwareUpgrade, data), nil
}

// NewScanTask 创建网段扫描任务
func NewScanTask(payload ScanNetworkAction) (*asynq.Task, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("序列化网段扫描参数失败: %w", err)
	}
	return asynq.NewTask(TypeScanNetwork, data), nil
}

// NewBatchTask 创建批量操作任务
func NewBatchTask(payload BatchAction) (*asynq.Task, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("序列化批量操作参数失败: %w", err)
	}
	return asynq.NewTask(TypeBatchAction, data), nil
}

// HandleTask 统一任务处理入口
// 根据任务类型分发到对应的处理器函数
func HandleTask(ctx context.Context, t *asynq.Task) error {
	switch t.Type() {
	case TypePowerOn, TypePowerOff, TypePowerRestart:
		return handlePowerTask(ctx, t)
	case TypeInspect:
		return handleInspectTask(ctx, t)
	case TypeReinstall:
		return handleReinstallTask(ctx, t)
	case TypeFirmwareUpgrade:
		return handleFirmwareTask(ctx, t)
	case TypeScanNetwork:
		return handleScanTask(ctx, t)
	case TypeBatchAction:
		return handleBatchTask(ctx, t)
	default:
		return fmt.Errorf("未知的任务类型: %s", t.Type())
	}
}

// handlePowerTask 处理电源操作任务
func handlePowerTask(ctx context.Context, t *asynq.Task) error {
	var payload PowerAction
	if err := json.Unmarshal(t.Payload(), &payload); err != nil {
		return fmt.Errorf("解析电源操作参数失败: %w", err)
	}
	// TODO: 在 Task #6 中实现具体的 BMC 电源操作逻辑
	return nil
}

// handleInspectTask 处理硬件检查任务
func handleInspectTask(ctx context.Context, t *asynq.Task) error {
	var payload InspectAction
	if err := json.Unmarshal(t.Payload(), &payload); err != nil {
		return fmt.Errorf("解析硬件检查参数失败: %w", err)
	}
	// TODO: 在 Task #6 中实现具体的硬件检查逻辑
	return nil
}

// handleReinstallTask 处理装机任务
func handleReinstallTask(ctx context.Context, t *asynq.Task) error {
	var payload ReinstallAction
	if err := json.Unmarshal(t.Payload(), &payload); err != nil {
		return fmt.Errorf("解析装机参数失败: %w", err)
	}
	// TODO: 在 Task #6 中实现具体的系统装机逻辑
	return nil
}

// handleFirmwareTask 处理固件升级任务
func handleFirmwareTask(ctx context.Context, t *asynq.Task) error {
	var payload FirmwareUpgradeAction
	if err := json.Unmarshal(t.Payload(), &payload); err != nil {
		return fmt.Errorf("解析固件升级参数失败: %w", err)
	}
	// TODO: 在 Task #6 中实现具体的固件升级逻辑
	return nil
}

// handleScanTask 处理网段扫描任务
func handleScanTask(ctx context.Context, t *asynq.Task) error {
	var payload ScanNetworkAction
	if err := json.Unmarshal(t.Payload(), &payload); err != nil {
		return fmt.Errorf("解析网段扫描参数失败: %w", err)
	}
	// TODO: 在 Task #6 中实现具体的网段扫描逻辑
	return nil
}

// handleBatchTask 处理批量操作任务
func handleBatchTask(ctx context.Context, t *asynq.Task) error {
	var payload BatchAction
	if err := json.Unmarshal(t.Payload(), &payload); err != nil {
		return fmt.Errorf("解析批量操作参数失败: %w", err)
	}
	// TODO: 在 Task #6 中实现具体的批量操作逻辑
	return nil
}
