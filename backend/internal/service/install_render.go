package service

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"text/template"

	"baremetal-manager/backend/internal/model"

	"golang.org/x/crypto/bcrypt"
)

// InstallVars 承载渲染装机配置所需的替换变量。
// 说明：RootPassword 为明文口令，渲染器会在需要的位置自动进行哈希；
// PasswordHash 为可直接写入应答文件的 crypt/bcrypt 哈希（若为空则由 RootPassword 计算）。
type InstallVars struct {
	Hostname     string            // 目标主机名
	MAC          string            // 安装网卡 MAC
	IP           string            // 静态 IP，空表示 DHCP
	Netmask      string            // 子网掩码（对应 $SUBNET）
	Gateway      string            // 默认网关
	DNS          string            // DNS 服务器（可空格/逗号分隔多个）
	RootPassword string            // root/admin 明文口令
	PasswordHash string            // 预先计算好的密码哈希（bcrypt 或 crypt-sha512）
	SSHKey       string            // 需要注入的 authorized_keys 内容
	HTTPBase     string            // cfg.PXE.HTTPBase，用于 netboot/autoinstall URL
	Extra        map[string]string // 其它自定义变量
}

// IsValidInstallFormat 判断给定的应答文件格式是否受支持（与 DB CHECK 约束一致）。
func IsValidInstallFormat(format string) bool {
	return allowedFormats[strings.ToLower(strings.TrimSpace(format))]
}

// 支持的应答文件格式集合，与 003_target_schema.sql 的 CHECK 约束保持一致。
var allowedFormats = map[string]bool{
	"kickstart":   true,
	"autoinstall": true,
	"preseed":     true,
	"unattend":    true,
}

// RenderInstallConfig 依据模板格式把 KickstartTemplate 渲染为最终装机配置文本。
// 按 template.Format 分派：kickstart / autoinstall / preseed / unattend。
func RenderInstallConfig(tpl model.KickstartTemplate, vars InstallVars) (string, error) {
	format := strings.ToLower(strings.TrimSpace(tpl.Format))
	if format == "" {
		// 未指定格式时，依据 os_type 做最小限度的推断，无法推断则报错。
		format = inferFormat(tpl.OSType)
	}
	if !allowedFormats[format] {
		return "", fmt.Errorf("不支持的应答文件格式: %q", tpl.Format)
	}
	// 若用户提供了自定义正文，先做注入检查。
	if strings.TrimSpace(tpl.TemplateContent) != "" {
		if err := SanitizeTemplateContent(tpl.TemplateContent); err != nil {
			return "", err
		}
	}
	switch format {
	case "autoinstall":
		return renderAutoinstall(tpl, vars)
	case "kickstart":
		return renderKickstart(tpl, vars)
	case "preseed":
		return renderPreseed(tpl, vars)
	case "unattend":
		return renderUnattend(tpl, vars)
	default:
		return "", fmt.Errorf("不支持的应答文件格式: %q", tpl.Format)
	}
}

// inferFormat 依据操作系统类型推断应答格式。
func inferFormat(osType string) string {
	switch strings.ToLower(osType) {
	case "ubuntu", "debian", "linux-ubuntu":
		// Debian 家族里 Ubuntu 用 autoinstall，纯 Debian 用 preseed；
		// 缺省优先 autoinstall（本项目主要面向 Ubuntu）。
		return "autoinstall"
	case "rhel", "centos", "rocky", "alma", "fedora", "oraclelinux":
		return "kickstart"
	default:
		return ""
	}
}

// ============================================================
// Ubuntu subiquity autoinstall
// ============================================================

// autoinstallData 是渲染 autoinstall YAML 时传给 text/template 的数据模型。
type autoinstallData struct {
	Hostname      string
	Locale        string
	Timezone      string
	Keyboard      string
	PasswordHash  string
	SSHKey        string
	HasSSHKey     bool
	HTTPBase      string
	UseStaticNet  bool
	Address       string // IP/掩码，如 10.0.0.5/24
	Gateway       string
	Nameservers   []string
	StorageConfig string // 已渲染好的 storage.config 代码块（缩进 8 空格）
	PostScript    string
	HasPostScript bool
}

// renderAutoinstall 生成 Ubuntu subiquity autoinstall（#cloud-config + autoinstall: 块）。
// 若 tpl.TemplateContent 非空，则把它当作用户自定义模板执行；否则使用内置默认模板。
func renderAutoinstall(tpl model.KickstartTemplate, vars InstallVars) (string, error) {
	data, err := buildAutoinstallData(tpl, vars)
	if err != nil {
		return "", err
	}
	content := strings.TrimSpace(tpl.TemplateContent)
	if content == "" {
		content = defaultAutoinstallTemplate
	}
	return executeTemplate("autoinstall", content, data, func() map[string]any {
		return templateFuncs()
	})
}

// buildAutoinstallData 归一化变量，生成供模板使用的数据结构。
func buildAutoinstallData(tpl model.KickstartTemplate, vars InstallVars) (autoinstallData, error) {
	d := autoinstallData{
		Hostname:     fallback(vars.Hostname, "ubuntu-target"),
		Locale:       "en_US.UTF-8",
		Timezone:     "UTC",
		Keyboard:     "us",
		HTTPBase:     strings.TrimRight(vars.HTTPBase, "/"),
		SSHKey:       strings.TrimSpace(vars.SSHKey),
		UseStaticNet: strings.TrimSpace(vars.IP) != "",
	}
	d.HasSSHKey = d.SSHKey != ""

	// 密码哈希：优先使用预计算哈希，否则对明文做 bcrypt 哈希。
	hash := strings.TrimSpace(vars.PasswordHash)
	if hash == "" {
		if strings.TrimSpace(vars.RootPassword) == "" {
			return d, errors.New("autoinstall 需要 root/admin 口令或密码哈希")
		}
		bcryptHash, err := HashPassword(vars.RootPassword)
		if err != nil {
			return d, err
		}
		hash = bcryptHash
	}
	d.PasswordHash = hash

	// 网络
	if d.UseStaticNet {
		mask := fallbackCIDR(vars.Netmask)
		d.Address = strings.TrimSpace(vars.IP) + mask
		d.Gateway = strings.TrimSpace(vars.Gateway)
		d.Nameservers = splitList(vars.DNS)
	}

	// 存储
	d.StorageConfig = buildStorageConfig(tpl.PartitionScheme)

	// 装机后脚本
	d.PostScript = strings.TrimSpace(tpl.PostScript)
	d.HasPostScript = d.PostScript != ""
	return d, nil
}

// buildStorageConfig 生成 autoinstall storage.config 代码块。
// 默认单盘 ext4 根分区；从 partitionScheme 文本里识别 swap / /home 及文件系统类型进行扩展。
func buildStorageConfig(partitionScheme string) string {
	scheme := strings.ToLower(partitionScheme)
	fstype := "ext4"
	if strings.Contains(scheme, "btrfs") {
		fstype = "btrfs"
	} else if strings.Contains(scheme, "xfs") {
		fstype = "xfs"
	}
	wantsSwap := strings.Contains(scheme, "swap")
	wantsHome := strings.Contains(scheme, "/home")

	var b strings.Builder
	// 磁盘
	b.WriteString("- type: disk\n")
	b.WriteString("  id: disk0\n")
	b.WriteString("  wipe: superblock-recursive\n")
	b.WriteString("  preserve: false\n")
	b.WriteString("  path: /dev/sda\n")
	b.WriteString("  pttype: gpt\n")
	// 根分区
	b.WriteString("- type: partition\n")
	b.WriteString("  id: part-root\n")
	b.WriteString("  device: disk0\n")
	b.WriteString("  number: 1\n")
	b.WriteString("  size: minimum\n")
	// 根文件系统
	b.WriteString("- type: filesystem\n")
	b.WriteString("  id: fs-root\n")
	b.WriteString("  fstype: " + fstype + "\n")
	b.WriteString("  volume: part-root\n")
	b.WriteString("  label: root\n")
	b.WriteString("- type: mount\n")
	b.WriteString("  id: mount-root\n")
	b.WriteString("  path: /\n")
	b.WriteString("  device: fs-root\n")

	idx := 2
	if wantsSwap {
		b.WriteString("- type: partition\n")
		fmt.Fprintf(&b, "  id: part-swap\n  device: disk0\n  number: %d\n  size: 2G\n", idx)
		idx++
		b.WriteString("- type: format\n")
		fmt.Fprintf(&b, "  id: fs-swap\n  fstype: swap\n  volume: part-swap\n")
		b.WriteString("- type: mount\n")
		b.WriteString("  id: mount-swap\n  path: none\n  device: fs-swap\n")
	}
	if wantsHome {
		b.WriteString("- type: partition\n")
		fmt.Fprintf(&b, "  id: part-home\n  device: disk0\n  number: %d\n  size: max\n", idx)
		b.WriteString("- type: format\n")
		fmt.Fprintf(&b, "  id: fs-home\n  fstype: %s\n  volume: part-home\n", fstype)
		b.WriteString("- type: mount\n")
		b.WriteString("  id: mount-home\n  path: /home\n  device: fs-home\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// defaultAutoinstallTemplate 是内置的 subiquity autoinstall 默认模板。
// 采用 text/template，FuncMap 仅暴露安全函数（lower/upper/base64/sha512crypt），
// 不含 exec/env/system 之类可造成命令注入的能力。
const defaultAutoinstallTemplate = `#cloud-config
autoinstall:
  version: 1
  locale: {{ .Locale }}
  keyboard:
    layout: {{ .Keyboard }}
  timezone: {{ .Timezone }}
  apt:
    primary:
      - arches: [default]
        uri: {{ if .HTTPBase }}{{ .HTTPBase }}/apt/{{ end }}
  network:
    version: 2
{{- if .UseStaticNet }}
    ethernets:
      id0:
        match:
          name: "en*"
        addresses:
          - {{ .Address }}
{{- if .Gateway }}
        routes:
          - to: default
            via: {{ .Gateway }}
{{- end }}
{{- if .Nameservers }}
        nameservers:
          addresses: [{{ range $i, $ns := .Nameservers }}{{ if $i }}, {{ end }}{{ printf "%q" $ns }}{{ end }}]
{{- end }}
{{- else }}
    ethernets:
      id0:
        match:
          name: "en*"
        dhcp4: true
{{- end }}
  storage:
    config:
{{ .StorageConfig | indent }}
  identity:
    hostname: {{ printf "%q" .Hostname }}
    username: admin
    password: {{ printf "%q" .PasswordHash }}
  ssh:
    install-server: true
    allow-pw: true
{{- if .HasSSHKey }}
    authorized_keys:
      - {{ printf "%q" .SSHKey }}
{{- end }}
  user-data:
    disable_root: false
    chpasswd: { expire: false }
    ssh_pwauth: true
{{- if .HasSSHKey }}
    write_files:
      - path: /home/admin/.ssh/authorized_keys
        permissions: '0600'
        owner: admin:admin
        content: |
          {{ .SSHKey }}
{{- end }}
    runcmd:
      - [ systemctl, enable, --now, ssh ]
{{- if .HasPostScript }}
  late-commands:
    - curtin in-target -- systemctl enable ssh
    - curtin in-target --target=/target -- sh -c {{ printf "%q" .PostScript }}
{{- else }}
  late-commands:
    - curtin in-target -- systemctl enable ssh
{{- end }}
`

// ============================================================
// RHEL 家族 kickstart
// ============================================================

// renderKickstart 生成 RHEL 系 kickstart（%pre/%packages/%post）并做变量替换。
// 若 tpl.TemplateContent 非空则以其为模板正文；否则使用内置默认 kickstart。
func renderKickstart(tpl model.KickstartTemplate, vars InstallVars) (string, error) {
	content := strings.TrimSpace(tpl.TemplateContent)
	if content == "" {
		content = defaultKickstartTemplate
	}
	out, err := executeTemplate("kickstart", content, kickstartData(tpl, vars), templateFuncs)
	if err != nil {
		return "", err
	}
	// 兼容 shell 风格的 $TOKEN 变量替换。
	return SubstituteVars(out, vars), nil
}

// kickstartData 归一化 kickstart 模板所需数据。
func kickstartData(tpl model.KickstartTemplate, vars InstallVars) map[string]any {
	hash := strings.TrimSpace(vars.PasswordHash)
	if hash == "" && strings.TrimSpace(vars.RootPassword) != "" {
		if h, err := HashPassword(vars.RootPassword); err == nil {
			hash = h
		}
	}
	return map[string]any{
		"Hostname":     fallback(vars.Hostname, "localhost"),
		"MAC":          strings.TrimSpace(vars.MAC),
		"IP":           strings.TrimSpace(vars.IP),
		"Netmask":      fallback(vars.Netmask, "255.255.255.0"),
		"Gateway":      strings.TrimSpace(vars.Gateway),
		"DNS":          strings.TrimSpace(vars.DNS),
		"PasswordHash": hash,
		"SSHKey":       strings.TrimSpace(vars.SSHKey),
		"HTTPBase":     strings.TrimRight(vars.HTTPBase, "/"),
		"PostScript":   strings.TrimSpace(tpl.PostScript),
		"Partitions":   buildKickstartPartitions(tpl.PartitionScheme),
	}
}

// buildKickstartPartitions 依据分区方案生成 kickstart part 行。
func buildKickstartPartitions(partitionScheme string) string {
	scheme := strings.ToLower(partitionScheme)
	var b strings.Builder
	b.WriteString("part /boot --fstype=ext4 --size=1024\n")
	if strings.Contains(scheme, "swap") {
		b.WriteString("part swap --size=2048\n")
	}
	b.WriteString("part / --fstype=ext4 --grow --size=10240\n")
	return strings.TrimRight(b.String(), "\n")
}

const defaultKickstartTemplate = `text
lang en_US.UTF-8
keyboard us
timezone --utc UTC
{{- if .IP }}
network --bootproto=static --ip={{ .IP }} --netmask={{ .Netmask }}{{ if .Gateway }} --gateway={{ .Gateway }}{{ end }}{{ if .DNS }} --nameserver={{ .DNS }}{{ end }} --onboot=on --device={{ if .MAC }}{{ .MAC }}{{ else }}bootif{{ end }}
{{- else }}
network --bootproto=dhcp --onboot=on
{{- end }}
rootpw --iscrypted {{ if .PasswordHash }}{{ .PasswordHash }}{{ else }}{{ "$6$CHANGE_ME" }}{{ end }}
{{- if .SSHKey }}
sshkey --username=root "{{ .SSHKey }}"
{{- end }}
{{ .Partitions }}
reboot
firewall --disabled
selinux --enforcing
%packages
@core
openssh-server
%end
%pre
echo "pre-install hooks for {{ .Hostname }}"
%end
%post
systemctl enable sshd
{{ .PostScript }}
%end
`

// ============================================================
// Debian preseed / Windows unattend（最小实现）
// ============================================================

// renderPreseed 生成 Debian preseed 配置，并对正文做变量替换。
func renderPreseed(tpl model.KickstartTemplate, vars InstallVars) (string, error) {
	content := strings.TrimSpace(tpl.TemplateContent)
	if content == "" {
		content = defaultPreseedTemplate
	}
	out, err := executeTemplate("preseed", content, kickstartData(tpl, vars), templateFuncs)
	if err != nil {
		return "", err
	}
	return SubstituteVars(out, vars), nil
}

const defaultPreseedTemplate = `d-i debian-installer/locale string {{ .Hostname | default "en_US" }}
d-i netcfg/choose_interface select auto
{{- if .IP }}
d-i netcfg/get_ipaddress string {{ .IP }}
d-i netcfg/get_netmask string {{ .Netmask }}
d-i netcfg/get_gateway string {{ .Gateway }}
d-i netcfg/get_nameservers string {{ .DNS }}
d-i netcfg/confirm_static boolean true
{{- else }}
d-i netcfg/get_ipaddress string dhcp
{{- end }}
d-i passwd/root-password-crypted password {{ .PasswordHash }}
d-i partman-auto/method string regular
d-i partman-auto/choose_recipe select atomic
d-i passwd/make-user boolean false
{{- if .SSHKey }}
d-i preseed/late_command string mkdir -p /target/root/.ssh; echo {{ printf "%q" .SSHKey }} >> /target/root/.ssh/authorized_keys
{{- end }}
{{ .PostScript }}
`

// renderUnattend 生成 Windows 无人值守应答（unattend.xml），做变量替换。
func renderUnattend(tpl model.KickstartTemplate, vars InstallVars) (string, error) {
	content := strings.TrimSpace(tpl.TemplateContent)
	if content == "" {
		content = defaultUnattendTemplate
	}
	out, err := executeTemplate("unattend", content, kickstartData(tpl, vars), templateFuncs)
	if err != nil {
		return "", err
	}
	return SubstituteVars(out, vars), nil
}

const defaultUnattendTemplate = `<?xml version="1.0" encoding="utf-8"?>
<unattend xmlns="urn:schemas-microsoft-com:unattend">
  <settings pass="oobeSystem">
    <component name="Microsoft-Windows-Shell-Setup" processorArchitecture="amd64" publicKeyToken="31bf3856ad364e35" language="neutral" versionScope="nonSxS" xmlns:wcm="http://schemas.microsoft.com/WMIConfig/2002/State">
      <ComputerName>{{ .Hostname }}</ComputerName>
      <AutoLogon><Enabled>false</Enabled></AutoLogon>
    </component>
  </settings>
</unattend>
`

// ============================================================
// 变量替换 / 哈希 / 安全
// ============================================================

// SubstituteVars 替换原始文本中的 shell 风格变量 token。
// 支持：$IP $HOSTNAME $ROOT_PASSWORD $MAC $GATEWAY $DNS $SUBNET $SSHKEY $HTTPBASE。
// 说明：$SUBNET 对应子网掩码（Netmask）。为避免误伤正文里的 "$" 变量，
// 仅替换上述明确列出的 token。
func SubstituteVars(raw string, vars InstallVars) string {
	pairs := []string{
		"$HOSTNAME", vars.Hostname,
		"$MAC", vars.MAC,
		"$IP", vars.IP,
		"$SUBNET", vars.Netmask,
		"$NETMASK", vars.Netmask,
		"$GATEWAY", vars.Gateway,
		"$DNS", vars.DNS,
		"$ROOT_PASSWORD", vars.RootPassword,
		"$PASSWORD_HASH", vars.PasswordHash,
		"$SSHKEY", vars.SSHKey,
		"$HTTPBASE", vars.HTTPBase,
	}
	// 先处理自定义变量，Extra 允许覆盖内置 token。
	for k, v := range vars.Extra {
		pairs = append(pairs, normalizeToken(k), v)
	}
	replacer := strings.NewReplacer(pairs...)
	return replacer.Replace(raw)
}

// normalizeToken 把变量键规范化为 $TOKEN 形式。
func normalizeToken(key string) string {
	if strings.HasPrefix(key, "$") {
		return key
	}
	return "$" + strings.ToUpper(key)
}

// HashPassword 返回适合写入 autoinstall identity.users.password 的哈希。
// 采用 bcrypt，因为 golang.org/x/crypto/bcrypt 已是本项目依赖；
// Ubuntu autoinstall identity.password 同时接受 crypt(3)（$6$...）与 bcrypt（$2b$...）格式。
// Go 标准 bcrypt 生成 $2a$ 前缀，这里规范化为现代 $2b$ 变体（校验时同样被 bcrypt 库与 libxcrypt 接受）。
func HashPassword(plaintext string) (string, error) {
	if plaintext == "" {
		return "", errors.New("cannot hash empty password")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(plaintext), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	// 将 $2a$ 前缀规范化为 $2b$（OpenBSD 修复 8-bit 的现行变体）。
	out := string(hash)
	if strings.HasPrefix(out, "$2a$") {
		out = "$2b$" + out[4:]
	}
	return out, nil
}

// bannedPatterns 定义会破坏宿主机或造成明显注入的危险片段。
// 采取保守策略：只拦截高置信度的破坏性命令，不影响正常装机脚本。
var bannedPatterns = []*regexp.Regexp{
	regexp.MustCompile(`rm\s+-[rf]{1,2}\s+(?:/\*?|\*)([\s\]}]|$)`), // rm -rf / 、rm -rf /* 、rm -rf *（RE2 无先行断言，用结束类匹配，放行 /var/... 子路径）
	regexp.MustCompile(`:\s*\|\s*:\s*&`),                           // fork bomb 核心特征 :|:&
	regexp.MustCompile(`mkfs\S*\s+/dev/(sd|nvme|vd)[a-z]*\s*$`),    // 直接对整盘 mkfs
	regexp.MustCompile(`dd\s+if=\S+\s+of=/dev/(sd|nvme|vd)[a-z]*`), // dd 覆盖整盘
	regexp.MustCompile(`>\s*/dev/(sd|nvme|vd)[a-z]*`),              // 重定向覆盖整盘
}

// SanitizeTemplateContent 检查模板正文是否包含被禁止的危险 shell 片段，用于缓解注入风险。
func SanitizeTemplateContent(content string) error {
	lower := strings.ToLower(content)
	for _, re := range bannedPatterns {
		if re.MatchString(lower) {
			return fmt.Errorf("模板内容包含被禁止的危险命令 (%s)", re.String())
		}
	}
	return nil
}

// ============================================================
// 模板执行辅助
// ============================================================

// templateFuncs 返回受限于安全函数的 text/template FuncMap。
// 仅提供 lower/upper/base64/sha512crypt，绝不暴露 exec/env/system 等执行能力。
func templateFuncs() map[string]any {
	return map[string]any{
		"lower": strings.ToLower,
		"upper": strings.ToUpper,
		// sha512crypt 在语义上代表“生成密码哈希”；此处复用 bcrypt（见 HashPassword 注释）。
		"sha512crypt": func(s string) (string, error) { return HashPassword(s) },
		"base64":      func(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) },
		// 缩进辅助：把多行文本整体缩进给定空格数，用于嵌入 YAML 代码块。
		"indent": func(s string) string { return indentBlock(s, 6) },
		// default：当值为空时回退。
		"default": func(def, val string) string {
			if strings.TrimSpace(val) == "" {
				return def
			}
			return val
		},
	}
}

// executeTemplate 使用安全 FuncMap 执行模板。
// funcs 为函数映射提供者，允许各格式注入不同数据。
func executeTemplate(name, content string, data any, funcs func() map[string]any) (string, error) {
	tmpl, err := template.New(name).Funcs(funcs()).Parse(content)
	if err != nil {
		return "", fmt.Errorf("解析模板失败: %w", err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("渲染模板失败: %w", err)
	}
	return buf.String(), nil
}

// indentBlock 将多行文本每行整体缩进 spaces 个空格。
func indentBlock(s string, spaces int) string {
	pad := strings.Repeat(" ", spaces)
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i, line := range lines {
		if line != "" {
			lines[i] = pad + line
		}
	}
	return strings.Join(lines, "\n")
}

// fallback 在 primary 为空（去空格后）时返回替代值。
func fallback(primary, alt string) string {
	if strings.TrimSpace(primary) == "" {
		return alt
	}
	return strings.TrimSpace(primary)
}

// fallbackCIDR 把子网掩码规范化为 CIDR 前缀（支持点分十进制或 /24 形式）。
func fallbackCIDR(netmask string) string {
	n := strings.TrimSpace(netmask)
	if n == "" {
		return "/24"
	}
	if strings.HasPrefix(n, "/") {
		return n
	}
	// 点分十进制转前缀长度。
	var total, count int
	for _, part := range strings.Split(n, ".") {
		if len(strings.TrimSpace(part)) == 0 {
			continue
		}
		var v int
		if _, err := fmt.Sscanf(part, "%d", &v); err != nil {
			return "/24"
		}
		total = total<<8 | v
		count++
	}
	if count != 4 {
		return "/24"
	}
	prefix := 0
	for total&0x80000000 != 0 && prefix < 32 {
		prefix++
		total <<= 1
	}
	if prefix == 0 {
		return "/24"
	}
	return fmt.Sprintf("/%d", prefix)
}

// splitList 按空格或逗号切分列表并去除空项。
func splitList(s string) []string {
	fields := strings.FieldsFunc(s, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\t' || r == '\n'
	})
	var out []string
	for _, f := range fields {
		if f = strings.TrimSpace(f); f != "" {
			out = append(out, f)
		}
	}
	return out
}
