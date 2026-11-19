package platform

import (
	"crypto/sha256"
	"encoding/hex"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"strings"
	"time"
	"unicode"
)

func loginTokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
func (a *App) authAudit(tx *gorm.DB, c *gin.Context, u user, action, outcome string) error {
	message := auditActions[action] + "成功"
	if outcome != "success" {
		message = auditActions[action] + "失败：账号、密码或账号状态不符合要求"
		if action == "auth:login" {
			account := []rune(strings.Map(func(r rune) rune {
				if unicode.IsControl(r) {
					return -1
				}
				return r
			}, u.Username))
			if len(account) > 128 {
				account = account[:128]
			}
			if len(account) > 0 {
				message += "（尝试账号：" + string(account) + "）"
			}
		}
	}
	return a.store.recordAuditNote(tx, scope{User: u.ID}, requestMeta(c, u.ID), action, "users", u.ID, outcome, map[string]any{"message": message})
}
func (a *App) logout(c *gin.Context) {
	u := current(c)
	raw := strings.TrimPrefix(c.GetHeader("Authorization"), "Bearer ")
	expiry, _ := c.Get("login_expiry")
	expires, valid := expiry.(time.Time)
	if !valid {
		fail(c, 403, "仅登录会话可以退出")
		return
	}
	err := a.store.db.WithContext(c.Request.Context()).Transaction(func(tx *gorm.DB) error {
		result := tx.Table("revoked_login_tokens").Clauses(clause.OnConflict{DoNothing: true}).Create(map[string]any{"token_hash": loginTokenHash(raw), "user_id": u.ID, "expires_at": expires})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return nil
		}
		if err := tx.Table("revoked_login_tokens").Where("expires_at < now()").Delete(&struct{}{}).Error; err != nil {
			return err
		}
		return a.authAudit(tx, c, u, "auth:logout", "success")
	})
	if err != nil {
		fail(c, 500, "退出失败，审计记录未保存，请重试")
		return
	}
	ok(c, nil)
}
