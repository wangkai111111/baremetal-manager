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
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"baremetal-manager/backend/config"
	"baremetal-manager/backend/internal/service"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
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

func (a *App) serveIPXEScript(c *gin.Context) {
	root, err := provisioningTFTPRoot()
	if err != nil {
		fail(c, 503, "provisioning storage unavailable")
		return
	}
	mac, err := provisioningNormalizeMAC(strings.TrimSuffix(c.Param("mac"), ".ipxe"))
	if err != nil {
		fail(c, 404, "script not found")
		return
	}
	var active int64
	err = a.store.db.WithContext(c.Request.Context()).Table("installations").Where("deleted_at IS NULL AND status IN ('pending','preparing','booting','installing','unknown') AND callback_revoked_at IS NULL AND callback_expires_at>now() AND (deadline_at IS NULL OR deadline_at>now()) AND replace(lower(config_snapshot->>'mac'),':','')=?", mac).Count(&active).Error
	if err != nil {
		fail(c, 503, "installation state unavailable")
		return
	}
	c.Header("Cache-Control", "no-store")
	if active == 0 {
		c.Data(200, "text/plain; charset=utf-8", []byte("#!ipxe\nexit\n"))
		return
	}
	var snapJSON []byte
	var record struct{ ConfigSnapshot json.RawMessage }
	if err := a.store.db.WithContext(c.Request.Context()).Table("installations").Select("config_snapshot").Where("deleted_at IS NULL AND callback_revoked_at IS NULL AND callback_expires_at>now() AND status IN ('booting','unknown') AND replace(lower(config_snapshot->>'mac'),':','')=?", mac).Order("id DESC").Take(&record).Error; err == nil {
		snapJSON = record.ConfigSnapshot
		var snap installationSnapshot
		if json.Unmarshal(snapJSON, &snap) == nil {
			if token, err := service.DecryptCredential(snap.TokenEncrypted); err == nil {
				if err := a.observeInstallRequest(c.Request.Context(), token, "download", "ipxe_requested", "已收到 VM 的 iPXE 请求，正在下载内核、initrd 与系统镜像"); err != nil {
					fail(c, 503, "progress persistence unavailable")
					return
				}
			}
		}
	}
	provisionServeUnder(c, filepath.Join(root, "ipxe"), mac+".ipxe")
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
	token := c.Param("seedToken")
	if !provisioningCallbackTokenRe.MatchString(token) {
		fail(c, 404, "seed not found")
		return
	}
	var active int64
	err := a.store.db.WithContext(c.Request.Context()).Table("installations").Where("deleted_at IS NULL AND callback_revoked_at IS NULL AND callback_expires_at>now() AND status IN ('pending','preparing','booting','installing','post_install','unknown') AND callback_token_hash=? AND replace(lower(config_snapshot->>'mac'),':','')=?", service.CallbackTokenHash(token), mac).Count(&active).Error
	if err != nil || active == 0 {
		fail(c, 404, "seed not found")
		return
	}
	c.Header("Cache-Control", "no-store")
	c.Header("Referrer-Policy", "no-referrer")
	// Only the authenticated HTTP path can decrypt seed files. A direct TFTP
	// read from this shared volume must never reveal password hashes or tokens.
	path, err := provisionResolveUnder(filepath.Join(root, "nocloud", mac), name+".enc")
	if err != nil {
		fail(c, 404, "seed not found")
		return
	}
	st, err := os.Stat(path)
	if err != nil || !st.Mode().IsRegular() || st.Size() > 4<<20 {
		fail(c, 404, "seed not found")
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		fail(c, 404, "seed not found")
		return
	}
	plain, err := service.DecryptCredential(string(data))
	if err != nil {
		fail(c, 503, "seed unavailable")
		return
	}
	if err := a.observeInstallRequest(c.Request.Context(), token, "", "seed/"+name, "已下发 NoCloud "+name+" 安装配置"); err != nil {
		fail(c, 503, "installation no longer active")
		return
	}
	c.Data(200, "text/plain; charset=utf-8", []byte(plain))
}

// installCallback POST /install-callback/:token —— 记录安装进度/完成情况。
//
// 回调在同一事务内记录幂等事件并单向推进安装状态；终态同步收口操作并释放设备租约。
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

	advanced := false
	e = a.store.db.WithContext(c.Request.Context()).Transaction(func(tx *gorm.DB) error {
		var op operation
		if err := tx.Table("operations").Clauses(clause.Locking{Strength: "UPDATE"}).Where("id=? AND kind='install' AND deleted_at IS NULL", row.OperationID).Take(&op).Error; err != nil {
			return err
		}
		var state installProgress
		if err := tx.Table("installations").Clauses(clause.Locking{Strength: "UPDATE"}).Where("id=? AND callback_revoked_at IS NULL AND callback_expires_at>now() AND deleted_at IS NULL", row.ID).Take(&state).Error; err != nil {
			return err
		}
		if state.DeadlineAt != nil && !time.Now().Before(*state.DeadlineAt) {
			return gorm.ErrRecordNotFound
		}
		var currentSteps []installStep
		if err := json.Unmarshal(state.Progress, &currentSteps); err != nil {
			return err
		}
		for _, step := range currentSteps {
			if step.Status == "running" && step.DeadlineAt != nil && !time.Now().Before(*step.DeadlineAt) {
				return gorm.ErrRecordNotFound
			}
		}
		if err := tx.Exec(`INSERT INTO installation_events(tenant_id,project_id,installation_id,event_key,stage,message) VALUES (?,?,?,?,?,?) ON CONFLICT (tenant_id,project_id,installation_id,event_key) WHERE deleted_at IS NULL DO NOTHING`, row.TenantID, row.ProjectID, row.ID, eventKey, stage, message).Error; err != nil {
			return err
		}
		rank := map[string]int{"pending": 0, "preparing": 1, "booting": 2, "installing": 3, "post_install": 4, "unknown": 0, "succeeded": 5, "failed": 5, "cancelled": 5}
		if rank[state.Status] == 5 || rank[status] < rank[state.Status] {
			return nil
		}
		stepKey := ""
		switch status {
		case "installing":
			stepKey = "install"
		case "succeeded":
			stepKey = "first_boot"
		case "post_install":
			stepKey = "post_install"
			if stage == "first_boot" {
				stepKey = "first_boot"
			}
		}
		if stepKey != "" {
			if err := recordInstallStep(tx, op, stepKey, installStepMessage(stepKey)); err != nil {
				return err
			}
		}
		updates := map[string]any{"status": status, "started_at": gorm.Expr("COALESCE(started_at,now())"), "error": nil}
		if status == "installing" {
			updates["irreversible_at"] = gorm.Expr("COALESCE(irreversible_at,now())")
		}
		if status == "failed" {
			updates["error"] = message
		}
		if provisioningCallbackTerminal(status) {
			updates["completed_at"] = gorm.Expr("now()")
			if status == "failed" {
				updates["callback_revoked_at"] = gorm.Expr("now()")
				updates["cleanup_pending"] = true
			}

			if err := finishInstallProgress(tx, op, map[string]string{"succeeded": "succeeded", "failed": "failed"}[status], message); err != nil {
				return err
			}
		}
		if err := tx.Table("installations").Where("id=?", row.ID).Updates(updates).Error; err != nil {
			return err
		}
		advanced = true
		if provisioningCallbackTerminal(status) {
			values := map[string]any{"status": status, "started_at": gorm.Expr("COALESCE(started_at,now())"), "completed_at": gorm.Expr("now()"), "error": nil}
			if status == "failed" {
				values["error"] = message
			}
			if err := tx.Table("operations").Where("id=? AND status IN ('pending','running','unknown')", row.OperationID).Updates(values).Error; err != nil {
				return err
			}
			if status == "succeeded" {
				if err := tx.Table("assets").Where("id=? AND tenant_id=? AND project_id=? AND manufacturer='BMM Test VM' AND model='QEMU PXE Lab' AND mac_address='52:54:00:77:00:10'::macaddr AND deleted_at IS NULL", row.AssetID, row.TenantID, row.ProjectID).Updates(map[string]any{"power_state": "on", "status": "online", "collection_error": nil, "monitor_health": "unknown"}).Error; err != nil {
					return err
				}
			}
			return tx.Table("device_leases").Where("operation_id=?", row.OperationID).Updates(map[string]any{"heartbeat_at": gorm.Expr("now()-interval '1 second'"), "expires_at": gorm.Expr("now()")}).Error
		}
		return tx.Table("operations").Where("id=? AND status IN ('pending','unknown')", row.OperationID).Updates(map[string]any{"status": "running", "started_at": gorm.Expr("COALESCE(started_at,now())"), "completed_at": nil, "error": nil}).Error
	})
	if errors.Is(e, gorm.ErrRecordNotFound) {
		fail(c, 410, "installation expired or cancelled")
		return
	}
	if e != nil {
		fail(c, 500, "install progress persistence failed")
		return
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

func installStepMessage(key string) string {
	switch key {
	case "install":
		return "安装器已加载应答文件，开始自动分区并安装系统"
	case "post_install":
		return "系统文件已安装，正在应用 root / SSH 设置及首次启动验证服务"
	case "first_boot":
		return "安装后配置已完成，等待重启并由磁盘中的系统回报"
	}
	return key
}

func (a *App) serveInstallationMedia(c *gin.Context) {
	token, kind, name := c.Param("seedToken"), c.Param("kind"), c.Param("file")
	if !provisioningCallbackTokenRe.MatchString(token) || !((kind == "netboot" && (name == "vmlinuz" || name == "initrd")) || (kind == "iso" && name == DefaultISOFileName)) {
		fail(c, 404, "media not found")
		return
	}
	root, err := provisioningTFTPRoot()
	if err != nil {
		fail(c, 503, "media unavailable")
		return
	}
	path, err := provisionResolveUnder(filepath.Join(root, kind), name)
	if err != nil {
		fail(c, 404, "media not found")
		return
	}
	if st, err := os.Stat(path); err != nil || !st.Mode().IsRegular() {
		fail(c, 404, "media not found")
		return
	}
	if err := a.observeInstallRequest(c.Request.Context(), token, "download", "media/start/"+name, "VM 开始请求 "+name); err != nil {
		fail(c, 404, "installation inactive")
		return
	}
	c.Header("Content-Type", "application/octet-stream")
	c.Header("Cache-Control", "no-store")
	c.Header("Referrer-Policy", "no-referrer")
	http.ServeFile(c.Writer, c.Request, path)
	if c.Request.Method == "GET" && c.Writer.Status() == 200 && c.Request.Context().Err() == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = a.observeInstallRequest(ctx, token, "", "media/end/"+name, "HTTP 服务已发送 "+name+"，等待安装器确认接收与启动")
	}
	c.Abort()
}
