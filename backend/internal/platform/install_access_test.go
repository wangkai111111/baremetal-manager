package platform

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"baremetal-manager/backend/internal/model"
	"baremetal-manager/backend/internal/service"
	"github.com/GehirnInc/crypt/sha512_crypt"
	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/ssh"
	"gopkg.in/yaml.v3"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func installTestPublicKey(t *testing.T) string {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	key, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key)))
}

func TestInstallAccessValidation(t *testing.T) {
	key := installTestPublicKey(t)
	for _, tc := range []struct {
		name  string
		in    installAccessInput
		valid bool
	}{
		{"console-password", installAccessInput{RootPassword: "test-password"}, true},
		{"key-only", installAccessInput{SSHAuthorizedKeys: []string{key}, RootSSHLogin: "prohibit-password"}, true},
		{"remote-password", installAccessInput{RootPassword: "test-password", RootSSHLogin: "yes"}, true},
		{"hostname", installAccessInput{Hostname: "node-01", RootPassword: "test-password"}, true},
		{"empty", installAccessInput{}, true},
		{"short", installAccessInput{RootPassword: "short"}, false},
		{"long", installAccessInput{RootPassword: strings.Repeat("a", 129)}, false},
		{"unicode-bytes", installAccessInput{RootPassword: strings.Repeat("密", 43)}, false},
		{"nul", installAccessInput{RootPassword: "password\x00"}, false},
		{"newline", installAccessInput{RootPassword: "password\n"}, false},
		{"spaces", installAccessInput{RootPassword: "        "}, false},
		{"bad-hostname", installAccessInput{Hostname: "node;id", RootPassword: "password"}, false},
		{"fake-key", installAccessInput{SSHAuthorizedKeys: []string{"ssh-ed25519 AAAA"}}, false},
		{"private-key", installAccessInput{SSHAuthorizedKeys: []string{"-----BEGIN OPENSSH PRIVATE KEY-----"}}, false},
		{"key-options", installAccessInput{SSHAuthorizedKeys: []string{"command=\"id\" " + key}}, false},
		{"multiple-on-line", installAccessInput{SSHAuthorizedKeys: []string{key + "\n" + key}}, false},
		{"missing-key", installAccessInput{RootPassword: "password", RootSSHLogin: "prohibit-password"}, false},
		{"missing-password", installAccessInput{SSHAuthorizedKeys: []string{key}, RootSSHLogin: "yes"}, true},
		{"unknown-policy", installAccessInput{RootPassword: "password", RootSSHLogin: "all"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.in.validate(); (err == nil) != tc.valid {
				t.Fatalf("valid=%v err=%v", tc.valid, err)
			}
		})
	}
	in := installAccessInput{SSHAuthorizedKeys: []string{key + " comment $(id)", key + " duplicate"}}
	if err := in.validate(); err != nil || len(in.SSHAuthorizedKeys) != 1 || in.SSHAuthorizedKeys[0] != key {
		t.Fatal("keys must be canonicalized and deduplicated")
	}
}

func TestInstallAccessSnapshotAndAutoinstall(t *testing.T) {
	t.Setenv("BMC_ENCRYPTION_KEY", "01234567890123456789012345678901")
	key := installTestPublicKey(t)
	for _, policy := range []string{"no", "prohibit-password", "yes"} {
		t.Run(policy, func(t *testing.T) {
			in := installAccessInput{RootPassword: "password-for-install", SSHAuthorizedKeys: []string{key}, RootSSHLogin: policy}
			snap, err := in.snapshot()
			if err != nil {
				t.Fatal(err)
			}
			encoded, _ := json.Marshal(snap)
			if strings.Contains(string(encoded), in.RootPassword) || strings.Contains(string(encoded), "$6$") {
				t.Fatal("snapshot leaked password/hash")
			}
			hash, err := service.DecryptCredential(snap.RootHashEncrypted)
			if err != nil || !strings.HasPrefix(hash, "$6$") || sha512_crypt.New().Verify(hash, []byte(in.RootPassword)) != nil {
				t.Fatal("Linux shadow hash cannot verify password")
			}
			again, _ := in.snapshot()
			second, _ := service.DecryptCredential(again.RootHashEncrypted)
			if hash == second {
				t.Fatal("password salts must be random")
			}
			rendered, err := service.RenderInstallConfig(model.KickstartTemplate{Format: "autoinstall", TemplateContent: testVMAutoinstall}, service.InstallVars{Hostname: "node-01", PasswordHash: "!"})
			if err != nil {
				t.Fatal(err)
			}
			var doc map[string]any
			if err := yaml.Unmarshal([]byte(rendered), &doc); err != nil {
				t.Fatal(err)
			}
			ai := doc["autoinstall"].(map[string]any)
			if err := applyInstallAccess(ai, snap); err != nil {
				t.Fatal(err)
			}
			output, err := yaml.Marshal(doc)
			if err != nil {
				t.Fatal(err)
			}
			var roundtrip map[string]any
			if err := yaml.Unmarshal(output, &roundtrip); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(output), in.RootPassword) {
				t.Fatal("plaintext in seed")
			}
			if ai["identity"].(map[string]any)["password"] != "!" {
				t.Fatal("root password reused for admin")
			}
			sshd := ai["ssh"].(map[string]any)
			if sshd["allow-pw"] != (policy == "yes") || len(sshd["authorized-keys"].([]string)) != 1 {
				t.Fatal("SSH settings ignored")
			}
			ud := ai["user-data"].(map[string]any)
			if ud["disable_root"] != (policy == "no") || ud["ssh_pwauth"] != (policy == "yes") {
				t.Fatal("cloud-init policy mismatch")
			}
			commands := ai["late-commands"].([]any)
			root := commands[0].([]any)
			if root[6] != hash || root[7] != "root" {
				t.Fatal("root hash not installed")
			}
			encodedSSHD := strings.Split(commands[4].([]any)[2].(string), "'")[1]
			config, _ := base64.StdEncoding.DecodeString(encodedSSHD)
			if !strings.Contains(string(config), "PermitRootLogin "+policy+"\n") || !strings.Contains(string(config), "KbdInteractiveAuthentication no") {
				t.Fatal("root SSH policy not written")
			}
			if len(commands) != 7 {
				t.Fatal("key-only admin must have passwordless sudo")
			}
			// Execute only the generated file-writing commands in a disposable
			// target directory; never invoke usermod or touch the host's accounts.
			target := t.TempDir()
			for _, command := range commands[1:] {
				var args []string
				for _, part := range command.([]any) {
					args = append(args, strings.ReplaceAll(part.(string), "/target", target))
				}
				if err := exec.Command(args[0], args[1:]...).Run(); err != nil {
					t.Fatalf("generated file command failed: %v", err)
				}
			}
			keysPath := filepath.Join(target, "root/.ssh/authorized_keys")
			keysContent, _ := os.ReadFile(keysPath)
			keysMode, _ := os.Stat(keysPath)
			if string(keysContent) != key+"\n" || keysMode.Mode().Perm() != 0600 {
				t.Fatal("authorized_keys content or permissions invalid")
			}
			if sshdPath, err := exec.LookPath("sshd"); err == nil {
				out, err := exec.Command(sshdPath, "-T", "-f", filepath.Join(target, "etc/ssh/sshd_config.d/00-bmm-access.conf")).Output()
				if err != nil {
					t.Fatalf("OpenSSH rejected generated configuration: %v", err)
				}
				if !strings.Contains(string(out), "permitrootlogin "+policy+"\n") && !(policy == "prohibit-password" && strings.Contains(string(out), "permitrootlogin without-password\n")) {
					t.Fatal("OpenSSH effective root login policy mismatch")
				}
			}
		})
	}
	noPassword, err := (&installAccessInput{SSHAuthorizedKeys: []string{key}, RootSSHLogin: "prohibit-password"}).snapshot()
	if err != nil {
		t.Fatal(err)
	}
	ai := map[string]any{"identity": map[string]any{"username": "admin"}}
	if err := applyInstallAccess(ai, noPassword); err != nil {
		t.Fatal(err)
	}
	password, err := service.DecryptCredential(noPassword.RootPasswordEncrypted)
	hash, _ := service.DecryptCredential(noPassword.RootHashEncrypted)
	if err != nil || !noPassword.PasswordGenerated || len(password) < 32 || sha512_crypt.New().Verify(hash, []byte(password)) != nil {
		t.Fatal("blank password must generate a recoverable encrypted random password and matching hash")
	}
	generated, err := (*installAccessInput)(nil).snapshot()
	if err != nil || generated.RootSSHLogin != "yes" || !generated.PasswordGenerated || generated.RootPasswordEncrypted == noPassword.RootPasswordEncrypted {
		t.Fatal("default request must generate unique credentials and enable root SSH")
	}
	noPassword.RootHashEncrypted = "broken"
	if err := applyInstallAccess(ai, noPassword); err == nil {
		t.Fatal("corrupt credential accepted")
	}
}

func TestNoCloudRejectsMACOnly(t *testing.T) {
	t.Setenv("PXE_TFTP_ROOT", t.TempDir())
	// No token must be rejected before any database or file access.
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/nocloud/525400770010/user-data", nil)
	c.Params = gin.Params{{Key: "mac", Value: "525400770010"}, {Key: "file", Value: "user-data"}}
	(&App{}).serveAutoinstall(c)
	if w.Code != 404 {
		t.Fatalf("MAC-only seed request status=%d", w.Code)
	}
}

func TestInstallAccessPersistsAndSeedExpires(t *testing.T) {
	dsn := os.Getenv("PLATFORM_TEST_DSN")
	if dsn == "" {
		t.Skip("requires disposable database with PXE lab fixtures")
	}
	t.Setenv("BMC_ENCRYPTION_KEY", "01234567890123456789012345678901")
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	conn, _ := db.DB()
	defer conn.Close()
	tx := db.Begin()
	defer tx.Rollback()
	seedIntegrationAdmin(t, tx)
	var fixture struct{ TenantID, ProjectID, AssetID, PlanID, NetworkID int64 }
	if err := tx.Raw(`SELECT a.tenant_id,a.project_id,a.id AS asset_id,p.id AS plan_id,p.network_id FROM assets a JOIN install_plans p ON p.tenant_id=a.tenant_id AND p.project_id=a.project_id AND p.name=? AND p.deleted_at IS NULL WHERE a.manufacturer='BMM Test VM' AND a.model='QEMU PXE Lab' AND a.deleted_at IS NULL`, testVMInstallName).Scan(&fixture).Error; err != nil {
		t.Fatal(err)
	}
	if fixture.PlanID == 0 {
		t.Skip("PXE lab fixtures not present")
	}
	var uid int64
	tx.Table("users").Select("id").Where("username='integration-admin'").Scan(&uid)
	sc := scope{Tenant: fixture.TenantID, Project: fixture.ProjectID, User: uid, Admin: true}
	app := &App{store: &store{db: tx}}
	root := t.TempDir()
	t.Setenv("PXE_TFTP_ROOT", root)
	key := installTestPublicKey(t)
	selectedMedia := makeInstallationMediaFixture(t, root, true)
	var selectedID int64
	if err := tx.Raw(`INSERT INTO images(tenant_id,project_id,name,object_key,size_bytes,sha256,os_type,version,architecture,status,create_by)
 VALUES (?,?,?,?,?,?,'ubuntu','24.04.1','x86_64','ready',?) RETURNING id`, sc.Tenant, sc.Project, "Selected Ubuntu 24.04", selectedMedia.ObjectKey, selectedMedia.SizeBytes, selectedMedia.SHA256, sc.User).Scan(&selectedID).Error; err != nil {
		t.Fatal(err)
	}
	input := &installAccessInput{Hostname: "pxe-access-test", RootPassword: "test-only-password", SSHAuthorizedKeys: []string{key}, RootSSHLogin: "yes"}
	inst, opID, err := app.enqueueInstallation(context.Background(), sc, fixture.AssetID, fixture.PlanID, fixture.NetworkID, root, input, auditMeta{}, selectedID)
	if err != nil {
		t.Fatal(err)
	}
	var row struct {
		ConfigSnapshot    string
		CallbackTokenHash string
	}
	if err := tx.Table("installations").Where("id=?", inst).Take(&row).Error; err != nil {
		t.Fatal(err)
	}
	if strings.Contains(row.ConfigSnapshot, input.RootPassword) || strings.Contains(row.ConfigSnapshot, "$6$") {
		t.Fatal("database contains plaintext password or hash")
	}
	var snap installationSnapshot
	if err := json.Unmarshal([]byte(row.ConfigSnapshot), &snap); err != nil {
		t.Fatal(err)
	}
	if snap.Hostname != input.Hostname || snap.Access == nil || snap.Access.RootSSHLogin != "yes" || snap.Media == nil || snap.Media.ID != selectedID || snap.Media.Version != "24.04.1" {
		t.Fatal("access snapshot not saved")
	}
	// The credential endpoint is scoped, audited and never cacheable.
	getAccess := func(testScope scope, guarded bool) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		router := gin.New()
		router.Use(func(c *gin.Context) { c.Set("scope", testScope) })
		if guarded {
			router.GET("/assets/:id/initial-access", app.require("assets:operate"), app.initialAccess)
		} else {
			router.GET("/assets/:id/initial-access", app.initialAccess)
		}
		req := httptest.NewRequest("GET", fmt.Sprintf("/assets/%d/initial-access", fixture.AssetID), nil)
		req.RemoteAddr = "192.0.2.45:1234"
		router.ServeHTTP(w, req)
		return w
	}
	w := getAccess(sc, true)
	if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("authorized initial access failed")
	}
	var accessResponse struct {
		Data struct{ Password, Username, SSHCommand, Note string }
	}
	// JSON uses snake case for the SSH command; check account and password here.
	if json.Unmarshal(w.Body.Bytes(), &accessResponse) != nil || accessResponse.Data.Password != input.RootPassword || accessResponse.Data.Username != "root" {
		t.Fatal("initial credentials do not match installation")
	}
	wrongScope := sc
	wrongScope.Project++
	if getAccess(wrongScope, true).Code != 404 {
		t.Fatal("cross-project credentials exposed")
	}
	unauthorized := sc
	unauthorized.Admin = false
	if getAccess(unauthorized, true).Code != 403 || getAccess(unauthorized, false).Code != 404 {
		t.Fatal("unassigned user accessed credentials")
	}
	var auditDoc string
	if err := tx.Raw("SELECT to_jsonb(a)::text FROM audits a WHERE action='initial-access:read' AND tenant_id=? AND project_id=? ORDER BY id DESC LIMIT 1", sc.Tenant, sc.Project).Scan(&auditDoc).Error; err != nil || auditDoc == "" || strings.Contains(auditDoc, input.RootPassword) {
		t.Fatal("credential access audit missing or contains secret")
	}
	token, _ := service.DecryptCredential(snap.TokenEncrypted)
	if service.CallbackTokenHash(token) != row.CallbackTokenHash {
		t.Fatal("callback hash mismatch")
	}
	mac, _ := provisioningNormalizeMAC(snap.MAC)
	seedDir := filepath.Join(root, "nocloud", mac)
	for _, name := range []string{"netboot/vmlinuz", "netboot/initrd", "iso/" + DefaultISOFileName} {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("isolated preparation fixture"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	// An operation that is still pending must fence the external VM reboot.
	// Exercise all real preparation steps, with temporary media and seed paths.
	if err := app.prepareTestInstallation(context.Background(), operation{ID: opID, TenantID: sc.Tenant, ProjectID: sc.Project}); err == nil {
		t.Fatal("pending operation did not fence reboot")
	}
	ciphertext, _ := os.ReadFile(filepath.Join(seedDir, "user-data.enc"))
	if strings.Contains(string(ciphertext), "#cloud-config") || !strings.HasPrefix(string(ciphertext), "v1:") {
		t.Fatal("shared seed file is not encrypted")
	}
	seed, err := service.DecryptCredential(string(ciphertext))
	if err != nil || !strings.HasPrefix(seed, "#cloud-config\n") || strings.Contains(seed, input.RootPassword) {
		t.Fatal("prepared seed not usable or leaked password")
	}
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(seed), &doc); err != nil {
		t.Fatal(err)
	}
	ai := doc["autoinstall"].(map[string]any)
	if ai["identity"].(map[string]any)["hostname"] != input.Hostname || ai["ssh"].(map[string]any)["allow-pw"] != true {
		t.Fatal("selected access settings not prepared")
	}
	script, err := os.ReadFile(filepath.Join(root, "ipxe", mac+".ipxe"))
	if err != nil || !strings.Contains(string(script), "/nocloud-token/"+token+"/"+mac+"/") {
		t.Fatal("iPXE does not request token-protected seed")
	}
	if _, err := os.Stat(filepath.Join(seedDir, "user-data")); !os.IsNotExist(err) {
		t.Fatal("unencrypted seed file exists")
	}
	getSeed := func(token, mac string) int {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("GET", "/", nil)
		c.Params = gin.Params{{Key: "mac", Value: mac}, {Key: "file", Value: "user-data"}, {Key: "seedToken", Value: token}}
		app.serveAutoinstall(c)
		if w.Code == 200 && w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("seed can be cached")
		}
		if w.Code == 200 && w.Body.String() != seed {
			t.Fatal("seed not decrypted for installer")
		}
		return w.Code
	}
	if err := tx.Table("operations").Where("id=?", opID).Update("status", "running").Error; err != nil {
		t.Fatal(err)
	}
	if err := tx.Table("installations").Where("id=?", inst).Update("status", "booting").Error; err != nil {
		t.Fatal(err)
	}
	getMedia := func(testToken, kind, name string) int {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("GET", "/", nil)
		c.Params = gin.Params{{Key: "seedToken", Value: testToken}, {Key: "kind", Value: kind}, {Key: "file", Value: name}}
		app.serveInstallationMedia(c)
		if w.Code == 200 && name == "vmlinuz" && w.Body.String() != "selected-image-matching-kernel" {
			t.Fatal("token served a different image kernel")
		}
		return w.Code
	}
	if getMedia(token, "netboot", "vmlinuz") != 200 || getMedia(token, "iso", DefaultISOFileName) != 404 || getMedia(strings.Repeat("x", 32), "netboot", "vmlinuz") != 404 {
		t.Fatal("selected media token binding failed")
	}
	if getSeed(token, mac) != 200 {
		t.Fatal("valid seed token rejected")
	}
	if getSeed(strings.Repeat("x", 32), mac) != 404 || getSeed(token, "525400770099") != 404 {
		t.Fatal("invalid token or MAC accepted")
	}
	if err := tx.Table("installations").Where("id=?", inst).Update("callback_expires_at", time.Now().Add(-time.Hour)).Error; err != nil {
		t.Fatal(err)
	}
	if getSeed(token, mac) != 404 {
		t.Fatal("expired seed token accepted")
	}
	tx.Table("installations").Where("id=?", inst).Updates(map[string]any{"callback_expires_at": time.Now().Add(time.Hour), "callback_revoked_at": time.Now()})
	if getSeed(token, mac) != 404 || getMedia(token, "netboot", "vmlinuz") != 404 {
		t.Fatal("revoked seed or media token accepted")
	}
	tx.Table("installations").Where("id=?", inst).Updates(map[string]any{"callback_revoked_at": nil, "status": "succeeded"})
	if getSeed(token, mac) != 404 {
		t.Fatal("completed installation seed accessible")
	}
}
