package adapter

import (
	"fmt"
	"net/smtp"
	"strings"
)

// EmailNotifier 邮件推送适配器
type EmailNotifier struct {
	SMTPHost string // SMTP 服务器地址
	SMTPPort string // SMTP 服务器端口
	Username string // 发件人邮箱
	Password string // 邮箱授权码
	From     string // 发件人显示名称
	To       string // 收件人列表（逗号分隔）
}

// NewEmailNotifier 根据配置创建邮件推送适配器
func NewEmailNotifier(config map[string]interface{}) (*EmailNotifier, error) {
	host, _ := config["smtp_host"].(string)
	port, _ := config["smtp_port"].(string)
	username, _ := config["username"].(string)
	password, _ := config["password"].(string)
	to, _ := config["to"].(string)

	if host == "" || username == "" || to == "" {
		return nil, fmt.Errorf("邮件推送需要配置 smtp_host、username 和 to")
	}
	if port == "" {
		port = "587"
	}

	return &EmailNotifier{
		SMTPHost: host,
		SMTPPort: port,
		Username: username,
		Password: password,
		From:     username,
		To:       to,
	}, nil
}

// Send 发送告警通知邮件
func (e *EmailNotifier) Send(payload NotifyPayload) error {
	// 构造邮件主题
	subject := fmt.Sprintf("[裸金属告警] %s - %s", payload.Severity, payload.AssetName)

	// 构造邮件正文（纯文本格式）
	body := fmt.Sprintf(
		"裸金属设备告警通知\n\n"+
			"告警级别: %s\n"+
			"设备名称: %s\n"+
			"设备 ID: %d\n"+
			"告警规则: %s\n"+
			"告警内容: %s\n"+
			"告警时间: %s\n",
		payload.Severity,
		payload.AssetName,
		payload.AssetID,
		payload.RuleName,
		payload.Message,
		payload.CreatedAt,
	)

	// 组装邮件
	addr := e.SMTPHost + ":" + e.SMTPPort
	recipients := strings.Split(e.To, ",")
	for i := range recipients {
		recipients[i] = strings.TrimSpace(recipients[i])
	}

	msg := fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n%s",
		e.From,
		strings.Join(recipients, ","),
		subject,
		body,
	)

	// 发送邮件
	auth := smtp.PlainAuth("", e.Username, e.Password, e.SMTPHost)
	if err := smtp.SendMail(addr, auth, e.Username, recipients, []byte(msg)); err != nil {
		return fmt.Errorf("发送邮件失败: %w", err)
	}
	return nil
}

// Type 返回渠道类型标识
func (e *EmailNotifier) Type() string {
	return "email"
}
