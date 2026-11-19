package platform

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestSettingCatalogMatchesSupportedSettings(t *testing.T) {
	defaults := allSettingDefaults()
	seen := map[string]bool{}
	for _, option := range settingCatalog() {
		if seen[option.Key] || option.Label == "" || option.Description == "" || option.Category == "" {
			t.Fatalf("incomplete/duplicate option: %s", option.Key)
		}
		seen[option.Key] = true
		value, ok := defaults[option.Key]
		if !ok || !reflect.DeepEqual(value, option.Default) || !validSetting(option.Key, option.Default) {
			t.Fatalf("unsupported default: %s", option.Key)
		}
		switch option.Kind {
		case "text", "textarea", "color", "secret":
			if _, ok := value.(string); !ok {
				t.Fatal(option.Key)
			}
		case "boolean":
			if _, ok := value.(bool); !ok {
				t.Fatal(option.Key)
			}
		case "number":
			if option.Key != "smtp.port" {
				t.Fatal(option.Key)
			}
		default:
			t.Fatalf("unknown control: %s", option.Kind)
		}
	}
	if len(seen) != len(defaults) {
		t.Fatal("some supported settings have no UI control")
	}
}

func TestAlertRuleValidation(t *testing.T) {
	n := 1.0
	base := alertRuleValues{Name: "健康", Metric: "bmc.health", Operator: "gte", Threshold: &n, Severity: "warning", Selector: json.RawMessage(`{}`)}
	if err := base.validate(); err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]func(*alertRuleValues){
		"empty name":           func(r *alertRuleValues) { r.Name = " " },
		"unsupported metric":   func(r *alertRuleValues) { r.Metric = "cpu.usage" },
		"unsupported duration": func(r *alertRuleValues) { r.DurationSeconds = 30 },
		"unsupported operator": func(r *alertRuleValues) { r.Operator = "missing" },
		"missing threshold":    func(r *alertRuleValues) { r.Threshold = nil },
		"out of range":         func(r *alertRuleValues) { v := 3.0; r.Threshold = &v },
		"fraction":             func(r *alertRuleValues) { v := 1.5; r.Threshold = &v },
		"NaN":                  func(r *alertRuleValues) { v := math.NaN(); r.Threshold = &v },
		"severity":             func(r *alertRuleValues) { r.Severity = "bad" },
		"selector null":        func(r *alertRuleValues) { r.Selector = json.RawMessage(`null`) },
		"selector field":       func(r *alertRuleValues) { r.Selector = json.RawMessage(`{"asset_id":1}`) },
		"duplicate tags":       func(r *alertRuleValues) { r.Selector = json.RawMessage(`{"tags":["A","A"]}`) },
		"blank tags":           func(r *alertRuleValues) { r.Selector = json.RawMessage(`{"tags":[""]}`) },
		"failure condition":    func(r *alertRuleValues) { r.Metric = "bmc.collection_failed" },
	} {
		t.Run(name, func(t *testing.T) {
			r := base
			change(&r)
			if r.validate() == nil {
				t.Fatal("invalid rule accepted")
			}
		})
	}
	failure := base
	failure.Metric = "bmc.collection_failed"
	failure.Operator = "collection_failed"
	failure.Threshold = nil
	if err := failure.validate(); err != nil {
		t.Fatal(err)
	}
}

// All changes are made in a rollback transaction on a disposable database.
// This verifies real constraints, atomic rule associations and routing without
// running a worker or sending notifications.
func TestAlertSettingsIntegration(t *testing.T) {
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
	tx := db.Begin()
	defer tx.Rollback()
	seedIntegrationAdmin(t, tx)
	app, err := New(tx)
	if err != nil {
		t.Fatal(err)
	}
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
		app.Router().ServeHTTP(w, req)
		if w.Code != want {
			t.Fatalf("%s %s: %d want %d: %s", method, path, w.Code, want, w.Body.String())
		}
		var result map[string]any
		if json.Unmarshal(w.Body.Bytes(), &result) != nil {
			t.Fatal("bad response")
		}
		if data, ok := result["data"].(map[string]any); ok {
			return data
		}
		return result
	}
	token = request("POST", "/api/v1/auth/login", map[string]any{"username": "integration-admin", "password": "test-only-password-2026"}, 200)["token"].(string)
	var project struct{ ID, TenantID int64 }
	if err := tx.Table("projects").Where("deleted_at IS NULL AND status='active'").Order("id").Take(&project).Error; err != nil {
		t.Fatal(err)
	}
	base := fmt.Sprintf("/api/v1/projects/%d", project.ID)
	createChannel := func(name string) int64 {
		return asInt(request("POST", base+"/alert-channels", map[string]any{"name": name, "kind": "webhook", "config": map[string]any{}, "secret": "https://example.com/hook/test-only", "enabled": true}, 200)["id"])
	}
	c1, c2 := createChannel("atomic-channel-1"), createChannel("atomic-channel-2")
	channelPath := fmt.Sprintf("%s/alert-channels/%d", base, c1)
	channel := request("GET", channelPath, nil, 200)
	if channel["has_secret"] != true {
		t.Fatal("saved secret indicator missing")
	}
	for _, k := range []string{"secret_ciphertext", "secret_key_id", "secret"} {
		if _, ok := channel[k]; ok {
			t.Fatal("secret exposed")
		}
	}
	request("PUT", channelPath, map[string]any{"name": "atomic-channel-renamed", "enabled": false}, 200)
	if request("GET", channelPath, nil, 200)["has_secret"] != true {
		t.Fatal("blank edit lost saved secret")
	}
	request("PUT", channelPath, map[string]any{"name": "atomic-channel-renamed", "kind": "email", "config": map[string]any{"host": "smtp.example.com", "port": 587, "username": "", "from": "a@example.com", "to": "b@example.com", "tls": true}, "secret": ""}, 200)
	if request("GET", channelPath, nil, 200)["has_secret"] != false {
		t.Fatal("secret not cleared")
	}
	rule := map[string]any{"name": "atomic-rule", "metric": "bmc.health", "operator": "gte", "threshold": 1, "duration_seconds": 0, "severity": "warning", "enabled": true, "selector": map[string]any{}, "channel_ids": []int64{c1, c2}}
	rid := asInt(request("POST", base+"/alert-rules", rule, 200)["id"])
	rp := fmt.Sprintf("%s/alert-rules/%d", base, rid)
	check := func(want int) {
		t.Helper()
		row := request("GET", rp, nil, 200)
		if len(row["channel_ids"].([]any)) != want {
			t.Fatal("wrong associated channels")
		}
	}
	check(2)
	var otherProject, foreignChannel int64
	if err := tx.Raw("INSERT INTO projects(tenant_id,name,code,status) VALUES (?,'alert-isolation','alert-isolation','active') RETURNING id", project.TenantID).Scan(&otherProject).Error; err != nil {
		t.Fatal(err)
	}
	if err := tx.Raw("INSERT INTO alert_channels(tenant_id,project_id,name,kind,config) VALUES (?,?,'foreign-channel','webhook','{}') RETURNING id", project.TenantID, otherProject).Scan(&foreignChannel).Error; err != nil {
		t.Fatal(err)
	}
	request("PUT", rp, map[string]any{"channel_ids": []int64{foreignChannel}}, 400)
	request("POST", base+"/alert-rules", map[string]any{"name": "invalid-create", "metric": "bmc.collection_failed", "operator": "collection_failed", "severity": "warning", "channel_ids": []int64{foreignChannel}}, 400)
	var partial int64
	if err := tx.Table("alert_rules").Where("name='invalid-create'").Count(&partial).Error; err != nil || partial != 0 {
		t.Fatal("invalid association left a partially created rule")
	}
	request("PUT", rp, map[string]any{"enabled": false}, 200)
	check(2)
	request("PUT", rp, map[string]any{"name": "must-not-persist", "channel_ids": []int64{c1, 9223372036854775807}}, 400)
	if request("GET", rp, nil, 200)["name"] != "atomic-rule" {
		t.Fatal("invalid channel partially updated rule")
	}
	check(2)
	request("PUT", rp, map[string]any{"channel_ids": []int64{c1, c1}}, 400)
	request("PUT", rp, map[string]any{"channel_ids": []int64{c2}}, 200)
	check(1)
	request("PUT", rp, map[string]any{"channel_ids": []int64{c1, c2}}, 200)
	check(2)
	request("DELETE", channelPath, nil, 409)
	request("DELETE", rp, nil, 200)
	request("DELETE", channelPath, nil, 200)
	request("DELETE", fmt.Sprintf("%s/alert-channels/%d", base, c2), nil, 200)
	settings := request("GET", "/api/v1/settings", nil, 200)
	if len(settings["catalog"].([]any)) != len(allSettingDefaults()) {
		t.Fatal("settings catalog incomplete")
	}
	request("PUT", "/api/v1/settings", map[string]any{"smtp.port": 465, "smtp.tls": false, "app.footer_visible": false}, 200)
	request("PUT", "/api/v1/settings", map[string]any{"smtp.port": "587", "app.footer_visible": true}, 400)
	settings = request("GET", "/api/v1/settings", nil, 200)
	for _, item := range settings["items"].([]any) {
		row := item.(map[string]any)
		if row["key"] == "smtp.port" && row["value"] != float64(465) {
			t.Fatal("port typed save failed")
		}
		if row["key"] == "app.footer_visible" && row["value"] != false {
			t.Fatal("invalid batch partially saved")
		}
	}
	// Escalation routes critical notifications once; subsequent identical samples
	// are suppressed and recovery reaches the channel that saw the escalation.
	var asset struct{ ID int64 }
	if err := tx.Table("assets").Where("tenant_id=? AND project_id=? AND deleted_at IS NULL", project.TenantID, project.ID).Take(&asset).Error; err != nil {
		t.Fatal(err)
	}
	c3 := createChannel("escalation-channel")
	rule["name"] = "critical-only-rule"
	rule["severity"] = "critical"
	rule["channel_ids"] = []int64{c3}
	rule["threshold"] = 2
	request("POST", base+"/alert-rules", rule, 200)
	// Isolate the fixture from unrelated live-clone monitoring rules/incidents.
	if err := tx.Exec("UPDATE alert_rules SET enabled=false WHERE tenant_id=? AND project_id=? AND name<>?", project.TenantID, project.ID, "critical-only-rule").Error; err != nil {
		t.Fatal(err)
	}
	if err := tx.Exec("UPDATE alerts SET status='resolved',resolved_at=now() WHERE tenant_id=? AND project_id=? AND asset_id=? AND status IN ('firing','acknowledged')", project.TenantID, project.ID, asset.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := tx.Exec("UPDATE alert_shields SET deleted_at=now() WHERE tenant_id=? AND project_id=?", project.TenantID, project.ID).Error; err != nil {
		t.Fatal(err)
	}
	op := operation{TenantID: project.TenantID, ProjectID: project.ID, AssetID: asset.ID}
	for _, sample := range []struct {
		s        string
		v        int
		resolved bool
	}{{"warning", 1, false}, {"critical", 2, false}, {"critical", 2, false}, {"ok", 0, true}} {
		if err := monitorAlert(tx, op, "bmc.health", sample.s, sample.v, sample.resolved); err != nil {
			t.Fatal(err)
		}
	}
	var eventTypes []string
	if err := tx.Raw("SELECT e.event_type FROM notification_deliveries d JOIN alert_events e ON e.id=d.alert_event_id WHERE d.channel_id=? ORDER BY e.id", c3).Scan(&eventTypes).Error; err != nil {
		t.Fatal(err)
	}
	if strings.Join(eventTypes, ",") != "repeated,resolved" {
		t.Fatalf("bad escalation routing: %v", eventTypes)
	}
}
