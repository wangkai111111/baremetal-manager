package platform

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type ticket struct {
	Scope   scope
	Asset   int64
	Expires time.Time
}

var solTickets sync.Map
var solSlots = make(chan struct{}, 16)

func originAllowed(c *gin.Context) bool {
	origin := c.GetHeader("Origin")
	if origin == "" {
		return false
	}
	u, e := url.Parse(origin)
	if e != nil || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return false
	}
	scheme := "http"
	if c.Request.TLS != nil {
		scheme = "https"
	}
	if u.Scheme == scheme && u.Host == c.Request.Host {
		return true
	}
	return false
}
func (a *App) solTicket(c *gin.Context) {
	if !originAllowed(c) {
		fail(c, 403, "same-origin request required")
		return
	}
	sc := contextScope(c)
	var count int64
	queryScope(a.store.db, resource{table: "assets"}, sc).Where("id=?", number(c.Param("id"))).Count(&count)
	if count != 1 {
		fail(c, 404, "asset not found")
		return
	}
	key := uuid.NewString()
	solTickets.Store(key, ticket{sc, number(c.Param("id")), time.Now().Add(30 * time.Second)})
	time.AfterFunc(30*time.Second, func() { solTickets.Delete(key) })
	ok(c, gin.H{"ticket": key, "url": "/api/v1/sol/connect?ticket=" + key, "expires_in": 30})
}
func (a *App) solWithTicket(c *gin.Context) {
	v, ok := solTickets.LoadAndDelete(c.Query("ticket"))
	if !ok {
		fail(c, 401, "invalid SOL ticket")
		return
	}
	t := v.(ticket)
	u, e := a.store.activeUser(t.Scope.User)
	if e != nil || time.Now().After(t.Expires) {
		fail(c, 401, "expired SOL ticket")
		return
	}
	t.Scope.Admin = u.IsPlatformAdmin
	if !t.Scope.Admin && !a.store.member(t.Scope) {
		fail(c, 403, "project access denied")
		return
	}
	if !a.store.allowed(t.Scope, "sol:access") {
		fail(c, 403, "permission denied")
		return
	}
	c.Set("user", u)
	c.Set("scope", t.Scope)
	a.openSOL(c, t.Scope, t.Asset)
}
func (a *App) sol(c *gin.Context) { a.openSOL(c, contextScope(c), number(c.Param("id"))) }
func (a *App) openSOL(c *gin.Context, sc scope, id int64) {
	if !originAllowed(c) {
		fail(c, 403, "same-origin request required")
		return
	}
	select {
	case solSlots <- struct{}{}:
		defer func() { <-solSlots }()
	default:
		fail(c, 429, "SOL capacity exceeded")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	asset, d, e := a.loadDevice(ctx, sc, id)
	if e != nil {
		fail(c, 404, "asset unavailable")
		return
	}
	session := map[string]any{"tenant_id": sc.Tenant, "project_id": sc.Project, "asset_id": id, "user_id": sc.User, "node_id": a.node, "expires_at": time.Now().Add(15 * time.Minute)}
	if e = a.store.db.Table("sol_sessions").Clauses(clause.Returning{Columns: []clause.Column{{Name: "id"}}}).Create(&session).Error; e != nil {
		fail(c, 500, "session persistence failed")
		return
	}
	reason := "connection closed"
	defer func() {
		a.store.db.Table("sol_sessions").Where("id=? AND ended_at IS NULL", asInt(session["id"])).Updates(map[string]any{"ended_at": time.Now(), "end_reason": reason})
	}()
	port := d.BMCPort
	if d.BMCProtocol == "redfish" {
		port = 623
	}
	args := []string{"-I", "lanplus", "-H", d.BMCAddress, "-p", strconv.Itoa(port), "-U", d.BMCUser, "-E", "sol"}
	deactivate := func() {
		dctx, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		cmd := exec.CommandContext(dctx, "ipmitool", append(args, "deactivate")...)
		cmd.Env = append(cmd.Environ(), "IPMI_PASSWORD="+asset.BMCSecret)
		_ = cmd.Run()
	}
	deactivate()
	cmd := exec.CommandContext(ctx, "ipmitool", append(args, "activate")...)
	cmd.Env = append(cmd.Environ(), "IPMI_PASSWORD="+asset.BMCSecret)
	stdin, e := cmd.StdinPipe()
	if e != nil {
		fail(c, 500, "SOL process unavailable")
		return
	}
	stdout, e := cmd.StdoutPipe()
	if e != nil {
		fail(c, 500, "SOL process unavailable")
		return
	}
	if e = cmd.Start(); e != nil {
		fail(c, 502, "SOL connection failed")
		return
	}
	defer deactivate()
	defer func() { cancel(); _ = cmd.Wait() }()
	upgrader := websocket.Upgrader{CheckOrigin: func(_ *http.Request) bool { return originAllowed(c) }}
	ws, e := upgrader.Upgrade(c.Writer, c.Request, nil)
	if e != nil {
		return
	}
	defer ws.Close()
	ws.SetReadLimit(4096)
	go func() { <-ctx.Done(); _ = ws.Close() }()
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				var count int64
				if err := queryScope(a.store.db.WithContext(ctx), resource{table: "assets"}, sc).Where("id=? AND COALESCE(subscription_state,'legacy') <> 'reclaim_pending'", id).Count(&count).Error; err != nil || count == 0 {
					cancel()
					return
				}
			}
		}
	}()
	go func() {
		defer cancel()
		for {
			_, data, e := ws.ReadMessage()
			if e != nil {
				return
			}
			if _, e = stdin.Write(data); e != nil {
				return
			}
		}
	}()
	buf := make([]byte, 4096)
	for {
		n, e := stdout.Read(buf)
		if n > 0 {
			ws.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if e = ws.WriteMessage(websocket.BinaryMessage, buf[:n]); e != nil {
				return
			}
		}
		if e != nil {
			if ctx.Err() != nil {
				reason = "expired or disconnected"
			}
			return
		}
	}
}

// 只公开品牌与运营白名单设置，不把敏感设置或任意数据库字段投射到前端。
var brandDefaults = map[string]any{"brand.platform_name": "METALOPS", "brand.platform_tagline": "BAREMETAL CONTROL PLANE", "brand.logo_url": "", "theme.nav_bg": "#101e2b", "theme.nav_text": "#ffffff", "app.footer_text": "控制平面在线", "app.footer_visible": true, "app.login_notice": "", "platform.operation_mode": "self_hosted", "registration.enabled": false, "registration.email_verification": false, "billing.enabled": false, "billing.model_prices": []any{}}

var smtpDefaults = map[string]any{"smtp.host": "", "smtp.port": 587, "smtp.username": "", "smtp.password": "", "smtp.from": "", "smtp.tls": true}

func allSettingDefaults() map[string]any {
	out := map[string]any{}
	for k, v := range brandDefaults {
		out[k] = v
	}
	for k, v := range smtpDefaults {
		out[k] = v
	}
	return out
}

// legacyKeyMapping maps unprefixed bootstrap seed keys to their prefixed equivalents.
var legacyKeyMapping = map[string]string{
	"platform_name":  "brand.platform_name",
	"logo_url":       "brand.logo_url",
	"nav_color":      "theme.nav_bg",
	"sidebar_color":  "theme.nav_text",
	"sidebar_footer": "app.footer_text",
	"login_notice":   "app.login_notice",
}

func (a *App) publicSettings() map[string]any {
	out := map[string]any{}
	for k, v := range brandDefaults {
		out[k] = v
	}
	for k, v := range smtpDefaults {
		if k != "smtp.password" {
			out[k] = v
		}
	}
	var rows []struct {
		Key   string
		Value string
	}
	// 查询所有已知前缀 key + 旧版无前缀 key
	allKeys := settingKeys()
	legacyKeys := make([]string, 0, len(legacyKeyMapping))
	for k := range legacyKeyMapping {
		legacyKeys = append(legacyKeys, k)
	}
	queryKeys := append(allKeys, legacyKeys...)
	a.store.db.Table("system_settings").Select("key,value::text").Where("deleted_at IS NULL AND key IN ?", queryKeys).Scan(&rows)
	for _, r := range rows {
		if r.Key == "smtp.password" {
			continue
		}
		var value any
		if json.Unmarshal([]byte(r.Value), &value) == nil {
			// 映射旧版无前缀 key 到前缀 key
			effectiveKey := r.Key
			if mapped, isLegacy := legacyKeyMapping[r.Key]; isLegacy {
				effectiveKey = mapped
			}
			if validSetting(effectiveKey, value) {
				out[effectiveKey] = value
			}
		}
	}
	var smtpConfigured int64
	a.store.db.Table("system_settings").Where("deleted_at IS NULL AND key='smtp.password' AND value::text <> '\"\"'").Count(&smtpConfigured)
	if smtpConfigured > 0 {
		out["smtp.password"] = "********"
	}
	return out
}
func settingKeys() []string {
	out := []string{}
	for k := range allSettingDefaults() {
		out = append(out, k)
	}
	return out
}
func (a *App) brand(c *gin.Context) {
	s := a.publicSettings()
	// 用权威源 system_modes 表读取 operation_mode
	operationMode := s["platform.operation_mode"]
	if mode, err := GetSystemMode(a.store.db); err == nil && mode != "" {
		switch mode {
		case ModeExternalOperation:
			operationMode = "external"
		case ModeSelfUse:
			operationMode = "self_hosted"
		}
	}
	ok(c, gin.H{"platform_name": s["brand.platform_name"], "platform_tagline": s["brand.platform_tagline"], "logo_url": s["brand.logo_url"], "nav_bg_color": s["theme.nav_bg"], "nav_text_color": s["theme.nav_text"], "footer_text": s["app.footer_text"], "footer_visible": s["app.footer_visible"], "login_notice": s["app.login_notice"], "operation_mode": operationMode, "feature_flags": gin.H{"registration_enabled": s["registration.enabled"], "email_verification": s["registration.email_verification"], "billing_enabled": s["billing.enabled"]}, "model_prices": s["billing.model_prices"]})
}
func (a *App) settings(c *gin.Context) {
	if !current(c).IsPlatformAdmin {
		fail(c, 403, "platform administrator required")
		return
	}
	items := []gin.H{}
	for key, value := range a.publicSettings() {
		if category := c.Param("category"); category != "" && !strings.HasPrefix(key, category+".") {
			continue
		}
		items = append(items, gin.H{"key": key, "value": value, "category": strings.Split(key, ".")[0]})
	}
	ok(c, gin.H{"items": items, "total": len(items), "page": 1, "page_size": 200})
}
func (a *App) updateSettings(c *gin.Context) {
	if !current(c).IsPlatformAdmin {
		fail(c, 403, "platform administrator required")
		return
	}
	var in map[string]any
	c.Request.Body = &limitedBody{ReadCloser: c.Request.Body, remaining: 2 << 20}
	if c.ShouldBindJSON(&in) != nil || len(in) == 0 {
		fail(c, 400, "invalid settings")
		return
	}
	if key := c.Param("key"); key != "" {
		value, exists := in["value"]
		if !exists {
			fail(c, 400, "value required")
			return
		}
		in = map[string]any{key: value}
	}
	for k, v := range in {
		if !validSetting(k, v) {
			fail(c, 400, "unsupported setting, invalid type or size")
			return
		}
	}
	// 部署模式的唯一权威源是 system_modes 表；system_settings 里的 platform.operation_mode
	// 只是冗余缓存，初始化锁定后禁止通过设置接口改写。
	if _, exists := in["platform.operation_mode"]; exists {
		locked, e := IsModeLocked(a.store.db)
		if e != nil {
			fail(c, 500, "查询部署模式锁定状态失败")
			return
		}
		if locked {
			fail(c, 403, "部署模式初始化后不可更改，如需变更请联系运维人员执行数据迁移")
			return
		}
	}
	// 租户自助注册只在对外运营模式下可用（同样以 system_modes 为判断依据）。
	if want, exists := in["registration.enabled"]; exists {
		if enabled, isBool := want.(bool); isBool && enabled {
			mode, e := GetSystemMode(a.store.db)
			if e != nil {
				fail(c, 500, "查询部署模式失败")
				return
			}
			if mode != ModeExternalOperation {
				fail(c, 403, "租户自助注册仅在对外运营模式（external_operation）下可启用")
				return
			}
		}
	}
	if enabled, ok := in["registration.email_verification"].(bool); ok && enabled && !a.smtpReady(in) {
		fail(c, 400, "SMTP settings are required before enabling email verification")
		return
	}
	e := a.store.db.WithContext(c.Request.Context()).Transaction(func(tx *gorm.DB) error {
		// 全局设置沿用现有项目审计表，归档到首个有效项目，不新增全局审计 DDL。
		var project struct{ ID, TenantID int64 }
		if e := tx.Table("projects").Where("deleted_at IS NULL AND status=?", "active").Order("id").Take(&project).Error; e != nil {
			return e
		}
		sc := scope{Tenant: project.TenantID, Project: project.ID, User: current(c).ID, Admin: true}
		for k, v := range in {
			data, _ := json.Marshal(v)
			if e := tx.Exec("INSERT INTO system_settings(key,value,create_by) VALUES (?,?::jsonb,?) ON CONFLICT (key) WHERE deleted_at IS NULL DO UPDATE SET value=EXCLUDED.value,update_by=EXCLUDED.create_by", k, string(data), sc.User).Error; e != nil {
				return e
			}
			if e := tx.Table("audits").Create(map[string]any{"tenant_id": sc.Tenant, "project_id": sc.Project, "user_id": sc.User, "request_id": uuid.NewString(), "action": "settings:write", "target_type": "system_settings", "target_id": k, "outcome": "success"}).Error; e != nil {
				return e
			}
		}
		return nil
	})
	if e != nil {
		fail(c, 500, "settings update failed")
		return
	}
	// 兜底清理：未锁定状态下允许写入运行模式缓存，写入后清除模式缓存
	if _, exists := in["platform.operation_mode"]; exists {
		InvalidateModeCache()
	}
	ok(c, a.publicSettings())
}

func (a *App) smtpReady(pending map[string]any) bool {
	values := map[string]any{}
	for k := range smtpDefaults {
		values[k] = nil
	}
	var rows []struct{ Key, Value string }
	a.store.db.Table("system_settings").Select("key,value::text").Where("deleted_at IS NULL AND key IN ?", settingKeys()).Scan(&rows)
	for _, r := range rows {
		var v any
		if json.Unmarshal([]byte(r.Value), &v) == nil {
			values[r.Key] = v
		}
	}
	for k, v := range pending {
		if strings.HasPrefix(k, "smtp.") {
			values[k] = v
		}
	}
	host, h := values["smtp.host"].(string)
	user, u := values["smtp.username"].(string)
	password, p := values["smtp.password"].(string)
	from, f := values["smtp.from"].(string)
	port, po := values["smtp.port"].(float64)
	if !po {
		if i, ok := values["smtp.port"].(int); ok {
			port = float64(i)
			po = true
		}
	}
	return h && u && p && f && po && host != "" && user != "" && password != "" && from != "" && port >= 1 && port <= 65535
}

func validSetting(key string, value any) bool {
	def, exists := allSettingDefaults()[key]
	if !exists {
		return false
	}
	data, err := json.Marshal(value)
	if err != nil || len(data) > 1048576 {
		return false
	}
	switch key {
	case "platform.operation_mode":
		return value == "self_hosted" || value == "external"
	case "smtp.port":
		switch port := value.(type) {
		case float64:
			return port >= 1 && port <= 65535 && port == float64(int(port))
		case int:
			return port >= 1 && port <= 65535
		default:
			return false
		}
	case "smtp.host", "smtp.username", "smtp.from":
		str, ok := value.(string)
		return ok && len(str) <= 255
	case "smtp.password":
		str, ok := value.(string)
		return ok && len(str) <= 4096
	case "billing.model_prices":
		switch value.(type) {
		case []any, map[string]any:
			return !sensitive(value)
		default:
			return false
		}
	}
	switch def.(type) {
	case bool:
		_, ok := value.(bool)
		return ok
	case string:
		str, ok := value.(string)
		if !ok {
			return false
		}
		return key != "brand.logo_url" || str == "" || strings.HasPrefix(str, "/") || strings.HasPrefix(str, "https://") || strings.HasPrefix(str, "data:image/png;base64,") || strings.HasPrefix(str, "data:image/jpeg;base64,")
	}
	return false
}
