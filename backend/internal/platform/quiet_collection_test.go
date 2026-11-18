package platform

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"sync/atomic"
	"testing"

	"baremetal-manager/backend/internal/service"
	"github.com/gin-gonic/gin"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// Exercise the real claim/finalization path against a local fake Redfish server.
// No worker may reach a copied production outbox entry in this disposable DB.
func TestQuietAutomaticCollectionIntegration(t *testing.T) {
	dsn := os.Getenv("PLATFORM_TEST_DSN")
	if dsn == "" {
		t.Skip("requires disposable migrated database")
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
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(tx.Exec("UPDATE task_outbox SET status='published',locked_until=NULL").Error)
	var sc scope
	must(tx.Raw("SELECT p.tenant_id AS tenant,p.id AS project,u.id AS user FROM projects p CROSS JOIN users u WHERE p.deleted_at IS NULL AND p.status='active' AND u.username='integration-admin' ORDER BY p.id LIMIT 1").Scan(&sc).Error)
	sc.Admin = true
	var fail atomic.Bool
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Error("collection sent non-GET request")
			http.Error(w, "read only", 405)
			return
		}
		if fail.Load() {
			http.Error(w, "mock unavailable", 503)
			return
		}
		switch r.URL.Path {
		case "/redfish/v1/Systems":
			fmt.Fprint(w, `{"Members":[{"@odata.id":"/redfish/v1/Systems/1"}]}`)
		case "/redfish/v1/Systems/1":
			fmt.Fprint(w, `{"Manufacturer":"Quiet Test","Model":"Mock Hardware","PowerState":"On","ProcessorSummary":{"Count":2},"MemorySummary":{"TotalSystemMemoryGiB":64}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	u, _ := url.Parse(server.URL)
	host, port, _ := net.SplitHostPort(u.Host)
	p, _ := strconv.Atoi(port)
	secret, err := service.EncryptCredential("quiet-test-secret")
	must(err)
	var asset int64
	must(tx.Raw(`INSERT INTO assets(tenant_id,project_id,name,bmc_address,bmc_port,bmc_user,bmc_secret_ciphertext,bmc_secret_key_id,bmc_tls_verify)
 VALUES (?,?,'quiet-collection-test',?,?,'test',?,'v1',false) RETURNING id`, sc.Tenant, sc.Project, host, p, []byte(secret)).Scan(&asset).Error)
	app := &App{store: &store{tx}, node: "quiet-collection-test"}
	count := func(table, where string, args ...any) int64 {
		t.Helper()
		var n int64
		must(tx.Table(table).Where(where, args...).Count(&n).Error)
		return n
	}
	enqueue := func() operation {
		t.Helper()
		must(enqueueAutomaticCollection(tx, sc, asset))
		var op operation
		must(tx.Table("operations").Where("asset_id=? AND automatic AND status='pending'", asset).Take(&op).Error)
		return op
	}
	contextFor := func(id int64) (*httptest.ResponseRecorder, *gin.Context) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("GET", "/", nil)
		c.Set("scope", sc)
		c.Params = gin.Params{{Key: "id", Value: strconv.FormatInt(id, 10)}}
		return w, c
	}
	summary := func() map[string]any {
		t.Helper()
		w, c := contextFor(asset)
		app.collection(c)
		if w.Code != 200 {
			t.Fatalf("summary: %d", w.Code)
		}
		var body map[string]any
		must(json.Unmarshal(w.Body.Bytes(), &body))
		return body["data"].(map[string]any)
	}
	assertGone := func(op operation) {
		t.Helper()
		for _, table := range []string{"operations", "task_outbox", "operation_logs"} {
			key := "operation_id=?"
			if table == "operations" {
				key = "id=?"
			}
			if count(table, key, op.ID) != 0 {
				t.Fatalf("automatic history retained in %s", table)
			}
		}
	}
	checkFence := func(want int, detached bool) {
		t.Helper()
		var lease struct {
			FencingToken int
			OperationID  *int64
		}
		must(tx.Table("device_leases").Where("asset_id=?", asset).Take(&lease).Error)
		if lease.FencingToken != want || (lease.OperationID == nil) != detached {
			t.Fatalf("lease lost fencing state: %#v", lease)
		}
	}
	op := enqueue()
	var visible int64
	must(queryScope(tx, resource{table: "operations"}, sc).Where("id=?", op.ID).Count(&visible).Error)
	if visible != 0 {
		t.Fatal("automatic queue exposed in task list")
	}
	w, c := contextFor(op.ID)
	app.getTask(c)
	if w.Code != 404 {
		t.Fatal("automatic task detail exposed")
	}
	if summary()["status"] != "pending" {
		t.Fatal("automatic collection progress missing")
	}
	must(operationLog(tx, op, 1, "info", "should not be persisted"))
	if count("operation_logs", "operation_id=?", op.ID) != 0 {
		t.Fatal("automatic execution log persisted")
	}
	// Old automatic logs are also hidden by generic list/detail/export scope.
	must(tx.Table("operation_logs").Create(map[string]any{"tenant_id": sc.Tenant, "project_id": sc.Project, "operation_id": op.ID, "sequence": 1, "level": "info", "message": "legacy poll"}).Error)
	must(queryScope(tx, resource{table: "operation_logs"}, sc).Where("operation_id=?", op.ID).Count(&visible).Error)
	if visible != 0 {
		t.Fatal("legacy automatic log exposed")
	}
	_, err = app.store.enqueue(context.Background(), sc, asset, "collect", "manual-while-auto", auditMeta{})
	if !errors.Is(err, errAutomaticCollectionBusy) {
		t.Fatalf("manual action returned hidden internal task: %v", err)
	}
	app.runOne(context.Background())
	assertGone(op)
	checkFence(1, true)
	if summary()["status"] != "succeeded" {
		t.Fatal("snapshot missing after automatic queue cleanup")
	}
	manual, err := app.store.enqueue(context.Background(), sc, asset, "collect", "quiet-manual", auditMeta{})
	must(err)
	fail.Store(true)
	app.runOne(context.Background())
	checkFence(2, false)
	if count("operations", "id=? AND status='failed' AND NOT automatic", manual.ID) != 1 || count("operation_logs", "operation_id=?", manual.ID) != 2 {
		t.Fatal("manual failure/logs were removed")
	}
	fail.Store(false)
	op = enqueue()
	app.runOne(context.Background())
	assertGone(op)
	checkFence(3, true)
	if summary()["status"] != "succeeded" {
		t.Fatal("old manual failure overrode new automatic snapshot")
	}
	fail.Store(true)
	op = enqueue()
	app.runOne(context.Background())
	assertGone(op)
	checkFence(4, true)
	if summary()["status"] != "failed" {
		t.Fatal("automatic failure state lost during cleanup")
	}
	// A disabled/retired asset skips the request but still removes its queue record.
	op = enqueue()
	must(tx.Table("assets").Where("id=?", asset).Update("status", "retired").Error)
	app.runOne(context.Background())
	assertGone(op)
	checkFence(5, true)
	// An expired worker claim is reconciled without replay or keeping poll history.
	must(tx.Table("assets").Where("id=?", asset).Update("status", "unknown").Error)
	op = enqueue()
	claimed, event, err := app.claim(context.Background())
	must(err)
	if claimed.ID != op.ID || event.ID == 0 {
		t.Fatal("test claim missing")
	}
	must(tx.Exec("UPDATE task_outbox SET locked_until=now()-interval '1 second' WHERE id=?", event.ID).Error)
	must(tx.Exec("UPDATE device_leases SET heartbeat_at=now()-interval '3 seconds',expires_at=now()-interval '1 second' WHERE operation_id=?", op.ID).Error)
	_, event, err = app.claim(context.Background())
	must(err)
	if event.ID != 0 {
		t.Fatal("expired collection replayed")
	}
	assertGone(op)
	checkFence(6, true)
	// Manual logs remain visible, including after later automatic polling.
	must(queryScope(tx, resource{table: "operation_logs"}, sc).Where("operation_id=?", manual.ID).Count(&visible).Error)
	if visible != 2 {
		t.Fatal("manual logs hidden by automatic filter")
	}
}
