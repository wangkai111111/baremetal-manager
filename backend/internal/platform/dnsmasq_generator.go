package platform

// 本文件把「设备台账 + 网络 + PXE 配置」翻译成一份可被 dnsmasq 直接加载的引导配置，
// 参考实现为 deploy/dnsmasq/dnsmasq.conf（iPXE 链式引导：BIOS undionly.kpxe、UEFI ipxe.efi）。
//
// 关键约束：
//   - 生成的文件永远写在 tftp-root 之下，绝不落盘 /etc，避免误操作系统级 dnsmasq；
//   - 默认关闭 DNS（port=0），仅承担 DHCP/TFTP；
//   - 两段式引导：PROM 阶段按架构下发 NBP（undionly.kpxe / ipxe.efi / snponly.efi），
//     iPXE 起来后的第二次 DHCP 请求（option 175）才下发该机器专属脚本 ipxe/{mac}.ipxe；
//     两个阶段分别用 tag:ipxe / tag:!ipxe 互斥，因此不会出现 BIOS PROM 误读文本脚本的问题；
//   - per-host 文件名使用「小写无冒号」MAC，与 IPXEScriptGenerator 的落盘约定完全一致；
//   - dhcp-host 中的 MAC 必须写成冒号小写（dnsmasq 唯一可识别写法）。

import (
	"context"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"baremetal-manager/backend/config"

	"gorm.io/gorm"
)

// provisioningHostnameRe 校验 dnsmasq 可接受的主机名。
var provisioningHostnameRe = regexp.MustCompile(`^[a-z0-9._-]{1,63}$`)

// provisioningIPRangeRe 解析 "10.0.0.100-10.0.0.200" 形式的地址段。
var provisioningIPRangeRe = regexp.MustCompile(`^([^,\s]+)\s*-\s*([^,\s]+)$`)

// 内置默认链式引导文件（相对 tftp-root），可被环境变量或 pxe_configs 覆盖。
const (
	defaultBIOSChainload = "undionly.kpxe"
	defaultEFIChainload  = "ipxe.efi"
	defaultARMChainload  = "snponly.efi"
)

// DnsmasqConfigGenerator emits a dnsmasq config for PXE boot from cfg.PXE + networks + assets.
type DnsmasqConfigGenerator struct {
	cfg   *config.Config
	store *store

	// OutputPath 指定生成文件位置；为空时写入 {TFTPRoot}/dnsmasq.conf（环境变量 PXE_DNSMASQ_CONF 可覆盖）。
	OutputPath string
	// Interface 指定 provisioning 网卡；为空时读取 PXE_INTERFACE，仍为空则输出注释提示由运维绑定，
	// 因为 dnsmasq 默认监听所有网卡，直接上生产网段有风险。
	Interface string
	// ServerAddress 是引导服务器地址；proxyDHCP 模式必须给出。
	// 为空时依次尝试 PXE_TFTP_SERVER、pxe_configs.next_server、PXE_HTTP_BASE 中的 IP 字面量。
	ServerAddress string
	// ProxyDHCP 为真时只下发引导信息、不参与地址分配（dhcp-range=<网络>,proxy + pxe-service 菜单）。
	ProxyDHCP bool
	// Authoritative 控制是否输出 dhcp-authoritative；nil 时读 PXE_DHCP_AUTHORITATIVE，默认关闭。
	Authoritative *bool
	// LeaseTime DHCP 租期，默认 12h。
	LeaseTime string
	// BIOSBootFile / EFIBootFile / ARMBootFile 为架构链式引导文件；为空时按 pxe_configs 再回退内置默认。
	BIOSBootFile string
	EFIBootFile  string
	ARMBootFile  string
}

// provisioningHost 是参与 PXE 引导的单台设备视图。
type provisioningHost struct {
	MAC  string // 归一化：12 位小写无分隔
	IP   string // 可能为空（台账未填地址，交由 DHCP 动态分配）
	Name string // 规整后的主机名
}

// NewDnsmasqConfigGenerator 构造生成器。cfg 为 nil 时按环境变量加载；db 为 nil 时仅生成全局段，
// 不含 per-host 条目（便于无数据库环境下预演配置）。
func NewDnsmasqConfigGenerator(cfg *config.Config, db *gorm.DB) *DnsmasqConfigGenerator {
	if cfg == nil {
		cfg = config.Load()
	}
	g := &DnsmasqConfigGenerator{cfg: cfg, Interface: strings.TrimSpace(os.Getenv("PXE_INTERFACE"))}
	if db != nil {
		g.store = &store{db: db}
	}
	if path := strings.TrimSpace(os.Getenv("PXE_DNSMASQ_CONF")); path != "" {
		g.OutputPath = path
	}
	if addr := strings.TrimSpace(os.Getenv("PXE_TFTP_SERVER")); provisioningIsIP(addr) {
		g.ServerAddress = addr
	}
	if g.ServerAddress == "" {
		if base, e := provisioningBootBase(cfg.PXE.HTTPBase); e == nil {
			if host := provisioningURLHost(base); provisioningIsIP(host) {
				g.ServerAddress = host
			}
		}
	}
	if mode := strings.TrimSpace(os.Getenv("PXE_DHCP_MODE")); strings.EqualFold(mode, "proxy") {
		g.ProxyDHCP = true
	}
	if lease := strings.TrimSpace(os.Getenv("PXE_DHCP_LEASE")); lease != "" {
		g.LeaseTime = lease
	}
	if value := strings.TrimSpace(os.Getenv("PXE_DHCP_AUTHORITATIVE")); value != "" {
		if on, e := strconv.ParseBool(value); e == nil {
			g.Authoritative = &on
		}
	}
	bootFiles := map[string]*string{"PXE_BIOS_BOOTFILE": &g.BIOSBootFile, "PXE_EFI_BOOTFILE": &g.EFIBootFile, "PXE_ARM64_BOOTFILE": &g.ARMBootFile}
	for env, place := range bootFiles {
		if value := strings.TrimSpace(os.Getenv(env)); provisioningSafeBootFile(value) {
			*place = value
		}
	}
	return g
}

// Generate 生成 dnsmasq 配置，写入 {TFTPRoot}/dnsmasq.conf（或 OutputPath），并返回文件内容。
func (d *DnsmasqConfigGenerator) Generate(ctx context.Context) (string, error) {
	if d.cfg == nil || strings.TrimSpace(d.cfg.PXE.TFTPRoot) == "" {
		return "", fmt.Errorf("pxe tftp root is not configured")
	}
	hosts, skipped, err := d.hosts(ctx)
	if err != nil {
		return "", err
	}
	notes := d.networkNotes(ctx)
	content := d.render(hosts, skipped, notes)

	path := strings.TrimSpace(d.OutputPath)
	if path == "" {
		path = filepath.Join(d.cfg.PXE.TFTPRoot, "dnsmasq.conf")
	}
	if e := os.MkdirAll(filepath.Dir(path), 0o755); e != nil {
		return "", fmt.Errorf("create dnsmasq config dir: %w", e)
	}
	tmp, e := os.CreateTemp(filepath.Dir(path), "dnsmasq.conf.*.tmp")
	if e != nil {
		return "", fmt.Errorf("create temp dnsmasq config: %w", e)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if _, e = tmp.WriteString(content); e != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("write dnsmasq config: %w", e)
	}
	if e = tmp.Chmod(0o644); e != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("chmod dnsmasq config: %w", e)
	}
	if e = tmp.Close(); e != nil {
		return "", fmt.Errorf("close dnsmasq config: %w", e)
	}
	if e = os.Rename(tmpName, path); e != nil {
		return "", fmt.Errorf("replace dnsmasq config: %w", e)
	}
	return content, nil
}

// GenerateHostEntry returns a single dhcp-host line for one asset MAC.
// 行内同时打上 set: 标签，配合渲染阶段的 dhcp-boot=tag:<标签> 实现「一机一个引导脚本」；
// 输入非法时返回注释行，保证整份配置始终可用。
func (d *DnsmasqConfigGenerator) GenerateHostEntry(mac, ip, hostname string) string {
	macFile, err := provisioningNormalizeMAC(mac)
	if err != nil {
		return fmt.Sprintf("# 跳过非法 MAC：%q", strings.TrimSpace(mac))
	}
	colon, err := provisionMACColon(macFile)
	if err != nil {
		return fmt.Sprintf("# 跳过非法 MAC：%q", strings.TrimSpace(mac))
	}
	line := "dhcp-host=" + colon + ",set:" + provisioningHostTag(macFile)
	if ip = strings.TrimSpace(ip); provisioningIsIP(ip) {
		line += "," + ip
	}
	return line + "," + provisioningHostname(hostname, macFile)
}

// hosts 读取台账中登记了 MAC 的设备：地址优先取未释放的 IP 分配记录，回退设备管理地址。
func (d *DnsmasqConfigGenerator) hosts(ctx context.Context) ([]provisioningHost, int, error) {
	if d.store == nil || d.store.db == nil {
		return nil, 0, nil
	}
	var rows []struct{ Name, MAC, IP string }
	e := d.store.db.WithContext(ctx).Raw(`
		SELECT a.name AS name, a.mac_address::text AS mac, COALESCE(alloc.address, a.management_ip)::text AS ip
		FROM assets a
		LEFT JOIN LATERAL (
			SELECT ia.address::text AS address
			FROM ip_allocations ia
			WHERE ia.tenant_id = a.tenant_id AND ia.project_id = a.project_id AND ia.asset_id = a.id
			  AND ia.deleted_at IS NULL AND ia.status <> 'released'
			ORDER BY ia.id DESC
			LIMIT 1
		) alloc ON TRUE
		WHERE a.deleted_at IS NULL AND a.mac_address IS NOT NULL AND a.status <> 'retired'
		ORDER BY a.id`).Scan(&rows).Error
	if e != nil {
		return nil, 0, fmt.Errorf("query pxe hosts: %w", e)
	}
	out := make([]provisioningHost, 0, len(rows))
	skipped := 0
	for _, row := range rows {
		macFile, err := provisioningNormalizeMAC(row.MAC)
		if err != nil {
			skipped++
			continue // MAC 非法的设备无法 PXE，计入跳过清单
		}
		ip := strings.TrimSpace(row.IP)
		if ip != "" && !provisioningIsIP(ip) {
			ip = ""
		}
		out = append(out, provisioningHost{MAC: macFile, IP: ip, Name: provisioningHostname(row.Name, macFile)})
	}
	return out, skipped, nil
}

// networkNotes 汇总启用 DHCP 的网段与已启用的 pxe_configs，既作为注释供人工核对，
// 也用于补全引导服务器地址与架构引导文件（环境变量优先级最高）。
func (d *DnsmasqConfigGenerator) networkNotes(ctx context.Context) []string {
	if d.store == nil || d.store.db == nil {
		return nil
	}
	notes := []string{}
	var networkRows []struct{ Name, Subnet, Gateway string }
	if e := d.store.db.WithContext(ctx).Table("networks").
		Select("name, subnet::text AS subnet, COALESCE(gateway::text,'') AS gateway").
		Where("deleted_at IS NULL AND dhcp_enabled").Order("id").Scan(&networkRows).Error; e == nil {
		for _, row := range networkRows {
			notes = append(notes, fmt.Sprintf("dhcp network %s subnet %s%s", row.Name, row.Subnet, gatewaySuffix(row.Gateway)))
		}
	}
	var pxeRows []struct{ BootMode, NextServer, BootFile string }
	if e := d.store.db.WithContext(ctx).Table("pxe_configs").
		Select("lower(boot_mode) AS boot_mode, COALESCE(next_server::text,'') AS next_server, boot_file").
		Where("deleted_at IS NULL AND enabled").Order("id").Scan(&pxeRows).Error; e == nil {
		for _, row := range pxeRows {
			notes = append(notes, fmt.Sprintf("pxe_config boot_mode=%s boot_file=%s next_server=%s", row.BootMode, row.BootFile, row.NextServer))
			if d.ServerAddress == "" && provisioningIsIP(row.NextServer) {
				d.ServerAddress = row.NextServer
			}
			if !provisioningSafeBootFile(row.BootFile) {
				continue
			}
			switch strings.ToLower(row.BootMode) {
			case "bios":
				if d.BIOSBootFile == "" {
					d.BIOSBootFile = row.BootFile
				}
			case "uefi":
				if d.EFIBootFile == "" {
					d.EFIBootFile = row.BootFile
				}
			}
		}
	}
	return notes
}

// render 组装完整配置文件文本。
func (d *DnsmasqConfigGenerator) render(hosts []provisioningHost, skipped int, notes []string) string {
	var b strings.Builder
	b.WriteString("# ==========================================================================\n")
	b.WriteString("# Baremetal Manager PXE/dnsmasq 配置 —— 自动生成，请勿手工编辑\n")
	b.WriteString("# 生成器：backend/internal/platform/dnsmasq_generator.go\n")
	fmt.Fprintf(&b, "# 生成时间：%s\n", time.Now().Format(time.RFC3339))
	b.WriteString("# 加载方式：dnsmasq --conf-file=<本文件> --keep-in-foreground\n")
	b.WriteString("#          （docker compose --profile provisioning 的 dhcp-tftp 服务即采用该方式）\n")
	b.WriteString("# 安全提示：本文件只落在 tftp-root 下；在共享生产网段启用 DHCP 前，\n")
	b.WriteString("#          务必用 interface= 绑定 provisioning 网卡并确认地址段无冲突。\n")
	b.WriteString("# ==========================================================================\n\n")
	b.WriteString("port=0\n")
	b.WriteString("log-dhcp\n")
	b.WriteString("bind-dynamic\n")
	if d.Interface != "" {
		fmt.Fprintf(&b, "interface=%s\n", d.Interface)
	} else {
		b.WriteString("# interface=eth0	# 未设置 PXE_INTERFACE：建议显式绑定 provisioning 网卡\n")
	}
	if d.authoritative() {
		b.WriteString("# 已开启 dhcp-authoritative：本服务是该隔离网段的唯一 DHCP 源（PXE_DHCP_AUTHORITATIVE=true）\n")
		b.WriteString("dhcp-authoritative\n")
	} else {
		b.WriteString("# dhcp-authoritative\n")
		b.WriteString("# 仅当本服务是隔离 provisioning 网段的唯一 DHCP 源时才放开（PXE_DHCP_AUTHORITATIVE=true），可避免客户端向其他服务器索取地址\n")
	}
	dhcpRangeLines := d.dhcpRangeLines(notes)
	for _, line := range dhcpRangeLines {
		b.WriteString(line + "\n")
	}
	if gateway := provisioningSingleGateway(notes); gateway != "" {
		fmt.Fprintf(&b, "dhcp-option=option:router,%s\n", gateway)
	} else {
		b.WriteString("# dhcp-option=option:router,10.0.0.1	# 存在多个网段或未登记网关，请按需补充\n")
	}
	fmt.Fprintf(&b, "dhcp-leasefile=%s\n", filepath.Join(d.cfg.PXE.TFTPRoot, "dnsmasq.leases"))
	b.WriteString("\n# ---- TFTP ----\n")
	b.WriteString("enable-tftp\n")
	fmt.Fprintf(&b, "tftp-root=%s\n", d.cfg.PXE.TFTPRoot)

	b.WriteString("\n# ---- 客户端识别：架构 + 是否已运行 iPXE ----\n")
	b.WriteString("pxe-prompt=\"Baremetal Manager PXE\",4\n")
	b.WriteString("dhcp-match=set:ipxe,175\n")
	b.WriteString("dhcp-match=set:bios,option:client-arch,0\n")
	b.WriteString("dhcp-match=set:bios,option:client-arch,6\n")
	b.WriteString("dhcp-match=set:efi-x64,option:client-arch,7\n")
	b.WriteString("dhcp-match=set:efi-x64,option:client-arch,9\n")
	b.WriteString("dhcp-match=set:efi-arm64,option:client-arch,11\n")
	b.WriteString("# 部分 iPXE 构建会附带 user-class=iPXE，可作为 175 选项之外的兜底识别方式\n")
	b.WriteString("# dhcp-userclass=set:ipxe,iPXE\n")

	b.WriteString("\n# ---- 第一段：PROM 按架构链入 iPXE 固件 ----\n")
	b.WriteString(d.bootLine("tag:!ipxe,tag:efi-x64", d.efiBootFile()) + "\n")
	b.WriteString(d.bootLine("tag:!ipxe,tag:efi-arm64", d.armBootFile()) + "\n")
	b.WriteString(d.bootLine("tag:!ipxe", d.biosBootFile()) + "\n")
	b.WriteString(d.bootMenu())

	b.WriteString(fmt.Sprintf("\n# ---- 第二段：已运行 iPXE 的机器按 MAC 取专属脚本（%d 台）----\n", len(hosts)))
	if len(hosts) == 0 {
		b.WriteString("# 台账中暂无登记 MAC 的设备，所有机器只会链入 iPXE 固件，不加载专属脚本。\n")
	}
	for _, host := range hosts {
		b.WriteString("# host " + host.Name + " mac " + host.MAC)
		if host.IP != "" {
			b.WriteString(" ip " + host.IP)
		}
		b.WriteString("\n")
		b.WriteString(d.GenerateHostEntry(host.MAC, host.IP, host.Name) + "\n")
		b.WriteString(d.bootLine("tag:ipxe,tag:"+provisioningHostTag(host.MAC), "ipxe/"+host.MAC+".ipxe") + "\n")
	}
	if skipped > 0 {
		fmt.Fprintf(&b, "# 另有 %d 台设备因 MAC 非法被跳过，请在设备台账中修正后重新生成。\n", skipped)
	}
	b.WriteString("# 未登记设备的兜底脚本（需自行准备 ipxe/default.ipxe）：\n")
	b.WriteString("# " + d.bootLine("tag:ipxe", "ipxe/default.ipxe") + "\n")

	b.WriteString("\n# ---- 生成上下文（人工核对用，不参与匹配）----\n")
	for _, note := range notes {
		b.WriteString("# " + note + "\n")
	}
	fmt.Fprintf(&b, "# http_base=%s\n", d.cfg.PXE.HTTPBase)
	fmt.Fprintf(&b, "# dhcp_range_config=%s\n", d.cfg.PXE.DHCPRange)
	fmt.Fprintf(&b, "# tftp_root=%s\n", d.cfg.PXE.TFTPRoot)
	return b.String()
}

// bootMenu 输出 pxe-service 菜单项：proxyDHCP 模式下这是实际下发引导文件的机制；
// 完整 DHCP 模式下默认注释，避免 PXE 菜单与 dhcp-boot 争抢 BIOS 客户端的文件名。
func (d *DnsmasqConfigGenerator) bootMenu() string {
	lines := []string{
		fmt.Sprintf("pxe-service=x86PC,\"Chainload iPXE (BIOS)\",%s%s", d.biosBootFile(), d.bootServerSuffix()),
		fmt.Sprintf("pxe-service=x86-64_EFI,\"Chainload iPXE (UEFI x64)\",%s%s", d.efiBootFile(), d.bootServerSuffix()),
		fmt.Sprintf("pxe-service=Aarch64_EFI,\"Chainload iPXE (UEFI ARM64)\",%s%s", d.armBootFile(), d.bootServerSuffix()),
	}
	if d.ProxyDHCP {
		return "\n# proxyDHCP：由下面的 pxe-service 菜单向 PROM 下发引导文件\n" + strings.Join(lines, "\n") + "\n"
	}
	return "\n# 完整 DHCP 模式使用 dhcp-boot；以下三行仅在 proxyDHCP（PXE_DHCP_MODE=proxy）下启用\n# " +
		strings.Join(lines, "\n# ") + "\n"
}

// bootLine 组装一条 dhcp-boot；tags 为逗号分隔的标签条件（可为空表示不限定）。
func (d *DnsmasqConfigGenerator) bootLine(tags, file string) string {
	if !provisioningSafeBootFile(file) {
		return "# 跳过非法引导文件名：" + file
	}
	line := "dhcp-boot="
	if tags != "" {
		line += tags + ","
	}
	return line + file + d.bootServerSuffix()
}

// bootServerSuffix 在 proxyDHCP 模式下必须显式指明引导服务器。
func (d *DnsmasqConfigGenerator) bootServerSuffix() string {
	if !d.ProxyDHCP || d.ServerAddress == "" {
		return ""
	}
	return ",,baremetal-manager," + d.ServerAddress
}

// dhcpRangeLines 把 cfg.PXE.DHCPRange 翻译成 dnsmasq 语法。
// 支持 "start-end"、含逗号的完整写法（原样透传）；proxyDHCP 模式输出 "<网络>,proxy"。
func (d *DnsmasqConfigGenerator) dhcpRangeLines(notes []string) []string {
	raw := strings.TrimSpace(d.cfg.PXE.DHCPRange)
	if raw == "" {
		return []string{"# dhcp-range 未配置（PXE_DHCP_RANGE），补全前请勿启用 DHCP"}
	}
	if strings.Contains(raw, ",") {
		return []string{"dhcp-range=" + raw}
	}
	if d.ProxyDHCP {
		return []string{fmt.Sprintf("dhcp-range=%s,proxy	# 只下发引导信息，不分配地址", provisioningRangeNetwork(raw))}
	}
	lease := d.leaseTime()
	match := provisioningIPRangeRe.FindStringSubmatch(raw)
	if match == nil || !provisioningIsIP(match[1]) || !provisioningIsIP(match[2]) {
		return []string{"dhcp-range=" + raw + "	# 无法解析为地址段，原样输出，请人工核对"}
	}
	if mask := provisioningMaskFor(notes, match[1]); mask != "" {
		return []string{fmt.Sprintf("dhcp-range=%s,%s,%s,%s", match[1], match[2], mask, lease)}
	}
	// 省略掩码时 dnsmasq 会用落在该段内的接口地址推导掩码。
	return []string{fmt.Sprintf("dhcp-range=%s,%s,%s	# 掩码由接口地址自动推导", match[1], match[2], lease)}
}

func (d *DnsmasqConfigGenerator) leaseTime() string {
	if lease := strings.TrimSpace(d.LeaseTime); lease != "" {
		return lease
	}
	return "12h"
}

func (d *DnsmasqConfigGenerator) authoritative() bool {
	if d.Authoritative != nil {
		return *d.Authoritative
	}
	return false
}

func (d *DnsmasqConfigGenerator) biosBootFile() string {
	return d.bootOrDefault(d.BIOSBootFile, defaultBIOSChainload)
}
func (d *DnsmasqConfigGenerator) efiBootFile() string {
	return d.bootOrDefault(d.EFIBootFile, defaultEFIChainload)
}
func (d *DnsmasqConfigGenerator) armBootFile() string {
	return d.bootOrDefault(d.ARMBootFile, defaultARMChainload)
}

func (d *DnsmasqConfigGenerator) bootOrDefault(value, fallback string) string {
	if provisioningSafeBootFile(value) {
		return value
	}
	return fallback
}

// gatewaySuffix 拼接网关注释片段。
func gatewaySuffix(gateway string) string {
	if gateway == "" {
		return ""
	}
	return " gw " + gateway
}

// provisioningHostTag 为每台机器生成稳定的标签名，用于把 dhcp-host 与专属引导文件绑定。
func provisioningHostTag(macFile string) string { return "h_" + macFile }

// provisioningSingleGateway 只有单一启用网段且登记了网关时才返回网关地址。
func provisioningSingleGateway(notes []string) string {
	var gateway string
	networks := 0
	for _, note := range notes {
		if !strings.HasPrefix(note, "dhcp network ") {
			continue
		}
		networks++
		if index := strings.Index(note, " gw "); index >= 0 {
			gateway = strings.TrimSpace(note[index+len(" gw "):])
		}
	}
	if networks == 1 && provisioningIsIP(gateway) {
		return gateway
	}
	return ""
}

// provisioningSafeBootFile 引导文件名必须是相对 tftp-root 的安全路径（禁止绝对路径、".."、空白与引号）。
func provisioningSafeBootFile(file string) bool {
	file = strings.TrimSpace(file)
	if file == "" || filepath.IsAbs(file) || strings.ContainsAny(file, " \t\r\n\"'\\") {
		return false
	}
	for _, segment := range strings.Split(filepath.ToSlash(file), "/") {
		if segment == "" || segment == "." || segment == ".." {
			return false
		}
	}
	return true
}

// provisioningIsIP 判断字符串是否为合法 IP 字面量。
func provisioningIsIP(value string) bool {
	_, e := netip.ParseAddr(strings.TrimSpace(value))
	return e == nil
}

// provisioningURLHost 从已校验过的 URL 中取出主机部分。
func provisioningURLHost(base string) string {
	withoutScheme := base
	if index := strings.Index(withoutScheme, "://"); index >= 0 {
		withoutScheme = withoutScheme[index+3:]
	}
	if index := strings.IndexAny(withoutScheme, "/?#"); index >= 0 {
		withoutScheme = withoutScheme[:index]
	}
	if index := strings.LastIndex(withoutScheme, ":"); index > 0 && !strings.Contains(withoutScheme[index:], "]") {
		withoutScheme = withoutScheme[:index]
	}
	return withoutScheme
}

// provisioningRangeNetwork 返回地址段所属网络（proxyDHCP 需要网络地址或段内任一地址）。
func provisioningRangeNetwork(raw string) string {
	if match := provisioningIPRangeRe.FindStringSubmatch(strings.TrimSpace(raw)); match != nil {
		return strings.TrimSpace(match[1])
	}
	return strings.TrimSpace(raw)
}

// provisioningMaskFor 依据启用 DHCP 网段的 CIDR 推导点分掩码：地址段起始地址落在某个前缀内即复用其掩码。
func provisioningMaskFor(notes []string, start string) string {
	address, e := netip.ParseAddr(start)
	if e != nil {
		return ""
	}
	for _, note := range notes {
		if !strings.HasPrefix(note, "dhcp network ") {
			continue
		}
		for _, field := range strings.Fields(note) {
			prefix, e := netip.ParsePrefix(field)
			if e != nil || !prefix.Addr().IsValid() {
				continue
			}
			if prefix.Contains(address) {
				return provisioningNetmaskFromBits(prefix.Bits())
			}
		}
	}
	return ""
}

// provisioningHostname 规整主机名：转小写、仅保留 [a-z0-9.-]，非法或为空时回退 host-{mac}。
func provisioningHostname(raw, macFile string) string {
	name := strings.ToLower(strings.TrimSpace(raw))
	if name == "" {
		return "host-" + macFile
	}
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '.', r == '-':
			b.WriteRune(r)
		case r == '_':
			b.WriteRune('-')
		default:
			// 其余字符（空格、中文等）不能进入 dnsmasq 配置，直接丢弃
		}
	}
	cleaned := strings.Trim(b.String(), "-.")
	if cleaned == "" || !provisioningHostnameRe.MatchString(cleaned) {
		return "host-" + macFile
	}
	return cleaned
}
