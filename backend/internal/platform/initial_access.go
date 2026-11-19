package platform

import (
	"encoding/json"
	"net/netip"
	"strings"
	"time"

	"baremetal-manager/backend/internal/service"
	"github.com/gin-gonic/gin"
)

// Passwords are available only through this scoped, audited, non-cacheable API.
// Neither resource listings nor audit records contain the decrypted credential.
func (a *App) initialAccess(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	c.Header("Pragma", "no-cache")
	sc := contextScope(c)
	var asset struct {
		ID                 int64
		Name, ManagementIP string
	}
	if err := queryScope(a.store.db.WithContext(c.Request.Context()), resource{table: "assets"}, sc).
		Select("id,name,COALESCE(host(management_ip),'') AS management_ip").Where("id=?", number(c.Param("id"))).Take(&asset).Error; err != nil {
		fail(c, 404, "服务器不存在或无权访问")
		return
	}
	var row struct {
		ConfigSnapshot json.RawMessage
		Status         string
		CreatedAt      time.Time
	}
	err := a.store.db.WithContext(c.Request.Context()).Table("installations").
		Select("config_snapshot,status,created_at").Where("asset_id=? AND tenant_id=? AND project_id=? AND deleted_at IS NULL", asset.ID, sc.Tenant, sc.Project).Order("id DESC").Take(&row).Error
	if err != nil {
		fail(c, 404, "该服务器尚无安装记录，暂无初始登录信息")
		return
	}
	var snap installationSnapshot
	if json.Unmarshal(row.ConfigSnapshot, &snap) != nil {
		fail(c, 500, "安装登录信息读取失败")
		return
	}
	password := ""
	if snap.Access != nil && snap.Access.RootPasswordEncrypted != "" {
		password, err = service.DecryptCredential(snap.Access.RootPasswordEncrypted)
		if err != nil {
			fail(c, 503, "初始密码无法解密，请检查服务端凭据密钥")
			return
		}
	}
	if err := a.store.recordAudit(a.store.db.WithContext(c.Request.Context()), sc, requestMeta(c, sc.User), "initial-access:read", "assets", asset.ID, "success"); err != nil {
		fail(c, 500, "登录信息访问记录失败")
		return
	}
	address := strings.TrimSpace(asset.ManagementIP)
	if address == "" {
		address = snap.IP
	}
	command := ""
	if ip, err := netip.ParseAddr(address); err == nil {
		command = "ssh root@" + ip.String()
	} else {
		address = ""
	}
	policy := "no"
	keys := []string{}
	generated := false
	if snap.Access != nil {
		policy = snap.Access.RootSSHLogin
		keys = snap.Access.SSHAuthorizedKeys
		generated = snap.Access.PasswordGenerated
	}
	note := "初始凭据不反映安装后手动修改的密码。"
	if password == "" {
		note = "旧安装未保存可恢复的初始密码。"
	}
	if row.Status != "succeeded" {
		note += "最近一次安装尚未成功，本次凭据尚未验证生效。"
	}
	ok(c, gin.H{"asset_name": asset.Name, "username": "root", "password": password, "password_generated": generated, "address": address, "ssh_command": command, "root_ssh_login": policy, "ssh_authorized_keys": keys, "installation_status": row.Status, "installed_at": row.CreatedAt, "note": note})
}
