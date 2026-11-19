package platform

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"net/netip"
	"os"
	"strings"
	"testing"

	"baremetal-manager/backend/internal/pxeservice"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestNetworkHostBoundaries(t *testing.T) {
	for _, tc := range []struct {
		prefix, ip string
		want       bool
	}{
		{"192.0.2.0/24", "192.0.2.0", false}, {"192.0.2.0/24", "192.0.2.255", false}, {"192.0.2.0/24", "192.0.2.254", true},
		{"192.0.2.0/31", "192.0.2.0", true}, {"192.0.2.0/31", "192.0.2.1", true}, {"192.0.2.1/32", "192.0.2.1", true},
		{"2001:db8::/64", "2001:db8::10", true}, {"192.0.2.0/24", "198.51.100.1", false},
	} {
		if got := usableAddress(netip.MustParsePrefix(tc.prefix), netip.MustParseAddr(tc.ip)); got != tc.want {
			t.Errorf("%s in %s: %t", tc.ip, tc.prefix, got)
		}
	}
}
func TestNetworkInputValidation(t *testing.T) {
	for _, tc := range []struct {
		table string
		body  map[string]any
	}{
		{"networks", map[string]any{"subnet": "192.0.2.4/24"}},
		{"networks", map[string]any{"gateway": "192.0.2.1/24"}},
		{"networks", map[string]any{"dns_servers": []any{"invalid"}}},
		{"networks", map[string]any{"dns_servers": "192.0.2.1"}},
		{"networks", map[string]any{"vlan_id": float64(4095)}},
		{"networks", map[string]any{"dhcp_enabled": "true"}},
		{"ip_pools", map[string]any{"start_address": "0.0.0.0"}},
	} {
		if err := normalizeNetworkInput(tc.table, tc.body); err == nil {
			t.Errorf("accepted invalid %s input", tc.table)
		}
	}
	if err := normalizeNetworkInput("networks", map[string]any{"subnet": "2001:db8::/64", "dns_servers": []any{}, "vlan_id": nil, "gateway": nil}); err != nil {
		t.Fatal(err)
	}
}

func TestNetworkOptionsIntegration(t *testing.T) {
	dsn := os.Getenv("PLATFORM_TEST_DSN")
	if dsn == "" {
		t.Skip("requires migrated disposable database")
	}
	t.Setenv("JWT_SECRET", "platform-test-secret-32-bytes-long-only")
	t.Setenv("BMC_ENCRYPTION_KEY", "12345678901234567890123456789012")
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	conn, _ := db.DB()
	defer conn.Close()
	tx := db.Begin()
	defer tx.Rollback()
	seedIntegrationAdmin(t, tx)
	app, err := New(tx)
	if err != nil {
		t.Fatal(err)
	}
	app.networkState = func(context.Context) (pxeservice.Status, error) { return pxeservice.Status{}, nil }
	token := ""
	request := func(method, path string, body any, want int) map[string]any {
		t.Helper()
		raw, _ := json.Marshal(body)
		req := httptest.NewRequest(method, path, bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		app.Router().ServeHTTP(w, req)
		if w.Code != want {
			t.Fatalf("%s %s: %d, want %d: %s", method, path, w.Code, want, w.Body.String())
		}
		var result map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if data, ok := result["data"].(map[string]any); ok {
			return data
		}
		return result
	}
	token = request("POST", "/api/v1/auth/login", map[string]any{"username": "integration-admin", "password": "test-only-password-2026"}, 200)["token"].(string)
	var project struct{ ID, TenantID int64 }
	if err := tx.Table("projects").Select("id,tenant_id").Where("deleted_at IS NULL AND status='active'").Order("id").Take(&project).Error; err != nil {
		t.Fatal(err)
	}
	base := fmt.Sprintf("/api/v1/projects/%d", project.ID)
	for _, path := range []string{"/ip-allocations", "/ip-allocations/1", "/ip-allocations/1/release", "/pxe-configs", "/pxe-configs/1", "/pxe-config", "/networks/1/auto-assign"} {
		for _, method := range []string{"GET", "POST", "PUT", "DELETE"} {
			request(method, base+path, nil, 404)
		}
	}
	netbody := map[string]any{"name": "network-options-test", "subnet": "198.19.232.0/24", "gateway": "198.19.232.1", "dns_servers": []string{"223.5.5.5", "2001:4860:4860::8888"}, "dhcp_enabled": true}
	netid := asInt(request("POST", base+"/networks", netbody, 200)["id"])
	np := fmt.Sprintf("%s/networks/%d", base, netid)
	data := request("GET", np, nil, 200)
	if len(data["dns_servers"].([]any)) != 2 {
		t.Fatal("DNS not persisted")
	}
	request("POST", base+"/networks", map[string]any{"name": "overlap", "subnet": "198.19.232.128/25"}, 409)
	other := asInt(request("POST", base+"/networks", map[string]any{"name": "different-vlan", "subnet": "198.19.232.128/25", "vlan_id": 4093}, 200)["id"])
	pool := func(start, end string) map[string]any {
		return map[string]any{"name": "network-options-pool", "network_id": netid, "start_address": start, "end_address": end}
	}
	request("POST", base+"/ip-pools", pool("198.19.232.200", "198.19.232.100"), 409)
	request("POST", base+"/ip-pools", pool("198.19.232.0", "198.19.232.20"), 409)
	request("POST", base+"/ip-pools", pool("198.19.232.1", "198.19.232.20"), 409)
	request("POST", base+"/ip-pools", pool("198.19.232.200", "198.19.232.255"), 409)
	poolid := asInt(request("POST", base+"/ip-pools", pool("198.19.232.100", "198.19.232.200"), 200)["id"])
	// Authoritative subnet parameters replace client-supplied values; DHCPv4 omits IPv6 DNS.
	cfg := pxeservice.Config{TenantID: project.TenantID, ProjectID: project.ID, NetworkID: netid, PoolID: poolid, DHCPMode: "relay", Interface: "ens33", Server: "192.0.2.23", Root: "/var/lib/tftpboot", BIOS: "undionly.kpxe", EFI: "ipxe.efi", Gateway: "203.0.113.1", DNS: "invalid"}
	if err := app.validatePXEBinding(context.Background(), &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Subnet != "198.19.232.0/24" || cfg.Gateway != "198.19.232.1" || cfg.DNS != "223.5.5.5" {
		t.Fatalf("wrong authoritative DHCP parameters: %+v", cfg)
	}
	cfg.DHCPMode = "direct"
	if err := app.validatePXEBinding(context.Background(), &cfg); err == nil {
		t.Fatal("direct mode accepted remote server")
	}
	cfg.DHCPMode = "relay"
	pp := fmt.Sprintf("%s/ip-pools/%d", base, poolid)
	request("POST", base+"/ip-pools", pool("198.19.232.150", "198.19.232.220"), 409)
	request("PUT", np, map[string]any{"gateway": "198.19.232.100"}, 409)
	request("PUT", np, map[string]any{"subnet": "198.19.232.0/25"}, 409)
	request("PUT", pp, map[string]any{"network_id": other}, 409)
	request("PUT", pp, map[string]any{"name": "renamed-pool", "end_address": "198.19.232.180"}, 200)
	request("PUT", np, map[string]any{"gateway": nil, "vlan_id": nil, "dns_servers": []any{}}, 200)
	if err := app.validatePXEBinding(context.Background(), &cfg); err == nil {
		t.Fatal("relay accepted a subnet without a gateway")
	}
	data = request("GET", np, nil, 200)
	if data["gateway"] != nil || data["vlan_id"] != nil || len(data["dns_servers"].([]any)) != 0 || data["dhcp_enabled"] != true {
		t.Fatal("optional clear or partial edit failed")
	}
	// The protected bound network remains readable and may be renamed; structural changes and deletion are blocked.
	app.networkState = func(context.Context) (pxeservice.Status, error) {
		return pxeservice.Status{DHCP: true, Config: pxeservice.Config{TenantID: project.TenantID, ProjectID: project.ID, NetworkID: netid, PoolID: poolid}}, nil
	}
	request("PUT", np, map[string]any{"name": "active-network-name"}, 200)
	request("PUT", np, map[string]any{"dns_servers": []any{}}, 200)
	request("PUT", np, map[string]any{"dns_servers": []string{"1.1.1.1"}}, 409)
	request("PUT", np, map[string]any{"gateway": "198.19.232.1"}, 409)
	request("PUT", np, map[string]any{"dhcp_enabled": false}, 409)
	request("PUT", pp, map[string]any{"end_address": "198.19.232.179"}, 409)
	request("DELETE", pp, nil, 409)
	request("DELETE", np, nil, 409)
	app.networkState = func(context.Context) (pxeservice.Status, error) { return pxeservice.Status{}, errors.New("offline") }
	result := request("PUT", np, map[string]any{"dhcp_enabled": false}, 409)
	if !strings.Contains(fmt.Sprint(result["message"]), "无法确认") {
		t.Fatal("controller failure did not fail closed")
	}
	app.networkState = func(context.Context) (pxeservice.Status, error) { return pxeservice.Status{}, nil }
	request("DELETE", pp, nil, 200)
	request("DELETE", np, nil, 200)
}
