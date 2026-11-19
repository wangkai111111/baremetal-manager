package platform

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"baremetal-manager/backend/migrations"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestUserInitialCredentials(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		p, err := newUserPassword()
		if err != nil || len(p) != 32 || seen[p] {
			t.Fatal("invalid or repeated generated password")
		}
		seen[p] = true
	}
	for _, v := range []string{"user@example.com", "first.last+tag@example.com"} {
		if !validUserEmail(v) {
			t.Fatal(v)
		}
	}
	for _, v := range []string{"", "user", "Name <user@example.com>", "a@example.com,b@example.com", "a@example.com\r\nBcc:b@example.com"} {
		if validUserEmail(v) {
			t.Fatal("invalid email accepted")
		}
	}
}

// This test runs only against an explicitly supplied disposable database.
// Mail is captured in-process; no transport or worker is started.
func TestUserOnboardingIntegration(t *testing.T) {
	dsn := os.Getenv("PLATFORM_TEST_DSN")
	if dsn == "" {
		t.Skip("requires disposable database")
	}
	t.Setenv("JWT_SECRET", "platform-test-secret-32-bytes-long-only")
	t.Setenv("BMC_ENCRYPTION_KEY", "12345678901234567890123456789012")
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	pool, _ := db.DB()
	defer pool.Close()
	if err = migrations.Apply(db); err != nil {
		t.Fatal(err)
	}
	if err = migrations.Apply(db); err != nil {
		t.Fatal("migration not idempotent", err)
	}
	if err = migrations.Verify(db); err != nil {
		t.Fatal(err)
	}
	tx := db.Begin()
	defer tx.Rollback()
	seedIntegrationAdmin(t, tx)
	app, err := New(tx)
	if err != nil {
		t.Fatal(err)
	}
	router := app.Router()
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
		router.ServeHTTP(w, req)
		if w.Code != want {
			t.Fatalf("%s %s status %d want %d: %s", method, path, w.Code, want, w.Body.String())
		}
		if strings.Contains(path, "/users") && w.Header().Get("Cache-Control") != "no-store" && method == "POST" {
			t.Fatal("initial credentials may be cached")
		}
		var out map[string]any
		if json.Unmarshal(w.Body.Bytes(), &out) != nil {
			t.Fatal("invalid JSON")
		}
		if data, ok := out["data"].(map[string]any); ok {
			return data
		}
		return out
	}
	login := func(username, password string, want int) map[string]any {
		t.Helper()
		return request("POST", "/api/v1/auth/login", map[string]any{"username": username, "password": password}, want)
	}
	token = login("integration-admin", "test-only-password-2026", 200)["token"].(string)
	adminToken := token
	var project struct{ ID, TenantID int64 }
	if err = tx.Table("projects").Where("deleted_at IS NULL AND status='active'").Order("id").Take(&project).Error; err != nil {
		t.Fatal(err)
	}
	base := fmt.Sprintf("/api/v1/projects/%d", project.ID)
	// Missing SMTP rejects the entire create; invalid recipient never reaches sender.
	if err = tx.Exec("DELETE FROM system_settings WHERE key LIKE 'smtp.%'").Error; err != nil {
		t.Fatal(err)
	}
	if request("GET", "/api/v1/users/options", nil, 200)["smtp_ready"] != false {
		t.Fatal("unconfigured SMTP ready")
	}
	request("POST", "/api/v1/auth/users", map[string]any{"username": "onboarding-missing-smtp", "email": "a@example.com", "send_email": true}, 400)
	var count int64
	tx.Table("users").Where("username='onboarding-missing-smtp'").Count(&count)
	if count != 0 {
		t.Fatal("failed validation created user")
	}
	request("POST", "/api/v1/auth/users", map[string]any{"username": "onboarding-invalid", "email": "Name <a@example.com>"}, 400)
	request("POST", "/api/v1/auth/users", map[string]any{"username": "onboarding-short", "password": "short"}, 400)
	// Blank password is generated once, stored as a bcrypt hash, and force policy persists.
	created := request("POST", "/api/v1/auth/users", map[string]any{"username": "onboarding-forced", "must_change_password": true}, 200)
	password := created["initial_password"].(string)
	uid := asInt(created["id"])
	if len(password) != 32 || created["password_generated"] != true || created["must_change_password"] != true || created["email_status"] != "skipped" {
		t.Fatal("wrong initial credential response")
	}
	var stored user
	tx.Table("users").Where("id=?", uid).Take(&stored)
	if !stored.MustChangePassword || bcrypt.CompareHashAndPassword([]byte(stored.PasswordHash), []byte(password)) != nil {
		t.Fatal("credentials not hashed/persisted")
	}
	listed, _ := json.Marshal(request("GET", "/api/v1/users", nil, 200))
	if bytes.Contains(listed, []byte(password)) || bytes.Contains(listed, []byte("password_hash")) || bytes.Contains(listed, []byte("password_version")) {
		t.Fatal("credentials exposed in users list")
	}
	var audits string
	if err = tx.Raw("SELECT coalesce(jsonb_agg(to_jsonb(a))::text,'') FROM audits a WHERE target_type='users' AND target_id=?", fmt.Sprint(uid)).Scan(&audits).Error; err != nil {
		t.Fatal(err)
	}
	if strings.Contains(audits, password) || strings.Contains(audits, stored.PasswordHash) {
		t.Fatal("credentials exposed in audit")
	}
	result := login("onboarding-forced", password, 200)
	token = result["token"].(string)
	if result["user"].(map[string]any)["must_change_password"] != true {
		t.Fatal("missing login policy")
	}
	request("GET", "/api/v1/auth/me", nil, 200)
	if request("GET", base+"/assets", nil, 403)["password_change_required"] != true {
		t.Fatal("business API not gated")
	}
	request("GET", "/api/v1/users/options", nil, 403)
	request("PUT", fmt.Sprintf("/api/v1/users/%d", uid), map[string]any{"must_change_password": false}, 403)
	request("PUT", "/api/v1/auth/password", map[string]any{"old_password": "wrong", "new_password": "new-password-2026"}, 403)
	request("PUT", "/api/v1/auth/password", map[string]any{"old_password": password, "new_password": password}, 400)
	request("PUT", "/api/v1/auth/password", map[string]any{"old_password": password, "new_password": strings.Repeat("中", 25)}, 400)
	request("PUT", "/api/v1/auth/password", map[string]any{"old_password": password, "new_password": "new-password-2026"}, 200)
	request("GET", "/api/v1/auth/me", nil, 401)
	login("onboarding-forced", password, 401)
	result = login("onboarding-forced", "new-password-2026", 200)
	if result["user"].(map[string]any)["must_change_password"] != false {
		t.Fatal("force flag not cleared")
	}
	tx.Table("users").Where("id=?", uid).Take(&stored)
	if stored.PasswordVersion != 1 {
		t.Fatal("session version not incremented")
	}
	token = adminToken
	// Saved SMTP settings (including secret) are used, transport errors do not roll back accounts.
	request("PUT", "/api/v1/settings", map[string]any{"smtp.host": "smtp.example.com", "smtp.port": 587, "smtp.username": "sender@example.com", "smtp.password": "test-only-smtp-secret", "smtp.from": "sender@example.com", "smtp.tls": true}, 200)
	if request("GET", "/api/v1/users/options", nil, 200)["smtp_ready"] != true {
		t.Fatal("saved SMTP not ready")
	}
	sends := 0
	failSend := false
	app.userMailSender = func(ctx context.Context, in smtpTestRequest, subject, body string) error {
		sends++
		if in.Host != "smtp.example.com" || in.Port != 587 || in.Username != "sender@example.com" || in.Password != "test-only-smtp-secret" || in.From != "sender@example.com" || !in.TLS || in.To != "recipient@example.com" || subject == "" || !strings.Contains(body, "manual-password-2026") {
			t.Fatal("saved SMTP/credentials not used")
		}
		if failSend {
			return errors.New("transport failed")
		}
		return nil
	}
	for i := 0; i < 2; i++ {
		failSend = i == 1
		r := request("POST", "/api/v1/auth/users", map[string]any{"username": fmt.Sprintf("onboarding-mail-%d", i), "password": "manual-password-2026", "email": "recipient@example.com", "send_email": true, "must_change_password": false}, 200)
		want := "sent"
		if failSend {
			want = "failed"
		}
		if r["email_status"] != want || r["password_generated"] != false || r["initial_password"] != nil {
			t.Fatal("incorrect email/password result")
		}
		login(fmt.Sprintf("onboarding-mail-%d", i), "manual-password-2026", 200)
	}
	request("POST", "/api/v1/auth/users", map[string]any{"username": "onboarding-mail-0", "password": "manual-password-2026", "email": "recipient@example.com", "send_email": true}, 409)
	if sends != 2 {
		t.Fatal("duplicate create sent email")
	}
	// Force requirements apply even to platform admins and their pre-existing API keys.
	key := request("POST", base+"/api-keys", map[string]any{"name": "force-password-test", "permissions": []string{"assets:read"}}, 200)["key"].(string)
	var admin user
	tx.Table("users").Where("username='integration-admin'").Take(&admin)
	tx.Table("users").Where("id=?", admin.ID).Update("must_change_password", true)
	request("GET", "/api/v1/users", nil, 403)
	token = key
	request("GET", base+"/assets", nil, 403)
	token = adminToken
	request("GET", "/api/v1/auth/me", nil, 200)
}
