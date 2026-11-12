package platform

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"baremetal-manager/backend/internal/service"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type notificationChannel struct {
	ID, TenantID, ProjectID int64
	Kind                    string
	Config                  json.RawMessage
	SecretCiphertext        []byte
	SecretKeyID             string
	Enabled                 bool
}

type channelConfig struct {
	Host     string `json:"host,omitempty"`
	Port     int    `json:"port,omitempty"`
	Username string `json:"username,omitempty"`
	From     string `json:"from,omitempty"`
	To       string `json:"to,omitempty"`
	TLS      bool   `json:"tls,omitempty"`
}

func validWebhook(raw string, kind string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Fragment != "" || (u.Port() != "" && u.Port() != "443") {
		return false
	}
	switch kind {
	case "lark":
		if u.Hostname() != "open.feishu.cn" && u.Hostname() != "open.larksuite.com" {
			return false
		}
	case "wechat":
		if u.Hostname() != "qyapi.weixin.qq.com" {
			return false
		}
	case "dingtalk":
		if u.Hostname() != "oapi.dingtalk.com" {
			return false
		}
	}
	if ip, err := netip.ParseAddr(u.Hostname()); err == nil && !publicNotificationIP(ip) {
		return false
	}
	return true
}
func publicNotificationIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	return ip.IsGlobalUnicast() && !ip.IsPrivate() && !ip.IsLoopback() && !ip.IsLinkLocalUnicast() && !netip.MustParsePrefix("100.64.0.0/10").Contains(ip)
}

func validateChannel(kind string, config json.RawMessage, secret string) error {
	var cfg channelConfig
	decoder := json.NewDecoder(bytes.NewReader(config))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&cfg) != nil || decoder.Decode(new(any)) != io.EOF {
		return errors.New("invalid channel configuration")
	}
	switch kind {
	case "email":
		in := smtpTestRequest{Host: cfg.Host, Port: cfg.Port, Username: cfg.Username, Password: secret, From: cfg.From, To: cfg.To, TLS: cfg.TLS}
		if !in.valid() || (in.Username == "") != (in.Password == "") || (in.Username != "" && !in.TLS && in.Port != 465) {
			return errors.New("invalid SMTP configuration; authentication requires TLS and a password")
		}
	case "webhook", "lark", "wechat", "dingtalk":
		if string(config) != "{}" || !validWebhook(secret, kind) {
			return errors.New("provide an HTTPS webhook URL as secret; config must be {}")
		}
	default:
		return errors.New("supported channels: email, webhook, lark, wechat, dingtalk")
	}
	return nil
}

func (a *App) writeChannel(create bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		var in struct {
			Name    string          `json:"name"`
			Kind    string          `json:"kind"`
			Enabled *bool           `json:"enabled"`
			Config  json.RawMessage `json:"config"`
			Secret  *string         `json:"secret"`
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16<<10)
		dec := json.NewDecoder(c.Request.Body)
		dec.DisallowUnknownFields()
		if dec.Decode(&in) != nil || dec.Decode(new(any)) != io.EOF || strings.TrimSpace(in.Name) == "" || len(in.Name) > 255 {
			fail(c, 400, "invalid channel settings")
			return
		}
		sc := contextScope(c)
		id := number(c.Param("id"))
		secret := ""
		if !create {
			var old notificationChannel
			if queryScope(a.store.db.WithContext(c.Request.Context()), resource{table: "alert_channels"}, sc).Where("id=?", id).Take(&old).Error != nil {
				fail(c, 404, "channel not found")
				return
			}
			if old.SecretKeyID != "" {
				var err error
				secret, err = service.DecryptCredential(string(old.SecretCiphertext))
				if err != nil || old.SecretKeyID != "v1" {
					fail(c, 500, "channel credentials unavailable")
					return
				}
			}
			if in.Kind == "" {
				in.Kind = old.Kind
			}
			if len(in.Config) == 0 {
				in.Config = old.Config
			}
		}
		if len(in.Config) == 0 {
			in.Config = json.RawMessage(`{}`)
		}
		if in.Secret != nil {
			secret = *in.Secret
		}
		if len(secret) > 4096 {
			fail(c, 400, "channel secret too long")
			return
		}
		var normalized any
		if json.Unmarshal(in.Config, &normalized) != nil {
			fail(c, 400, "invalid channel config")
			return
		}
		in.Config, _ = json.Marshal(normalized)
		if err := validateChannel(in.Kind, in.Config, secret); err != nil {
			fail(c, 400, err.Error())
			return
		}
		values := map[string]any{"name": in.Name, "kind": in.Kind, "config": gorm.Expr("?::jsonb", string(in.Config)), "last_test_status": "untested", "last_test_at": nil, "last_test_message": nil, "test_claim_until": nil}
		if in.Enabled != nil {
			values["enabled"] = *in.Enabled
		}
		if secret != "" {
			cipher, err := service.EncryptCredential(secret)
			if err != nil {
				fail(c, 500, "channel encryption failed")
				return
			}
			values["secret_ciphertext"] = []byte(cipher)
			values["secret_key_id"] = "v1"
		} else {
			values["secret_ciphertext"] = nil
			values["secret_key_id"] = nil
		}
		if err := a.store.saveResource(c.Request.Context(), resource{table: "alert_channels"}, sc, id, values, create, &id); err != nil {
			fail(c, 409, "channel save conflict")
			return
		}
		ok(c, gin.H{"id": id})
	}
}

func sendChannel(ctx context.Context, channel notificationChannel, subject, message string) error {
	secret := ""
	if channel.SecretKeyID != "" {
		var err error
		if channel.SecretKeyID != "v1" {
			return errors.New("unsupported credential key")
		}
		secret, err = service.DecryptCredential(string(channel.SecretCiphertext))
		if err != nil {
			return errors.New("channel credentials unavailable")
		}
	}
	if err := validateChannel(channel.Kind, channel.Config, secret); err != nil {
		return err
	}
	if channel.Kind == "email" {
		var cfg channelConfig
		_ = json.Unmarshal(channel.Config, &cfg)
		return sendSMTPMessage(ctx, smtpTestRequest{Host: cfg.Host, Port: cfg.Port, Username: cfg.Username, Password: secret, From: cfg.From, To: cfg.To, TLS: cfg.TLS}, subject, message)
	}
	payload := map[string]any{"subject": subject, "message": message}
	switch channel.Kind {
	case "lark":
		payload = map[string]any{"msg_type": "text", "content": map[string]string{"text": subject + "\n" + message}}
	case "wechat":
		payload = map[string]any{"msgtype": "text", "text": map[string]string{"content": subject + "\n" + message}}
	case "dingtalk":
		payload = map[string]any{"msgtype": "text", "text": map[string]string{"content": subject + "\n" + message}}
	}
	body, _ := json.Marshal(payload)
	transport := &http.Transport{Proxy: nil, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		if err != nil || len(ips) == 0 {
			return nil, errors.New("webhook DNS failed")
		}
		for _, ip := range ips {
			if !publicNotificationIP(ip) {
				return nil, errors.New("webhook target is not public")
			}
		}
		return (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, network, net.JoinHostPort(ips[0].String(), port))
	}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("redirect forbidden") }}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, secret, bytes.NewReader(body))
	if err != nil {
		return errors.New("invalid webhook")
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := client.Do(req)
	if err != nil {
		return errors.New("webhook request failed")
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return errors.New("webhook rejected message")
	}
	if channel.Kind != "webhook" {
		var result struct {
			Code       *int `json:"code"`
			StatusCode *int `json:"StatusCode"`
			ErrCode    *int `json:"errcode"`
		}
		if json.NewDecoder(io.LimitReader(res.Body, 16384)).Decode(&result) != nil {
			return errors.New("notification provider returned an invalid response")
		}
		accepted := result.ErrCode != nil && *result.ErrCode == 0
		if channel.Kind == "lark" {
			accepted = (result.Code != nil || result.StatusCode != nil) && (result.Code == nil || *result.Code == 0) && (result.StatusCode == nil || *result.StatusCode == 0)
		}
		if !accepted {
			return errors.New("notification provider rejected message")
		}
	}
	return nil
}

func (a *App) testChannel(c *gin.Context) {
	sc := contextScope(c)
	id := number(c.Param("id"))
	var channel notificationChannel
	claimUntil := time.Now().Add(time.Minute).UTC().Truncate(time.Microsecond)
	err := a.store.db.WithContext(c.Request.Context()).Transaction(func(tx *gorm.DB) error {
		if err := queryScope(tx, resource{table: "alert_channels"}, sc).Clauses(clause.Locking{Strength: "UPDATE"}).Where("id=? AND (test_claim_until IS NULL OR test_claim_until<now())", id).Take(&channel).Error; err != nil {
			return err
		}
		return tx.Table("alert_channels").Where("id=?", id).Update("test_claim_until", claimUntil).Error
	})
	if err != nil {
		fail(c, 409, "channel unavailable or test already running")
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 20*time.Second)
	defer cancel()
	err = sendChannel(ctx, channel, "[TEST] Baremetal notification channel", "This is a test notification. No real alert was created.")
	status, msg := "succeeded", "服务端已接受测试消息，不保证实际收信；本次测试未创建告警"
	if err != nil {
		status, msg = "failed", "测试失败，请检查配置、连接、TLS、凭据及接收权限"
	}
	finish, cancelFinish := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelFinish()
	result := a.store.db.WithContext(finish).Table("alert_channels").Where("id=? AND tenant_id=? AND project_id=? AND deleted_at IS NULL AND test_claim_until=?", id, sc.Tenant, sc.Project, claimUntil).Updates(map[string]any{"last_test_status": status, "last_test_at": time.Now(), "last_test_message": msg, "test_claim_until": nil})
	if result.Error != nil {
		fail(c, 500, "test result could not be saved")
		return
	}
	if result.RowsAffected != 1 {
		fail(c, 409, "channel changed during test; test again with current settings")
		return
	}
	if err != nil {
		fail(c, 502, msg)
		return
	}
	ok(c, gin.H{"message": msg, "status": status})
}

type notificationDelivery struct {
	ID, TenantID, ProjectID, AlertEventID, ChannelID int64
	Attempts                                         int
}

func (a *App) deliverNotification(ctx context.Context) {
	var job notificationDelivery
	err := a.store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(`UPDATE notification_deliveries SET status='failed',locked_until=NULL,last_error='Final delivery lease expired; outcome uncertain' WHERE deleted_at IS NULL AND status='sending' AND locked_until<now() AND attempts>=5`).Error; err != nil {
			return err
		}
		if err := tx.Raw(`SELECT id,tenant_id,project_id,alert_event_id,channel_id,attempts FROM notification_deliveries WHERE deleted_at IS NULL AND attempts<5 AND ((status='pending' AND next_attempt_at<=now()) OR (status='sending' AND locked_until<now())) ORDER BY id LIMIT 1 FOR UPDATE SKIP LOCKED`).Scan(&job).Error; err != nil {
			return err
		}
		if job.ID == 0 {
			return nil
		}
		job.Attempts++
		return tx.Table("notification_deliveries").Where("id=?", job.ID).Updates(map[string]any{"status": "sending", "attempts": job.Attempts, "locked_until": time.Now().Add(time.Minute)}).Error
	})
	if err != nil || job.ID == 0 {
		return
	}
	var channel notificationChannel
	var event struct{ Message string }
	status := "sent"
	err = queryScope(a.store.db.WithContext(ctx), resource{table: "alert_channels"}, scope{Tenant: job.TenantID, Project: job.ProjectID, Admin: true}).Where("id=? AND enabled", job.ChannelID).Where(`EXISTS (SELECT 1 FROM projects p JOIN tenants t ON t.id=p.tenant_id WHERE p.id=alert_channels.project_id AND p.tenant_id=alert_channels.tenant_id AND p.deleted_at IS NULL AND p.status='active' AND t.deleted_at IS NULL AND t.status='active')`).Take(&channel).Error
	if err != nil {
		status = "suppressed"
	} else {
		err = a.store.db.WithContext(ctx).Table("alert_events").Select("message").Where("id=? AND tenant_id=? AND project_id=? AND deleted_at IS NULL", job.AlertEventID, job.TenantID, job.ProjectID).Take(&event).Error
		if err == nil {
			sendCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
			err = sendChannel(sendCtx, channel, "Baremetal monitoring alert", event.Message)
			cancel()
		}
	}
	updates := map[string]any{"status": status, "locked_until": nil, "last_error": nil}
	if status == "suppressed" {
		updates["last_error"] = "Channel unavailable or disabled"
	} else if err != nil {
		updates["status"] = "pending"
		if job.Attempts >= 5 {
			updates["status"] = "failed"
		}
		updates["last_error"] = "Notification failed; check channel settings, connectivity and permissions"
		updates["next_attempt_at"] = time.Now().Add(time.Duration(job.Attempts*job.Attempts) * time.Minute)
	} else {
		updates["sent_at"] = time.Now()
	}
	finish, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	a.store.db.WithContext(finish).Table("notification_deliveries").Where("id=? AND status='sending' AND attempts=?", job.ID, job.Attempts).Updates(updates)
}
