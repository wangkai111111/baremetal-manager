package platform

import (
	"bytes"
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

func TestSelfUseResourceRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	a := &App{}
	router := a.routes()
	want := map[string]string{
		"GET /api/v1/projects/:projectId/departments":            "listDepartments",
		"POST /api/v1/projects/:projectId/departments":           "createDepartment",
		"PUT /api/v1/projects/:projectId/departments/:id":        "updateDepartment",
		"DELETE /api/v1/projects/:projectId/departments/:id":     "deleteDepartment",
		"GET /api/v1/projects/:projectId/assets/depreciation":    "listAssetDepreciation",
		"GET /api/v1/projects/:projectId/assets/warranty-alerts": "listWarrantyAlerts",
	}
	for _, route := range router.Routes() {
		key := route.Method + " " + route.Path
		if handler, ok := want[key]; ok {
			if !strings.HasSuffix(route.Handler, "."+handler+"-fm") {
				t.Errorf("%s uses %s, want %s", key, route.Handler, handler)
			}
			delete(want, key)
		}
	}
	for route := range want {
		t.Errorf("missing route: %s", route)
	}
	for _, path := range []string{"depreciation", "warranty-alerts"} {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/projects/1/assets/"+path, nil))
		if w.Code != 401 {
			t.Errorf("%s must require authentication, got %d", path, w.Code)
		}
	}
	if tree := buildDepartmentTree(nil, 0); tree == nil || len(tree) != 0 {
		t.Fatal("empty department tree must be an empty array")
	}
}

func TestSetupCredentials(t *testing.T) {
	username, password, generated, err := setupCredentials("", "")
	if err != nil || username != "admin" || !generated || len(password) != 32 {
		t.Fatal("default credentials were not generated correctly")
	}
	_, second, _, err := setupCredentials("   ", "")
	if err != nil || second == password {
		t.Fatal("generated passwords must be independent")
	}
	username, explicit, generated, err := setupCredentials("operator", strings.Repeat("a", 12))
	if err != nil || username != "operator" || generated || explicit != strings.Repeat("a", 12) {
		t.Fatal("explicit credentials were not preserved")
	}
	username, _, generated, err = setupCredentials("", strings.Repeat("b", 12))
	if err != nil || username != "admin" || generated {
		t.Fatal("default username with explicit password failed")
	}
	for _, invalid := range []string{"short", strings.Repeat("x", 73)} {
		if _, _, _, err := setupCredentials("admin", invalid); err == nil {
			t.Fatal("invalid explicit password accepted")
		}
	}
}

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
}

func TestAssetDatacenterInput(t *testing.T) {
	var asset resource
	for _, r := range resources {
		if r.path == "racks" || r.path == "rack-labels" {
			t.Fatal("removed resource registered")
		}
		if r.table == "assets" {
			asset = r
		}
	}
	for _, tc := range []struct {
		body  string
		valid bool
	}{
		{`{"datacenter_id":1}`, true}, {`{"datacenter_id":null}`, true},
		{`{"datacenter_id":0}`, false}, {`{"datacenter_id":1.5}`, false},
		{`{"rack_id":1}`, false}, {`{"rack_u_position":1}`, false}, {`{"height_u":1}`, false},
	} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest("PUT", "/", strings.NewReader(tc.body))
		c.Request.Header.Set("Content-Type", "application/json")
		_, err := inputResource(c, asset, false)
		if (err == nil) != tc.valid {
			t.Fatalf("input %s: %v", tc.body, err)
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
	seedIntegrationAdmin(t, db)
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
	code, res = request("PUT", "/api/v1/settings", `{"smtp.host":"smtp.test","smtp.port":587,"smtp.username":"test","smtp.password":"test-password","smtp.from":"noreply@test"}`, token)
	if code != 200 {
		t.Fatalf("SMTP settings update: %d %v", code, res)
	}
	var auditCount int64
	if e := db.Table("audits").Where("target_type=? AND action=? AND target_id=?", "system_settings", "settings:write", "smtp.host").Count(&auditCount).Error; e != nil || auditCount == 0 {
		t.Fatalf("settings audit missing: %v", e)
	}
	code, res = request("GET", "/api/v1/brand", "", "")
	if code != 200 {
		t.Fatalf("brand: %d %v", code, res)
	}
	if _, exists := res["data"].(map[string]any)["feature_flags"]; exists {
		t.Fatal("commercial feature flags exposed")
	}
	code, _ = request("PUT", "/api/v1/settings", `{"platform.operation_mode":"external"}`, token)
	if code != 400 {
		t.Fatalf("removed setting accepted: %d", code)
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
	summaryPath := fmt.Sprintf("%s/assets/%d/collection", base, id)
	code, res = request("GET", summaryPath, "", token)
	if code != 200 || res["data"].(map[string]any)["status"] != "pending" {
		t.Fatalf("initial collection %d %v", code, res)
	}
	assetUpdates, err := collectionAssetUpdates(json.RawMessage(`{"Manufacturer":"Test vendor","Model":"Test model","SerialNumber":"Test serial","ProcessorSummary":{"Count":2},"MemorySummary":{"TotalSystemMemoryGiB":64},"Secret":"not returned"}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Table("assets").Where("id=?", id).Updates(assetUpdates).Error; err != nil {
		t.Fatal(err)
	}
	// Complete the automatic operation as a worker would when persisting the snapshot.
	if err := db.Table("operations").Where("asset_id=? AND kind='collect'", id).Update("status", "succeeded").Error; err != nil {
		t.Fatal(err)
	}
	code, res = request("GET", summaryPath, "", token)
	if code != 200 || res["data"].(map[string]any)["status"] != "succeeded" || res["data"].(map[string]any)["cpu_count"] != float64(2) {
		t.Fatalf("collection summary %d %v", code, res)
	}
	b, _ = json.Marshal(res)
	if bytes.Contains(b, []byte("not returned")) || bytes.Contains(b, []byte("snapshot")) || bytes.Contains(b, []byte("bmc_secret")) {
		t.Fatal("collection summary leaked private fields")
	}
	code, res = request("GET", fmt.Sprintf("%s/assets/%d", base, id), "", token)
	if code != 200 || res["data"].(map[string]any)["manufacturer"] != "Test vendor" || res["data"].(map[string]any)["model"] != "Test model" || res["data"].(map[string]any)["serial_number"] != "Test serial" {
		t.Fatalf("identity backfill %d %v", code, res)
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
	if code != 400 {
		t.Fatalf("unconfirmed install must be rejected, got %d", code)
	}
	code, _ = request("GET", "/api/v1/docs/openapi.json", "", "")
	if code != 200 {
		t.Fatalf("docs status %d", code)
	}
}
