package platform

import (
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func assetQueryTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(postgres.New(postgres.Config{DSN: "host=127.0.0.1 user=test dbname=test sslmode=disable"}), &gorm.Config{DryRun: true, DisableAutomaticPing: true})
	if err != nil {
		t.Fatal(err)
	}
	return db
}

func TestAssetSearchCondition(t *testing.T) {
	auto, err := assetSearchCondition("auto")
	if err != nil || strings.Count(auto, "?") != 8 {
		t.Fatalf("auto search: %s, %v", auto, err)
	}
	for _, field := range []string{"id", "name", "bmc_address", "management_ip", "serial_number", "manufacturer", "model", "datacenter"} {
		condition, err := assetSearchCondition(field)
		if err != nil || strings.Count(condition, "?") != 1 || !strings.Contains(auto, condition) {
			t.Errorf("field %s: %s, %v", field, condition, err)
		}
	}
	for _, fragment := range []string{"assets.id::text ILIKE ?", "host(assets.bmc_address)", "host(assets.management_ip)", "dc.tenant_id=assets.tenant_id", "dc.project_id=assets.project_id", "dc.deleted_at IS NULL"} {
		if !strings.Contains(auto, fragment) {
			t.Errorf("missing %s", fragment)
		}
	}
	for _, field := range []string{"", "status", "power_state", "name OR 1=1", "Name"} {
		if _, err := assetSearchCondition(field); err == nil {
			t.Errorf("accepted invalid field %q", field)
		}
	}
}

func TestAssetListQuerySQL(t *testing.T) {
	db := assetQueryTestDB(t)
	condition, _ := assetSearchCondition("name")
	build := func() *gorm.DB {
		base := queryScope(db, resource{table: "assets"}, scope{Tenant: 11, Project: 22, Admin: true})
		// 确认派生查询内部仍保留资产归属限制。
		base = base.Where("EXISTS (SELECT 1 FROM orders WHERE orders.asset_id=assets.id AND orders.tenant_id=? AND orders.status='completed')", int64(11))
		return assetListQuery(base, condition, " needle' OR 1=1 -- ", " production, blue ,,", "running", 33)
	}
	var total int64
	count := build().Count(&total)
	if count.Error != nil {
		t.Fatal(count.Error)
	}
	sql := count.Statement.SQL.String()
	for _, fragment := range []string{"SELECT count(*) FROM (SELECT assets.*, COALESCE(", " AS display_status FROM \"assets\"", "tenant_id=$1 AND project_id=$2 AND deleted_at IS NULL", "orders.asset_id=assets.id", "assets.name ILIKE $4", "assets.datacenter_id=$5", "t.name IN ($6,$7)", ") AS assets WHERE assets.display_status=$8", "at.tenant_id=assets.tenant_id", "at.project_id=assets.project_id", "at.deleted_at IS NULL", "t.deleted_at IS NULL"} {
		if !strings.Contains(sql, fragment) {
			t.Errorf("missing %q in SQL: %s", fragment, sql)
		}
	}
	wantVars := []any{int64(11), int64(22), int64(11), "%needle' OR 1=1 --%", int64(33), "production", "blue", "running"}
	if !reflect.DeepEqual(count.Statement.Vars, wantVars) {
		t.Errorf("vars = %#v, want %#v", count.Statement.Vars, wantVars)
	}
	if strings.Contains(sql, "needle") || strings.Contains(sql, "LIMIT $") || strings.Contains(sql, "OFFSET") {
		t.Error("search must be bound and count must not be paginated")
	}
	var rows []struct{ Doc string }
	list := build().Order("id DESC").Limit(20).Offset(40).Select("(to_jsonb(assets)" + hidden + ")::text AS doc").Find(&rows)
	if list.Error != nil {
		t.Fatal(list.Error)
	}
	sql = list.Statement.SQL.String()
	for _, fragment := range []string{"to_jsonb(assets)", "- 'bmc_secret_ciphertext'", "AS display_status", "assets.display_status=$8", "ORDER BY id DESC LIMIT $9 OFFSET $10"} {
		if !strings.Contains(sql, fragment) {
			t.Errorf("missing %q in list SQL: %s", fragment, sql)
		}
	}
	if strings.Contains(sql, "UPDATE") {
		t.Error("display status must not update stored status")
	}
	empty := assetListQuery(queryScope(db, resource{table: "assets"}, scope{Tenant: 11, Project: 22, Admin: true}), condition, "", " , ", "", 0).Count(&total)
	if len(empty.Statement.Vars) != 2 {
		t.Errorf("empty filters added parameters: %#v", empty.Statement.Vars)
	}
}

func TestAssetDisplayStatusSQL(t *testing.T) {
	sql := assetDisplayStatusSQL
	for _, fragment := range []string{
		"op.tenant_id=assets.tenant_id AND op.project_id=assets.project_id",
		"op.asset_id=assets.id AND op.deleted_at IS NULL",
		"WHEN op.status IN ('pending','retry_wait') THEN 'queued'",
		"WHEN op.kind='power_on' THEN 'starting'", "WHEN op.kind='power_off' THEN 'stopping'",
		"WHEN op.kind='power_cycle' THEN 'restarting'",
		"WHEN op.kind='collect' AND assets.collected_at IS NULL THEN 'managing'",
		"WHEN op.kind='collect' THEN 'collecting'", "WHEN op.kind='install' THEN 'reinstalling'",
		"WHEN op.kind='firmware_upgrade' THEN 'upgrading'",
		"ORDER BY CASE WHEN op.status='running' THEN 0 ELSE 1 END, op.created_at DESC, op.id DESC LIMIT 1",
		"op.status='unknown' AND op.created_at>COALESCE(assets.collected_at,assets.created_at)",
		"orders.tenant_id=assets.tenant_id AND orders.asset_id=assets.id",
		"orders.type='purchase' AND orders.status='completed' AND orders.deleted_at IS NULL",
	} {
		if !strings.Contains(sql, fragment) {
			t.Errorf("missing status rule %q", fragment)
		}
	}
	previous := -1
	for _, status := range []string{"THEN assets.status", "THEN 'error'", "THEN 'unknown'", "THEN 'stopped'", "THEN 'in_use'", "THEN 'running'", "THEN 'pending_management'", "THEN 'offline'"} {
		index := strings.Index(sql, status)
		if index <= previous {
			t.Errorf("invalid precedence for %s", status)
		}
		previous = index
	}
	if strings.Contains(sql, "orders.project_id") || strings.Contains(sql, "'reinstall'") || strings.Contains(sql, "'online'") {
		t.Error("status SQL assumes fields/kinds or online assignment not supported by schema")
	}
}

func TestListAssetsRejectsInvalidSearchField(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("GET", "/?search_field=bad", nil)
	c.Set("scope", scope{Tenant: 1, Project: 2, Admin: true})
	a := &App{store: &store{db: assetQueryTestDB(t)}}
	a.listResource(resource{table: "assets"})(c)
	if c.Writer.Status() != 400 {
		t.Fatalf("got %d, want 400", c.Writer.Status())
	}
}
