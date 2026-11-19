package platform

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestBrandModernSettingsOverrideLegacy(t *testing.T) {
	rows := []publicSettingRow{
		{"brand.platform_name", `"新平台"`}, {"platform_name", `"旧平台"`},
		{"theme.nav_bg", `"#123456"`}, {"nav_color", `"#abcdef"`},
		{"brand.logo_url", `""`}, {"logo_url", `"https://example.com/old.png"`},
		{"app.footer_visible", `false`}, {"smtp.password", `"secret-must-not-be-public"`},
	}
	for _, reverse := range []bool{false, true} {
		input := append([]publicSettingRow{}, rows...)
		if reverse {
			for i, j := 0, len(input)-1; i < j; i, j = i+1, j-1 {
				input[i], input[j] = input[j], input[i]
			}
		}
		result := map[string]any{}
		mergePublicSettingRows(result, input)
		if result["brand.platform_name"] != "新平台" || result["theme.nav_bg"] != "#123456" || result["brand.logo_url"] != "" || result["app.footer_visible"] != false {
			t.Fatal("legacy row order changed explicit saved settings")
		}
		if _, exists := result["smtp.password"]; exists {
			t.Fatal("SMTP password exposed")
		}
	}
	fallback := map[string]any{}
	mergePublicSettingRows(fallback, []publicSettingRow{{"nav_color", `"#abcdef"`}, {"theme.nav_bg", `false`}})
	if fallback["theme.nav_bg"] != "#abcdef" {
		t.Fatal("invalid modern value suppressed valid legacy fallback")
	}
}

func TestBrandSettingsPersistenceIntegration(t *testing.T) {
	dsn := os.Getenv("PLATFORM_TEST_DSN")
	if dsn == "" {
		t.Skip("requires disposable database")
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
	var uid int64
	tx.Table("users").Select("id").Where("username='integration-admin'").Scan(&uid)
	app := &App{store: &store{db: tx}}
	router := gin.New()
	router.Use(func(c *gin.Context) { c.Set("user", user{ID: uid, IsPlatformAdmin: true}) })
	router.PUT("/settings", app.updateSettings)
	router.GET("/brand", app.brand)
	request := func(method, path string, data map[string]any) *httptest.ResponseRecorder {
		t.Helper()
		body, _ := json.Marshal(data)
		req := httptest.NewRequest(method, path, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		return w
	}
	// Keep the legacy row present to reproduce the mixed-key configuration.
	if err := tx.Exec(`INSERT INTO system_settings(key,value,create_by) VALUES ('platform_name','"Legacy brand"'::jsonb,?) ON CONFLICT (key) WHERE deleted_at IS NULL DO UPDATE SET value=EXCLUDED.value`, uid).Error; err != nil {
		t.Fatal(err)
	}
	settings := map[string]any{"brand.platform_name": "Saved brand", "brand.logo_url": "http://intranet/logo.png", "theme.nav_bg": "#123456", "theme.nav_text": "#abcdef"}
	if w := request("PUT", "/settings", settings); w.Code != 200 {
		t.Fatalf("save returned %d", w.Code)
	}
	for i := 0; i < 5; i++ {
		w := request("GET", "/brand", nil)
		var result struct{ Data map[string]any }
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if w.Code != 200 || result.Data["platform_name"] != "Saved brand" || result.Data["nav_bg_color"] != "#123456" || result.Data["logo_url"] != "http://intranet/logo.png" || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("saved brand was not consistently returned")
		}
	}
	if w := request("PUT", "/settings", map[string]any{"brand.platform_name": "Must not persist", "theme.nav_bg": false}); w.Code != 400 {
		t.Fatalf("invalid batch returned %d", w.Code)
	}
	if app.publicSettings()["brand.platform_name"] != "Saved brand" {
		t.Fatal("invalid batch partially persisted")
	}
	if w := request("PUT", "/settings", map[string]any{"brand.logo_url": ""}); w.Code != 200 || app.publicSettings()["brand.logo_url"] != "" {
		t.Fatal("logo removal did not persist")
	}
}
