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

func TestDepartmentRelationsIntegration(t *testing.T) {
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
			t.Fatalf("%s %s: status %d, want %d: %s", method, path, w.Code, want, w.Body.String())
		}
		var result map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if data, ok := result["data"].(map[string]any); ok {
			return data
		}
		return result
	}
	token = request("POST", "/api/v1/auth/login", map[string]any{"username": "integration-admin", "password": "test-only-password-2026"}, 200)["token"].(string)
	var fixture struct{ ID, TenantID int64 }
	if err := tx.Table("projects").Select("id,tenant_id").Where("deleted_at IS NULL AND status='active'").Order("id").Take(&fixture).Error; err != nil {
		t.Fatal(err)
	}
	base := fmt.Sprintf("/api/v1/projects/%d", fixture.ID)
	root := asInt(request("POST", base+"/departments", map[string]any{"name": "relations-root"}, 200)["id"])
	child := asInt(request("POST", base+"/departments", map[string]any{"name": "relations-child", "parent_id": root}, 200)["id"])
	childPath := fmt.Sprintf("%s/departments/%d", base, child)
	rootPath := fmt.Sprintf("%s/departments/%d", base, root)
	request("PUT", rootPath, map[string]any{"parent_id": child}, 409)
	request("DELETE", rootPath, nil, 409)
	// Explicit null moves to root; an omitted parent preserves the relationship.
	request("PUT", childPath, map[string]any{"parent_id": nil}, 200)
	var parent *int64
	tx.Raw("SELECT parent_id FROM departments WHERE id=?", child).Scan(&parent)
	if parent != nil {
		t.Fatal("null parent did not move department to root")
	}
	request("PUT", childPath, map[string]any{"parent_id": root}, 200)
	request("PUT", childPath, map[string]any{"description": "keep parent"}, 200)
	var parentID int64
	tx.Raw("SELECT parent_id FROM departments WHERE id=?", child).Scan(&parentID)
	if parentID != root {
		t.Fatal("omitted parent was cleared")
	}
	asset := map[string]any{"name": "department-relations-asset", "bmc_address": "2001:db8:ffff::1", "bmc_user": "test", "bmc_secret": "test-only-secret", "bmc_protocol": "ipmi", "department_id": child}
	id := asInt(request("POST", base+"/assets", asset, 200)["id"])
	assetPath := fmt.Sprintf("%s/assets/%d", base, id)
	assertDepartment := func(want int64) {
		t.Helper()
		data := request("GET", assetPath, nil, 200)
		if asInt(data["department_id"]) != want {
			t.Fatalf("department %v, want %d", data["department_id"], want)
		}
	}
	assertDepartment(child)
	request("PUT", assetPath, map[string]any{"model": "preserve-membership"}, 200)
	assertDepartment(child)
	request("DELETE", childPath, nil, 409)
	request("POST", assetPath+"/assign", map[string]any{}, 400)
	assertDepartment(child)
	request("POST", assetPath+"/assign", map[string]any{"department_id": -1}, 400)
	request("PUT", assetPath, map[string]any{"department_id": 999999999}, 409)
	assertDepartment(child)
	// A valid department from a different organization must not be assignable.
	var otherTenant int64
	if err := tx.Raw("INSERT INTO tenants(name,code,status) VALUES ('relations-other','relations-other','active') RETURNING id").Scan(&otherTenant).Error; err != nil {
		t.Fatal(err)
	}
	var otherDept int64
	if err := tx.Raw("INSERT INTO departments(tenant_id,name) VALUES (?,'relations-other') RETURNING id", otherTenant).Scan(&otherDept).Error; err != nil {
		t.Fatal(err)
	}
	request("PUT", assetPath, map[string]any{"department_id": otherDept}, 409)
	request("POST", assetPath+"/assign", map[string]any{"department_id": otherDept}, 404)
	assertDepartment(child)
	request("POST", base+"/assets/batch-assign", map[string]any{"asset_ids": []int64{id, 999999999}, "department_id": root}, 404)
	assertDepartment(child)
	result := request("POST", base+"/assets/batch-assign", map[string]any{"asset_ids": []int64{id, id}, "department_id": root}, 200)
	if asInt(result["updated_count"]) != 1 {
		t.Fatal("duplicate IDs were not deduplicated")
	}
	assertDepartment(root)
	request("POST", assetPath+"/assign", map[string]any{"department_id": child}, 200)
	// Power state, rather than the legacy status column, drives online statistics.
	if err := tx.Table("assets").Where("id=?", id).Updates(map[string]any{"power_state": "on", "status": "unknown"}).Error; err != nil {
		t.Fatal(err)
	}
	stats := request("GET", childPath+"/stats", nil, 200)
	if asInt(stats["asset_count"]) != 1 || asInt(stats["online_count"]) != 1 {
		t.Fatalf("unexpected stats: %v", stats)
	}
	request("PUT", assetPath, map[string]any{"department_id": nil}, 200)
	assertDepartment(0)
	// Departments can be used in any project of the same organization.
	var project int64
	if err := tx.Raw("INSERT INTO projects(tenant_id,name,code,status) VALUES (?,'relations-second','relations-second','active') RETURNING id", fixture.TenantID).Scan(&project).Error; err != nil {
		t.Fatal(err)
	}
	second := fmt.Sprintf("/api/v1/projects/%d", project)
	asset["bmc_address"] = "2001:db8:ffff::2"
	id2 := asInt(request("POST", second+"/assets", asset, 200)["id"])
	request("DELETE", childPath, nil, 409)
	stats = request("GET", childPath+"/stats", nil, 200)
	if asInt(stats["asset_count"]) != 0 {
		t.Fatal("stats leaked another project's assets")
	}
	request("POST", assetPath+"/assign", map[string]any{"department_id": 0}, 200)
	request("PUT", assetPath, map[string]any{"datacenter_id": 999999999}, 409)
	request("DELETE", fmt.Sprintf("%s/assets/%d", second, id2), nil, 200)
	request("DELETE", childPath, nil, 200)
	request("POST", assetPath+"/assign", map[string]any{"department_id": child}, 404)
	asset["bmc_address"] = "2001:db8:ffff::3"
	request("POST", base+"/assets", asset, 409)
	request("DELETE", rootPath, nil, 200)
}
