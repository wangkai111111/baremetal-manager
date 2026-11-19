package pxeservice

import (
	"context"
	"encoding/json"
	"net"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestHostInterfaceAddresses(t *testing.T) {
	addrs := []net.Addr{}
	for _, cidr := range []string{"192.0.2.2/24", "192.0.2.3/24", "192.0.2.2/24", "2001:db8::1/64", "127.0.0.1/8", "0.0.0.0/32"} {
		ip, prefix, err := net.ParseCIDR(cidr)
		if err != nil {
			t.Fatal(err)
		}
		prefix.IP = ip
		addrs = append(addrs, prefix)
	}
	row := interfaceOption(net.Interface{Name: "eno2", Flags: net.FlagUp}, addrs)
	if !row.Up || len(row.Addresses) != 2 || row.Addresses[0].Address != "192.0.2.2" || row.Addresses[0].Subnet != "192.0.2.0/24" {
		t.Fatalf("wrong interface options: %+v", row)
	}
}

func TestBootDiscoveryConfinedToUsableFiles(t *testing.T) {
	base := t.TempDir()
	write := func(path string) {
		t.Helper()
		if err := os.WriteFile(path, []byte("fixture"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(base, "undionly.kpxe"))
	write(filepath.Join(base, "default.ipxe"))
	outside := t.TempDir()
	write(filepath.Join(outside, "ipxe.efi"))
	if err := os.Symlink(filepath.Join(outside, "ipxe.efi"), filepath.Join(base, "ipxe.efi")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(base, "escape")); err != nil {
		t.Fatal(err)
	}
	seed := filepath.Join(base, "nocloud")
	os.Mkdir(seed, 0755)
	write(filepath.Join(seed, "user-data"))
	custom := filepath.Join(base, "custom")
	os.Mkdir(custom, 0755)
	write(filepath.Join(custom, "custom.efi"))
	roots := bootRoots(base, Config{Root: custom, EFI: "custom.efi"})
	if len(roots) != 2 || len(roots[0].BIOS) != 1 || len(roots[0].EFI) != 0 || !roots[0].ChainReady || len(roots[1].EFI) != 1 {
		t.Fatalf("unexpected discovered roots: %+v", roots)
	}
	encoded, _ := json.Marshal(roots)
	if strings.Contains(string(encoded), "user-data") || strings.Contains(string(encoded), "escape") {
		t.Fatal("discovery exposed unrelated or escaped paths")
	}
}

func TestBuiltinBootFilesBootstrapPreservesLocalFiles(t *testing.T) {
	root, source := t.TempDir(), t.TempDir()
	for _, name := range []string{"undionly.kpxe", "ipxe.efi", "snponly.efi"} {
		if err := os.WriteFile(filepath.Join(source, name), []byte("bundled "+name), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "undionly.kpxe"), []byte("local boot"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := ensureBuiltinBootFiles(root, source, "http://192.0.2.2:8080/api/v1/provision/"); err != nil {
		t.Fatal(err)
	}
	if err := ensureBuiltinBootFiles(root, source, "http://192.0.2.99/api/v1/provision"); err != nil {
		t.Fatal(err)
	}
	local, _ := os.ReadFile(filepath.Join(root, "undionly.kpxe"))
	chain, _ := os.ReadFile(filepath.Join(root, "default.ipxe"))
	if string(local) != "local boot" || !strings.Contains(string(chain), "http://192.0.2.2:8080/api/v1/provision/ipxe/${net0/mac:hexhyp}") || !regularFileWithin(root, "ipxe.efi") {
		t.Fatal("bootstrap overwrote local files or generated an invalid chain")
	}
	entries, _ := os.ReadDir(root)
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".builtin-") {
			t.Fatal("bootstrap leaked temporary files")
		}
	}
	for _, base := range []string{"http://user:secret@host/api", "file:///etc/passwd", "http://host/api\nreboot", "http://host/api?token=secret"} {
		if ensureBuiltinBootFiles(t.TempDir(), source, base) == nil {
			t.Fatal("unsafe chain API base accepted")
		}
	}
}

func TestHostOptionsReadDoesNotChangeServices(t *testing.T) {
	c := &controller{config: validConfig(), commands: map[string]*exec.Cmd{}, active: func(context.Context) (bool, error) {
		t.Fatal("read-only discovery must not invoke mutation guard")
		return true, nil
	}}
	w := httptest.NewRecorder()
	c.ServeHTTP(w, httptest.NewRequest("GET", "/options", nil))
	var options HostOptions
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &options) != nil || w.Header().Get("Cache-Control") != "no-store" || len(c.commands) != 0 {
		t.Fatal("host discovery failed or changed services")
	}
	for _, row := range options.Interfaces {
		iface, err := net.InterfaceByName(row.Name)
		if err != nil || iface.Flags&net.FlagLoopback != 0 {
			t.Fatal("inventory does not reflect actual non-loopback interfaces")
		}
	}
}

func TestSaveRejectsStaleHostInterface(t *testing.T) {
	cfg := validConfig()
	cfg.Interface = "bmm-does-not-exist"
	encoded, _ := json.Marshal(cfg)
	c := &controller{config: validConfig(), commands: map[string]*exec.Cmd{}, active: func(context.Context) (bool, error) { return false, nil }}
	w := httptest.NewRecorder()
	c.ServeHTTP(w, httptest.NewRequest("PUT", "/config", strings.NewReader(string(encoded))))
	if w.Code != 400 || c.config.Interface != "provision0" {
		t.Fatal("stale host selection was saved")
	}
}

func TestBootFilesRequireIPXEChain(t *testing.T) {
	cfg := validConfig()
	cfg.Root = t.TempDir()
	for _, name := range []string{cfg.BIOS, cfg.EFI} {
		if err := os.WriteFile(filepath.Join(cfg.Root, name), []byte("fixture"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if err := cfg.BootFiles(); err == nil {
		t.Fatal("missing default.ipxe chain accepted")
	}
	if err := os.WriteFile(filepath.Join(cfg.Root, "default.ipxe"), []byte("#!ipxe\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := cfg.BootFiles(); err != nil {
		t.Fatal(err)
	}
}
