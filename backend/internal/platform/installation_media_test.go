package platform

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func makeInstallationMediaFixture(t *testing.T, root string, hasCasper bool) *installationMedia {
	t.Helper()
	if _, err := exec.LookPath("xorriso"); err != nil {
		t.Skip("xorriso required for real ISO extraction test")
	}
	source := t.TempDir()
	if hasCasper {
		os.MkdirAll(filepath.Join(source, "casper"), 0755)
		os.MkdirAll(filepath.Join(source, ".disk"), 0755)
		os.WriteFile(filepath.Join(source, ".disk/info"), []byte("Ubuntu-Server 24.04.1 LTS Test amd64 (fixture)"), 0644)
		os.WriteFile(filepath.Join(source, "casper/vmlinuz"), []byte("selected-image-matching-kernel"), 0644)
		os.WriteFile(filepath.Join(source, "casper/initrd"), []byte("selected-image-matching-initrd"), 0644)
	} else {
		os.WriteFile(filepath.Join(source, "README"), []byte("not a live server installer"), 0644)
	}
	os.MkdirAll(filepath.Join(root, "iso"), 0755)
	path := filepath.Join(root, "iso", "fixture.iso")
	if err := exec.Command("xorriso", "-as", "mkisofs", "-quiet", "-o", path, source).Run(); err != nil {
		t.Fatal("cannot create isolated ISO")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(data)
	return &installationMedia{ID: 123, Name: "fixture Ubuntu", ObjectKey: "file://" + path, SHA256: hex.EncodeToString(hash[:]), SizeBytes: int64(len(data)), OSType: "ubuntu", Version: "24.04.1", Architecture: "x86_64", Status: "ready"}
}
func TestSelectedInstallationMedia(t *testing.T) {
	root := t.TempDir()
	m := makeInstallationMediaFixture(t, root, true)
	if err := prepareInstallationMedia(context.Background(), root, m); err != nil {
		t.Fatal(err)
	}
	snap := installationSnapshot{Media: m}
	for _, file := range []string{"vmlinuz", "initrd"} {
		path, err := installationMediaPath(root, snap, "netboot", file)
		if err != nil {
			t.Fatal(err)
		}
		data, _ := os.ReadFile(path)
		if string(data) != "selected-image-matching-"+map[string]string{"vmlinuz": "kernel", "initrd": "initrd"}[file] {
			t.Fatal("media from another ISO")
		}
	}
	if err := prepareInstallationMedia(context.Background(), root, m); err != nil {
		t.Fatal("cache cannot be reused")
	}
	for _, pair := range [][2]string{{"iso", DefaultISOFileName}, {"iso", "../image.iso"}, {"netboot", "ready.json"}, {"other", "vmlinuz"}} {
		if _, err := installationMediaPath(root, snap, pair[0], pair[1]); err == nil {
			t.Fatal("unauthorized media path accepted")
		}
	}
	broken := *m
	broken.SHA256 = strings.Repeat("a", 64)
	if err := prepareInstallationMedia(context.Background(), root, &broken); err == nil {
		t.Fatal("checksum mismatch accepted")
	}
	dir, _ := mediaDirectory(root, &broken)
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("unverified media published")
	}
	broken = *m
	broken.ObjectKey = "file:///etc/passwd"
	broken.SHA256 = strings.Repeat("b", 64)
	if err := prepareInstallationMedia(context.Background(), root, &broken); err == nil {
		t.Fatal("local file outside ISO directory accepted")
	}
	badroot := t.TempDir()
	bad := makeInstallationMediaFixture(t, badroot, false)
	if err := prepareInstallationMedia(context.Background(), badroot, bad); err == nil {
		t.Fatal("non-Live ISO accepted")
	}
}
func TestInstallationMediaCompatibilityAndPaths(t *testing.T) {
	m := installationMedia{Status: "ready", OSType: "ubuntu", Version: "22.04.5", Architecture: "amd64", SHA256: strings.Repeat("a", 64), SizeBytes: 1, ObjectKey: "object"}
	if m.unavailableReason() != "" {
		t.Fatal("supported Ubuntu rejected")
	}
	for _, field := range []string{"version", "arch", "os", "status", "sha"} {
		bad := m
		switch field {
		case "version":
			bad.Version = "20.04"
		case "arch":
			bad.Architecture = "aarch64"
		case "os":
			bad.OSType = "debian"
		case "status":
			bad.Status = "pending"
		case "sha":
			bad.SHA256 = "../outside"
		}
		if bad.unavailableReason() == "" {
			t.Fatal("incompatible media accepted: " + field)
		}
	}
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "ipxe/install-media"), 0755)
	os.Symlink(t.TempDir(), filepath.Join(root, "ipxe/install-media", m.SHA256))
	if _, err := mediaDirectory(root, &m); err == nil {
		t.Fatal("cache symlink escaped provisioning root")
	}
}

// Opt-in smoke test with the site's real ISO, confined to a temporary cache.
func TestRealUbuntuMediaPreparation(t *testing.T) {
	path := os.Getenv("BMM_REAL_ISO_TEST_FILE")
	if path == "" {
		t.Skip("set BMM_REAL_ISO_TEST_FILE for real ISO extraction")
	}
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "iso"), 0755)
	linked := filepath.Join(root, "iso", "real.iso")
	if err := os.Link(path, linked); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(linked)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	hash := sha256.New()
	size, err := io.Copy(hash, f)
	if err != nil {
		t.Fatal(err)
	}
	media := &installationMedia{ObjectKey: "file://" + linked, SHA256: hex.EncodeToString(hash.Sum(nil)), SizeBytes: size, OSType: "ubuntu", Version: "22.04.5", Architecture: "amd64", Status: "ready"}
	if err := prepareInstallationMedia(context.Background(), root, media); err != nil {
		t.Fatal(err)
	}
	dir, _ := mediaDirectory(root, media)
	for _, file := range []string{"vmlinuz", "initrd"} {
		st, err := os.Stat(filepath.Join(dir, file))
		if err != nil || st.Size() < 1<<20 {
			t.Fatal("real ISO kernel or initrd missing")
		}
	}
}
