package adapter

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// WebhookNotifier 通用 Webhook 推送适配器
type WebhookNotifier struct {
	URL string // Webhook 回调地址
}

// NewWebhookNotifier 根据配置创建通用 Webhook 推送适配器
func NewWebhookNotifier(config map[string]interface{}) (*WebhookNotifier, error) {
	url, ok := config["url"].(string)
	if !ok || url == "" {
		return nil, fmt.Errorf("Webhook推送需要配置 url")
	}
	return &WebhookNotifier{URL: url}, nil
}

// Send 直接 POST JSON 格式的告警数据到配置的 URL
func (w *WebhookNotifier) Send(payload NotifyPayload) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("序列化Webhook消息失败: %w", err)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Post(w.URL, "application/json", bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("发送Webhook消息失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("Webhook返回非2xx状态码: %d", resp.StatusCode)
	}
	return nil
}

// Type 返回渠道类型标识
func (w *WebhookNotifier) Type() string {
	return "webhook"
}
