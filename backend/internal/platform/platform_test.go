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
	"gorm.io/gorm/clause"
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
	for i := 0; i < 3; i++ {
		var u struct{ ID int64 }
		if err := db.Raw("INSERT INTO users(username,password_hash,name) VALUES (?,?,?) RETURNING id", fmt.Sprintf("mode-%d-%d", time.Now().UnixNano(), i), "test-hash", "mode test").Scan(&u).Error; err != nil {
			t.Fatal(err)
		}
		sc := scope{Tenant: p.TenantID, Project: p.ID, User: u.ID}
		var id int64
		in := map[string]any{"name": fmt.Sprintf("mode-%d", u.ID), "bmc_address": fmt.Sprintf("2001:db8::%x", u.ID), "bmc_user": "test", "bmc_secret_ciphertext": []byte("test"), "bmc_secret_key_id": "v1"}
		if i == 1 {
			in["bmc_tls_verify"] = true
		}
		admin := sc
		admin.Admin = true
		if err := s.saveResource(context.Background(), resource{table: "assets"}, admin, 0, in, true, &id); err != nil {
			t.Fatal(err)
		}
		scopes = append(scopes, sc)
		ids = append(ids, id)
	}
	for i := 0; i < 2; i++ {
		if err := db.Table("orders").Create(map[string]any{"tenant_id": p.TenantID, "user_id": scopes[i].User, "asset_id": ids[i], "type": "purchase", "status": "completed", "amount": 0, "final_amount": 0}).Error; err != nil {
			t.Fatal(err)
		}
	}
	var dcID int64
	if err := s.saveResource(context.Background(), resource{table: "datacenters"}, scopes[0], 0, map[string]any{"name": fmt.Sprintf("dc-%d", time.Now().UnixNano())}, true, &dcID); err != nil {
		t.Fatal(err)
	}
	var out int64
	fixtureAdmin := scopes[0]
	fixtureAdmin.Admin = true
	if err := s.saveResource(context.Background(), resource{table: "assets"}, fixtureAdmin, ids[0], map[string]any{"datacenter_id": dcID}, false, &out); err != nil {
		t.Fatal(err)
	}
	foreign := fixtureAdmin
	foreign.Project = -1
	if err := s.saveResource(context.Background(), resource{table: "assets"}, foreign, 0, map[string]any{"datacenter_id": dcID}, true, &out); err == nil {
		t.Fatal("cross-project datacenter accepted")
	}
	foreign = fixtureAdmin
	foreign.Tenant = -1
	if err := s.saveResource(context.Background(), resource{table: "assets"}, foreign, 0, map[string]any{"datacenter_id": dcID}, true, &out); err == nil {
		t.Fatal("cross-tenant datacenter accepted")
	}
	for _, mode := range []string{ModeExternalOperation, ModeSelfUse} {
		// 使用权威源 system_modes 表设置模式
		var modeRows int64
		if err := db.Table("system_modes").Count(&modeRows).Error; err != nil {
			t.Fatal(err)
		}
		if modeRows == 0 {
			if err := db.Exec("INSERT INTO system_modes(mode,locked) VALUES (?,false)", mode).Error; err != nil {
				t.Fatal(err)
			}
		} else {
			if err := db.Exec("UPDATE system_modes SET mode=?", mode).Error; err != nil {
				t.Fatal(err)
			}
		}
		InvalidateModeCache()
		t.Cleanup(InvalidateModeCache)
		if mode == ModeExternalOperation {
			tenant := scopes[0]
			admin := tenant
			admin.Admin = true
			request := func(sc scope, body, transition string, id int64, handler gin.HandlerFunc) int {
				w := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(w)
				c.Set("scope", sc)
				c.Set("user", user{ID: sc.User})
				c.Params = gin.Params{{Key: "id", Value: fmt.Sprint(id)}, {Key: "transition", Value: transition}}
				c.Request = httptest.NewRequest("POST", "/", strings.NewReader(body))
				handler(c)
				return w.Code
			}
			a := &App{store: s}
			if code := request(tenant, `{}`, "", 0, a.writeResource(resource{table: "assets"}, true)); code != 403 {
				t.Fatalf("tenant asset create: %d", code)
			}
			if err := s.saveResource(context.Background(), resource{table: "assets"}, tenant, 0, map[string]any{}, true, new(int64)); err != errAssetProvisioningForbidden {
				t.Fatalf("repository asset create: %v", err)
			}
			if code := request(tenant, "", "", ids[0], a.deleteResource(resource{table: "assets"})); code != 403 {
				t.Fatalf("tenant asset delete: %d", code)
			}
			if code := request(tenant, fmt.Sprintf(`{"type":"purchase","asset_id":%d}`, ids[0]), "", 0, a.createOrder); code != 403 {
				t.Fatalf("tenant supplied purchase asset: %d", code)
			}
			for _, transition := range []string{"paid", "processing", "completed", "refunded"} {
				if code := request(tenant, "", transition, 1, a.transitionOrder); code != 403 {
					t.Fatalf("tenant transition %s: %d", transition, code)
				}
			}
			newOrder := func(status string) int64 {
				row := map[string]any{"tenant_id": p.TenantID, "user_id": tenant.User, "type": "purchase", "status": status, "amount": 0, "final_amount": 0}
				if err := db.Table("orders").Clauses(clause.Returning{Columns: []clause.Column{{Name: "id"}}}).Create(&row).Error; err != nil {
					t.Fatal(err)
				}
				return asInt(row["id"])
			}
			if code := request(tenant, "", "cancelled", newOrder("pending"), a.transitionOrder); code != 200 {
				t.Fatalf("tenant pending cancellation: %d", code)
			}
			id := newOrder("processing")
			if code := request(admin, "", "completed", id, a.transitionOrder); code == 200 {
				t.Fatal("purchase completed without asset")
			}
			body := fmt.Sprintf(`{"asset_id":%d}`, ids[2])
			for _, field := range []string{"tenant", "project"} {
				foreign := admin
				if field == "tenant" {
					foreign.Tenant = -1
				} else {
					foreign.Project = -1
				}
				if code := request(foreign, body, "completed", id, a.transitionOrder); code == 200 {
					t.Fatalf("cross-%s delivery accepted", field)
				}
			}
			if code := request(admin, fmt.Sprintf(`{"asset_id":%d}`, ids[0]), "completed", id, a.transitionOrder); code == 200 {
				t.Fatal("duplicate purchase delivery accepted")
			}
			if err := db.Table("assets").Where("id=?", ids[2]).Update("deleted_at", time.Now()).Error; err != nil {
				t.Fatal(err)
			}
			if code := request(admin, body, "completed", id, a.transitionOrder); code == 200 {
				t.Fatal("deleted asset delivery accepted")
			}
			if err := db.Table("assets").Where("id=?", ids[2]).Update("deleted_at", nil).Error; err != nil {
				t.Fatal(err)
			}
			if code := request(admin, body, "completed", id, a.transitionOrder); code != 200 {
				t.Fatalf("platform delivery binding: %d", code)
			}
			var delivered struct {
				Status  string
				AssetID int64
			}
			if err := db.Table("orders").Where("id=?", id).Take(&delivered).Error; err != nil || delivered.Status != "completed" || delivered.AssetID != ids[2] {
				t.Fatalf("delivery binding not persisted: %+v %v", delivered, err)
			}
			// 恢复无订单资产场景，后续隔离断言仅统计最初两个已交付资产。
			if err := db.Table("orders").Where("id=?", id).Update("deleted_at", time.Now()).Error; err != nil {
				t.Fatal(err)
			}
		}
		if err := s.deleteResource(context.Background(), resource{table: "datacenters"}, scopes[1], dcID); err == nil {
			t.Fatal("datacenter with another user's asset deleted")
		}
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Set("scope", scopes[0])
		c.Request = httptest.NewRequest("GET", fmt.Sprintf("/assets?datacenter_id=%d", dcID), nil)
		a := &App{store: s}
		a.listResource(resource{table: "assets"})(c)
		var response struct {
			Data struct {
				Items []map[string]any
				Total int64
			}
		}
		if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil || w.Code != 200 || response.Data.Total != 1 || len(response.Data.Items) != 1 || asInt(response.Data.Items[0]["datacenter_id"]) != dcID {
			t.Fatalf("datacenter list filter: %d %s %v", w.Code, w.Body.String(), err)
		}
		for i, sc := range scopes[:2] {
			var n int64
			if err := queryScope(db, resource{table: "assets"}, sc).Where("id IN ?", ids).Count(&n).Error; err != nil {
				t.Fatal(err)
			}
			want := int64(2)
			if mode == ModeSelfUse {
				want = 3
			}
			if n != want {
				t.Fatalf("%s user %d sees %d, want %d", mode, i, n, want)
			}
			for _, r := range resources {
				if err := queryScope(db, r, sc).Count(new(int64)).Error; err != nil {
					t.Fatalf("scope %s: %v", r.table, err)
				}
			}
			if mode == ModeExternalOperation {
				var out int64
				if err := s.saveResource(context.Background(), resource{table: "assets"}, sc, ids[2], map[string]any{"name": "forbidden"}, false, &out); err == nil {
					t.Fatal("foreign asset update allowed")
				}
				if err := s.saveResource(context.Background(), resource{table: "asset_monitor_configs"}, sc, 0, map[string]any{"asset_id": ids[2]}, true, &out); err == nil {
					t.Fatal("foreign asset association allowed")
				}
				if err := s.saveResource(context.Background(), resource{table: "asset_monitor_configs"}, sc, 0, map[string]any{"asset_id": ids[i]}, true, &out); err != nil {
					t.Fatal(err)
				}
				if err := queryScope(db, resource{table: "asset_monitor_configs"}, scopes[1-i]).Where("id=?", out).Count(&n).Error; err != nil || n != 1 {
					t.Fatalf("same-tenant delivered associated row missing: %d %v", n, err)
				}
			}
		}
		admin := scopes[0]
		admin.Admin = true
		var n int64
		if err := queryScope(db, resource{table: "assets"}, admin).Where("id IN ?", ids).Count(&n).Error; err != nil || n != 3 {
			t.Fatalf("admin isolation regression %d %v", n, err)
		}
	}
	if err := s.saveResource(context.Background(), resource{table: "assets"}, scopes[0], ids[0], map[string]any{"datacenter_id": nil}, false, &out); err != nil {
		t.Fatal(err)
	}
	if err := s.deleteResource(context.Background(), resource{table: "datacenters"}, scopes[0], dcID); err != nil {
		t.Fatalf("empty datacenter delete: %v", err)
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
	for _, mode := range []string{ModeSelfUse, ModeExternalOperation} {
		for _, feature := range GetAvailableFeatures(mode) {
			if feature == "racks" {
				t.Fatal("removed feature advertised")
			}
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
	// 部署模式的唯一权威源是 system_modes，且初始化后立即锁定。
	// 先把权威源固定为“运营模式 + 已锁定”，测试数据写在事务内，结束时整体回滚。
	var modeRows int64
	if e := db.Table("system_modes").Count(&modeRows).Error; e != nil {
		t.Fatal(e)
	}
	if modeRows == 0 {
		if e := db.Exec("INSERT INTO system_modes(mode,locked) VALUES ('external_operation',true)").Error; e != nil {
			t.Fatal(e)
		}
	} else if e := db.Exec("UPDATE system_modes SET mode='external_operation', locked=true").Error; e != nil {
		t.Fatal(e)
	}
	InvalidateModeCache()
	code, res = request("PUT", "/api/v1/settings", `{"registration.email_verification":true,"billing.enabled":true,"billing.model_prices":[{"model":"server","price":10}],"smtp.host":"smtp.test","smtp.port":587,"smtp.username":"test","smtp.password":"test-password","smtp.from":"noreply@test"}`, token)
	if code != 200 {
		t.Fatalf("settings update %d %v", code, res)
	}
	var auditCount int64
	if e := db.Table("audits").Where("target_type=? AND action=? AND target_id=?", "system_settings", "settings:write", "registration.email_verification").Count(&auditCount).Error; e != nil || auditCount == 0 {
		t.Fatalf("settings audit missing: %v", e)
	}
	code, res = request("GET", "/api/v1/brand", "", "")
	if code != 200 || res["data"].(map[string]any)["operation_mode"] != "external" || res["data"].(map[string]any)["feature_flags"].(map[string]any)["billing_enabled"] != true {
		t.Fatalf("public settings %d %v", code, res)
	}
	// 模式锁定后，通过设置接口改写 platform.operation_mode 必须被拒绝。
	code, _ = request("PUT", "/api/v1/settings", `{"platform.operation_mode":"self_hosted"}`, token)
	if code != 403 {
		t.Fatalf("locked deployment mode must not be changeable via settings, got %d", code)
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
	if code != 200 || res["data"].(map[string]any)["status"] != "not_collected" {
		t.Fatalf("initial collection %d %v", code, res)
	}
	assetUpdates, err := collectionAssetUpdates(json.RawMessage(`{"Manufacturer":"Test vendor","Model":"Test model","SerialNumber":"Test serial","ProcessorSummary":{"Count":2},"MemorySummary":{"TotalSystemMemoryGiB":64},"Secret":"not returned"}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Table("assets").Where("id=?", id).Updates(assetUpdates).Error; err != nil {
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
	if code != 501 {
		t.Fatalf("unsupported install status %d", code)
	}
	code, _ = request("GET", "/api/v1/docs/openapi.json", "", "")
	if code != 200 {
		t.Fatalf("docs status %d", code)
	}
}
