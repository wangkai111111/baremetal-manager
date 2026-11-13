package pxeservice

import (
	"context"
	"errors"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"
)

func validConfig() Config {
	return Config{TenantID: 1, ProjectID: 2, NetworkID: 3, PoolID: 4, Interface: "provision0", Subnet: "192.0.2.0/24", Start: "192.0.2.100", End: "192.0.2.200", Server: "192.0.2.2", Root: "/var/lib/tftpboot", BIOS: "undionly.kpxe", EFI: "ipxe.efi"}
}
func TestValidateAndRender(t *testing.T) {
	c := validConfig()
	if e := c.Validate("dhcp", false); e != nil {
		t.Fatal(e)
	}
	dhcp, tftp := c.Render("dhcp"), c.Render("tftp")
	if strings.Contains(dhcp, "enable-tftp") || strings.Contains(tftp, "dhcp-range") || !strings.Contains(dhcp, "interface=provision0") || !strings.Contains(dhcp, "dhcp-range=192.0.2.100,192.0.2.200,255.255.255.0") {
		t.Fatal("services not isolated")
	}
	cases := []func(*Config){func(c *Config) { c.Interface = "eth0\ninterface=eth1" }, func(c *Config) { c.Root = "/etc" }, func(c *Config) { c.BIOS = "../secret" }, func(c *Config) { c.Start = "192.0.2.0" }, func(c *Config) { c.End = "192.0.2.255" }, func(c *Config) { c.Start = "192.0.2.1" }, func(c *Config) { c.End = "198.51.100.1" }, func(c *Config) { c.Interface = "" }}
	for i, change := range cases {
		c := validConfig()
		change(&c)
		if c.Validate("dhcp", false) == nil {
			t.Errorf("invalid case %d accepted", i)
		}
	}
}
func TestControllerFailsClosedAndWhitelist(t *testing.T) {
	for _, active := range []func(context.Context) (bool, error){func(context.Context) (bool, error) { return true, nil }, func(context.Context) (bool, error) { return false, errors.New("db unavailable") }} {
		c := &controller{config: validConfig(), commands: map[string]*exec.Cmd{}, active: active}
		w := httptest.NewRecorder()
		c.ServeHTTP(w, httptest.NewRequest("POST", "/dhcp/stop", nil))
		if w.Code != 409 && w.Code != 503 {
			t.Fatalf("unexpected status %d", w.Code)
		}
	}
	c := &controller{config: validConfig(), commands: map[string]*exec.Cmd{}, active: func(context.Context) (bool, error) { return false, nil }}
	w := httptest.NewRecorder()
	c.ServeHTTP(w, httptest.NewRequest("POST", "/shell/start", nil))
	if w.Code != 404 {
		t.Fatal(w.Code)
	}
	w = httptest.NewRecorder()
	c.ServeHTTP(w, httptest.NewRequest("GET", "/status", nil))
	if strings.Contains(w.Body.String(), `"dhcp_running":true`) || strings.Contains(w.Body.String(), `"tftp_running":true`) {
		t.Fatal("default service enabled")
	}
}
