package platform

import (
	"os"
	"strings"
	"testing"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestResourceDisplayIntegration(t *testing.T) {
	dsn := os.Getenv("PLATFORM_TEST_DSN")
	if dsn == "" {
		t.Skip("requires disposable migrated database")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	conn, _ := db.DB()
	defer conn.Close()
	tx := db.Begin()
	defer tx.Rollback()
	// Resolve every registered resource against the real schema, including empty
	// tables, so a new label lookup cannot silently break an unrelated page.
	for _, r := range resources {
		if _, err := decodeRows(queryScope(tx, r, scope{Tenant: -1, Project: -1, Admin: true}).Limit(1), r); err != nil {
			t.Fatalf("%s display query: %v", r.path, err)
		}
	}
	for _, statement := range []string{
		`CREATE TEMP TABLE assets (id bigint, tenant_id bigint, project_id bigint, name text, management_ip inet, bmc_address inet, serial_number text, deleted_at timestamptz) ON COMMIT DROP`,
		`CREATE TEMP TABLE operations (id bigint, tenant_id bigint, project_id bigint, asset_id bigint, user_id bigint, deleted_at timestamptz) ON COMMIT DROP`,
		`CREATE TEMP TABLE users (id bigint, username text, name text, deleted_at timestamptz) ON COMMIT DROP`,
		`CREATE TEMP TABLE work_orders (id bigint, user_id bigint, assigned_to bigint) ON COMMIT DROP`,
		`INSERT INTO assets VALUES (101,11,22,'可见节点','192.0.2.10','192.0.2.11','SN123',NULL), (102,99,22,'其他组织机密',NULL,NULL,NULL,NULL), (103,11,99,'其他项目机密',NULL,NULL,NULL,NULL)`,
		`INSERT INTO users VALUES (7,'operator','运维人员',NULL), (8,'admin','admin',NULL)`,
		`INSERT INTO operations VALUES (1,11,22,101,7,NULL),(2,11,22,102,7,NULL),(3,11,22,103,7,NULL)`,
		`INSERT INTO work_orders VALUES (1,7,8)`,
	} {
		if err := tx.Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
	var docs []struct{ Doc string }
	r := resource{table: "operations", readonly: true}
	if err := tx.Table("operations").Select("(" + resourceDisplaySQL(r) + ")::text AS doc").Order("id").Scan(&docs).Error; err != nil {
		t.Fatal(err)
	}
	if len(docs) != 3 || !strings.Contains(docs[0].Doc, "可见节点") || !strings.Contains(docs[0].Doc, "192.0.2.10") || !strings.Contains(docs[0].Doc, "SN123") || !strings.Contains(docs[0].Doc, "运维人员 · operator") {
		t.Fatalf("unreadable identity: %v", docs)
	}
	for _, doc := range docs[1:] {
		if strings.Contains(doc.Doc, "机密") {
			t.Fatal("cross-scope resource identity leaked")
		}
	}
	var ticket struct{ SubmitterName, AssigneeName string }
	if err := tx.Table("work_orders").Select(workOrderDisplayColumns()).Take(&ticket).Error; err != nil {
		t.Fatal(err)
	}
	if ticket.SubmitterName != "运维人员 · operator" || ticket.AssigneeName != "admin" {
		t.Fatalf("unreadable ticket participants: %+v", ticket)
	}
}
