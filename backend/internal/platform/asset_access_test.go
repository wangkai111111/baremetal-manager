package platform

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"testing"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestAssetAccessAndFinanceIntegration(t *testing.T) {
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
	token := ""
	req := func(method, path string, body any, want int) map[string]any {
		t.Helper()
		raw, _ := json.Marshal(body)
		r := httptest.NewRequest(method, path, bytes.NewReader(raw))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", "http://example.com")
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		app.Router().ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s status=%d want=%d response=%s", method, path, w.Code, want, w.Body.String())
		}
		var out map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		if data, ok := out["data"].(map[string]any); ok {
			return data
		}
		return out
	}
	login := func(name string) string {
		t.Helper()
		return req("POST", "/api/v1/auth/login", map[string]any{"username": name, "password": "test-only-password-2026"}, 200)["token"].(string)
	}
	admin := login("integration-admin")
	token = admin
	tenant := asInt(req("POST", "/api/v1/tenants", map[string]any{"name": "Access QA", "code": "access-qa"}, 200)["id"])
	project := asInt(req("POST", "/api/v1/projects", map[string]any{"tenant_id": tenant, "name": "Access QA", "code": "access-qa"}, 200)["id"])
	second := asInt(req("POST", "/api/v1/projects", map[string]any{"tenant_id": tenant, "name": "Access QA 2", "code": "access-qa-2"}, 200)["id"])
	base := fmt.Sprintf("/api/v1/projects/%d", project)
	users := map[string]int64{}
	for _, name := range []string{"access-user", "access-manager", "access-other", "access-org-admin"} {
		users[name] = asInt(req("POST", fmt.Sprintf("/api/v1/users?project_id=%d", project), map[string]any{"username": name, "password": "test-only-password-2026", "role": "user"}, 200)["id"])
	}
	dept := func(name string, parent int64) int64 {
		t.Helper()
		return asInt(req("POST", base+"/departments", map[string]any{"name": name, "parent_id": parent}, 200)["id"])
	}
	root := dept("Access root", 0)
	child := dept("Access child", root)
	other := dept("Access sibling", 0)
	assets := []int64{}
	for i, d := range []int64{root, child, other, 0} {
		body := map[string]any{"name": fmt.Sprintf("Access asset %d", i), "bmc_address": fmt.Sprintf("2001:db8:acc::%d", i+1), "bmc_protocol": "ipmi", "bmc_user": "qa", "bmc_secret": "qa-not-real"}
		if d > 0 {
			body["department_id"] = d
		}
		assets = append(assets, asInt(req("POST", base+"/assets", body, 200)["id"]))
	}
	grantPath := func(name string) string { return fmt.Sprintf("%s/users/%d/access-grants", base, users[name]) }
	grant := func(name string, dept any, manager bool) {
		t.Helper()
		req("PUT", grantPath(name), map[string]any{"grants": []any{map[string]any{"department_id": dept, "is_admin": manager}}}, 200)
	}
	grant("access-user", root, false)
	grant("access-manager", root, true)
	grant("access-other", other, false)
	grant("access-org-admin", nil, true)
	plain := login("access-user")
	manager := login("access-manager")
	orgAdmin := login("access-org-admin")
	token = plain
	if got := asInt(req("GET", "/api/v1/projects", nil, 200)["total"]); got != 2 {
		t.Fatalf("grants must automatically expose both projects, got %d", got)
	}
	req("GET", fmt.Sprintf("/api/v1/projects/%d/assets", second), nil, 200)
	if got := asInt(req("GET", base+"/assets", nil, 200)["total"]); got != 2 {
		t.Fatalf("subtree asset count=%d", got)
	}
	req("GET", fmt.Sprintf("%s/assets/%d", base, assets[2]), nil, 404)
	req("GET", fmt.Sprintf("%s/assets/%d/collection", base, assets[2]), nil, 404)
	req("POST", fmt.Sprintf("%s/assets/%d/sol-ticket", base, assets[2]), nil, 404)
	req("POST", base+"/assets", map[string]any{}, 403)
	req("PUT", fmt.Sprintf("%s/assets/%d", base, assets[0]), map[string]any{"name": "denied"}, 403)
	req("POST", fmt.Sprintf("%s/assets/%d/assign", base, assets[0]), map[string]any{"department_id": other}, 403)
	req("PUT", grantPath("access-other"), map[string]any{"grants": []any{}}, 403)
	req("GET", base+"/dashboard/resources", nil, 200)
	req("GET", base+"/dashboard/metrics/trend?metric=temperature_celsius", nil, 200)
	// Task and indirect log visibility follows current asset assignment, even for
	// tasks created by someone else. No worker runs against this transaction.
	var op1, op2 int64
	for i, aid := range []int64{assets[0], assets[2]} {
		var id int64
		if e := tx.Raw(`INSERT INTO operations(tenant_id,project_id,asset_id,kind,idempotency_key,status) VALUES (?,?,?,'power_on',?,'succeeded') RETURNING id`, tenant, project, aid, fmt.Sprintf("access-task-%d", i)).Scan(&id).Error; e != nil {
			t.Fatal(e)
		}
		if i == 0 {
			op1 = id
		} else {
			op2 = id
		}
		if e := tx.Exec(`INSERT INTO operation_logs(tenant_id,project_id,operation_id,sequence,level,message) VALUES (?,?,?,1,'info','QA')`, tenant, project, id).Error; e != nil {
			t.Fatal(e)
		}
	}
	if asInt(req("GET", base+"/tasks", nil, 200)["total"]) != 1 {
		t.Fatal("task list leaked")
	}
	req("GET", fmt.Sprintf("%s/tasks/%d", base, op1), nil, 200)
	req("GET", fmt.Sprintf("%s/tasks/%d", base, op2), nil, 404)
	req("GET", fmt.Sprintf("%s/tasks/%d/logs", base, op2), nil, 404)
	if asInt(req("GET", base+"/operation-logs", nil, 200)["total"]) != 1 {
		t.Fatal("operation logs leaked")
	}
	// Exercise every generic resource that contains a direct or indirect asset ID.
	for _, path := range []string{"hardware", "firmware-inventory", "monitor-configs", "sensor-records", "alerts", "alert-events", "installations", "installation-events", "firmware-upgrades", "sol-sessions", "asset-tags"} {
		req("GET", base+"/"+path, nil, 200)
	}
	token = manager
	req("PUT", grantPath("access-user"), map[string]any{"grants": []any{map[string]any{"department_id": other}}}, 409)
	req("PUT", grantPath("access-user"), map[string]any{"grants": []any{map[string]any{"department_id": nil, "is_admin": true}}}, 409)
	req("PUT", grantPath("access-manager"), map[string]any{"grants": []any{}}, 403)
	// Only the manageable slice is replaced; independent sibling grants survive.
	grant("access-other", child, false)
	if err := tx.Table("user_access_grants").Where("tenant_id=? AND user_id=?", tenant, users["access-other"]).Count(new(int64)).Error; err != nil {
		t.Fatal(err)
	}
	var count int64
	tx.Table("user_access_grants").Where("tenant_id=? AND user_id=?", tenant, users["access-other"]).Count(&count)
	if count != 2 {
		t.Fatal("delegated grant erased outside scope")
	}
	req("POST", base+"/assets", map[string]any{"name": "managed new", "bmc_address": "2001:db8:acc::10", "bmc_user": "qa", "bmc_secret": "qa", "bmc_protocol": "ipmi", "department_id": child}, 200)
	req("POST", base+"/assets", map[string]any{"name": "unassigned denied", "bmc_address": "2001:db8:acc::11", "bmc_user": "qa", "bmc_secret": "qa", "bmc_protocol": "ipmi"}, 409)
	req("PUT", fmt.Sprintf("%s/assets/%d", base, assets[2]), map[string]any{"name": "denied"}, 409)
	req("POST", base+"/assets/batch-assign", map[string]any{"asset_ids": []int64{assets[0], assets[2]}, "department_id": child}, 404)
	// Department admins cannot change inherited access by moving hierarchy.
	req("PUT", fmt.Sprintf("%s/departments/%d", base, child), map[string]any{"parent_id": other}, 403)
	req("POST", base+"/departments", map[string]any{"name": "denied root"}, 403)
	req("POST", base+"/departments", map[string]any{"name": "managed descendant", "parent_id": child}, 200)
	depPath := fmt.Sprintf("%s/assets/%d/depreciation", base, assets[0])
	finance := map[string]any{"purchase_cost": 10000, "salvage_value": 1000, "useful_life_months": 36, "start_date": "2020-01-01", "depreciation_method": "straight_line", "status": "active"}
	dep := req("PUT", depPath, finance, 200)
	if toFloat64(dep["net_value"]) != 1000 || toFloat64(dep["accumulated_depreciation"]) != 9000 || dep["status"] != "completed" {
		t.Fatalf("incorrect completed depreciation %v", dep)
	}
	finance["purchase_cost"] = 0
	finance["salvage_value"] = 0
	dep = req("PUT", depPath, finance, 200)
	if toFloat64(dep["net_value"]) != 0 || toFloat64(dep["accumulated_depreciation"]) != 0 {
		t.Fatal("zero depreciation not capped")
	}
	finance["salvage_value"] = 1
	req("PUT", depPath, finance, 400)
	finance["purchase_cost"] = 100
	finance["salvage_value"] = 0
	finance["start_date"] = "2099-01-01"
	dep = req("PUT", depPath, finance, 200)
	if toFloat64(dep["accumulated_depreciation"]) != 0 {
		t.Fatal("future start depreciated")
	}
	req("PUT", fmt.Sprintf("%s/assets/%d/depreciation", base, assets[2]), finance, 403)
	lifePath := fmt.Sprintf("%s/assets/%d/lifecycle", base, assets[0])
	req("PUT", lifePath, map[string]any{"purchase_date": "2020-01-01", "warranty_expiry": "2021-01-01", "vendor": "QA vendor"}, 200)
	req("PUT", lifePath, map[string]any{"purchase_date": "2022-01-01", "warranty_expiry": "2021-01-01"}, 400)
	if asInt(req("GET", base+"/assets/warranty-alerts", nil, 200)["total"]) != 1 {
		t.Fatal("expired warranty omitted")
	}
	req("PUT", lifePath, map[string]any{"purchase_date": nil, "warranty_expiry": nil, "vendor": ""}, 200)
	if asInt(req("GET", base+"/assets/warranty-alerts", nil, 200)["total"]) != 0 {
		t.Fatal("cleared warranty retained")
	}
	token = plain
	req("PUT", depPath, finance, 403)
	token = orgAdmin
	if asInt(req("GET", base+"/assets", nil, 200)["total"]) != 5 {
		t.Fatal("organization grant did not include unassigned assets")
	}
	req("POST", fmt.Sprintf("%s/assets/%d/assign", base, assets[3]), map[string]any{"department_id": root}, 200)
	token = admin
	// A second organization never becomes accessible through department grants.
	foreignTenant := asInt(req("POST", "/api/v1/tenants", map[string]any{"name": "Access foreign", "code": "access-foreign"}, 200)["id"])
	foreignProject := asInt(req("POST", "/api/v1/projects", map[string]any{"tenant_id": foreignTenant, "name": "Foreign", "code": "foreign"}, 200)["id"])
	token = plain
	req("GET", fmt.Sprintf("/api/v1/projects/%d/assets", foreignProject), nil, 403)
	// Explicit read-only project roles still restrict operation capabilities.
	var viewer int64
	tx.Table("roles").Select("id").Where("tenant_id=? AND code='viewer'", tenant).Take(&viewer)
	if err := tx.Exec("INSERT INTO memberships(tenant_id,project_id,user_id,role_id) VALUES (?,?,?,?)", tenant, project, users["access-user"], viewer).Error; err != nil {
		t.Fatal(err)
	}
	req("POST", fmt.Sprintf("%s/assets/%d/actions", base, assets[0]), map[string]any{"action": "on"}, 403)
	req("GET", base+"/assets", nil, 200)
	// A preexisting API key shares its owner's current data scope.
	key, hash, err := newIAMKey()
	if err != nil {
		t.Fatal(err)
	}
	var keyID int64
	if err := tx.Raw("INSERT INTO api_keys(tenant_id,project_id,user_id,name,key_prefix,secret_hash,expires_at) VALUES (?,?,?,'QA key',?,?,now()+interval '1 hour') RETURNING id", tenant, project, users["access-user"], key[:12], hash).Scan(&keyID).Error; err != nil {
		t.Fatal(err)
	}
	if err := tx.Exec("INSERT INTO api_key_permissions(tenant_id,project_id,api_key_id,permission_id) SELECT ?,?,?,id FROM permissions WHERE code='assets:read' AND deleted_at IS NULL", tenant, project, keyID).Error; err != nil {
		t.Fatal(err)
	}
	token = key
	if asInt(req("GET", base+"/assets", nil, 200)["total"]) != 4 {
		t.Fatal("key data scope mismatch")
	}
	req("GET", fmt.Sprintf("%s/assets/%d", base, assets[2]), nil, 404)

	// Revocation applies to existing JWTs and to inherited child access immediately.
	token = admin
	req("PUT", grantPath("access-user"), map[string]any{"grants": []any{}}, 200)
	token = plain
	req("GET", base+"/assets", nil, 403)
	token = key
	req("GET", base+"/assets", nil, 403)
}

func TestFinanceValidation(t *testing.T) {
	cost, salvage := 1.0, 0.0
	valid := depreciationInput{PurchaseCost: &cost, SalvageValue: &salvage, UsefulLifeMonths: 36, StartDate: "2026-11-18"}
	if err := valid.validate(); err != nil {
		t.Fatal(err)
	}
	for _, months := range []int{-1, 0, 1201} {
		in := valid
		in.UsefulLifeMonths = months
		if in.validate() == nil {
			t.Fatalf("invalid months %d accepted", months)
		}
	}
	bad := valid
	bad.StartDate = "2026-02-30"
	if bad.validate() == nil {
		t.Fatal("invalid date accepted")
	}
	bad = valid
	bad.Method = "double_declining"
	if bad.validate() == nil {
		t.Fatal("unsupported method accepted")
	}
}
