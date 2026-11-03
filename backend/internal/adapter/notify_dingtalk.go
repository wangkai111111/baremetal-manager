package adapter

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// DingtalkNotifier 钉钉机器人推送适配器
type DingtalkNotifier struct {
	WebhookURL string // 钉钉机器人 Webhook 地址
}

// NewDingtalkNotifier 根据配置创建钉钉推送适配器
func NewDingtalkNotifier(config map[string]interface{}) (*DingtalkNotifier, error) {
	webhookURL, ok := config["webhook_url"].(string)
	if !ok || webhookURL == "" {
		return nil, fmt.Errorf("钉钉推送需要配置 webhook_url")
	}
	return &DingtalkNotifier{WebhookURL: webhookURL}, nil
}

// Send 发送告警通知到钉钉机器人
func (d *DingtalkNotifier) Send(payload NotifyPayload) error {
	// 构造 Markdown 格式的告警消息
	text := fmt.Sprintf(
		"## 裸金属设备告警\n\n"+
			"**告警级别**: %s\n\n"+
			"**设备名称**: %s\n\n"+
			"**告警规则**: %s\n\n"+
			"**告警内容**: %s\n\n"+
			"**告警时间**: %s\n",
		payload.Severity,
		payload.AssetName,
		payload.RuleName,
		payload.Message,
		payload.CreatedAt,
	)

	body := map[string]interface{}{
		"msgtype": "markdown",
		"markdown": map[string]string{
			"title": "告警通知",
			"text":  text,
		},
	}
	data, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("序列化钉钉消息失败: %w", err)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Post(d.WebhookURL, "application/json", bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("发送钉钉消息失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("钉钉返回非200状态码: %d", resp.StatusCode)
	}

	// 解析响应检查业务错误
	var result struct {
		ErrCode int    `json:"errcode"`
		ErrMsg  string `json:"errmsg"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("解析钉钉响应失败: %w", err)
	}
	if result.ErrCode != 0 {
		return fmt.Errorf("钉钉推送失败: %s", result.ErrMsg)
	}
	return nil
}

// Type 返回渠道类型标识
func (d *DingtalkNotifier) Type() string {
	return "dingtalk"
}
