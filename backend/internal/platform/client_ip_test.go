package platform

import (
	"fmt"
	"net/http/httptest"
	"os"
	"reflect"
	"testing"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestTrustedProxyCIDRs(t *testing.T) {
	for _, tc := range []struct {
		input string
		want  []string
	}{
		{"", nil},
		{"  ,\r\n\t", nil},
		{"172.23.0.9, 127.0.0.1\n2001:db8::1\t172.23.4.3/16", []string{"172.23.0.9", "127.0.0.1", "2001:db8::1", "172.23.0.0/16"}},
		{"::ffff:192.0.2.1", []string{"192.0.2.1"}},
	} {
		got, err := trustedProxyCIDRs(tc.input)
		if err != nil || !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("parse %q: %v %v", tc.input, got, err)
		}
	}
	for _, value := range []string{"invalid", "172.23.0.1/33", "::ffff:192.0.2.1/120", "127.0.0.1,untrusted", "fe80::1%eth0"} {
		if _, err := trustedProxyCIDRs(value); err == nil {
			t.Fatalf("accepted invalid proxy %q", value)
		}
	}
}

// Only enqueue in an isolated transaction; never start a worker or contact BMC.
func TestOperationClientIPIntegration(t *testing.T) {
	dsn := os.Getenv("PLATFORM_TEST_DSN")
	if dsn == "" {
		t.Skip("requires disposable migrated database")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	conn, _ := db.DB()
	defer conn.Close()
	tx := db.Begin()
	defer tx.Rollback()
	seedIntegrationAdmin(t, tx)
	var sc scope
	if err := tx.Raw(`SELECT p.tenant_id AS tenant,p.id AS project,u.id AS user FROM projects p CROSS JOIN users u
 WHERE p.deleted_at IS NULL AND p.status='active' AND u.username='integration-admin' ORDER BY p.id LIMIT 1`).Scan(&sc).Error; err != nil {
		t.Fatal(err)
	}
	sc.Admin = true
	var asset int64
	if err := tx.Raw(`INSERT INTO assets(tenant_id,project_id,name,bmc_address,bmc_user,bmc_protocol,bmc_secret_ciphertext,bmc_secret_key_id)
	 VALUES (?,?,'client-ip-test','192.0.2.254','test','redfish',?,'v1') RETURNING id`, sc.Tenant, sc.Project, []byte("test-only-not-used")).Scan(&asset).Error; err != nil {
		t.Fatal(err)
	}
	for i, tc := range []struct {
		remote, forwarded, want string
		trust                   []string
	}{
		{"172.23.0.9:1234", "192.0.2.42, 172.23.0.1", "192.0.2.42", []string{"172.23.0.0/16"}},
		{"192.0.2.43:1234", "203.0.113.99", "192.0.2.43", nil},
	} {
		a := &App{store: &store{tx}, trustedProxies: tc.trust}
		router := a.routes()
		key := fmt.Sprintf("client-ip-test-%d", i)
		router.POST("/test-submit-ip/:id", func(c *gin.Context) {
			c.Set("scope", sc)
			a.submit(c, "collect", key)
		})
		r := httptest.NewRequest("POST", fmt.Sprintf("/test-submit-ip/%d", asset), nil)
		r.RemoteAddr = tc.remote
		r.Header.Set("X-Forwarded-For", tc.forwarded)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		if w.Code != 202 {
			t.Fatalf("enqueue: %d %s", w.Code, w.Body.String())
		}
		var ip string
		if err := tx.Raw("SELECT host(client_ip) FROM operations WHERE idempotency_key=?", key).Scan(&ip).Error; err != nil || ip != tc.want {
			t.Fatalf("stored IP %q, want %q: %v", ip, tc.want, err)
		}
		// Complete without executing, so the second request creates a separate task.
		if err := tx.Exec("UPDATE operations SET status='cancelled' WHERE idempotency_key=?", key).Error; err != nil {
			t.Fatal(err)
		}
	}
}

func TestRequestClientIP(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name, remote, forwarded, real, want string
		trust                               []string
	}{
		{"direct ignores spoof", "192.0.2.42:1234", "203.0.113.99", "203.0.113.99", "192.0.2.42", nil},
		{"untrusted peer ignores spoof", "192.0.2.42:1234", "203.0.113.99", "203.0.113.99", "192.0.2.42", []string{"172.23.0.0/16"}},
		{"proxy chain", "172.23.0.9:1234", "192.0.2.42, 172.23.0.1", "203.0.113.99", "192.0.2.42", []string{"172.23.0.0/16"}},
		{"stop at untrusted hop", "172.23.0.9:1234", "203.0.113.99, 192.0.2.42, 172.23.0.1", "", "192.0.2.42", []string{"172.23.0.0/16"}},
		{"invalid header falls back", "172.23.0.9:1234", "invalid", "203.0.113.99", "172.23.0.9", []string{"172.23.0.0/16"}},
		{"ignore alternate header", "172.23.0.9:1234", "", "203.0.113.99", "172.23.0.9", []string{"172.23.0.0/16"}},
		{"IPv6 client", "172.23.0.9:1234", "2001:db8::42, 172.23.0.1", "", "2001:db8::42", []string{"172.23.0.0/16"}},
		{"IPv6 direct", "[2001:db8::42]:1234", "203.0.113.99", "", "2001:db8::42", nil},
		{"mapped IPv4 direct", "[::ffff:192.0.2.42]:1234", "203.0.113.99", "", "192.0.2.42", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			router := (&App{trustedProxies: tc.trust}).routes()
			router.GET("/test-client-ip", func(c *gin.Context) { c.String(200, requestMeta(c, 1).ClientIP) })
			req := httptest.NewRequest("GET", "/test-client-ip", nil)
			req.RemoteAddr = tc.remote
			req.Header.Set("X-Forwarded-For", tc.forwarded)
			req.Header.Set("X-Real-IP", tc.real)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			if w.Code != 200 || w.Body.String() != tc.want {
				t.Fatalf("got %d %q, want %q", w.Code, w.Body.String(), tc.want)
			}
		})
	}
}
