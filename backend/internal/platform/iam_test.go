package platform

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestIAMKeyMaterial(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		key, hash, e := newIAMKey()
		if e != nil {
			t.Fatal(e)
		}
		sum := sha256.Sum256([]byte(key))
		if len(key) != 68 || !strings.HasPrefix(key, "bmm_") || hash != hex.EncodeToString(sum[:]) || seen[key] {
			t.Fatal("invalid or repeated key material")
		}
		seen[key] = true
	}
}
func TestIAMStatus(t *testing.T) {
	for _, s := range []string{"active", "disabled", "locked"} {
		if !validIAMStatus(s) {
			t.Fatal(s)
		}
	}
	for _, s := range []string{"", "admin", "inactive", "ACTIVE"} {
		if validIAMStatus(s) {
			t.Fatal(s)
		}
	}
}
func TestIAMEarlyDenials(t *testing.T) {
	gin.SetMode(gin.TestMode)
	a := &App{}
	for _, tc := range []struct {
		method, body, id string
		admin            bool
		handler          gin.HandlerFunc
	}{
		{"DELETE", "", "7", true, a.iamWriteUser},
		{"PUT", `{"status":"invalid"}`, "8", true, a.iamWriteUser},
		{"POST", `{"username":"new","password":"short"}`, "", true, a.iamWriteUser},
		{"PUT", `{"role":"manager"}`, "7", true, a.iamWriteMember},
		{"POST", `{}`, "", false, a.iamWriteRole},
		{"POST", `{"name":"key","expires_at":"2000-01-01T00:00:00Z"}`, "", true, a.iamCreateKey},
	} {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(tc.method, "/", strings.NewReader(tc.body))
		c.Request.Header.Set("Content-Type", "application/json")
		c.Params = gin.Params{{Key: "id", Value: tc.id}}
		c.Set("user", user{ID: 7, IsPlatformAdmin: tc.admin})
		c.Set("scope", scope{Tenant: 1, Project: 1, User: 7, Admin: tc.admin})
		tc.handler(c)
		if w.Code != 400 && w.Code != 403 {
			t.Fatalf("expected rejection, got %d", w.Code)
		}
	}
}
func TestIAMIntegration(t *testing.T) {
	dsn := os.Getenv("PLATFORM_TEST_DSN")
	if dsn == "" {
		t.Skip("PLATFORM_TEST_DSN not set")
	}
	db, e := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if e != nil {
		t.Fatal(e)
	}
	conn, e := db.DB()
	if e != nil {
		t.Fatal(e)
	}
	defer conn.Close()
	db = db.Begin()
	defer db.Rollback()
	t.Setenv("JWT_SECRET", "platform-test-secret-32-bytes-long-only")
	t.Setenv("BMC_ENCRYPTION_KEY", "12345678901234567890123456789012")
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
		if e := json.Unmarshal(w.Body.Bytes(), &out); e != nil {
			t.Fatal(e)
		}
		return w.Code, out
	}
	code, res := request("POST", "/api/v1/auth/login", `{"username":"integration-admin","password":"test-only-password-2026"}`, "")
	if code != 200 {
		t.Fatalf("login %d %v", code, res)
	}
	token := res["data"].(map[string]any)["token"].(string)
	code, res = request("GET", "/api/v1/projects", "", token)
	if code != 200 {
		t.Fatal(res)
	}
	projects := res["data"].(map[string]any)["items"].([]any)
	if len(projects) == 0 {
		t.Fatal("missing project")
	}
	pid := int64(projects[0].(map[string]any)["id"].(float64))
	base := fmt.Sprintf("/api/v1/projects/%d", pid)
	for _, path := range []string{"/roles", "/members", "/users", "/api-keys"} {
		code, res = request("GET", base+path, "", token)
		if code != 200 {
			t.Fatalf("list %s: %d %v", path, code, res)
		}
		b, _ := json.Marshal(res)
		if bytes.Contains(b, []byte("password_hash")) || bytes.Contains(b, []byte("secret_hash")) {
			t.Fatal("hash exposed")
		}
	}
	code, res = request("POST", base+"/api-keys", `{"name":"integration-key","permissions":["assets:read"]}`, token)
	if code != 200 {
		t.Fatalf("create key %d %v", code, res)
	}
	data := res["data"].(map[string]any)
	key := data["key"].(string)
	id := int64(data["id"].(float64))
	var hash string
	if e := db.Table("api_keys").Select("secret_hash").Where("id=?", id).Scan(&hash).Error; e != nil {
		t.Fatal(e)
	}
	sum := sha256.Sum256([]byte(key))
	if hash != hex.EncodeToString(sum[:]) {
		t.Fatal("key not hashed")
	}
	code, res = request("GET", base+"/api-keys", "", token)
	b, _ := json.Marshal(res)
	if code != 200 || bytes.Contains(b, []byte(key)) || bytes.Contains(b, []byte(hash)) {
		t.Fatalf("unsafe list %d %s", code, b)
	}
	code, res = request("GET", base+"/assets", "", key)
	if code != 200 {
		t.Fatalf("key read %d %v", code, res)
	}
	r := httptest.NewRequest("GET", base+"/assets", nil)
	r.Header.Set("X-API-Key", key)
	w := httptest.NewRecorder()
	a.Router().ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("X-API-Key read %d %s", w.Code, w.Body.String())
	}
	for _, tc := range []struct{ method, path, body string }{
		{"POST", base + "/assets", `{}`},
		{"GET", fmt.Sprintf("/api/v1/projects/%d/assets", pid+999999), ""},
		{"GET", "/api/v1/projects", ""},
		{"PUT", "/api/v1/auth/password", `{}`},
		{"GET", base + "/roles", ""},
		{"GET", base + "/members", ""},
		{"GET", base + "/users", ""},
		{"POST", base + "/api-keys", `{}`},
		{"POST", base + "/assets/1/sol-ticket", `{}`},
		{"POST", base + "/assets/1/actions", `{"action":"on"}`},
	} {
		code, res = request(tc.method, tc.path, tc.body, key)
		if code != 403 {
			t.Fatalf("key restriction %s %s: %d %v", tc.method, tc.path, code, res)
		}
	}
	var owner struct{ UserID, TenantID int64 }
	if e := db.Table("api_keys").Select("user_id,tenant_id").Where("id=?", id).Take(&owner).Error; e != nil {
		t.Fatal(e)
	}
	for _, tc := range []struct {
		name   string
		status int
		change func() error
	}{
		{"expired", 401, func() error {
			return db.Table("api_keys").Where("id=?", id).Update("expires_at", time.Now().Add(-time.Hour)).Error
		}},
		{"deleted", 401, func() error { return db.Table("api_keys").Where("id=?", id).Update("deleted_at", time.Now()).Error }},
		{"inactive owner", 401, func() error { return db.Table("users").Where("id=?", owner.UserID).Update("status", "disabled").Error }},
		{"inactive membership", 401, func() error {
			return db.Table("tenant_memberships").Where("tenant_id=? AND user_id=?", owner.TenantID, owner.UserID).Update("status", "disabled").Error
		}},
		{"key grant removed", 403, func() error {
			return db.Table("api_key_permissions").Where("api_key_id=?", id).Update("deleted_at", time.Now()).Error
		}},
		{"owner RBAC removed", 403, func() error {
			if e := db.Table("users").Where("id=?", owner.UserID).Update("is_platform_admin", false).Error; e != nil {
				return e
			}
			return db.Exec("UPDATE role_permissions SET deleted_at=now() WHERE tenant_id=? AND permission_id IN (SELECT id FROM permissions WHERE code='assets:read')", owner.TenantID).Error
		}},
	} {
		if e := db.SavePoint("iam_auth_case").Error; e != nil {
			t.Fatal(e)
		}
		if e := tc.change(); e != nil {
			t.Fatal(e)
		}
		code, res = request("GET", base+"/assets", "", key)
		if code != tc.status {
			t.Fatalf("%s: %d %v", tc.name, code, res)
		}
		if e := db.RollbackTo("iam_auth_case").Error; e != nil {
			t.Fatal(e)
		}
	}
	code, _ = request("DELETE", fmt.Sprintf("%s/api-keys/%d", base, id), "", token)
	if code != 200 {
		t.Fatalf("revoke %d", code)
	}
	code, _ = request("GET", base+"/assets", "", key)
	if code != 401 {
		t.Fatalf("revoked API key must be rejected: %d", code)
	}
	username := fmt.Sprintf("iam-test-%d", time.Now().UnixNano())
	code, res = request("POST", fmt.Sprintf("/api/v1/auth/users?project_id=%d", pid), fmt.Sprintf(`{"username":%q,"password":"a-strong-test-password","role":"user"}`, username), token)
	if code != 200 {
		t.Fatalf("create user %d %v", code, res)
	}
	uid := int64(res["data"].(map[string]any)["id"].(float64))
	var stored string
	db.Table("users").Select("password_hash").Where("id=?", uid).Scan(&stored)
	if bcrypt.CompareHashAndPassword([]byte(stored), []byte("a-strong-test-password")) != nil {
		t.Fatal("password not bcrypt")
	}
	code, res = request("POST", "/api/v1/auth/login", fmt.Sprintf(`{"username":%q,"password":"a-strong-test-password"}`, username), "")
	if code != 200 {
		t.Fatal(res)
	}
	userToken := res["data"].(map[string]any)["token"].(string)
	code, _ = request("POST", fmt.Sprintf("/api/v1/auth/users?project_id=%d", pid), `{"username":"escalate","password":"a-strong-test-password","role":"admin"}`, userToken)
	if code == 200 {
		t.Fatal("non-admin created administrator")
	}
	code, res = request("GET", base+"/roles", "", token)
	roles := res["data"].(map[string]any)["items"].([]any)
	if code != 200 || len(roles) == 0 {
		t.Fatal("role list missing")
	}
	if _, ok := roles[0].(map[string]any)["permissions"].([]any); !ok {
		t.Fatal("role permissions missing")
	}
}
