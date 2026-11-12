package platform

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"io"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// Scripted SQL exercises transaction decisions without a database or external services.
type monitorSQLStep struct {
	match   string
	columns []string
	rows    [][]driver.Value
}
type monitorSQLConn struct {
	t       *testing.T
	steps   []monitorSQLStep
	queries []string
}
type monitorSQLDriver struct{}
type monitorSQLConnector struct{ conn *monitorSQLConn }
type monitorSQLRows struct {
	columns []string
	rows    [][]driver.Value
}

var registerMonitorDriver sync.Once

func (monitorSQLDriver) Open(string) (driver.Conn, error)                  { return nil, errors.New("use connector") }
func (c monitorSQLConnector) Connect(context.Context) (driver.Conn, error) { return c.conn, nil }
func (monitorSQLConnector) Driver() driver.Driver                          { return monitorSQLDriver{} }
func (c *monitorSQLConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("unexpected prepare")
}
func (c *monitorSQLConn) Close() error              { return nil }
func (c *monitorSQLConn) Begin() (driver.Tx, error) { return c, nil }
func (c *monitorSQLConn) Commit() error             { return nil }
func (c *monitorSQLConn) Rollback() error           { return nil }
func (c *monitorSQLConn) step(query string) monitorSQLStep {
	c.t.Helper()
	c.queries = append(c.queries, query)
	if len(c.steps) == 0 {
		c.t.Fatalf("unexpected SQL: %s", query)
	}
	step := c.steps[0]
	c.steps = c.steps[1:]
	if !strings.Contains(query, step.match) {
		c.t.Fatalf("want SQL containing %q, got %s", step.match, query)
	}
	return step
}
func (c *monitorSQLConn) QueryContext(_ context.Context, q string, _ []driver.NamedValue) (driver.Rows, error) {
	s := c.step(q)
	return &monitorSQLRows{s.columns, s.rows}, nil
}
func (c *monitorSQLConn) ExecContext(_ context.Context, q string, _ []driver.NamedValue) (driver.Result, error) {
	c.step(q)
	return driver.RowsAffected(1), nil
}
func (r *monitorSQLRows) Columns() []string { return r.columns }
func (r *monitorSQLRows) Close() error      { return nil }
func (r *monitorSQLRows) Next(dest []driver.Value) error {
	if len(r.rows) == 0 {
		return io.EOF
	}
	copy(dest, r.rows[0])
	r.rows = r.rows[1:]
	return nil
}
func monitoringDB(t *testing.T, steps ...monitorSQLStep) (*gorm.DB, *monitorSQLConn) {
	t.Helper()
	registerMonitorDriver.Do(func() { sql.Register("monitor-script", monitorSQLDriver{}) })
	conn := &monitorSQLConn{t: t, steps: steps}
	pool := sql.OpenDB(monitorSQLConnector{conn})
	t.Cleanup(func() { pool.Close() })
	db, err := gorm.Open(postgres.New(postgres.Config{Conn: pool}), &gorm.Config{DisableAutomaticPing: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if len(conn.steps) != 0 {
			t.Errorf("%d SQL steps not executed", len(conn.steps))
		}
	})
	return db, conn
}
func TestMonitorConsecutiveFailuresAndRecovery(t *testing.T) {
	failures := 0
	for i, success := range []bool{false, false, false, true, false} {
		health, n := monitorResult(failures, success, json.RawMessage(`{"Status":{"Health":"OK"}}`))
		failures = n
		want := []int{1, 2, 3, 0, 1}[i]
		if n != want {
			t.Fatalf("sample %d: failures %d want %d", i, n, want)
		}
		if success && health != "ok" {
			t.Fatal("successful health not retained")
		}
	}
	_, n := monitorResult(2147483647, false, nil)
	if n != 2147483647 {
		t.Fatal("counter overflow")
	}
	for _, tc := range []struct{ raw, want string }{
		{`{}`, "unknown"}, {`null`, "unknown"}, {`{"Status":{"Health":"OK","HealthRollup":"Warning"}}`, "warning"},
		{`{"Status":{"Health":"Warning","HealthRollup":"Critical"}}`, "critical"}, {`{"Status":{"Health":"Unknown","HealthRollup":"OK"}}`, "unknown"},
	} {
		if got := monitorHealth(json.RawMessage(tc.raw)); got != tc.want {
			t.Errorf("%s: %s", tc.raw, got)
		}
	}
}
func TestMonitorAlertDedupAndRecovery(t *testing.T) {
	op := operation{TenantID: 11, ProjectID: 22, AssetID: 33}
	for _, severity := range []string{"warning", "critical"} {
		db, conn := monitoringDB(t, monitorSQLStep{`alert_shields`, []string{"id"}, nil}, monitorSQLStep{`SELECT id,severity`, []string{"id", "severity"}, [][]driver.Value{{int64(7), "warning"}}}, monitorSQLStep{`UPDATE "alerts"`, nil, nil})
		if err := monitorAlert(db, op, "bmc.health", severity, 1, false); err != nil {
			t.Fatal(err)
		}
		if len(conn.queries) != 3 {
			t.Fatal("continuous incident created another event or delivery")
		}
	}
	// 命中屏蔽规则时不得新建告警、事件或投递。
	shielded, conn := monitoringDB(t, monitorSQLStep{`alert_shields`, []string{"id"}, [][]driver.Value{{int64(4)}}})
	if err := monitorAlert(shielded, op, "bmc.health", "critical", 1, false); err != nil {
		t.Fatal(err)
	}
	if len(conn.queries) != 1 {
		t.Fatalf("shielded alert still created an incident: %d queries", len(conn.queries))
	}
	db, _ := monitoringDB(t, monitorSQLStep{`SELECT id,severity`, []string{"id", "severity"}, [][]driver.Value{{int64(7), "critical"}}}, monitorSQLStep{`UPDATE "alerts"`, nil, nil}, monitorSQLStep{`INSERT INTO "alert_events"`, []string{"id"}, [][]driver.Value{{int64(8)}}}, monitorSQLStep{`SELECT DISTINCT d.tenant_id`, nil, nil})
	if err := monitorAlert(db, op, "bmc.health", "ok", 0, true); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ { // No active incident, including after recovery: fire a fresh one.
		db, _ := monitoringDB(t, monitorSQLStep{`alert_shields`, []string{"id"}, nil}, monitorSQLStep{`SELECT id,severity`, []string{"id", "severity"}, nil}, monitorSQLStep{`INSERT INTO "alerts"`, []string{"id"}, [][]driver.Value{{int64(9)}}}, monitorSQLStep{`INSERT INTO "alert_events"`, []string{"id"}, [][]driver.Value{{int64(10)}}}, monitorSQLStep{`SELECT DISTINCT r.tenant_id`, nil, nil})
		if err := monitorAlert(db, op, "bmc.health", "warning", 1, false); err != nil {
			t.Fatal(err)
		}
	}
}
func TestApplyMonitorFailureThreshold(t *testing.T) {
	for _, prior := range []int64{0, 1} {
		steps := []monitorSQLStep{{`SELECT "monitor_consecutive_failures"`, []string{"monitor_consecutive_failures"}, [][]driver.Value{{prior}}}, {`UPDATE "assets"`, nil, nil}}
		if prior == 1 {
			steps = append(steps, monitorSQLStep{`alert_shields`, []string{"id"}, nil}, monitorSQLStep{`SELECT id,severity`, []string{"id", "severity"}, [][]driver.Value{{int64(7), "warning"}}}, monitorSQLStep{`UPDATE "alerts"`, nil, nil})
		}
		db, _ := monitoringDB(t, steps...)
		if err := applyMonitorResult(db, operation{TenantID: 1, ProjectID: 2, AssetID: 3}, false, nil); err != nil {
			t.Fatal(err)
		}
	}
}
func TestSchedulerClaimsAndInterval(t *testing.T) {
	db, conn := monitoringDB(t, monitorSQLStep{`FOR UPDATE OF a SKIP LOCKED`, []string{"id", "tenant_id", "project_id"}, [][]driver.Value{{int64(3), int64(1), int64(2)}}}, monitorSQLStep{`INSERT INTO "operations"`, []string{"id"}, [][]driver.Value{{int64(4)}}}, monitorSQLStep{`INSERT INTO "task_outbox"`, nil, nil}, monitorSQLStep{`now()+interval '5 minutes'`, nil, nil})
	if err := (&store{db: db}).scheduleCollections(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, part := range []string{"monitor_next_at<=now()", "op.project_id=a.project_id", "op.tenant_id=a.tenant_id", "'pending','running','retry_wait'", "p.status='active'", "NOT mc.enabled"} {
		if !strings.Contains(conn.queries[0], part) {
			t.Errorf("missing scheduler safeguard: %s", part)
		}
	}
	if strings.Contains(conn.queries[1], "user_id") {
		t.Fatal("system collection must not use zero actor foreign key")
	}
}
func TestChannelValidationAndSSRF(t *testing.T) {
	for _, target := range []string{"http://example.com", "https://127.0.0.1/secret", "https://[::1]", "https://169.254.169.254", "https://10.0.0.1", "https://100.64.0.1", "https://user:pass@example.com", "https://example.com:8443", "https://example.com/#secret"} {
		if validWebhook(target, "webhook") {
			t.Errorf("accepted %s", target)
		}
	}
	for _, ip := range []string{"127.0.0.1", "::ffff:127.0.0.1", "::1", "fe80::1", "10.0.0.1", "100.64.1.1"} {
		if publicNotificationIP(netip.MustParseAddr(ip)) {
			t.Errorf("accepted IP %s", ip)
		}
	}
	for kind, target := range map[string]string{"webhook": "https://example.com/hook?token=secret", "lark": "https://open.feishu.cn/open-apis/bot/v2/hook/secret", "wechat": "https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=secret", "dingtalk": "https://oapi.dingtalk.com/robot/send?access_token=secret"} {
		if err := validateChannel(kind, json.RawMessage(`{}`), target); err != nil {
			t.Errorf("%s: %v", kind, err)
		}
		if err := validateChannel(kind, json.RawMessage(`{"secret":"hidden"}`), target); err == nil {
			t.Fatal("secret allowed in public config")
		}
	}
	if validWebhook("https://example.com/hook", "wechat") || validWebhook("https://example.com/hook", "dingtalk") {
		t.Fatal("provider hostname not enforced")
	}
	if err := validateChannel("email", json.RawMessage(`{"host":"smtp.example.com","port":25,"username":"u","from":"a@example.com","to":"b@example.com"}`), "hidden"); err == nil {
		t.Fatal("plaintext SMTP authentication accepted")
	}
	for _, field := range []string{"secret_ciphertext", "secret_key_id", "test_claim_until"} {
		if !strings.Contains(hidden, "- '"+field+"'") {
			t.Errorf("exposes %s", field)
		}
	}
}
func TestChannelTestStoresSanitizedFailure(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, conn := monitoringDB(t,
		monitorSQLStep{`SELECT * FROM "alert_channels"`, []string{"id", "tenant_id", "project_id", "kind", "config", "secret_key_id", "secret_ciphertext"}, [][]driver.Value{{int64(3), int64(1), int64(2), "webhook", []byte(`{}`), "unsupported", []byte("do-not-echo")}}},
		monitorSQLStep{`UPDATE "alert_channels"`, nil, nil}, monitorSQLStep{`UPDATE "alert_channels"`, nil, nil})
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set("scope", scope{Tenant: 1, Project: 2, Admin: true})
	c.Params = gin.Params{{Key: "id", Value: "3"}}
	c.Request = httptest.NewRequest("POST", "/", nil)
	(&App{store: &store{db: db}}).testChannel(c)
	if w.Code != 502 || strings.Contains(w.Body.String(), "do-not-echo") || strings.Contains(w.Body.String(), "unsupported") {
		t.Fatalf("unsafe test failure: %d %s", w.Code, w.Body.String())
	}
	for _, fragment := range []string{"tenant_id=", "project_id=", "deleted_at IS NULL", "test_claim_until="} {
		if !strings.Contains(conn.queries[2], fragment) {
			t.Errorf("test result missing safeguard %s", fragment)
		}
	}
}

func TestNotificationRetryLimitAndLease(t *testing.T) {
	db, conn := monitoringDB(t, monitorSQLStep{`attempts>=5`, nil, nil}, monitorSQLStep{`FOR UPDATE SKIP LOCKED`, []string{"id"}, nil})
	(&App{store: &store{db: db}}).deliverNotification(context.Background())
	if !strings.Contains(conn.queries[1], "attempts<5") || !strings.Contains(conn.queries[1], "locked_until<now()") {
		t.Fatal("unbounded retries or missing expired-lease recovery")
	}
}

func TestChannelRoutesRequireAuthentication(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, path := range []string{"/api/v1/projects/2/alert-channels", "/api/v1/projects/2/alert-channels/1/test"} {
		w := httptest.NewRecorder()
		(&App{}).routes().ServeHTTP(w, httptest.NewRequest("POST", path, strings.NewReader(`{}`)))
		if w.Code != 401 {
			t.Fatalf("%s: %d", path, w.Code)
		}
	}
}
