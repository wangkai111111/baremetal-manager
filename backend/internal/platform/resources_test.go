package platform

import (
	"encoding/json"
	"net/http/httptest"
	"os"
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
		return assetListQuery(base, condition, " needle' OR 1=1 -- ", " production, blue ,,", "running", 33)
	}
	var total int64
	count := build().Count(&total)
	if count.Error != nil {
		t.Fatal(count.Error)
	}
	sql := count.Statement.SQL.String()
	for _, fragment := range []string{"SELECT count(*) FROM (SELECT assets.*, COALESCE(", " AS display_status FROM \"assets\"", "tenant_id=$1 AND project_id=$2 AND deleted_at IS NULL", "assets.name ILIKE $3", "assets.datacenter_id=$4", "t.name IN ($5,$6)", ") AS assets WHERE assets.display_status=$7", "at.tenant_id=assets.tenant_id", "at.project_id=assets.project_id", "at.deleted_at IS NULL", "t.deleted_at IS NULL"} {
		if !strings.Contains(sql, fragment) {
			t.Errorf("missing %q in SQL: %s", fragment, sql)
		}
	}
	wantVars := []any{int64(11), int64(22), "%needle' OR 1=1 --%", int64(33), "production", "blue", "running"}
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
	for _, fragment := range []string{"to_jsonb(assets)", "- 'bmc_secret_ciphertext'", "AS display_status", "assets.display_status=$7", "ORDER BY id DESC LIMIT $8 OFFSET $9"} {
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
	} {
		if !strings.Contains(sql, fragment) {
			t.Errorf("missing status rule %q", fragment)
		}
	}
	previous := -1
	for _, status := range []string{"THEN assets.status", "THEN 'error'", "THEN 'unknown'", "THEN 'stopped'", "THEN 'running'", "THEN 'pending_management'", "THEN 'offline'"} {
		index := strings.Index(sql, status)
		if index <= previous {
			t.Errorf("invalid precedence for %s", status)
		}
		previous = index
	}
	if strings.Contains(sql, "orders") || strings.Contains(sql, "'reinstall'") || strings.Contains(sql, "'online'") {
		t.Error("status SQL assumes fields/kinds or online assignment not supported by schema")
	}
}

func TestOperationResourceDocumentSQL(t *testing.T) {
	db := assetQueryTestDB(t)
	for _, path := range []string{"tasks", "operations"} {
		var r resource
		for _, candidate := range resources {
			if candidate.path == path {
				r = candidate
			}
		}
		var rows []struct{ Doc string }
		q := queryScope(db, r, scope{Tenant: 11, Project: 22, Admin: true}).
			Where("id=?", 33).Select(resourceDocumentSQL(r)).Find(&rows)
		sql := q.Statement.SQL.String()
		for _, part := range []string{"jsonb_build_object(", "'operator_name'", "u.id=operations.user_id", "NULLIF(NULLIF(u.name,''), u.username)", "'client_ip', host(operations.client_ip)", "'bmc_ip'", "ast.tenant_id=operations.tenant_id", "ast.project_id=operations.project_id", "ast.id=operations.asset_id AND ast.deleted_at IS NULL", "'installation_stage'", "SELECT inst.status", "inst.tenant_id=operations.tenant_id", "inst.project_id=operations.project_id", "inst.operation_id=operations.id AND inst.asset_id=operations.asset_id", "operations.kind='install' AND inst.deleted_at IS NULL", "tenant_id=$1 AND project_id=$2 AND deleted_at IS NULL", "- 'payload'", "- 'result'"} {
			if !strings.Contains(sql, part) {
				t.Errorf("%s missing %q: %s", path, part, sql)
			}
		}
		if !reflect.DeepEqual(q.Statement.Vars, []any{int64(11), int64(22), 33}) {
			t.Errorf("%s scope vars: %#v", path, q.Statement.Vars)
		}
	}
	if strings.Contains(resourceDocumentSQL(resource{table: "assets"}), "installation_stage") {
		t.Fatal("task fields leaked to other resources")
	}
}

func TestOperationRequestMetaIP(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct{ remote, want string }{
		{"192.0.2.12:1234", "192.0.2.12"},
		{"[2001:db8::12]:1234", "2001:db8::12"},
		{"invalid", ""},
	} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest("POST", "/", nil)
		c.Request.RemoteAddr = tc.remote
		if got := requestMeta(c, 1).ClientIP; got != tc.want {
			t.Errorf("%q: got %q, want %q", tc.remote, got, tc.want)
		}
	}
}

func TestOperationResourceResponseIntegration(t *testing.T) {
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
	tx := db.Begin()
	if tx.Error != nil {
		t.Fatal(tx.Error)
	}
	defer tx.Rollback()
	for _, sql := range []string{
		`CREATE TEMP TABLE operations (id bigint, tenant_id bigint, project_id bigint, asset_id bigint, user_id bigint, kind text, status text, client_ip inet, deleted_at timestamptz, payload jsonb, result jsonb) ON COMMIT DROP`,
		`CREATE TEMP TABLE assets (id bigint, tenant_id bigint, project_id bigint, bmc_address inet, deleted_at timestamptz) ON COMMIT DROP`,
		`CREATE TEMP TABLE users (id bigint, name text, deleted_at timestamptz) ON COMMIT DROP`,
		`CREATE TEMP TABLE installations (operation_id bigint, asset_id bigint, tenant_id bigint, project_id bigint, status text, deleted_at timestamptz) ON COMMIT DROP`,
		`INSERT INTO operations VALUES (1,11,22,101,7,'install','running','2001:db8::12',NULL,'{}','{}'), (2,11,22,102,NULL,'collect','succeeded',NULL,NULL,'{}','{}'), (3,99,22,101,7,'install','running',NULL,NULL,'{}','{}'), (4,11,99,101,7,'install','running',NULL,NULL,'{}','{}')`,
		`INSERT INTO users VALUES (7,'操作员',NULL)`,
		`INSERT INTO assets VALUES (101,11,22,'192.0.2.1',NULL), (101,99,22,'192.0.2.99',NULL), (102,99,22,'192.0.2.98',NULL)`,
		`INSERT INTO installations VALUES (1,101,11,22,'post_install',NULL), (1,101,99,22,'failed',NULL), (1,101,11,99,'failed',NULL), (1,999,11,22,'failed',NULL), (1,101,11,22,'failed',now())`,
		`ALTER TABLE installations ADD COLUMN progress jsonb NOT NULL DEFAULT '[]'`,
		`ALTER TABLE operations ADD COLUMN automatic boolean NOT NULL DEFAULT false, ADD COLUMN error text`,
		`ALTER TABLE users ADD COLUMN username text NOT NULL DEFAULT ''`,
		`ALTER TABLE assets ADD COLUMN name text NOT NULL DEFAULT '测试服务器', ADD COLUMN management_ip inet, ADD COLUMN serial_number text`,
	} {
		if err := tx.Exec(sql).Error; err != nil {
			t.Fatal(err)
		}
	}
	a := &App{store: &store{db: tx}}
	gin.SetMode(gin.TestMode)
	for _, path := range []string{"tasks", "operations"} {
		r := resource{path: path, table: "operations"}
		for _, id := range []string{"", "1", "2", "3", "4"} {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest("GET", "/"+path+"/"+id, nil)
			c.Set("scope", scope{Tenant: 11, Project: 22, Admin: true})
			c.Params = gin.Params{{Key: "id", Value: id}}
			if id == "" {
				a.listResource(r)(c)
			} else {
				a.getResource(r)(c)
			}
			if id == "3" || id == "4" {
				if w.Code != 404 {
					t.Fatalf("cross-scope detail: %d %s", w.Code, w.Body.String())
				}
				continue
			}
			if w.Code != 200 {
				t.Fatalf("%s/%s: %d %s", path, id, w.Code, w.Body.String())
			}
			var body struct{ Data map[string]any }
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			items := []any{body.Data}
			if id == "" {
				items = body.Data["items"].([]any)
				if body.Data["total"] != float64(2) || len(items) != 2 {
					t.Fatalf("scope/pagination: %s", w.Body.String())
				}
			}
			for _, raw := range items {
				item := raw.(map[string]any)
				want := map[string]any{"operator_name": nil, "client_ip": nil, "bmc_ip": nil, "installation_stage": nil}
				if item["id"] == float64(1) {
					want = map[string]any{"operator_name": "操作员", "client_ip": "2001:db8::12", "bmc_ip": "192.0.2.1", "installation_stage": "post_install", "asset_name": "测试服务器", "asset_label": "测试服务器 · 192.0.2.1"}
				}
				for key, expected := range want {
					if value, exists := item[key]; !exists || value != expected {
						t.Errorf("%s/%s %s: %v, want %v", path, id, key, value, expected)
					}
				}
				for _, key := range []string{"payload", "result", "progress"} {
					if _, exists := item[key]; exists {
						t.Errorf("unexpected field %s", key)
					}
				}
			}
		}
	}
	// Historical tasks keep their server identity after removal. Searching a
	// displayed IP must still find the task without matching foreign assets.
	if err := tx.Exec("UPDATE assets SET deleted_at=now() WHERE tenant_id=11").Error; err != nil {
		t.Fatal(err)
	}
	for search, want := range map[string]float64{"192.0.2.1": 1, "192.0.2.99": 0} {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("GET", "/tasks?search="+search, nil)
		c.Set("scope", scope{Tenant: 11, Project: 22, Admin: true})
		a.listResource(resource{path: "tasks", table: "operations", readonly: true})(c)
		var body struct{ Data struct{ Total float64 } }
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &body) != nil || body.Data.Total != want {
			t.Fatalf("historical identity search: %d %s", w.Code, w.Body.String())
		}
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
