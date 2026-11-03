package adapter

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// WechatNotifier 企业微信机器人推送适配器
type WechatNotifier struct {
	WebhookURL string // 企业微信机器人 Webhook 地址
}

// NewWechatNotifier 根据配置创建企业微信推送适配器
func NewWechatNotifier(config map[string]interface{}) (*WechatNotifier, error) {
	webhookURL, ok := config["webhook_url"].(string)
	if !ok || webhookURL == "" {
		return nil, fmt.Errorf("企业微信推送需要配置 webhook_url")
	}
	return &WechatNotifier{WebhookURL: webhookURL}, nil
}

// Send 发送告警通知到企业微信机器人
func (w *WechatNotifier) Send(payload NotifyPayload) error {
	// 构造 Markdown 格式的告警消息
	content := fmt.Sprintf(
		"## 裸金属设备告警\n"+
			"> **告警级别**: <font color=\"warning\">%s</font>\n"+
			"> **设备名称**: %s\n"+
			"> **告警规则**: %s\n"+
			"> **告警内容**: %s\n"+
			"> **告警时间**: %s\n",
		payload.Severity,
		payload.AssetName,
		payload.RuleName,
		payload.Message,
		payload.CreatedAt,
	)

	body := map[string]interface{}{
		"msgtype": "markdown",
		"markdown": map[string]string{
			"content": content,
		},
	}
	data, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("序列化企业微信消息失败: %w", err)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Post(w.WebhookURL, "application/json", bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("发送企业微信消息失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("企业微信返回非200状态码: %d", resp.StatusCode)
	}

	// 解析响应检查业务错误
	var result struct {
		ErrCode int    `json:"errcode"`
		ErrMsg  string `json:"errmsg"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("解析企业微信响应失败: %w", err)
	}
	if result.ErrCode != 0 {
		return fmt.Errorf("企业微信推送失败: %s", result.ErrMsg)
	}
	return nil
}

// Type 返回渠道类型标识
func (w *WechatNotifier) Type() string {
	return "wechat"
}
