package adapter

import "fmt"

// NotifyPayload 告警推送数据
type NotifyPayload struct {
	AlertID   uint   `json:"alert_id"`
	Severity  string `json:"severity"`    // INFO / WARNING / CRITICAL
	Message   string `json:"message"`     // 告警消息内容
	AssetName string `json:"asset_name"`  // 设备名称
	AssetID   uint   `json:"asset_id"`    // 设备 ID
	ProjectID uint   `json:"project_id"`  // 项目 ID
	RuleName  string `json:"rule_name"`   // 触发的规则名称
	CreatedAt string `json:"created_at"`  // 告警创建时间
}

// Notifier 告警推送适配器接口
type Notifier interface {
	// Send 发送告警通知
	Send(payload NotifyPayload) error
	// Type 返回渠道类型标识
	Type() string
}

// NewNotifier 根据渠道类型创建对应的推送适配器
func NewNotifier(channelType string, config map[string]interface{}) (Notifier, error) {
	switch channelType {
	case "wechat":
		return NewWechatNotifier(config)
	case "dingtalk":
		return NewDingtalkNotifier(config)
	case "webhook":
		return NewWebhookNotifier(config)
	case "email":
		return NewEmailNotifier(config)
	default:
		return nil, fmt.Errorf("不支持的推送渠道: %s", channelType)
	}
}
