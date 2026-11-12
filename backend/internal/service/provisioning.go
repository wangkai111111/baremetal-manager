package service

// ============================================================
// 装机编排流水线（Task #22）
//
// 本文件把已完成的三块能力串成一条可执行的自动装机链路：
//   1. Task #20 渲染引擎：RenderInstallConfig / InstallVars / HashPassword；
//   2. Task #21 PXE 交付：IPXEScriptGenerator（ipxe/{mac12}.ipxe）、
//      DnsmasqConfigGenerator（{TFTPRoot}/dnsmasq.conf）、
//      provisioning_http（/nocloud/{mac}/{file} 与 /install-callback/{token}）；
//   3. BMC 控制与进度回收：adapter.BMC 的 SetBootOrder/Power + installations 回调事件。
//
// 依赖方向约束：internal/platform 已导入 internal/service，因此本文件绝不能反向
// 导入 platform；生成器以接口形式注入，由 platform（worker）或 task 在构造时装配。
//
// MAC 书写约定（与 Task #21 全链路唯一约定一致）：
//   - 磁盘路径与 HTTP URL 路径段：12 位小写无分隔，例如 525400123456；
//   - dnsmasq / 内核命令行：冒号分隔小写，由生成器内部负责，本文件只传归一化形式。
// ============================================================

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"baremetal-manager/backend/config"
	"baremetal-manager/backend/internal/adapter"
	"baremetal-manager/backend/internal/model"

	"gorm.io/gorm"
)

// ---------- 外部依赖契约（签名与 Task #20/#21 完全一致） ----------

// IPXEScriptWriter 是编排侧需要的 iPXE 生成器能力，由 *platform.IPXEScriptGenerator 实现。
type IPXEScriptWriter interface {
	UbuntuScript(mac, ip, netmask, gateway, isoFileName string) (string, error)
	WriteScript(mac, content string) (string, error)
	ScriptPath(mac string) (string, error)
	BootFile(mac string) (string, error)
}

// DnsmasqRegenerator 由 *platform.DnsmasqConfigGenerator 实现，负责整份 dnsmasq.conf。
type DnsmasqRegenerator interface {
	Generate(ctx context.Context) (string, error)
	GenerateHostEntry(mac, ip, hostname string) string
}

// BMCSwitch 由 *adapter.BMC 实现：设置一次性 PXE 引导项并重启设备。
type BMCSwitch interface {
	SetBootOrder(ctx context.Context, asset model.Asset, bootDevice string, persistent bool) error
	Power(ctx context.Context, asset model.Asset, action string) error
}

// ---------- 常量与错误 ----------

const (
	// DefaultProvisionISOName 与 platform.DefaultISOFileName 保持一致：live-server 的 casper
	// 只接受 url= 指向整张 ISO，因此这里只关心文件名，不关心对象存储里的真实 key。
	DefaultProvisionISOName = "ubuntu-22.04.5-live-server-amd64.iso"
	// defaultInstallTimeout 装机监控默认上限（无回调即视为结果未知，交由人工对账）。
	defaultInstallTimeout = 60 * time.Minute
	// defaultCallbackTTL 一次性回调凭据默认有效期，覆盖「装机 + 重启 + 重试」窗口。
	defaultCallbackTTL = 12 * time.Hour
	// defaultProgressPollInterval MonitorProgress 轮询装机状态的节奏。
	defaultProgressPollInterval = 5 * time.Second
	// leaseHeartbeatInterval 长任务租约心跳间隔：租约原始 TTL 仅 120s，必须周期续租。
	leaseHeartbeatInterval = 60 * time.Second
	// leaseHeartbeatTTL 每次心跳把 expires_at 推到 now()+TTL，须恒定大于心跳间隔。
	leaseHeartbeatTTL = 180 * time.Second
	// envSimulateBMC 无真实 BMC（TCG / E2E 冒烟）时跳过 BMC 调用，只记录意图。
	envSimulateBMC = "PROVISION_SIMULATE_BMC"
)

var (
	// ErrLeaseLost 表示设备租约已被其它 worker 接管，必须立刻停止写入。
	ErrLeaseLost = errors.New("device lease lost")
	// ErrInstallTimeout 表示装机在给定时间内未回报终态，结果不确定。
	ErrInstallTimeout = errors.New("install progress timeout; result unknown")
	// provisionISONameRe 允许进入引导 URL 的 ISO 文件名字符集（与 Task #21 白名单一致）。
	provisionISONameRe = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)
	// provisionTokenRe 回调 token 形状，必须与 provisioning_http 的校验保持一致。
	provisionTokenRe = regexp.MustCompile(`^[A-Za-z0-9_-]{16,128}$`)
)

// ---------- 数据结构 ----------

// InstallScope 描述一次编排的租户/项目作用域与归属操作。
// TenantID/ProjectID 为 0 时由资产自身推导（asynq 遗留通道即如此）。
type InstallScope struct {
	TenantID    int64
	ProjectID   int64
	UserID      int64
	OperationID int64
}

// InstallationResult 是 StartInstall 的产出，供 worker 写回操作结果与日志。
type InstallationResult struct {
	InstallationID int64    `json:"installation_id"`
	OperationID    int64    `json:"operation_id"`
	AssetID        int64    `json:"asset_id"`
	InstallPlanID  int64    `json:"install_plan_id"`
	MAC            string   `json:"mac"`
	IP             string   `json:"ip"`
	Hostname       string   `json:"hostname"`
	SeedDir        string   `json:"seed_dir"`
	ScriptPath     string   `json:"script_path"`
	ISOFileName    string   `json:"iso_file_name"`
	Status         string   `json:"status"`
	CallbackURL    string   `json:"-"`
	RootPassword   string   `json:"root_password,omitempty"`
	GeneratedRoot  bool     `json:"generated_root_password"`
	SimulatedBMC   bool     `json:"simulated_bmc"`
	BMCActions     []string `json:"bmc_actions"`
	TimeoutSeconds int      `json:"timeout_seconds"`
}

// provisioningAsset 是编排需要的资产视图（含网络地址与 BMC 密文）。
// 列名以显式 tag 固定，避免 GORM 对 BMC*/MAC* 这类缩写的蛇形转换产生歧义。
type provisioningAsset struct {
	ID                  int64  `gorm:"column:id"`
	TenantID            int64  `gorm:"column:tenant_id"`
	ProjectID           int64  `gorm:"column:project_id"`
	Name                string `gorm:"column:name"`
	MAC                 string `gorm:"column:mac"`
	IP                  string `gorm:"column:ip"`
	BMCAddress          string `gorm:"column:bmc_address"`
	BMCUser             string `gorm:"column:bmc_user"`
	BMCProtocol         string `gorm:"column:bmc_protocol"`
	BMCSecretKeyID      string `gorm:"column:bmc_secret_key_id"`
	BMCPort             int    `gorm:"column:bmc_port"`
	BMCTLSVerify        bool   `gorm:"column:bmc_tls_verify"`
	BMCSecretCiphertext []byte `gorm:"column:bmc_secret_ciphertext"`
	NetworkID           int64  `gorm:"column:network_id"`
}

// provisioningNetwork 是装机使用的 provisioning 网络参数。
type provisioningNetwork struct {
	ID      int64  `gorm:"column:id"`
	Name    string `gorm:"column:name"`
	Subnet  string `gorm:"column:subnet"`
	Gateway string `gorm:"column:gateway"`
	DNS     string `gorm:"column:dns"`
}

// planConfig 是 install_plans.config 里编排关心的字段（全部可选）。
type planConfig struct {
	Hostname          string `json:"hostname"`
	SSHPublicKey      string `json:"ssh_public_key"`
	PostScript        string `json:"post_script"`
	PartitionScheme   string `json:"partition_scheme"`
	ISOFileName       string `json:"iso_file_name"`
	InstallTimeoutMin int    `json:"install_timeout_minutes"`
	DNS               string `json:"dns"`
	Gateway           string `json:"gateway"`
}

// provisioningPlan 是解析后的装机计划视图（满足 installations 的 NOT NULL 复合外键链）。
type provisioningPlan struct {
	ID                  int64
	Name                string
	KickstartTemplateID int64
	ImageID             int64
	NetworkID           int64
	Config              planConfig
	Snapshot            json.RawMessage
}

// ProvisioningService 编排一次完整的 PXE 自动装机。
type ProvisioningService struct {
	DB  *gorm.DB
	Cfg *config.Config
	// 生成器与 BMC 通道通过构造注入，便于在无 BMC 环境下降级为模拟实现。
	IPXE    IPXEScriptWriter
	Dnsmasq DnsmasqRegenerator
	BMC     BMCSwitch
	// PollInterval 覆盖进度轮询节奏，零值表示使用默认 5s。
	PollInterval time.Duration
}

// NewProvisioningService 构造编排器。cfg 为空时按环境变量加载；bmc 为空时回退真实适配器。
func NewProvisioningService(db *gorm.DB, cfg *config.Config, ipxe IPXEScriptWriter, dnsmasq DnsmasqRegenerator, bmc BMCSwitch) *ProvisioningService {
	if cfg == nil {
		cfg = config.Load()
	}
	if bmc == nil {
		bmc = adapter.NewBMC()
	}
	return &ProvisioningService{DB: db, Cfg: cfg, IPXE: ipxe, Dnsmasq: dnsmasq, BMC: bmc, PollInterval: defaultProgressPollInterval}
}

// ============================================================
// 纯函数：路径 / URL / token 构造（可脱离数据库与 BMC 单测）
// ============================================================

// NormalizeMAC12 把任意常见写法的 MAC 归一化为「12 位小写无分隔」形式，
// 与 Task #21 的磁盘路径 / URL 路径段约定完全一致。
func NormalizeMAC12(mac string) (string, error) {
	value := strings.NewReplacer(":", "", "-", "", ".", "", " ", "", "\t", "").Replace(strings.ToLower(strings.TrimSpace(mac)))
	if len(value) != 12 {
		return "", fmt.Errorf("无效的 MAC 地址 %q", mac)
	}
	for _, r := range value {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return "", fmt.Errorf("无效的 MAC 地址 %q", mac)
		}
	}
	return value, nil
}

// MACWithColons 返回冒号分隔的小写 MAC（dnsmasq / 内核命令行写法）。
func MACWithColons(mac string) (string, error) {
	h, err := NormalizeMAC12(mac)
	if err != nil {
		return "", err
	}
	out := make([]byte, 0, 17)
	for i := 0; i < 12; i += 2 {
		if i > 0 {
			out = append(out, ':')
		}
		out = append(out, h[i], h[i+1])
	}
	return string(out), nil
}

// NormalizeHTTPBase 校验并规范化 cfg.PXE.HTTPBase：必须 http/https、带主机、无凭据与片段。
func NormalizeHTTPBase(raw string) (string, error) {
	base := strings.TrimRight(strings.TrimSpace(raw), "/")
	if base == "" {
		return "", errors.New("PXE_HTTP_BASE 未配置，无法生成可回源的引导 URL")
	}
	u, err := url.Parse(base)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.Fragment != "" {
		return "", fmt.Errorf("PXE_HTTP_BASE 配置非法: %q", raw)
	}
	return base, nil
}

// NoCloudSeedDir 返回某台设备的 NoCloud-net seed 目录：{TFTPRoot}/nocloud/{mac12}。
// 目录名必须与 iPXE 脚本里的 ds=nocloud-net;s={HTTPBase}/nocloud/{mac12}/ 完全一致。
func NoCloudSeedDir(tftpRoot, mac string) (string, error) {
	root := strings.TrimSpace(tftpRoot)
	if root == "" {
		return "", errors.New("PXE_TFTP_ROOT 未配置")
	}
	mac12, err := NormalizeMAC12(mac)
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "nocloud", mac12), nil
}

// SeedURL 返回 seed 目录对外可读的 URL 前缀（以 / 结尾，供 ds=nocloud-net;s= 使用）。
func SeedURL(httpBase, mac string) (string, error) {
	base, err := NormalizeHTTPBase(httpBase)
	if err != nil {
		return "", err
	}
	mac12, err := NormalizeMAC12(mac)
	if err != nil {
		return "", err
	}
	return base + "/nocloud/" + mac12 + "/", nil
}

// CallbackTokenHash 返回回调 token 的十六进制小写 SHA-256 摘要。
// provisioning_http.installCallback 以完全相同的算法比对 installations.callback_token_hash。
func CallbackTokenHash(token string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(token)))
	return hex.EncodeToString(sum[:])
}

// NewCallbackToken 生成一次性回调凭据（43 位 URL 安全串，满足 provisioning_http 的正则）。
func NewCallbackToken() (string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("生成回调凭据失败: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(raw[:])
	if !provisionTokenRe.MatchString(token) {
		return "", errors.New("生成的回调凭据不符合校验规则")
	}
	return token, nil
}

// InstallCallbackURL 拼接装机回调地址：{HTTPBase}/api/v1/provision/install-callback/{token}。
func InstallCallbackURL(httpBase, token string) string {
	base := strings.TrimRight(strings.TrimSpace(httpBase), "/")
	return base + "/api/v1/provision/install-callback/" + strings.TrimSpace(token)
}

// SanitizeISOName 校验并回退 live-server ISO 文件名，禁止任何路径分隔符进入引导 URL。
func SanitizeISOName(name string) (string, error) {
	value := strings.TrimSpace(name)
	if value == "" {
		return DefaultProvisionISOName, nil
	}
	if !provisionISONameRe.MatchString(value) || value == "." || value == ".." || strings.Contains(value, "..") {
		return "", fmt.Errorf("非法的 ISO 文件名 %q", name)
	}
	return value, nil
}

// CallbackCommand 生成一条「永不失败」的回调 shell 命令：curl 优先、wget 兜底、结尾 || true，
// 保证回调地址不可达时也不会把一次本来成功的安装判为失败。
func CallbackCommand(callbackURL, body string) string {
	u := strings.ReplaceAll(strings.TrimSpace(callbackURL), "'", "")
	payload := strings.ReplaceAll(strings.TrimSpace(body), "'", "")
	quoted := "'" + payload + "'"
	return fmt.Sprintf(
		"curl -fsS -m 20 -X POST -H 'Content-Type: application/json' --data %s %s || wget -q -T 20 -t 1 -O /dev/null --header='Content-Type: application/json' --post-data=%s %s || true",
		quoted, u, quoted, u)
}

// InjectInstallCallback 把完成回调命令注入渲染好的应答文件。
// autoinstall → late-commands；kickstart → 追加 %post；preseed → late_command 链式追加；
// unattend（Windows）没有可靠的收尾钩子，原样返回。
func InjectInstallCallback(rendered, format, command string) string {
	command = strings.TrimSpace(command)
	if command == "" || strings.TrimSpace(rendered) == "" {
		return rendered
	}
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "autoinstall":
		return appendAutoinstallLateCommand(rendered, command)
	case "kickstart":
		return strings.TrimRight(rendered, "\n") + "\n\n%post\n" + command + "\n%end\n"
	case "preseed":
		return appendPreseedLateCommand(rendered, command)
	default:
		return rendered
	}
}

// appendAutoinstallLateCommand 在 autoinstall 的 late-commands 列表末尾追加一项。
// 优先复用已有列表及其缩进；没有列表时（自定义模板）在 autoinstall 块下新建；
// 连 autoinstall 块都找不到则原样返回并留下告警，绝不产出破坏 YAML 结构的文本。
func appendAutoinstallLateCommand(rendered, command string) string {
	lines := strings.Split(strings.TrimRight(rendered, "\n"), "\n")
	index, indent := -1, ""
	for i, raw := range lines {
		if strings.TrimSpace(raw) != "late-commands:" {
			continue
		}
		index = i
		trimmed := strings.TrimLeft(raw, " ")
		indent = raw[:len(raw)-len(trimmed)]
	}
	if index < 0 {
		for _, raw := range lines {
			if strings.TrimRight(raw, " ") == "autoinstall:" {
				return strings.TrimRight(rendered, "\n") + "\n  late-commands:\n    - " + strconv.Quote(command) + "\n"
			}
		}
		log.Printf("provisioning: 应答模板缺少 autoinstall/late-commands 块，跳过完成回调注入")
		return rendered
	}
	// 探测列表项缩进（首个非空后继行）。
	itemIndent := indent + "  "
	next := index + 1
	for next < len(lines) && strings.TrimSpace(lines[next]) == "" {
		next++
	}
	if next < len(lines) {
		trimmed := strings.TrimLeft(lines[next], " ")
		if strings.HasPrefix(trimmed, "- ") && len(lines[next])-len(trimmed) > len(indent) {
			itemIndent = strings.Repeat(" ", len(lines[next])-len(trimmed))
		}
	}
	// 定位该列表的最后一行，把回调追加到其后。
	end := next
	for i := next; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "" {
			continue
		}
		trimmed := strings.TrimLeft(lines[i], " ")
		if len(lines[i])-len(trimmed) < len(itemIndent) {
			break
		}
		end = i + 1
	}
	out := make([]string, 0, len(lines)+1)
	out = append(out, lines[:end]...)
	out = append(out, itemIndent+"- "+strconv.Quote(command))
	out = append(out, lines[end:]...)
	return strings.Join(out, "\n") + "\n"
}

// appendPreseedLateCommand 把命令链入已有的 preseed/late_command，没有则新增一行。
func appendPreseedLateCommand(rendered, command string) string {
	lines := strings.Split(strings.TrimRight(rendered, "\n"), "\n")
	for i, raw := range lines {
		if strings.HasPrefix(strings.TrimSpace(raw), "d-i preseed/late_command string") {
			lines[i] = raw + " ; " + command
			return strings.Join(lines, "\n") + "\n"
		}
	}
	return strings.TrimRight(rendered, "\n") + "\nd-i preseed/late_command string " + command + "\n"
}

// ShouldSimulateBMC 判断是否跳过真实 BMC 调用：显式开关，或回环/占位地址。
// TCG 与冒烟 E2E 环境没有真实 BMC，跳过调用并记录意图，保证流水线仍可端到端验证。
func ShouldSimulateBMC(address string) bool {
	if value := strings.ToLower(strings.TrimSpace(os.Getenv(envSimulateBMC))); value != "" {
		if on, err := strconv.ParseBool(value); err == nil {
			return on
		}
	}
	host := strings.TrimSpace(address)
	if slash := strings.Index(host, "/"); slash >= 0 {
		host = host[:slash]
	}
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	if strings.HasPrefix(strings.ToLower(host), "mock") || strings.EqualFold(host, "simulated") {
		return true
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return true
	}
	return false
}

// installHostname 规整 autoinstall/dnsmasq 可用的主机名，非法时回退 host-{mac12}。
func installHostname(raw, mac12 string) string {
	name := strings.ToLower(strings.TrimSpace(raw))
	if name == "" {
		return "host-" + mac12
	}
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '.', r == '-':
			b.WriteRune(r)
		case r == '_' || r == ' ':
			b.WriteRune('-')
		}
	}
	cleaned := strings.Trim(b.String(), "-.")
	if len(cleaned) > 63 {
		cleaned = cleaned[:63]
	}
	cleaned = strings.Trim(cleaned, "-.")
	if cleaned == "" {
		return "host-" + mac12
	}
	return cleaned
}

// ============================================================
// 编排主体：StartInstall
// ============================================================

// StartInstall 编排一次完整的 PXE 自动装机：
//  1. 按作用域加载资产并解密 BMC 凭据；
//  2. 解析装机计划（模板 / 镜像 / 网络），必要时补齐 install_plans 外键链；
//  3. 组装 InstallVars 并调用 Task #20 的渲染引擎；
//  4. 落盘 NoCloud seed（user-data / meta-data）与 iPXE 脚本，重生成 dnsmasq.conf；
//  5. 建立 installations 记录（一次性回调凭据只存 SHA-256 摘要）；
//  6. BMC 切换一次性 PXE 引导并重启（无真实 BMC 时降级为记录意图）。
//
// overrides 来自 operations.payload，可携带装机计划、主机名、网关、镜像、口令密文等。
func (p *ProvisioningService) StartInstall(ctx context.Context, sc InstallScope, assetID, templateID, imageID int64, overrides map[string]string) (*InstallationResult, error) {
	if p == nil || p.DB == nil {
		return nil, errors.New("provisioning: 数据库未初始化")
	}
	if p.Cfg == nil {
		p.Cfg = config.Load()
	}
	if p.IPXE == nil || p.Dnsmasq == nil {
		return nil, errors.New("provisioning: iPXE / dnsmasq 生成器未装配")
	}
	if p.BMC == nil {
		p.BMC = adapter.NewBMC()
	}
	if p.PollInterval <= 0 {
		p.PollInterval = defaultProgressPollInterval
	}
	base, err := NormalizeHTTPBase(p.Cfg.PXE.HTTPBase)
	if err != nil {
		return nil, err
	}
	tftpRoot := strings.TrimSpace(p.Cfg.PXE.TFTPRoot)
	if tftpRoot == "" {
		return nil, errors.New("provisioning: PXE_TFTP_ROOT 未配置")
	}

	// 1) 资产：租户 / 项目作用域由调用方保证，retired 设备一律拒绝。
	asset, err := p.loadAsset(ctx, sc, assetID)
	if err != nil {
		return nil, err
	}
	if sc.TenantID == 0 {
		sc.TenantID = asset.TenantID
	}
	if sc.ProjectID == 0 {
		sc.ProjectID = asset.ProjectID
	}
	mac12, err := NormalizeMAC12(asset.MAC)
	if err != nil {
		return nil, fmt.Errorf("provisioning: 资产缺少可用于 PXE 的主网卡 MAC: %w", err)
	}
	// BMC 凭据解密：模拟模式下允许失败（后续根本不会发起 BMC 调用）。
	simulate := ShouldSimulateBMC(asset.BMCAddress) || override(overrides, "simulate_bmc") == "1"
	secret, secretErr := DecryptCredential(string(asset.BMCSecretCiphertext))
	if secretErr != nil {
		if !simulate {
			return nil, fmt.Errorf("provisioning: BMC 凭据不可用: %w", secretErr)
		}
		log.Printf("provisioning: 资产 %d 的 BMC 凭据无法解密（%v），已按模拟模式继续", asset.ID, secretErr)
		secret = ""
	}

	// 2) 装机计划 + 模板 + 镜像。
	plan, err := p.resolvePlan(ctx, sc, templateID, imageID, asset.NetworkID, overrides)
	if err != nil {
		return nil, err
	}
	effectiveTemplate := templateID
	if effectiveTemplate == 0 {
		effectiveTemplate = plan.KickstartTemplateID
	}
	tpl, err := p.loadTemplate(ctx, sc, effectiveTemplate)
	if err != nil {
		return nil, err
	}
	effectiveImage := imageID
	if effectiveImage == 0 {
		effectiveImage = plan.ImageID
	}
	isoName, osType, err := p.pickISOName(ctx, sc, effectiveImage, plan, overrides)
	if err != nil {
		return nil, err
	}

	// 3) 网络参数：静态三要素齐备才同时下发 iPXE 静态与 autoinstall 静态，否则统一回退 DHCP。
	netInfo, ip, netmask, gateway, err := p.resolveNetwork(ctx, sc, asset, plan, overrides)
	if err != nil {
		return nil, err
	}

	// 4) root 口令：优先使用调用方提供值，否则随机生成并回传给调用方一次性展示。
	rootPassword, generatedRoot, encPassword, err := p.resolveRootPassword(ctx, sc, overrides)
	if err != nil {
		return nil, err
	}

	vars := InstallVars{
		Hostname:     installHostname(firstNonEmpty(override(overrides, "hostname"), plan.Config.Hostname, asset.Name), mac12),
		MAC:          mac12,
		IP:           ip,
		Netmask:      netmask,
		Gateway:      gateway,
		DNS:          firstNonEmpty(override(overrides, "dns"), plan.Config.DNS, netInfo.DNS),
		RootPassword: rootPassword,
		SSHKey:       firstNonEmpty(override(overrides, "ssh_public_key"), plan.Config.SSHPublicKey),
		HTTPBase:     base,
		Extra: map[string]string{
			"mac_colon":  mustMACWithColons(mac12),
			"asset_name": asset.Name,
			"os_type":    osType,
			"seed_url":   base + "/nocloud/" + mac12 + "/",
		},
	}
	if tpl.OSType == "" {
		tpl.OSType = firstNonEmpty(osType, "linux")
	}
	tpl.PartitionScheme = firstNonEmpty(plan.Config.PartitionScheme, tpl.PartitionScheme)
	tpl.PostScript = firstNonEmpty(override(overrides, "post_script"), plan.Config.PostScript, tpl.PostScript)

	// 5) 渲染应答文件（Task #20 引擎）。
	rendered, err := RenderInstallConfig(tpl, vars)
	if err != nil {
		return nil, fmt.Errorf("provisioning: 应答文件渲染失败: %w", err)
	}

	// 6) 一次性回调凭据：先于文件落盘生成，使渲染结果能内嵌完整的回调 URL。
	token, err := NewCallbackToken()
	if err != nil {
		return nil, err
	}
	timeout, err := p.resolveInstallTimeout(plan, overrides)
	if err != nil {
		return nil, err
	}
	callbackURL := InstallCallbackURL(base, token)
	// 结尾 || true 保证回调不可达时不会把一次本来成功的安装判为失败。
	callbackBody := `{"status":"succeeded","stage":"autoinstall","event_key":"succeeded","message":"autoinstall completed"}`
	rendered = InjectInstallCallback(rendered, tpl.Format, CallbackCommand(callbackURL, callbackBody))

	// 7) 落盘 NoCloud seed。
	seedDir, err := NoCloudSeedDir(tftpRoot, mac12)
	if err != nil {
		return nil, err
	}
	if e := p.writeSeedFile(filepath.Join(seedDir, "user-data"), rendered); e != nil {
		return nil, fmt.Errorf("provisioning: 写入 user-data 失败: %w", e)
	}
	if e := p.writeSeedFile(filepath.Join(seedDir, "meta-data"), metaDataContent(vars.Hostname, mac12)); e != nil {
		return nil, fmt.Errorf("provisioning: 写入 meta-data 失败: %w", e)
	}

	// 8) iPXE 脚本：{TFTPRoot}/ipxe/{mac12}.ipxe，seed 路径与上面完全同源。
	script, err := p.IPXE.UbuntuScript(mac12, ip, netmask, gateway, isoName)
	if err != nil {
		return nil, fmt.Errorf("provisioning: 生成 iPXE 脚本失败: %w", err)
	}
	scriptPath, err := p.IPXE.WriteScript(mac12, script)
	if err != nil {
		return nil, fmt.Errorf("provisioning: 写入 iPXE 脚本失败: %w", err)
	}

	// 9) dnsmasq：整份重生成，保证新设备的 PROM 链入与 host 条目一致。
	if _, e := p.Dnsmasq.Generate(ctx); e != nil {
		return nil, fmt.Errorf("provisioning: 重新生成 dnsmasq 配置失败: %w", e)
	}

	// 10) operations / installations：先落库再动硬件，使失败可追溯。
	if sc.OperationID == 0 {
		sc.OperationID, err = p.ensureOperation(ctx, sc, asset.ID, overrides)
		if err != nil {
			return nil, err
		}
	}
	installationID, err := p.createInstallation(ctx, sc, asset, plan, tpl, vars, seedDir, scriptPath, isoName, token, timeout, encPassword, generatedRoot)
	if err != nil {
		return nil, err
	}
	result := &InstallationResult{
		InstallationID: installationID,
		OperationID:    sc.OperationID,
		AssetID:        asset.ID,
		InstallPlanID:  plan.ID,
		MAC:            mac12,
		IP:             ip,
		Hostname:       vars.Hostname,
		SeedDir:        seedDir,
		ScriptPath:     scriptPath,
		ISOFileName:    isoName,
		Status:         "preparing",
		CallbackURL:    callbackURL,
		GeneratedRoot:  generatedRoot,
		SimulatedBMC:   simulate,
		BMCActions:     []string{},
		TimeoutSeconds: int(timeout / time.Second),
	}
	if generatedRoot {
		result.RootPassword = rootPassword
	}
	// 明文口令只存在于返回值与本次渲染结果里：入库的一律是密文。
	if generatedRoot && encPassword != "" {
		p.mergeOperationPayload(ctx, sc.OperationID, map[string]any{"root_password_encrypted": encPassword})
	}
	if e := p.recordInstallationEvent(ctx, sc, installationID, "preparing", "seed 与引导文件已就绪", "info"); e != nil {
		log.Printf("provisioning: 记录装机事件失败 installation_id=%d: %v", installationID, e)
	}

	// 11) BMC：先设置一次性 PXE 引导项，再重启；任何一步失败都已完成“不可逆动作”，
	// 因此状态只能推进为 unknown 并返回 started 语义的 error。
	bmcAsset := model.Asset{
		ID:      uint(asset.ID),
		Name:    asset.Name,
		BMCAddress: func() string {
			if asset.BMCPort > 0 && asset.BMCPort != 443 {
				return withPort(asset.BMCAddress, asset.BMCPort)
			}
			return asset.BMCAddress
		}(),
		BMCUser:   asset.BMCUser,
		BMCSecret: secret,
	}
	if simulate {
		result.BMCActions = append(result.BMCActions, "set-boot-order pxe (simulated)", "power restart (simulated)")
		log.Printf("provisioning: 模拟 BMC 资产 id=%d mac=%s：意图 set boot order=pxe(persistent=false) 后 power restart", asset.ID, mac12)
		if e := p.markInstallation(ctx, installationID, "booting", "BMC 引导指令已模拟下发", true, false); e != nil {
			log.Printf("provisioning: 更新装机状态失败 installation_id=%d: %v", installationID, e)
		}
		result.Status = "booting"
		return result, nil
	}
	if e := p.BMC.SetBootOrder(ctx, bmcAsset, "pxe", false); e != nil {
		_ = p.failInstallation(ctx, installationID, fmt.Sprintf("设置 PXE 引导项失败: %v", e))
		return result, fmt.Errorf("provisioning: 设置一次性 PXE 引导项失败: %w", e)
	}
	result.BMCActions = append(result.BMCActions, "set-boot-order pxe")
	if e := p.BMC.Power(ctx, bmcAsset, "restart"); e != nil {
		// 引导项已改动，硬件状态不再可信：按“已开始但未确认”处理。
		_ = p.markInstallation(ctx, installationID, "unknown", fmt.Sprintf("引导项已切换但重启指令失败: %v", e), true, false)
		_ = p.recordInstallationEvent(ctx, sc, installationID, "failed", fmt.Sprintf("重启指令失败: %v", e), "error")
		return result, fmt.Errorf("provisioning: 重启设备失败（结果待人工确认）: %w", e)
	}
	result.BMCActions = append(result.BMCActions, "power restart")
	result.Status = "booting"
	if e := p.markInstallation(ctx, installationID, "booting", "已切换 PXE 引导并重启设备", true, false); e != nil {
		log.Printf("provisioning: 更新装机状态失败 installation_id=%d: %v", installationID, e)
	}
	if e := p.recordInstallationEvent(ctx, sc, installationID, "booting", "BMC 已下发一次性 PXE 引导并重启", "info"); e != nil {
		log.Printf("provisioning: 记录装机事件失败 installation_id=%d: %v", installationID, e)
	}
	log.Printf("provisioning: 装机已发起 installation_id=%d asset_id=%d mac=%s seed=%s script=%s", installationID, asset.ID, mac12, seedDir, scriptPath)
	return result, nil
}

// metaDataContent 生成最小可用的 NoCloud meta-data（instance-id + local-hostname）。
func metaDataContent(hostname, mac12 string) string {
	return fmt.Sprintf("instance-id: bmm-%s\nlocal-hostname: %s\n", mac12, hostname)
}
