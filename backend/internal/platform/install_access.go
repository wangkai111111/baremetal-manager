package platform

import (
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"unicode/utf8"

	"baremetal-manager/backend/internal/service"
	"github.com/GehirnInc/crypt/sha512_crypt"
	"golang.org/x/crypto/ssh"
)

// Install access settings belong to one installation, never to a reusable plan.
// Only an encrypted Linux shadow hash is persisted; plaintext never reaches SQL.
type installAccessInput struct {
	Hostname          string   `json:"hostname"`
	RootPassword      string   `json:"root_password"`
	SSHAuthorizedKeys []string `json:"ssh_authorized_keys"`
	RootSSHLogin      string   `json:"root_ssh_login"`
}

type installAccessSnapshot struct {
	RootHashEncrypted string   `json:"root_hash_encrypted,omitempty"`
	SSHAuthorizedKeys []string `json:"ssh_authorized_keys"`
	RootSSHLogin      string   `json:"root_ssh_login"`
}

var installHostnameRE = regexp.MustCompile(`^[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?$`)

// The directory is also exposed by TFTP, which has no token authentication.
// Keep even the Linux hash and callback token encrypted in the shared volume.
func writeEncryptedInstallSeed(path string, data []byte) error {
	ciphertext, err := service.EncryptCredential(string(data))
	if err != nil {
		return err
	}
	return os.WriteFile(path, []byte(ciphertext), 0600)
}

func (in *installAccessInput) validate() error {
	if in == nil {
		return nil // Compatibility with existing clients that use plan defaults.
	}
	if in.Hostname != "" && !installHostnameRE.MatchString(in.Hostname) {
		return errors.New("主机名须为 1–63 位字母、数字或中划线，且不能以中划线开头或结尾")
	}
	if in.RootPassword != "" && (!utf8.ValidString(in.RootPassword) || utf8.RuneCountInString(in.RootPassword) < 8 || len(in.RootPassword) > 128 || strings.ContainsAny(in.RootPassword, "\x00\r\n") || strings.TrimSpace(in.RootPassword) == "") {
		return errors.New("root 密码至少 8 个字符、最多 128 字节，不能含换行或空字符")
	}
	if len(in.SSHAuthorizedKeys) > 20 {
		return errors.New("最多添加 20 条 SSH 公钥")
	}
	keys := make([]string, 0, len(in.SSHAuthorizedKeys))
	seen := map[string]bool{}
	for _, line := range in.SSHAuthorizedKeys {
		if len(line) > 16384 || strings.ContainsAny(line, "\r\n\x00") {
			return errors.New("每条 SSH 公钥必须为单行 OpenSSH 公钥")
		}
		key, _, options, rest, err := ssh.ParseAuthorizedKey([]byte(strings.TrimSpace(line)))
		if err != nil || len(rest) != 0 || len(options) != 0 {
			return errors.New("SSH 公钥无效，请粘贴完整 OpenSSH 公钥，不支持私钥或 authorized_keys 选项")
		}
		if key.Type() != ssh.KeyAlgoED25519 && key.Type() != ssh.KeyAlgoRSA && key.Type() != ssh.KeyAlgoECDSA256 && key.Type() != ssh.KeyAlgoECDSA384 && key.Type() != ssh.KeyAlgoECDSA521 {
			return errors.New("SSH 公钥仅支持 Ed25519、RSA 或 ECDSA")
		}
		canonical := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key)))
		if !seen[canonical] {
			keys = append(keys, canonical)
			seen[canonical] = true
		}
	}
	in.SSHAuthorizedKeys = keys
	if in.RootPassword == "" && len(keys) == 0 {
		return errors.New("请至少设置 root 密码或添加一条 SSH 公钥")
	}
	if in.RootSSHLogin == "" {
		in.RootSSHLogin = "no"
	}
	switch in.RootSSHLogin {
	case "no":
	case "prohibit-password":
		if len(keys) == 0 {
			return errors.New("root 仅公钥登录需要至少一条 SSH 公钥")
		}
	case "yes":
		if in.RootPassword == "" {
			return errors.New("允许 root SSH 密码登录时必须设置 root 密码")
		}
	default:
		return errors.New("root SSH 登录策略无效")
	}
	return nil
}

func (in *installAccessInput) snapshot() (*installAccessSnapshot, error) {
	if in == nil {
		return nil, nil
	}
	if err := in.validate(); err != nil {
		return nil, err
	}
	snap := &installAccessSnapshot{SSHAuthorizedKeys: in.SSHAuthorizedKeys, RootSSHLogin: in.RootSSHLogin}
	if in.RootPassword != "" {
		// SHA512-crypt is supported by the target Ubuntu 22.04 libcrypt/PAM stack.
		hash, err := sha512_crypt.New().Generate([]byte(in.RootPassword), nil)
		if err != nil {
			return nil, errors.New("无法生成系统密码哈希")
		}
		snap.RootHashEncrypted, err = service.EncryptCredential(hash)
		if err != nil {
			return nil, errors.New("系统密码加密失败，请检查服务端凭据密钥")
		}
	}
	return snap, nil
}

// Apply after rendering so existing/custom templates cannot silently ignore the
// selected access policy. identity remains the separate, password-locked admin.
func applyInstallAccess(ai map[string]any, snap *installAccessSnapshot) error {
	if snap == nil {
		return nil
	}
	switch snap.RootSSHLogin {
	case "no":
	case "prohibit-password":
		if len(snap.SSHAuthorizedKeys) == 0 {
			return errors.New("安装快照缺少 root 登录公钥")
		}
	case "yes":
		if snap.RootHashEncrypted == "" {
			return errors.New("安装快照缺少 root 密码")
		}
	default:
		return errors.New("安装快照的 SSH 策略无效")
	}
	hash := "!"
	if snap.RootHashEncrypted != "" {
		var err error
		hash, err = service.DecryptCredential(snap.RootHashEncrypted)
		if err != nil || !strings.HasPrefix(hash, "$6$") {
			return errors.New("安装密码快照无法解密")
		}
	}
	allowPassword := snap.RootSSHLogin == "yes"
	ai["ssh"] = map[string]any{"install-server": true, "allow-pw": allowPassword, "authorized-keys": snap.SSHAuthorizedKeys}
	if identity, ok := ai["identity"].(map[string]any); ok {
		if identity["username"] != "admin" {
			return errors.New("系统访问设置目前要求 autoinstall 模板使用 admin 管理账户")
		}
		identity["password"] = "!"
	} else {
		return errors.New("autoinstall 模板缺少 identity 管理账户")
	}
	ud, _ := ai["user-data"].(map[string]any)
	if ud == nil {
		ud = map[string]any{}
	}
	ud["disable_root"] = snap.RootSSHLogin == "no"
	ud["ssh_pwauth"] = allowPassword
	ai["user-data"] = ud
	passwordAuth := "no"
	if allowPassword {
		passwordAuth = "yes"
	}
	sshd := fmt.Sprintf("# Managed by BMM for this installation\nPermitRootLogin %s\nPasswordAuthentication %s\nKbdInteractiveAuthentication no\nPubkeyAuthentication yes\nPermitEmptyPasswords no\n", snap.RootSSHLogin, passwordAuth)
	write := func(path, content, mode string) []any {
		// Encoded content cannot become shell syntax, including key comments.
		return []any{"sh", "-c", "printf %s '" + base64.StdEncoding.EncodeToString([]byte(content)) + "' | base64 -d > " + path + " && chmod " + mode + " " + path}
	}
	late, _ := ai["late-commands"].([]any)
	late = append(late,
		[]any{"curtin", "in-target", "--target=/target", "--", "usermod", "--password", hash, "root"},
		[]any{"install", "-d", "-m", "0700", "/target/root/.ssh"},
		write("/target/root/.ssh/authorized_keys", strings.Join(snap.SSHAuthorizedKeys, "\n")+"\n", "0600"),
		[]any{"install", "-d", "-m", "0755", "/target/etc/ssh/sshd_config.d"},
		write("/target/etc/ssh/sshd_config.d/00-bmm-access.conf", sshd, "0644"),
	)
	if len(snap.SSHAuthorizedKeys) > 0 {
		// With a locked admin password, key-authenticated admins need sudo that
		// does not prompt for a password that was never set.
		late = append(late, []any{"install", "-d", "-m", "0755", "/target/etc/sudoers.d"}, write("/target/etc/sudoers.d/90-bmm-admin", "admin ALL=(ALL) NOPASSWD:ALL\n", "0440"))
	}
	ai["late-commands"] = late
	return nil
}
