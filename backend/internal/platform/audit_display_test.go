package platform

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestAuditSourceMetadata(t *testing.T) {
	sc := scope{User: 7, Audit: auditMeta{RequestID: "request", ClientIP: "::ffff:198.51.100.24", UserAgent: "browser"}}
	row := auditRow(sc, auditMeta{}, "create", "tags", 1, "success")
	if row["client_ip"] != "198.51.100.24" || row["request_id"] != "request" || row["user_agent"] != "browser" {
		t.Fatal("request metadata was not propagated")
	}
	row = auditRow(sc, auditMeta{ClientIP: "invalid injected value"}, "create", "tags", 1, "success")
	if _, exists := row["client_ip"]; exists {
		t.Fatal("invalid source IP must not reach the inet column")
	}
	row = auditRow(scope{}, auditMeta{}, "create", "tags", 1, "success")
	if row["request_id"] == "" {
		t.Fatal("background audit requires a request identifier")
	}
}

func TestReadableAuditIntegration(t *testing.T) {
	dsn := os.Getenv("PLATFORM_TEST_DSN")
	if dsn == "" {
		t.Skip("requires disposable migrated database")
	}
	t.Setenv("JWT_SECRET", "platform-test-secret-32-bytes-long-only")
	t.Setenv("BMC_ENCRYPTION_KEY", "12345678901234567890123456789012")
	t.Setenv("TRUSTED_PROXY_CIDRS", "127.0.0.1/32")
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	conn, _ := db.DB()
	defer conn.Close()
	tx := db.Begin()
	defer tx.Rollback()
	seedIntegrationAdmin(t, tx)
	a, err := New(tx)
	if err != nil {
		t.Fatal(err)
	}
	request := func(method, path, body, token string) map[string]any {
		t.Helper()
		r := httptest.NewRequest(method, path, bytes.NewBufferString(body))
		r.RemoteAddr = "127.0.0.1:12345"
		r.Header.Set("X-Forwarded-For", "198.51.100.24")
		r.Header.Set("User-Agent", "audit-test-browser")
		r.Header.Set("Content-Type", "application/json")
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		a.Router().ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatalf("%s %s failed: %d %s", method, path, w.Code, w.Body.String())
		}
		var out map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	login := request("POST", "/api/v1/auth/login", `{"username":"integration-admin","password":"test-only-password-2026"}`, "")
	token := login["data"].(map[string]any)["token"].(string)
	var sc scope
	if err := tx.Raw(`SELECT p.tenant_id AS tenant, p.id AS project, u.id AS user FROM projects p CROSS JOIN users u
 WHERE p.deleted_at IS NULL AND p.status='active' AND u.username='integration-admin' ORDER BY p.id LIMIT 1`).Scan(&sc).Error; err != nil {
		t.Fatal(err)
	}
	sc.Admin = true
	base := fmt.Sprintf("/api/v1/projects/%d", sc.Project)
	created := request("POST", base+"/tags", `{"name":"审计定位原名","color":"#334455"}`, token)
	id := asInt(created["data"].(map[string]any)["id"])
	request("PUT", fmt.Sprintf("%s/tags/%d", base, id), `{"name":"审计定位新名"}`, token)
	request("DELETE", fmt.Sprintf("%s/tags/%d", base, id), "", token)
	// Invalid/foreign targets must not resolve another project's resource names.
	var foreignName string
	if err := tx.Raw(`SELECT COALESCE((`+auditTargetSQL("e")+`),'missing') FROM
 (SELECT ?::bigint AS tenant_id, -1::bigint AS project_id, 'tags'::text AS target_type, ?::text AS target_id) e`, sc.Tenant, fmt.Sprint(id)).Scan(&foreignName).Error; err != nil || foreignName != "missing" {
		t.Fatalf("cross-project target resolution: %s %v", foreignName, err)
	}
	// Even a later hard deletion cannot erase the name saved with the evidence.
	if err := tx.Exec("DELETE FROM tags WHERE id=?", id).Error; err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]int{"审计定位原名": 1, "审计定位新名": 2} {
		out := request("GET", base+"/audit?search="+name, "", token)["data"].(map[string]any)
		if int(out["total"].(float64)) != want {
			t.Fatalf("name search returned %v, want %d", out["total"], want)
		}
		for _, item := range out["items"].([]any) {
			row := item.(map[string]any)
			if row["target_name"] != name || row["client_ip"] != "198.51.100.24" || row["target_type_label"] != "标签" || !strings.Contains(fmt.Sprint(row["operator_name"]), "integration-admin") {
				t.Fatalf("unreadable or incomplete audit: %v", row)
			}
			if _, exists := row["after_snapshot"]; exists {
				t.Fatal("private snapshot leaked")
			}
		}
	}
	request("PUT", "/api/v1/settings", `{"smtp.password":"audit-secret-must-never-leak"}`, token)
	settings := request("GET", base+"/audit?search=邮件登录密码", "", token)
	encoded, _ := json.Marshal(settings)
	if strings.Contains(string(encoded), "audit-secret-must-never-leak") || !strings.Contains(string(encoded), "邮件登录密码") {
		t.Fatal("settings audit must contain the setting name without its secret value")
	}
	// The export has the same scope restrictions and contains no identifier columns.
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/?format=csv&search=审计定位", nil)
	c.Set("scope", sc)
	a.exportAuditLogs(c)
	rows, err := csv.NewReader(strings.NewReader(w.Body.String())).ReadAll()
	if err != nil || w.Code != 200 || len(rows) != 4 {
		t.Fatalf("audit export: %d, %v", w.Code, err)
	}
	for _, header := range rows[0] {
		if strings.Contains(header, "id") {
			t.Fatal("export contains an identifier column")
		}
	}
	blocked, err := decodeRows(queryScope(tx, resource{table: "audits"}, scope{Tenant: sc.Tenant, Project: sc.Project, User: sc.User}), resource{table: "audits"})
	if err != nil || len(blocked) != 0 {
		t.Fatal("non-administrator audit visibility changed")
	}
}
