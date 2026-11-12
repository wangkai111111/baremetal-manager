package platform

// 本文件提供 PXE 交付侧的 HTTP 处理能力：正在网络引导的机器没有任何凭据，
// 因此这些接口必须公开注册（在 JWT guard 之前），并且只做只读文件下发与带凭据的回调。
//
// 共享路径契约（与渲染 / 编排两侧保持一致，MAC 一律为小写无冒号，例如 52:54:00:12:34:56 → 525400123456）：
//
//	{HTTPBase}/netboot/{vmlinuz,initrd}          → 磁盘 {TFTPRoot}/netboot/
//	{HTTPBase}/iso/{live-server ISO}             → 磁盘 {TFTPRoot}/iso/
//	{HTTPBase}/nocloud/{mac}/{meta-data,user-data} → 磁盘 {TFTPRoot}/nocloud/{mac}/
//	{HTTPBase}/api/v1/provision/install-callback/{token} → 安装进度回调
//
// 上述路径同时挂在 /api/v1/provision 前缀下，便于 nginx 只转发 /api/ 的部署形态复用同一后端处理器。

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"baremetal-manager/backend/config"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// provisioningCallbackTokenRe 约束回调凭据形状，避免把任意字符串拼进数据库查询。
var provisioningCallbackTokenRe = regexp.MustCompile(`^[A-Za-z0-9_-]{16,128}$`)

// provisioningTextExtensions 是允许按内容嗅探类型的文本类引导文件后缀。
var provisioningTextExtensions = map[string]bool{".ipxe": true, ".txt": true, ".cfg": true, ".conf": true, ".json": true, ".yaml": true, ".yml": true}

// provisioningSeedFiles NoCloud seed 允许的文件名白名单，路由参数不参与路径拼接。
var provisioningSeedFiles = map[string]string{
	"user-data":      "user-data",
	"meta-data":      "meta-data",
	"vendor-data":    "vendor-data",
	"network-config": "network-config",
}

// provisioningTFTPRoot 返回 PXE 交付根目录；未配置时直接报错，避免读写到进程工作目录。
func provisioningTFTPRoot() (string, error) {
	root := strings.TrimSpace(config.Load().PXE.TFTPRoot)
	if root == "" {
		return "", fmt.Errorf("pxe tftp root is not configured")
	}
	return root, nil
}

// serveNetbootFile GET /netboot/*filepath —— 下发 {TFTPRoot}/netboot 下的内核与 initrd。
func (a *App) serveNetbootFile(c *gin.Context) {
	root, e := provisioningTFTPRoot()
	if e != nil {
		fail(c, 503, "provisioning storage unavailable")
		return
	}
	provisionServeUnder(c, filepath.Join(root, "netboot"), c.Param("filepath"))
}

// serveISOFile GET /iso/*filepath —— 下发 casper netboot 需要的 live-server ISO（{HTTPBase}/iso/{name}）。
// casper 通过 url= 拉取整张 ISO，Range 请求由 http.ServeFile 原生支持。
func (a *App) serveISOFile(c *gin.Context) {
	root, e := provisioningTFTPRoot()
	if e != nil {
		fail(c, 503, "provisioning storage unavailable")
		return
	}
	provisionServeUnder(c, filepath.Join(root, "iso"), c.Param("filepath"))
}

// serveAutoinstall GET /nocloud/:mac/:file —— 下发某台设备的 NoCloud-net seed（user-data/meta-data）。
// MAC 先归一化再定位目录，文件名走白名单，因此外部输入不会参与任意路径拼接。
func (a *App) serveAutoinstall(c *gin.Context) {
	root, e := provisioningTFTPRoot()
	if e != nil {
		fail(c, 503, "provisioning storage unavailable")
		return
	}
	mac, e := provisioningNormalizeMAC(c.Param("mac"))
	if e != nil {
		// 与文件不存在返回同样的状态，避免把合法 MAC 集合暴露成可枚举的探针
		fail(c, 404, "seed not found")
		return
	}
	name, exists := provisioningSeedFiles[strings.ToLower(strings.TrimSpace(c.Param("file")))]
	if !exists {
		fail(c, 400, "unsupported seed file")
		return
	}
	c.Header("Cache-Control", "no-store")
	provisionServeUnder(c, filepath.Join(root, "nocloud", mac), "/"+name)
}

// installCallback POST /install-callback/:token —— 记录安装进度/完成情况。
//
// 语义是「尽力而为」：只追加 installation_events 事件、并按只进不退的方向推进 installations 状态，
// 终态判定与后续动作由编排器（装机任务 / worker）负责，避免两处互相覆盖。
// 请求体：{"status": "booting|installing|post_install|succeeded|failed", "message": "...", "stage": "...", "event_key": "..."}
func (a *App) installCallback(c *gin.Context) {
	token := strings.TrimSpace(c.Param("token"))
	if !provisioningCallbackTokenRe.MatchString(token) {
		fail(c, 404, "callback token invalid")
		return
	}
	var in struct {
		Status   string `json:"status"`
		Message  string `json:"message"`
		Stage    string `json:"stage"`
		EventKey string `json:"event_key"`
	}
	c.Request.Body = &limitedBody{ReadCloser: c.Request.Body, remaining: 8 << 10}
	if e := c.ShouldBindJSON(&in); e != nil {
		fail(c, 400, "invalid callback payload")
		return
	}
	status, valid := provisioningCallbackStatus(in.Status)
	if !valid {
		fail(c, 400, "unsupported install status")
		return
	}

	// 回调凭据只在安装发起时以 SHA-256 摘要入库（与一次性 API Key 的处理方式一致）。
	hash := hex.EncodeToString(sha256New(token))
	var row struct {
		ID             int64
		TenantID       int64
		ProjectID      int64
		AssetID        int64
		OperationID    int64
		Status         string
		ConfigSnapshot string
	}
	e := a.store.db.WithContext(c.Request.Context()).Table("installations").
		Select("id, tenant_id, project_id, asset_id, operation_id, status, config_snapshot::text AS config_snapshot").
		Where("callback_token_hash=? AND deleted_at IS NULL AND callback_revoked_at IS NULL AND callback_expires_at > now()", hash).
		Take(&row).Error
	if e != nil {
		fail(c, 404, "callback token invalid")
		return
	}

	stage := strings.TrimSpace(in.Stage)
	if stage == "" {
		stage = status
	}
	message := provisioningCallbackMessage(in.Message, status)
	eventKey := provisioningCallbackEventKey(in.EventKey, row.ID, status, stage)

	// 事件按 (installation_id, event_key) 幂等：客户端重试不会刷出重复进度。
	e = a.store.db.WithContext(c.Request.Context()).Exec(`
		INSERT INTO installation_events(tenant_id, project_id, installation_id, event_key, stage, message)
		VALUES (?,?,?,?,?,?)
		ON CONFLICT (tenant_id, project_id, installation_id, event_key) WHERE deleted_at IS NULL DO NOTHING`,
		row.TenantID, row.ProjectID, row.ID, eventKey, stage, message).Error
	if e != nil {
		fail(c, 500, "install progress persistence failed")
		return
	}

	updates := map[string]any{"status": status, "updated_at": gorm.Expr("now()")}
	if status == "failed" {
		updates["error"] = message
	}
	if provisioningCallbackStarted(status) {
		updates["started_at"] = gorm.Expr("COALESCE(started_at, now())")
	}
	if provisioningCallbackTerminal(status) {
		updates["started_at"] = gorm.Expr("COALESCE(started_at, now())")
		updates["completed_at"] = gorm.Expr("COALESCE(completed_at, now())")
	}
	// 只进不退：已是终态（含编排器先行写入的结果）时不再覆盖。
	res := a.store.db.WithContext(c.Request.Context()).Table("installations").
		Where("id=? AND deleted_at IS NULL AND status NOT IN ('succeeded','failed','cancelled')", row.ID).
		Updates(updates)
	advanced := res.Error == nil && res.RowsAffected == 1

	// operations 只做「pending → running」的兜底推进，终态由 worker 统一收口。
	if status != "succeeded" && status != "failed" {
		a.store.db.WithContext(c.Request.Context()).Table("operations").
			Where("id=? AND deleted_at IS NULL AND kind='install' AND status='pending'", row.OperationID).
			Updates(map[string]any{"status": "running", "started_at": gorm.Expr("COALESCE(started_at, now())"), "updated_at": gorm.Expr("now()")})
	}

	if status == "succeeded" && row.Status != "failed" && row.Status != "cancelled" {
		var snapshot struct {
			IP string `json:"ip"`
		}
		if json.Unmarshal([]byte(row.ConfigSnapshot), &snapshot) == nil {
			if ip, err := netip.ParseAddr(strings.TrimSpace(snapshot.IP)); err == nil && ip.IsGlobalUnicast() {
				err = a.store.db.WithContext(c.Request.Context()).Exec(`
					UPDATE assets SET management_ip=?::inet, updated_at=now()
					WHERE id=? AND tenant_id=? AND project_id=? AND deleted_at IS NULL AND management_ip IS NULL
					AND EXISTS (SELECT 1 FROM installations WHERE id=? AND status='succeeded' AND deleted_at IS NULL)
					AND NOT EXISTS (SELECT 1 FROM installations WHERE asset_id=? AND id>? AND deleted_at IS NULL)`,
					ip.String(), row.AssetID, row.TenantID, row.ProjectID, row.ID, row.AssetID, row.ID).Error
				if err != nil {
					fail(c, 500, "management IP persistence failed")
					return
				}
			}
		}
	}

	ok(c, gin.H{"installation_id": row.ID, "status": status, "recorded": advanced, "asset_id": row.AssetID})
}

// sha256New 返回 SHA-256 摘要，供回调凭据比对使用。
func sha256New(value string) []byte {
	sum := sha256.Sum256([]byte(value))
	return sum[:]
}

// provisioningCallbackStatus 归一化回调状态，兼容 success/error 别名。
func provisioningCallbackStatus(raw string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "booting", "preparing":
		return "booting", true
	case "installing", "downloading", "configuring":
		return "installing", true
	case "post_install", "post-install", "finalizing":
		return "post_install", true
	case "succeeded", "success":
		return "succeeded", true
	case "failed", "error":
		return "failed", true
	}
	return "", false
}

// provisioningCallbackStarted 判断该状态是否意味着安装已实际开始（用于补 started_at）。
func provisioningCallbackStarted(status string) bool {
	switch status {
	case "installing", "post_install", "succeeded", "failed":
		return true
	}
	return false
}

// provisioningCallbackTerminal 判断是否为终态（用于补 completed_at）。
func provisioningCallbackTerminal(status string) bool {
	return status == "succeeded" || status == "failed"
}

// provisioningCallbackMessage 清洗回调消息：去控制字符、限长，并保留可读默认文案。
func provisioningCallbackMessage(raw, status string) string {
	message := strings.Map(func(r rune) rune {
		if r < 0x20 && r != '\t' {
			return -1
		}
		return r
	}, strings.TrimSpace(raw))
	if message == "" {
		message = "install progress reported by netboot client"
	}
	if len(message) > 500 {
		message = message[:500]
	}
	return status + ": " + message
}

// provisioningCallbackEventKey 生成幂等事件键：客户端未提供时，按「实例+状态+阶段+分钟」推导，
// 使同一分钟内的重复上报被自然去重，而不同阶段仍会留下独立事件。
func provisioningCallbackEventKey(raw string, installationID int64, status, stage string) string {
	if key := strings.TrimSpace(raw); key != "" {
		if len(key) > 128 {
			return key[:128]
		}
		return key
	}
	return fmt.Sprintf("%d/%s/%s/%s", installationID, status, stage, time.Now().UTC().Format("200601021504"))
}

// provisionServeUnder 在 containment 校验通过后下发 root 下的相对路径文件。
func provisionServeUnder(c *gin.Context, root, relative string) {
	path, e := provisionResolveUnder(root, relative)
	if e != nil {
		fail(c, 400, "invalid provisioning path")
		return
	}
	info, e := os.Stat(path)
	if e != nil || info.IsDir() {
		fail(c, 404, "provisioning file not found")
		return
	}
	if !provisioningTextExtensions[strings.ToLower(filepath.Ext(path))] {
		// 内核 / initrd / ISO 一律按二进制下发，避免内容嗅探给出错误 MIME。
		c.Header("Content-Type", "application/octet-stream")
	}
	http.ServeFile(c.Writer, c.Request, path)
	c.Abort()
}

// provisionResolveUnder 把请求中的相对路径安全地解析到 root 之下：
// 逐段拒绝 "."/".."/空段/绝对路径，并在 symlink 归一化后二次确认仍在 root 内。
func provisionResolveUnder(root, relative string) (string, error) {
	cleanRoot, e := filepath.Abs(root)
	if e != nil {
		return "", e
	}
	cleanRoot = filepath.Clean(cleanRoot)
	value := filepath.ToSlash(strings.TrimSpace(relative))
	if value == "" || strings.ContainsRune(value, 0) {
		return "", fmt.Errorf("empty path")
	}
	value = strings.TrimLeft(value, "/")
	if value == "" || filepath.IsAbs(value) || strings.Contains(value, "\\") {
		return "", fmt.Errorf("absolute path forbidden")
	}
	segments := strings.Split(value, "/")
	for _, segment := range segments {
		if segment == "" || segment == "." || segment == ".." {
			return "", fmt.Errorf("path traversal forbidden")
		}
	}
	candidate := filepath.Clean(filepath.Join(cleanRoot, filepath.FromSlash(value)))
	if candidate != cleanRoot && !strings.HasPrefix(candidate, cleanRoot+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes provisioning root")
	}
	// 软链接可能指向 root 之外，落盘产物由平台自己写入，这里再确认一次真实路径。
	if resolved, e := filepath.EvalSymlinks(candidate); e == nil {
		if resolved != cleanRoot && !strings.HasPrefix(resolved, cleanRoot+string(filepath.Separator)) {
			return "", fmt.Errorf("path escapes provisioning root")
		}
		return resolved, nil
	}
	return candidate, nil
}
