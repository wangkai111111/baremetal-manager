package platform

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/mail"
	"net/smtp"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

type smtpTestRequest struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Username string `json:"username"`
	Password string `json:"password"`
	From     string `json:"from"`
	TLS      bool   `json:"tls"`
	To       string `json:"to"`
}

func (in smtpTestRequest) valid() bool {
	if in.Host == "" || len(in.Host) > 253 || in.Port < 1 || in.Port > 65535 ||
		len(in.Username) > 255 || len(in.Password) > 4096 || in.Password == "********" ||
		strings.ContainsAny(in.Host+in.Username, "\r\n") {
		return false
	}
	if net.ParseIP(in.Host) == nil {
		for _, label := range strings.Split(strings.TrimSuffix(in.Host, "."), ".") {
			if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
				return false
			}
			for _, ch := range label {
				if !(ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || ch == '-') {
					return false
				}
			}
		}
	}
	for _, value := range []string{in.From, in.To} {
		address, err := mail.ParseAddress(value)
		if err != nil || len(value) > 254 || strings.ContainsAny(value, "\r\n") || address.Name != "" || address.Address != value {
			return false
		}
	}
	return true
}

func (a *App) smtpTest(c *gin.Context) {
	if _, exists := c.Get("api_key"); exists {
		fail(c, 403, "API keys cannot test SMTP settings")
		return
	}
	if !current(c).IsPlatformAdmin {
		fail(c, 403, "platform administrator required")
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16<<10)
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	var in smtpTestRequest
	if decoder.Decode(&in) != nil || decoder.Decode(new(any)) != io.EOF || !in.valid() {
		fail(c, 400, "invalid SMTP test settings; provide a host, port 1-65535 and single plain email addresses; omit masked passwords")
		return
	}
	if in.Password == "" {
		var rows []struct{ Value string }
		if err := a.store.db.WithContext(c.Request.Context()).Table("system_settings").Select("value::text").Where("deleted_at IS NULL AND key = ?", "smtp.password").Limit(1).Scan(&rows).Error; err != nil {
			fail(c, 500, "SMTP credentials unavailable")
			return
		}
		if len(rows) > 0 && json.Unmarshal([]byte(rows[0].Value), &in.Password) != nil {
			fail(c, 500, "SMTP credentials unavailable")
			return
		}
		if in.Password == "********" {
			fail(c, 400, "saved SMTP password is a placeholder; enter a real password")
			return
		}
	}
	if in.Username == "" && in.Password != "" || in.Username != "" && in.Password == "" {
		fail(c, 400, "SMTP authentication requires both username and password")
		return
	}
	if in.Username != "" && !in.TLS && in.Port != 465 {
		fail(c, 400, "SMTP authentication requires TLS; enable STARTTLS or use port 465")
		return
	}
	if err := sendSMTPTest(c.Request.Context(), in); err != nil {
		fail(c, 502, "SMTP test failed; check connectivity, TLS certificate, STARTTLS support, credentials and sender/recipient permissions")
		return
	}
	ok(c, gin.H{"message": "SMTP server accepted the test message; inbox delivery is not guaranteed"})
}

func sendSMTPTest(parent context.Context, in smtpTestRequest) error {
	return sendSMTPMessage(parent, in, "[TEST] SMTP configuration test", "This is a test message from the baremetal management platform.")
}

func sendSMTPMessage(parent context.Context, in smtpTestRequest, subject, body string) error {
	ctx, cancel := context.WithTimeout(parent, 15*time.Second)
	defer cancel()
	conn, err := (&net.Dialer{Timeout: 15 * time.Second}).DialContext(ctx, "tcp", net.JoinHostPort(in.Host, strconv.Itoa(in.Port)))
	if err != nil {
		return errors.New("SMTP connection failed")
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	deadline, _ := ctx.Deadline()
	if err := conn.SetDeadline(deadline); err != nil {
		return errors.New("SMTP deadline failed")
	}
	config := &tls.Config{ServerName: in.Host, MinVersion: tls.VersionTLS12}
	var transport net.Conn = conn
	if in.Port == 465 {
		secure := tls.Client(conn, config)
		if err := secure.HandshakeContext(ctx); err != nil {
			return errors.New("SMTP TLS failed")
		}
		transport = secure
	}
	client, err := smtp.NewClient(transport, in.Host)
	if err != nil {
		return errors.New("SMTP greeting failed")
	}
	defer client.Close()
	if in.Port != 465 && in.TLS {
		if supported, _ := client.Extension("STARTTLS"); !supported {
			return errors.New("SMTP STARTTLS required")
		}
		if err := client.StartTLS(config); err != nil {
			return errors.New("SMTP STARTTLS failed")
		}
	}
	if in.Username != "" || in.Password != "" {
		if _, secure := client.TLSConnectionState(); !secure {
			return errors.New("SMTP authentication requires TLS")
		}
		if err := client.Auth(smtp.PlainAuth("", in.Username, in.Password, in.Host)); err != nil {
			return errors.New("SMTP authentication failed")
		}
	}
	if err := client.Mail(in.From); err != nil {
		return errors.New("SMTP sender rejected")
	}
	if err := client.Rcpt(in.To); err != nil {
		return errors.New("SMTP recipient rejected")
	}
	writer, err := client.Data()
	if err != nil {
		return errors.New("SMTP data rejected")
	}
	message := "From: " + in.From + "\r\nTo: " + in.To + "\r\nSubject: " + subject + "\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n" + body + "\r\n"
	if _, err := io.WriteString(writer, message); err != nil {
		return errors.New("SMTP message write failed")
	}
	if err := writer.Close(); err != nil {
		return errors.New("SMTP message not accepted")
	}
	return nil
}
