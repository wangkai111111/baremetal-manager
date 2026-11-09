package platform

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestOperationSettingsValidation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	a := &App{}
	for _, handler := range []gin.HandlerFunc{a.settings, a.updateSettings} {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Set("user", user{ID: 1})
		handler(c)
		if w.Code != 403 {
			t.Fatalf("non-admin settings access: %d", w.Code)
		}
	}
	for key, value := range brandDefaults {
		if !validSetting(key, value) {
			t.Fatalf("invalid default %s", key)
		}
	}
	for _, tc := range []struct {
		key   string
		value any
	}{
		{"platform.operation_mode", "other"}, {"platform.operation_mode", map[string]any{}},
		{"registration.email_verification", "true"}, {"billing.enabled", nil},
		{"billing.model_prices", "[]"}, {"billing.model_prices", map[string]any{"secret": "hidden"}},
		{"brand.platform_name", false}, {"unknown.setting", true},
		{"billing.model_prices", []any{strings.Repeat("x", 1048576)}},
	} {
		if validSetting(tc.key, tc.value) {
			t.Fatalf("accepted invalid setting %s", tc.key)
		}
	}
	if !validSetting("billing.model_prices", map[string]any{"server": 10}) {
		t.Fatal("price object rejected")
	}
}

func TestOperationModeIsolation(t *testing.T) {
	dsn := os.Getenv("PLATFORM_TEST_DSN")
	if dsn == "" {
		t.Skip("PLATFORM_TEST_DSN not set")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	conn, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	db = db.Begin()
	if db.Error != nil {
		t.Fatal(db.Error)
	}
	defer db.Rollback()
	var p struct{ ID, TenantID int64 }
	if err := db.Table("projects").Where("deleted_at IS NULL").Order("id").Take(&p).Error; err != nil {
		t.Fatal(err)
	}
	s := &store{db: db}
	scopes := []scope{}
	ids := []int64{}
	for i := 0; i < 2; i++ {
		var u struct{ ID int64 }
		if err := db.Raw("INSERT INTO users(username,password_hash,name) VALUES (?,?,?) RETURNING id", fmt.Sprintf("mode-%d-%d", time.Now().UnixNano(), i), "test-hash", "mode test").Scan(&u).Error; err != nil {
			t.Fatal(err)
		}
		sc := scope{Tenant: p.TenantID, Project: p.ID, User: u.ID}
		var id int64
		in := map[string]any{"name": fmt.Sprintf("mode-%d", u.ID), "bmc_address": fmt.Sprintf("2001:db8::%x", u.ID), "bmc_user": "test", "bmc_secret_ciphertext": []byte("test"), "bmc_secret_key_id": "v1"}
		if err := s.saveResource(context.Background(), resource{table: "assets"}, sc, 0, in, true, &id); err != nil {
			t.Fatal(err)
		}
		scopes = append(scopes, sc)
		ids = append(ids, id)
	}
	for _, mode := range []string{"external", "self_hosted"} {
		if err := db.Exec("INSERT INTO system_settings(key,value) VALUES (?,?::jsonb) ON CONFLICT (key) WHERE deleted_at IS NULL DO UPDATE SET value=EXCLUDED.value", "platform.operation_mode", fmt.Sprintf("%q", mode)).Error; err != nil {
			t.Fatal(err)
		}
		for i, sc := range scopes {
			var n int64
			if err := queryScope(db, resource{table: "assets"}, sc).Where("id IN ?", ids).Count(&n).Error; err != nil {
				t.Fatal(err)
			}
			want := int64(1)
			if mode == "self_hosted" {
				want = 2
			}
			if n != want {
				t.Fatalf("%s user %d sees %d, want %d", mode, i, n, want)
			}
			for _, r := range resources {
				if err := queryScope(db, r, sc).Count(new(int64)).Error; err != nil {
					t.Fatalf("scope %s: %v", r.table, err)
				}
			}
			if mode == "external" {
				var out int64
				if err := s.saveResource(context.Background(), resource{table: "assets"}, sc, ids[1-i], map[string]any{"name": "forbidden"}, false, &out); err == nil {
					t.Fatal("foreign asset update allowed")
				}
				if err := s.saveResource(context.Background(), resource{table: "asset_monitor_configs"}, sc, 0, map[string]any{"asset_id": ids[1-i]}, true, &out); err == nil {
					t.Fatal("foreign asset association allowed")
				}
				if err := s.saveResource(context.Background(), resource{table: "asset_monitor_configs"}, sc, 0, map[string]any{"asset_id": ids[i]}, true, &out); err != nil {
					t.Fatal(err)
				}
				if err := queryScope(db, resource{table: "asset_monitor_configs"}, scopes[1-i]).Where("id=?", out).Count(&n).Error; err != nil || n != 0 {
					t.Fatalf("foreign associated row visible: %d %v", n, err)
				}
			}
		}
		admin := scopes[0]
		admin.Admin = true
		var n int64
		if err := queryScope(db, resource{table: "assets"}, admin).Where("id IN ?", ids).Count(&n).Error; err != nil || n != 2 {
			t.Fatalf("admin isolation regression %d %v", n, err)
		}
	}
}

func TestOrigin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, origin := range []string{"", "https://evil.example", "http://example.com.evil"} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest("GET", "http://example.com/", nil)
		c.Request.Header.Set("Origin", origin)
		if originAllowed(c) {
			t.Fatalf("accepted origin %q", origin)
		}
	}
}
func TestTargetIntegration(t *testing.T) {
	dsn := os.Getenv("PLATFORM_TEST_DSN")
	if dsn == "" {
		t.Skip("PLATFORM_TEST_DSN not set")
	}
	t.Setenv("JWT_SECRET", "platform-test-secret-32-bytes-long-only")
	t.Setenv("BMC_ENCRYPTION_KEY", "12345678901234567890123456789012")
	db, e := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if e != nil {
		t.Fatal(e)
	}
	conn, e := db.DB()
	if e != nil {
		t.Fatal(e)
	}
	defer conn.Close()
	// 每次用事务隔离测试数据，允许在同一个一次性测试库重复运行。
	db = db.Begin()
	if db.Error != nil {
		t.Fatal(db.Error)
	}
	defer db.Rollback()
	a, e := New(db)
	if e != nil {
		t.Fatal(e)
	}
	request := func(method, path, body, token string) (int, map[string]any) {
		r := httptest.NewRequest(method, path, bytes.NewBufferString(body))
		r.Header.Set("Content-Type", "application/json")
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		a.Router().ServeHTTP(w, r)
		var out map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		if w.Code >= 500 {
			t.Log(w.Body.String())
		}
		return w.Code, out
	}
	code, res := request("POST", "/api/v1/auth/login", `{"username":"integration-admin","password":"test-only-password-2026"}`, "")
	if code != 200 {
		t.Fatalf("login %d %v", code, res)
	}
	token := res["data"].(map[string]any)["token"].(string)
	code, res = request("PUT", "/api/v1/settings", `{"platform.operation_mode":"external","registration.email_verification":true,"billing.enabled":true,"billing.model_prices":[{"model":"server","price":10}],"smtp.host":"smtp.test","smtp.port":587,"smtp.username":"test","smtp.password":"test-password","smtp.from":"noreply@test"}`, token)
	if code != 200 {
		t.Fatalf("settings update %d %v", code, res)
	}
	var auditCount int64
	if e := db.Table("audits").Where("target_type=? AND action=? AND target_id=?", "system_settings", "settings:write", "platform.operation_mode").Count(&auditCount).Error; e != nil || auditCount == 0 {
		t.Fatalf("settings audit missing: %v", e)
	}
	code, res = request("GET", "/api/v1/brand", "", "")
	if code != 200 || res["data"].(map[string]any)["operation_mode"] != "external" || res["data"].(map[string]any)["feature_flags"].(map[string]any)["billing_enabled"] != true {
		t.Fatalf("public settings %d %v", code, res)
	}
	code, res = request("PUT", "/api/v1/settings", `{"platform.operation_mode":"self_hosted"}`, token)
	if code != 200 {
		t.Fatalf("restore self hosted %d %v", code, res)
	}
	code, res = request("GET", "/api/v1/projects", "", token)
	if code != 200 {
		t.Fatalf("projects %d %v", code, res)
	}
	items := res["data"].(map[string]any)["items"].([]any)
	if len(items) == 0 {
		t.Fatal("bootstrap project missing")
	}
	pid := int64(items[0].(map[string]any)["id"].(float64))
	base := fmt.Sprintf("/api/v1/projects/%d", pid)
	name := fmt.Sprintf("integration-%d", time.Now().UnixNano())
	address := fmt.Sprintf("2001:db8::%x:%x", uint64(time.Now().UnixNano())>>16&0xffff, uint64(time.Now().UnixNano())&0xffff)
	code, res = request("POST", base+"/assets", fmt.Sprintf(`{"name":%q,"bmc_address":%q,"bmc_user":"test","bmc_secret":"never-expose-this"}`, name, address), token)
	if code != 200 {
		t.Fatalf("create asset %d %v", code, res)
	}
	id := int64(res["data"].(map[string]any)["id"].(float64))
	code, res = request("GET", fmt.Sprintf("%s/assets/%d", base, id), "", token)
	if code != 200 {
		t.Fatalf("get asset %d %v", code, res)
	}
	b, _ := json.Marshal(res)
	if bytes.Contains(b, []byte("never-expose-this")) || bytes.Contains(b, []byte("ciphertext")) {
		t.Fatal("credential exposure")
	}
	path := fmt.Sprintf("%s/assets/%d/actions", base, id)
	payload := fmt.Sprintf(`{"action":"collect","idempotency_key":%q}`, name)
	code, res = request("POST", path, payload, token)
	if code != 202 {
		t.Fatalf("submit %d %v", code, res)
	}
	op := res["data"].(map[string]any)["id"]
	code, res = request("POST", path, payload, token)
	if code != 202 || res["data"].(map[string]any)["id"] != op {
		t.Fatalf("idempotency %d %v", code, res)
	}
	code, res = request("GET", base+"/assets", "", token)
	if code != 200 {
		t.Fatalf("list assets %d %v", code, res)
	}
	if _, ok := res["data"].(map[string]any)["total"]; !ok {
		t.Fatal("pagination missing")
	}
	code, _ = request("POST", fmt.Sprintf("%s/assets/%d/install", base, id), `{}`, token)
	if code != 501 {
		t.Fatalf("unsupported install status %d", code)
	}
	code, _ = request("GET", "/api/v1/docs/openapi.json", "", "")
	if code != 200 {
		t.Fatalf("docs status %d", code)
	}
}
