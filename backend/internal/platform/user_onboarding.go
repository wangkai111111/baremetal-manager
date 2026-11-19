package platform

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/mail"
	"strings"

	"github.com/gin-gonic/gin"
)

func newUserPassword() (string, error) {
	var raw [24]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw[:]), nil
}

func validUserEmail(value string) bool {
	address, err := mail.ParseAddress(value)
	return err == nil && len(value) <= 254 && address.Name == "" && address.Address == value && !strings.ContainsAny(value, "\r\n")
}

// Read the same saved global configuration used by the system SMTP test.
// Only the sending path sees the password; options expose readiness alone.
func (a *App) systemSMTP(ctx context.Context, to string) (smtpTestRequest, error) {
	values := map[string]any{}
	for key, value := range smtpDefaults {
		values[key] = value
	}
	var rows []publicSettingRow
	if err := a.store.db.WithContext(ctx).Table("system_settings").Select("key,value::text").Where("deleted_at IS NULL AND key IN ?", []string{"smtp.host", "smtp.port", "smtp.username", "smtp.password", "smtp.from", "smtp.tls"}).Scan(&rows).Error; err != nil {
		return smtpTestRequest{}, err
	}
	for _, row := range rows {
		var value any
		if json.Unmarshal([]byte(row.Value), &value) != nil || !validSetting(row.Key, value) {
			return smtpTestRequest{}, errors.New("invalid SMTP setting")
		}
		values[row.Key] = value
	}
	data, _ := json.Marshal(map[string]any{"host": values["smtp.host"], "port": values["smtp.port"], "username": values["smtp.username"], "password": values["smtp.password"], "from": values["smtp.from"], "tls": values["smtp.tls"], "to": to})
	var in smtpTestRequest
	if json.Unmarshal(data, &in) != nil || !in.valid() || (in.Username == "") != (in.Password == "") || in.Username != "" && !in.TLS && in.Port != 465 {
		return in, errors.New("SMTP not configured")
	}
	return in, nil
}

func (a *App) userCreateOptions(c *gin.Context) {
	_, err := a.systemSMTP(c.Request.Context(), "readiness@example.com")
	c.Header("Cache-Control", "no-store")
	ok(c, gin.H{"smtp_ready": err == nil})
}

func userWelcomeBody(username, password string, mustChange bool) string {
	policy := "登录后可在个人菜单中修改密码。"
	if mustChange {
		policy = "首次登录必须修改初始密码，修改完成后才能使用平台。"
	}
	return fmt.Sprintf("管理员已为您创建平台账号。\n\n登录用户名：%s\n初始密码：%s\n\n%s\n请通过管理员提供的平台地址登录，并妥善保管本邮件。", username, password, policy)
}
