package service

import (
	"strings"
	"testing"

	"baremetal-manager/backend/internal/model"

	"golang.org/x/crypto/bcrypt"
	"gopkg.in/yaml.v3"
)

// baseVars 提供渲染测试用的通用变量集合。
func baseVars() InstallVars {
	return InstallVars{
		Hostname:     "node-01",
		MAC:          "52:54:00:1f:2a:3b",
		IP:           "10.0.0.25",
		Netmask:      "255.255.255.0",
		Gateway:      "10.0.0.1",
		DNS:          "10.0.0.2 8.8.8.8",
		RootPassword: "S3cr3t!pass",
		SSHKey:       "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAA admin@bastion",
		HTTPBase:     "http://10.0.0.5:8080",
	}
}

// TestRenderAutoinstallValidYAML 校验 autoinstall 渲染结果为合法 YAML，
// 且包含 autoinstall/identity/ssh 及 version、静态网络、SSH 注入等关键结构。
func TestRenderAutoinstallValidYAML(t *testing.T) {
	cases := []struct {
		name        string
		tpl         model.KickstartTemplate
		vars        InstallVars
		mustContain []string
	}{
		{
			name: "静态网络-默认分区",
			tpl:  model.KickstartTemplate{Format: "autoinstall"},
			vars: baseVars(),
			mustContain: []string{
				"#cloud-config",
				"autoinstall:",
				"version: 1",
				"identity:",
				"ssh:",
				"authorized_keys:",
				"username: admin",
				"10.0.0.25/24",
				"dhcp4", // 不应出现——通过下方否定断言处理
			},
		},
		{
			name: "DHCP-无网关",
			tpl:  model.KickstartTemplate{Format: "autoinstall"},
			vars: InstallVars{Hostname: "dhcp-host", RootPassword: "letmein", SSHKey: "ssh-rsa AAAAB3 test"},
			mustContain: []string{
				"autoinstall:",
				"version: 1",
				"identity:",
				"ssh:",
				"dhcp4: true",
			},
		},
		{
			name: "含交换与home分区及post脚本",
			tpl: model.KickstartTemplate{
				Format:          "autoinstall",
				PartitionScheme: "swap + /home ext4",
				PostScript:      "echo provisioning-done",
			},
			vars: baseVars(),
			mustContain: []string{
				"autoinstall:",
				"storage:",
				"fstype: swap",
				"path: /home",
				"late-commands:",
				"provisioning-done",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := RenderInstallConfig(tc.tpl, tc.vars)
			if err != nil {
				t.Fatalf("渲染失败: %v", err)
			}
			// 必须是合法 YAML。
			var doc map[string]any
			if err := yaml.Unmarshal([]byte(out), &doc); err != nil {
				t.Fatalf("生成的不是合法 YAML: %v\n----\n%s", err, out)
			}
			// autoinstall 顶层结构存在。
			ai, ok := doc["autoinstall"].(map[string]any)
			if !ok {
				t.Fatalf("缺少 autoinstall 顶层键:\n%s", out)
			}
			if v, _ := ai["version"].(int); v != 1 {
				t.Fatalf("autoinstall.version 期望为 1, 实际: %#v", ai["version"])
			}
			for _, key := range []string{"identity", "ssh", "network", "storage"} {
				if _, ok := ai[key]; !ok {
					t.Errorf("autoinstall 缺少 %q 段:\n%s", key, out)
				}
			}
			for _, want := range tc.mustContain {
				if want == "dhcp4" {
					continue // 该 token 仅作占位，另在下方否定判断
				}
				if !strings.Contains(out, want) {
					t.Errorf("输出应包含 %q:\n%s", want, out)
				}
			}
			// 静态网络用例不应包含 dhcp4。
			if tc.name == "静态网络-默认分区" && strings.Contains(out, "dhcp4") {
				t.Errorf("静态网络不应出现 dhcp4:\n%s", out)
			}
			// 密码哈希应为 bcrypt $2b$，不应泄露明文。
			if strings.Contains(out, tc.vars.RootPassword) {
				t.Errorf("输出不应包含明文口令")
			}
			if !strings.Contains(out, "$2b$") {
				t.Errorf("identity.password 期望为 $2b$ bcrypt 哈希:\n%s", out)
			}
		})
	}
}

// TestRenderAutoinstallIdentityHashVerifiable 校验写入的哈希可用 bcrypt 反查原口令。
func TestRenderAutoinstallIdentityHashVerifiable(t *testing.T) {
	vars := InstallVars{Hostname: "n1", RootPassword: "correct-horse", SSHKey: "ssh-rsa AAAA x"}
	out, err := RenderInstallConfig(model.KickstartTemplate{Format: "autoinstall"}, vars)
	if err != nil {
		t.Fatalf("渲染失败: %v", err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("非法 YAML: %v", err)
	}
	ai := doc["autoinstall"].(map[string]any)
	idn := ai["identity"].(map[string]any)
	hash, _ := idn["password"].(string)
	if !strings.HasPrefix(hash, "$2b$") {
		t.Fatalf("identity.password 前缀期望 $2b$, 实际 %q", hash)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte("correct-horse")); err != nil {
		t.Fatalf("bcrypt 校验失败: %v", err)
	}
}

// TestSubstituteVars 表驱动校验 shell 风格 token 替换。
func TestSubstituteVars(t *testing.T) {
	vars := InstallVars{
		Hostname:     "web-01",
		MAC:          "aa:bb:cc:dd:ee:ff",
		IP:           "192.168.1.10",
		Netmask:      "255.255.255.0",
		Gateway:      "192.168.1.1",
		DNS:          "192.168.1.254",
		RootPassword: "p@ss",
		Extra:        map[string]string{"role": "database"},
	}
	cases := []struct {
		raw  string
		want string
	}{
		{"host=$HOSTNAME", "host=web-01"},
		{"ip=$IP mask=$SUBNET gw=$GATEWAY", "ip=192.168.1.10 mask=255.255.255.0 gw=192.168.1.1"},
		{"mac=$MAC dns=$DNS pw=$ROOT_PASSWORD", "mac=aa:bb:cc:dd:ee:ff dns=192.168.1.254 pw=p@ss"},
		{"role=$ROLE", "role=database"}, // Extra 覆盖
		{"no tokens here", "no tokens here"},
		{"$UNKNOWN stays", "$UNKNOWN stays"},
	}
	for _, tc := range cases {
		got := SubstituteVars(tc.raw, vars)
		if got != tc.want {
			t.Errorf("SubstituteVars(%q)=%q, 期望 %q", tc.raw, got, tc.want)
		}
	}
}

// TestHashPassword 校验返回 $2b$ bcrypt 哈希且可被校验。
func TestHashPassword(t *testing.T) {
	hash, err := HashPassword("hunter2")
	if err != nil {
		t.Fatalf("HashPassword 出错: %v", err)
	}
	if !strings.HasPrefix(hash, "$2b$") {
		t.Fatalf("期望 $2b$ 前缀, 实际 %q", hash)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte("hunter2")); err != nil {
		t.Fatalf("哈希不可校验: %v", err)
	}
	if _, err := HashPassword(""); err == nil {
		t.Fatal("空口令应返回错误")
	}
}

// TestSanitizeTemplateContent 校验危险片段被拒绝、正常脚本被放行。
func TestSanitizeTemplateContent(t *testing.T) {
	cases := []struct {
		content   string
		wantError bool
	}{
		{"rm -rf /", true},
		{"rm -rf /*", true},
		{":(){ :|:& };:", true},
		{"dd if=/dev/zero of=/dev/sda", true},
		{"echo hello > /dev/sda", true},
		{"#!/bin/bash\napt-get update\nsystemctl enable ssh", false},
		{"rm -rf /var/tmp/build-cache", false},
		{"echo 'normal script'", false},
	}
	for _, tc := range cases {
		err := SanitizeTemplateContent(tc.content)
		if tc.wantError && err == nil {
			t.Errorf("应拒绝危险内容: %q", tc.content)
		}
		if !tc.wantError && err != nil {
			t.Errorf("应放行正常内容: %q, 却报错: %v", tc.content, err)
		}
	}
}

// TestRenderInstallConfigDispatch 校验格式分派与非法格式拒绝。
func TestRenderInstallConfigDispatch(t *testing.T) {
	vars := baseVars()
	// 未知格式应报错。
	if _, err := RenderInstallConfig(model.KickstartTemplate{Format: "bogus"}, vars); err == nil {
		t.Error("未知格式应返回错误")
	}
	// kickstart 渲染包含关键段落。
	out, err := RenderInstallConfig(model.KickstartTemplate{Format: "kickstart", OSType: "rhel"}, vars)
	if err != nil {
		t.Fatalf("kickstart 渲染失败: %v", err)
	}
	for _, want := range []string{"rootpw", "%packages", "%post", "%pre", "node-01"} {
		if !strings.Contains(out, want) {
			t.Errorf("kickstart 输出应包含 %q:\n%s", want, out)
		}
	}
	// 自定义正文中的 token 应被替换，且经过安全过滤。
	custom := model.KickstartTemplate{
		Format:          "kickstart",
		TemplateContent: "hostname $HOSTNAME\nip $IP\n",
	}
	out, err = RenderInstallConfig(custom, vars)
	if err != nil {
		t.Fatalf("自定义 kickstart 渲染失败: %v", err)
	}
	if !strings.Contains(out, "hostname node-01") || !strings.Contains(out, "ip 10.0.0.25") {
		t.Errorf("自定义 token 未替换:\n%s", out)
	}
	// 恶意用户正文应被拒绝。
	malicious := model.KickstartTemplate{Format: "autoinstall", TemplateContent: "autoinstall:\n  version: 1\nruncmd: [rm -rf /]"}
	if _, err := RenderInstallConfig(malicious, vars); err == nil {
		t.Error("含危险命令的自定义正文应被拒绝")
	}
}

// TestIsValidInstallFormat 校验格式白名单。
func TestIsValidInstallFormat(t *testing.T) {
	for _, f := range []string{"kickstart", "autoinstall", "preseed", "unattend", "KickStart"} {
		if !IsValidInstallFormat(f) {
			t.Errorf("应视为合法格式: %q", f)
		}
	}
	for _, f := range []string{"", "exe", "cmd", "yaml"} {
		if IsValidInstallFormat(f) {
			t.Errorf("应视为非法格式: %q", f)
		}
	}
}
