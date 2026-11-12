package platform

// 本文件负责“每主机 iPXE 引导脚本”的生成与落盘，是 PXE 交付链路的最后一环：
// dnsmasq 把 dhcp-boot 指向 ipxe/{mac}.ipxe，iPXE 再按脚本里的 URL 从 HTTP 侧拉取
// 内核、initrd、live-server ISO 与 autoinstall seed。
//
// Canonical netboot 方案（基于 22.04.5 live-server 实测）：
//
//	{HTTPBase}/netboot/vmlinuz + {HTTPBase}/netboot/initrd   → 磁盘 {TFTPRoot}/netboot/
//	{HTTPBase}/iso/{isoFileName}（url= 参数）            → 磁盘 {TFTPRoot}/iso/
//	{HTTPBase}/nocloud/{mac}/{meta-data,user-data}      → 磁盘 {TFTPRoot}/nocloud/{mac}/
//
// ============================ MAC 书写约定（全链路唯一约定）============================
//  1. 磁盘路径：一律使用「小写、无分隔符」的 12 位十六进制串，例如
//     {TFTPRoot}/ipxe/aabbccddeeff.ipxe、{TFTPRoot}/nocloud/aabbccddeeff/user-data。
//  2. HTTP URL 路径段：同样使用「小写、无分隔符」形式（{HTTPBase}/nocloud/aabbccddeeff/meta-data），
//     避免 URL 中出现冒号带来的转义歧义；服务端 provision 处理器对入参做归一化，
//     因此 aa:bb:cc:dd:ee:ff、AA-BB-CC-DD-EE-FF、aabb.ccdd.eeff 等写法也能命中同一目录。
//  3. dnsmasq / 内核命令行：MAC 必须写成「冒号分隔的小写」形式（aa:bb:cc:dd:ee:ff），
//     这是 dhcp-host 与 ip= 参数可识别的写法，由 provisionMACColon 负责转换。
// ======================================================================================

import (
	"encoding/binary"
	"fmt"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"baremetal-manager/backend/config"
)

// provisioningMACFileRe 校验归一化之后的 MAC（12 位小写十六进制）。
var provisioningMACFileRe = regexp.MustCompile(`^[0-9a-f]{12}$`)

// provisioningFileNameRe 校验会直接进入 URL 路径的文件名（live-server ISO 等），禁止任何路径分隔符与 ".."。
var provisioningFileNameRe = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// DefaultISOFileName Ubuntu 22.04.5 live-server 镜像文件名（作为 {TFTPRoot}/iso/ 下的文件名，经
// {HTTPBase}/iso/{isoFileName} 以 url= 形式提供给 casper）。
const DefaultISOFileName = "ubuntu-22.04.5-live-server-amd64.iso"

// DefaultSquashfsName 已废弃：live-server 22.04.5 采用分层 squashfs，casper 只接受 url= 指向完整 ISO，
// 指向单个 squashfs 的方式并不生效。保留常量仅为兼容旧调用方，新代码请使用 DefaultISOFileName。
const DefaultSquashfsName = "squashfs"

// IPXEScriptGenerator 依据 cfg.PXE 生成引用 netboot + autoinstall URL 的每主机 iPXE 脚本。
type IPXEScriptGenerator struct {
	cfg *config.Config
}

// NewIPXEScriptGenerator 构造生成器；cfg 为 nil 时按环境变量加载默认配置。
func NewIPXEScriptGenerator(cfg *config.Config) *IPXEScriptGenerator {
	if cfg == nil {
		cfg = config.Load()
	}
	return &IPXEScriptGenerator{cfg: cfg}
}

// UbuntuScript 生成 Ubuntu 22.04 live-server autoinstall 的 iPXE 脚本（Canonical 认可的 netboot 方式）。
//
// 参数：
//   - mac：设备 MAC，接受多种写法，内部归一化为无冒号小写；
//   - ip/netmask/gateway：三者同时给出时使用静态网络（iPXE set net0/* + 内核 ip= 的 off 模式），
//     任一为空则回退 dhcp（iPXE dhcp + 内核 ip=dhcp）；
//   - isoFileName：{TFTPRoot}/iso/ 下的 live-server ISO 文件名，留空则用 DefaultISOFileName。
//
// 重要：22.04.5 live-server 采用分层 squashfs，casper 仅把 url= 指向完整 ISO 视为合法 netboot 源，
// 因此这里不下发 casper/squashfs 路径，而是 url={HTTPBase}/iso/{isoFileName}，
// 同时使用 boot=casper + initrd=initrd（无 .img 后缀）；seed 目录为 {HTTPBase}/nocloud/{mac}/。
func (g *IPXEScriptGenerator) UbuntuScript(mac, ip, netmask, gateway, isoFileName string) (string, error) {
	base, err := g.httpBase()
	if err != nil {
		return "", err
	}
	macFile, err := provisioningNormalizeMAC(mac)
	if err != nil {
		return "", err
	}
	name := strings.TrimSpace(isoFileName)
	if name == "" {
		name = DefaultISOFileName
	}
	if !provisioningFileNameRe.MatchString(name) || name == "." || name == ".." {
		return "", fmt.Errorf("invalid live-server iso name %q", isoFileName)
	}

	// 静态网络三要素齐备时下发内核 ip= 的 off 模式，否则回退 dhcp。
	static, maskText, staticErr := provisioningStaticIP(ip, netmask, gateway)
	kernelArgs := []string{"initrd=initrd", "boot=casper"}
	var networkLine string
	if staticErr == nil {
		kernelArgs = append(kernelArgs, "ip="+static)
		// iPXE 侧同步配置静态地址，保证抓取 initrd 之前的 HTTP 请求也能联网。
		networkLine = fmt.Sprintf("set net0/ip %s\nset net0/netmask %s\nset net0/gateway %s\nifopen net0",
			strings.TrimSpace(ip), maskText, strings.TrimSpace(gateway))
	} else {
		kernelArgs = append(kernelArgs, "ip=dhcp")
		networkLine = provisioningIPXEDHCP
	}
	// seed 目录以 NoCloud-net 形式提供；分号在 iPXE 脚本里必须转义，否则会被当作命令行分隔符截断。
	// ${mac} 在生成时直接展开为无冒号小写 MAC（与落盘目录名一致），不依赖 iPXE 内置变量（那是冒号形式）。
	kernelArgs = append(kernelArgs,
		"autoinstall",
		"url=${http-base}/iso/"+name,
		`ds=nocloud-net\;s=${http-base}/nocloud/`+macFile+`/`,
	)

	var b strings.Builder
	fmt.Fprintf(&b, "#!ipxe\n")
	fmt.Fprintf(&b, "# Ubuntu 22.04 live-server autoinstall —— 由 Baremetal Manager 自动生成，请勿手工编辑\n")
	fmt.Fprintf(&b, "# host mac: %s\n", macFile)
	fmt.Fprintf(&b, "set http-base %s\n", base)
	b.WriteString(networkLine + "\n")
	fmt.Fprintf(&b, "kernel ${http-base}/netboot/vmlinuz %s ---\n", strings.Join(kernelArgs, " "))
	b.WriteString("initrd ${http-base}/netboot/initrd\n")
	b.WriteString("boot\n")
	return b.String(), nil
}

// KickstartScript 生成 RHEL 系（RHEL/CentOS/Rocky/Alma）Anaconda 网络安装的 iPXE 脚本。
// ksURL 必填；repoURL 为空时不下发 inst.stage2/inst.repo，此时安装源需由 ks 文件自身指定。
func (g *IPXEScriptGenerator) KickstartScript(mac, ksURL, repoURL string) (string, error) {
	base, err := g.httpBase()
	if err != nil {
		return "", err
	}
	macFile, err := provisioningNormalizeMAC(mac)
	if err != nil {
		return "", err
	}
	ks, err := provisioningBootURL(ksURL, "ks URL")
	if err != nil {
		return "", err
	}
	args := []string{"initrd=initrd.img", "ip=dhcp"}
	if repo := strings.TrimSpace(repoURL); repo != "" {
		src, err := provisioningBootURL(repo, "repo URL")
		if err != nil {
			return "", err
		}
		// inst.stage2 决定 installer 镜像来源，inst.repo 决定软件包仓库，两者通常同址。
		args = append(args, "inst.stage2="+src, "inst.repo="+src)
	}
	args = append(args, "ks="+ks, "net.ifnames=0", "biosdevname=0")

	var b strings.Builder
	fmt.Fprintf(&b, "#!ipxe\n")
	fmt.Fprintf(&b, "# RHEL 系 kickstart 网络安装 —— 由 Baremetal Manager 自动生成，请勿手工编辑\n")
	fmt.Fprintf(&b, "# host mac: %s\n", macFile)
	b.WriteString(provisioningIPXEDHCP + "\n")
	fmt.Fprintf(&b, "kernel %s/netboot/vmlinuz %s ---\n", base, strings.Join(args, " "))
	fmt.Fprintf(&b, "initrd %s/netboot/initrd.img\n", base)
	b.WriteString("boot\n")
	return b.String(), nil
}

// WriteScript 把脚本写入 {TFTPRoot}/ipxe/{mac}.ipxe（文件名为归一化后的无冒号小写 MAC），返回落盘路径。
// 采用「临时文件 + rename」原子替换，避免正在 PXE 的机器读到半截脚本。
func (g *IPXEScriptGenerator) WriteScript(mac, content string) (string, error) {
	if g.cfg == nil || strings.TrimSpace(g.cfg.PXE.TFTPRoot) == "" {
		return "", fmt.Errorf("pxe tftp root is not configured")
	}
	macFile, err := provisioningNormalizeMAC(mac)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(content) == "" {
		return "", fmt.Errorf("ipxe script content is empty")
	}
	if len(content) > 64<<10 {
		return "", fmt.Errorf("ipxe script too large")
	}
	if !strings.HasPrefix(content, "#!ipxe") {
		return "", fmt.Errorf("ipxe script must start with #!ipxe")
	}
	dir := filepath.Join(g.cfg.PXE.TFTPRoot, "ipxe")
	if e := os.MkdirAll(dir, 0o755); e != nil {
		return "", fmt.Errorf("create ipxe dir: %w", e)
	}
	path := filepath.Join(dir, macFile+".ipxe")
	tmp, e := os.CreateTemp(dir, macFile+".ipxe.*.tmp")
	if e != nil {
		return "", fmt.Errorf("create temp ipxe script: %w", e)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if _, e = tmp.WriteString(content); e != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("write ipxe script: %w", e)
	}
	if e = tmp.Chmod(0o644); e != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("chmod ipxe script: %w", e)
	}
	if e = tmp.Close(); e != nil {
		return "", fmt.Errorf("close ipxe script: %w", e)
	}
	if e = os.Rename(tmpName, path); e != nil {
		return "", fmt.Errorf("replace ipxe script: %w", e)
	}
	return path, nil
}

// ScriptPath 返回某台设备 iPXE 脚本的预期磁盘路径，供 dhcp-boot / dnsmasq 生成侧引用。
func (g *IPXEScriptGenerator) ScriptPath(mac string) (string, error) {
	if g.cfg == nil || strings.TrimSpace(g.cfg.PXE.TFTPRoot) == "" {
		return "", fmt.Errorf("pxe tftp root is not configured")
	}
	macFile, err := provisioningNormalizeMAC(mac)
	if err != nil {
		return "", err
	}
	return filepath.Join(g.cfg.PXE.TFTPRoot, "ipxe", macFile+".ipxe"), nil
}

// BootFile 返回 dnsmasq dhcp-boot 使用的相对引导文件名（相对 tftp-root）。
func (g *IPXEScriptGenerator) BootFile(mac string) (string, error) {
	macFile, err := provisioningNormalizeMAC(mac)
	if err != nil {
		return "", err
	}
	return "ipxe/" + macFile + ".ipxe", nil
}

func (g *IPXEScriptGenerator) httpBase() (string, error) {
	if g.cfg == nil {
		return "", fmt.Errorf("pxe config is not loaded")
	}
	return provisioningBootBase(g.cfg.PXE.HTTPBase)
}

// provisioningIPXEDHCP iPXE 对默认网卡执行 DHCP 的语句（Canonical netboot 示例采用裸 dhcp）。
const provisioningIPXEDHCP = "dhcp"

// provisioningBootBase 校验并规范化 HTTPBase：必须是 http/https、无凭据、无查询串。
func provisioningBootBase(raw string) (string, error) {
	base := strings.TrimRight(strings.TrimSpace(raw), "/")
	if base == "" {
		return "", fmt.Errorf("pxe http base is not configured")
	}
	u, e := url.Parse(base)
	if e != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.Fragment != "" {
		return "", fmt.Errorf("invalid pxe http base %q", raw)
	}
	return base, nil
}

// provisioningBootURL 校验会被写入 iPXE 脚本的 URL，拦截空白/控制字符与非法 scheme，
// 防止外部输入把引导脚本改写成任意命令。
func provisioningBootURL(raw, label string) (string, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", fmt.Errorf("%s is required", label)
	}
	if strings.ContainsAny(value, " \t\r\n\"'\\") || strings.ContainsRune(value, 0) {
		return "", fmt.Errorf("%s contains forbidden characters", label)
	}
	u, e := url.Parse(value)
	if e != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.Fragment != "" {
		return "", fmt.Errorf("%s must be an absolute http(s) URL", label)
	}
	return value, nil
}

// provisioningStaticIP 组装内核 ip= 静态参数：CLIENT:SERVER:GW:MASK:HOST:DEV:AUTOCONF，
// 同时返回换算后的点分掩码供 iPXE 静态配置复用。任一字段非法时返回 error，调用方据此回退 dhcp。
func provisioningStaticIP(ip, netmask, gateway string) (string, string, error) {
	address, e1 := netip.ParseAddr(strings.TrimSpace(ip))
	gw, e3 := netip.ParseAddr(strings.TrimSpace(gateway))
	if e1 != nil || e3 != nil {
		return "", "", fmt.Errorf("invalid static network")
	}
	mask, e := provisioningNetmask(netmask)
	if e != nil {
		return "", "", e
	}
	if !address.Is4() || !gw.Is4() {
		return "", "", fmt.Errorf("static netboot only supports IPv4")
	}
	return fmt.Sprintf("%s::%s:%s:::off", address, gw, mask), mask, nil
}

// provisioningNetmask 把子网掩码的常见写法（点分十进制、CIDR 网段或纯前缀位数）
// 统一换算成内核 ip= 参数要求的点分十进制；非连续掩码视为非法。
func provisioningNetmask(netmask string) (string, error) {
	value := strings.TrimSpace(netmask)
	if value == "" {
		return "", fmt.Errorf("netmask is required")
	}
	if bits, e := strconv.Atoi(value); e == nil && bits >= 0 && bits <= 32 {
		return provisioningNetmaskFromBits(bits), nil
	}
	if strings.Contains(value, "/") {
		prefix, e := netip.ParsePrefix(value)
		if e != nil {
			return "", fmt.Errorf("invalid netmask %q", netmask)
		}
		return provisioningNetmaskFromBits(prefix.Bits()), nil
	}
	address, e := netip.ParseAddr(value)
	if e != nil || !address.Is4() {
		return "", fmt.Errorf("invalid netmask %q", netmask)
	}
	// 连续掩码判定：反掩码必须形如 2^n-1。
	octets := address.As4()
	inverse := ^binary.BigEndian.Uint32(octets[:])
	if inverse&(inverse+1) != 0 {
		return "", fmt.Errorf("netmask %q is not contiguous", netmask)
	}
	return value, nil
}

// provisioningNetmaskFromBits 由前缀长度生成点分十进制掩码。
func provisioningNetmaskFromBits(bits int) string {
	var mask uint32
	if bits > 0 {
		mask = ^uint32(0) << (32 - uint(bits))
	}
	return fmt.Sprintf("%d.%d.%d.%d", mask>>24, mask>>16&0xff, mask>>8&0xff, mask&0xff)
}

// provisionMACColon 返回冒号分隔的小写 MAC，用于 dnsmasq dhcp-host 与需要标准写法的场合。
func provisionMACColon(mac string) (string, error) {
	h, err := provisioningNormalizeMAC(mac)
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

// provisioningNormalizeMAC 按上述约定把任意常见写法的 MAC 归一化为 12 位小写无分隔串。
// 兼容 aa:bb:cc:dd:ee:ff / AA-BB-CC-DD-EE-FF / aabb.ccdd.eeff / aabbccddeeff。
func provisioningNormalizeMAC(mac string) (string, error) {
	stripped := strings.NewReplacer(":", "", "-", "", ".", "", " ", "").Replace(strings.ToLower(strings.TrimSpace(mac)))
	if !provisioningMACFileRe.MatchString(stripped) {
		return "", fmt.Errorf("invalid MAC address %q", mac)
	}
	return stripped, nil
}
