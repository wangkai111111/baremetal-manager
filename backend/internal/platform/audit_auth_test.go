package platform

import (
	"baremetal-manager/backend/migrations"
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
)

func TestAuditAuthScopeIntegration(t *testing.T) {
	dsn := os.Getenv("PLATFORM_TEST_DSN")
	if dsn == "" {
		t.Skip("requires disposable database")
	}
	t.Setenv("JWT_SECRET", "platform-test-secret-32-bytes-long-only")
	t.Setenv("BMC_ENCRYPTION_KEY", "12345678901234567890123456789012")
	t.Setenv("TRUSTED_PROXY_CIDRS", "127.0.0.1/32")
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	pool, _ := db.DB()
	defer pool.Close()
	if err = migrations.Apply(db); err != nil {
		t.Fatal(err)
	}
	if err = migrations.Apply(db); err != nil {
		t.Fatal(err)
	}
	if err = migrations.Verify(db); err != nil {
		t.Fatal(err)
	}
	tx := db.Begin()
	defer tx.Rollback()
	seedIntegrationAdmin(t, tx)
	a, err := New(tx)
	if err != nil {
		t.Fatal(err)
	}
	token := ""
	selectedProject := int64(0)
	call := func(method, path string, body any, want int) (map[string]any, string) {
		t.Helper()
		raw, _ := json.Marshal(body)
		r := httptest.NewRequest(method, path, bytes.NewReader(raw))
		r.Header.Set("Content-Type", "application/json")
		if selectedProject > 0 {
			r.Header.Set("X-Project-ID", fmt.Sprint(selectedProject))
		}
		r.RemoteAddr = "127.0.0.1:54321"
		r.Header.Set("X-Forwarded-For", "198.51.100.82")
		r.Header.Set("User-Agent", "audit-integration-browser")
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		a.Router().ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s: got %d want %d: %s", method, path, w.Code, want, w.Body.String())
		}
		out := map[string]any{}
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		if data, ok := out["data"].(map[string]any); ok {
			return data, w.Body.String()
		}
		return out, w.Body.String()
	}
	req := func(method, path string, body any, want int) map[string]any {
		r, _ := call(method, path, body, want)
		return r
	}
	login := func(name, password string) string {
		t.Helper()
		return req("POST", "/api/v1/auth/login", map[string]any{"username": name, "password": password}, 200)["token"].(string)
	}
	password := "test-only-password-2026"
	admin := login("integration-admin", password)
	token = admin
	tenant := asInt(req("POST", "/api/v1/tenants", map[string]any{"name": "Audit QA", "code": "audit-auth-qa"}, 200)["id"])
	project := asInt(req("POST", "/api/v1/projects", map[string]any{"tenant_id": tenant, "name": "Audit QA", "code": "audit-auth-qa"}, 200)["id"])
	base := fmt.Sprintf("/api/v1/projects/%d", project)
	selectedProject = project
	secondProject := asInt(req("POST", "/api/v1/projects", map[string]any{"tenant_id": tenant, "name": "Audit second project", "code": "audit-auth-second"}, 200)["id"])
	foreignTenant := asInt(req("POST", "/api/v1/tenants", map[string]any{"name": "Audit foreign organization", "code": "audit-auth-foreign"}, 200)["id"])
	req("POST", "/api/v1/projects", map[string]any{"tenant_id": foreignTenant, "name": "Audit foreign project", "code": "audit-auth-foreign"}, 200)

	dept := func(name string, parent int64) int64 {
		return asInt(req("POST", base+"/departments", map[string]any{"name": name, "parent_id": parent}, 200)["id"])
	}
	root := dept("Audit parent", 0)
	child := dept("Audit child", root)
	sibling := dept("Audit sibling", 0)
	users := map[string]int64{}
	for _, name := range []string{"audit-manager", "audit-plain", "audit-sibling", "audit-org"} {
		users[name] = asInt(req("POST", fmt.Sprintf("/api/v1/users?project_id=%d", project), map[string]any{"username": name, "password": password, "role": "user"}, 200)["id"])
	}
	grant := func(name string, deps []any, manager bool) {
		t.Helper()
		grants := []any{}
		for _, d := range deps {
			grants = append(grants, map[string]any{"department_id": d, "is_admin": manager})
		}
		req("PUT", fmt.Sprintf("%s/users/%d/access-grants", base, users[name]), map[string]any{"grants": grants}, 200)
	}
	grant("audit-manager", []any{root}, true)
	grant("audit-plain", []any{child, sibling}, false)
	grant("audit-sibling", []any{sibling}, true)
	grant("audit-org", []any{nil}, true)
	asset := func(name string, d int64, n int) int64 {
		return asInt(req("POST", base+"/assets", map[string]any{"name": name, "department_id": d, "bmc_address": fmt.Sprintf("2001:db8:ada::%d", n), "bmc_protocol": "ipmi", "bmc_user": "qa", "bmc_secret": "do-not-record-bmc-secret"}, 200)["id"])
	}
	owned := asset("audit-owned-original", child, 1)
	foreign := asset("audit-foreign", sibling, 2)
	sc := scope{Tenant: tenant, Project: project, User: users["audit-plain"], Audit: auditMeta{ClientIP: "198.51.100.82"}}
	for _, id := range []int64{owned, foreign} {
		if e := a.store.audit(tx, sc, "initial-access:read", "assets", id); e != nil {
			t.Fatal(e)
		}
	}
	// Persist a readable task submission tied to the target, even when its actor has
	// grants in two sibling departments.
	var operationID int64
	if err = tx.Raw(`INSERT INTO operations(tenant_id,project_id,asset_id,user_id,kind,status,idempotency_key) VALUES (?,?,?,?,'power_on','succeeded','audit-scope-qa') RETURNING id`, tenant, project, owned, sc.User).Scan(&operationID).Error; err != nil {
		t.Fatal(err)
	}
	if err = a.store.audit(tx, sc, "submit", "operations", operationID); err != nil {
		t.Fatal(err)
	}
	manager := login("audit-manager", password)
	plain := login("audit-plain", password)
	siblingToken := login("audit-sibling", password)
	org := login("audit-org", password)
	list := func(query string) []any {
		t.Helper()
		return req("GET", fmt.Sprintf("/api/v1/audit?project_id=%d&", project)+query, nil, 200)["items"].([]any)
	}
	token = admin
	req("POST", fmt.Sprintf("/api/v1/projects/%d/assets", secondProject), map[string]any{"name": "audit-second-project-server", "department_id": child, "bmc_address": "2001:db8:ada::3", "bmc_protocol": "ipmi", "bmc_user": "qa", "bmc_secret": "do-not-record-bmc-secret"}, 200)
	token = manager
	global := req("GET", "/api/v1/audit?search=audit-second-project-server", nil, 200)
	if global["total"].(float64) != 1 {
		t.Fatal("organization audit omitted managed asset in another project")
	}
	if len(list("search=audit-second-project-server")) != 0 {
		t.Fatal("explicit project filter ignored")
	}
	if req("GET", "/api/v1/audit?search="+url.QueryEscape("Audit foreign project"), nil, 200)["total"].(float64) != 0 {
		t.Fatal("foreign organization audit leaked")
	}
	if len(list("search=audit-owned-original")) != 3 || len(list("search=audit-foreign")) != 0 {
		t.Fatal("department audit visibility leaks sibling operations")
	}
	loginRows := list("action=auth:login&operator=audit-plain")
	if len(loginRows) == 1 && strings.Contains(fmt.Sprint(loginRows[0].(map[string]any)["scope_names"]), "Audit sibling") {
		t.Fatal("login exposed unmanaged scope labels")
	}
	if len(loginRows) != 1 {
		t.Fatal("department user login not visible")
	}
	var auditID int64
	for _, v := range list("search=audit-owned-original") {
		r := v.(map[string]any)
		auditID = asInt(r["id"])
		if r["client_ip"] != "198.51.100.82" {
			t.Fatal("operator IP missing")
		}
		raw, _ := json.Marshal(r)
		if strings.Contains(string(raw), "do-not-record") || r["after_snapshot"] != nil {
			t.Fatal("audit leaks secrets")
		}
	}
	req("GET", fmt.Sprintf("/api/v1/audit/%d?project_id=%d", auditID, project), nil, 200)
	// Changing asset assignment cannot move historical audit evidence to a sibling.
	if err = tx.Table("assets").Where("id=?", owned).Updates(map[string]any{"department_id": sibling, "name": "audit-moved-name"}).Error; err != nil {
		t.Fatal(err)
	}
	if len(list("search=audit-owned-original")) != 3 {
		t.Fatal("moved asset erased original audit scope")
	}
	if err = a.store.audit(tx, sc, "task:succeeded", "operations", operationID); err != nil {
		t.Fatal(err)
	}
	if len(list("search=audit-moved-name&action=task:")) != 1 {
		t.Fatal("task completion lost original audience")
	}
	token = siblingToken
	if len(list("search=audit-owned-original")) != 0 || len(list("search=audit-moved-name&action=task:")) != 0 {
		t.Fatal("history followed asset to new department")
	}
	req("GET", fmt.Sprintf("/api/v1/audit/%d?project_id=%d", auditID, project), nil, 404)
	_, csvText := call("GET", fmt.Sprintf("/api/v1/audit/export?project_id=%d&format=csv", project), nil, 200)
	records, e := csv.NewReader(strings.NewReader(csvText)).ReadAll()
	if e != nil || len(records) < 2 || strings.Contains(csvText, "audit-owned-original") {
		t.Fatal("export scope bypass")
	}
	token = plain
	req("GET", fmt.Sprintf("/api/v1/audit?project_id=%d", project), nil, 403)
	req("GET", base+"/audit", nil, 403)
	token = org
	if len(list("search=audit-owned-original")) != 3 || len(list("search=audit-foreign")) != 2 {
		t.Fatal("organization admin audit incomplete")
	}
	token = admin
	req("POST", "/api/v1/auth/login", map[string]any{"username": "audit-plain", "password": "wrong-password-no-log"}, 401)
	req("POST", "/api/v1/auth/login", map[string]any{"username": "unknown-audit-qa", "password": "wrong-password-no-log"}, 401)
	if len(list("action=auth:login&result=failed&operator=audit-plain")) != 1 {
		t.Fatal("failed login not audited")
	}
	_, all := call("GET", "/api/v1/audit?page_size=100", nil, 200)
	for _, secret := range []string{password, "wrong-password-no-log", "do-not-record-bmc-secret", admin} {
		if strings.Contains(all, secret) {
			t.Fatal("credential leaked into audit feed")
		}
	}
	// Independent logins in the same second must have distinct tokens; logging out
	// one session must not invalidate another session for the same user.
	second := login("audit-plain", password)
	if second == plain {
		t.Fatal("login token not unique")
	}
	token = plain
	req("POST", "/api/v1/auth/logout", nil, 200)
	req("GET", "/api/v1/auth/me", nil, 401)
	token = second
	req("GET", "/api/v1/auth/me", nil, 200)
	req("PUT", "/api/v1/auth/password", map[string]any{"old_password": password, "new_password": "audit-changed-password-2026"}, 200)
	req("GET", "/api/v1/auth/me", nil, 401)
	token = manager
	if len(list("action=auth:logout&operator=audit-plain")) != 1 || len(list("action=auth:password-change&operator=audit-plain")) != 1 {
		t.Fatal("logout/password change missing")
	}
	// Detail, filtering and exporting share the same policy after revocation.
	token = admin
	grant("audit-plain", []any{sibling}, false)
	token = manager
	accessEvents := list("action=access:manage&search=audit-plain")
	revoked := false
	for _, event := range accessEvents {
		r := event.(map[string]any)
		if strings.Contains(fmt.Sprint(r["description"]), "Audit sibling") {
			t.Fatal("grant event exposed sibling branch")
		}
		if strings.Contains(fmt.Sprint(r["description"]), "撤销访问权限") {
			revoked = true
		}
	}
	if !revoked {
		t.Fatal("revoked subject grant erased administrator evidence")
	}
	token = admin
	grant("audit-manager", nil, true)
	token = manager
	req("GET", fmt.Sprintf("/api/v1/audit?project_id=%d", project), nil, 403)
	// Unknown authentication events are platform-only, with nullable scope/user FKs.
	token = admin
	unknown := req("GET", "/api/v1/audit?search="+url.QueryEscape("未验证账号"), nil, 200)
	if unknown["total"].(float64) < 1 {
		t.Fatal("unknown login failure not retained")
	}
}

func TestAuditCSVText(t *testing.T) {
	for input, want := range map[string]string{"name": "name", "192.0.2.1": "192.0.2.1", "=SUM(1,2)": "'=SUM(1,2)", "  +cmd": "'  +cmd", "@cmd": "'@cmd"} {
		if got := auditCSVCell(input); got != want {
			t.Fatalf("CSV input %q got %q want %q", input, got, want)
		}
	}
}
