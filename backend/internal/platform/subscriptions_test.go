package platform

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestTermRefund(t *testing.T) {
	now := time.Date(2026, 1, 10, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name string
		term subscriptionTerm
		want int64
	}{
		{"half", subscriptionTerm{StartsAt: now.Add(-24 * time.Hour), EndsAt: now.Add(24 * time.Hour), PaidCents: 10001}, 5000},
		{"future", subscriptionTerm{StartsAt: now.Add(time.Hour), EndsAt: now.Add(25 * time.Hour), PaidCents: 10001}, 10001},
		{"expired", subscriptionTerm{StartsAt: now.Add(-48 * time.Hour), EndsAt: now.Add(-24 * time.Hour), PaidCents: 10001}, 0},
		{"invalid", subscriptionTerm{StartsAt: now, EndsAt: now, PaidCents: 10001}, 0},
		{"already refunded", subscriptionTerm{StartsAt: now.Add(-24 * time.Hour), EndsAt: now.Add(24 * time.Hour), PaidCents: 10001, RefundedCents: 10001}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := termRefund(tc.term, now); got != tc.want {
				t.Fatalf("refund=%d want=%d", got, tc.want)
			}
		})
	}
}
func TestSubscriptionEnd(t *testing.T) {
	start := time.Date(2026, 2, 10, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		cycle string
		want  time.Time
	}{
		{"hourly", start.Add(2 * time.Hour)}, {"daily", start.AddDate(0, 0, 2)},
		{"monthly", start.AddDate(0, 2, 0)}, {"yearly", start.AddDate(2, 0, 0)},
	} {
		got, err := subscriptionEnd(start, tc.cycle, 2)
		if err != nil || !got.Equal(tc.want) {
			t.Fatalf("%s: %v %v", tc.cycle, got, err)
		}
	}
	for _, tc := range []struct {
		cycle string
		count int
	}{{"bad", 1}, {"monthly", 0}, {"yearly", 37}} {
		if _, err := subscriptionEnd(start, tc.cycle, tc.count); err == nil {
			t.Fatalf("accepted %+v", tc)
		}
	}
}

func TestRenewalPrice(t *testing.T) {
	cents, err := renewalPrice(`{"monthly":12.34}`, "monthly", "monthly", 3)
	if err != nil || cents != 3702 {
		t.Fatalf("price=%d err=%v", cents, err)
	}
	cents, err = renewalPrice(`{"base_price":10}`, "yearly", "yearly", 2)
	if err != nil || cents != 2000 {
		t.Fatalf("fallback=%d err=%v", cents, err)
	}
	for _, tc := range []struct {
		pricing, cycle string
		count          int
	}{{`{}`, "monthly", 1}, {`{"monthly":-1}`, "monthly", 1}, {`{"base_price":10}`, "yearly", 1}, {`{"monthly":10}`, "monthly", 0}, {`{"monthly":10}`, "monthly", 37}, {`{"monthly":10}`, "invalid", 1}} {
		if _, err := renewalPrice(tc.pricing, "monthly", tc.cycle, tc.count); err == nil {
			t.Fatalf("accepted %+v", tc)
		}
	}
}
func TestSubscriptionBatchBounds(t *testing.T) {
	if !validSubscriptionBatch([]int64{1, 2}, "key") {
		t.Fatal("valid batch rejected")
	}
	for _, ids := range [][]int64{nil, {0}, {-1}, {1, 1}, make([]int64, 101)} {
		if validSubscriptionBatch(ids, "key") {
			t.Fatalf("accepted ids=%v", ids)
		}
	}
	if validSubscriptionBatch([]int64{1}, "") || validSubscriptionBatch([]int64{1}, strings.Repeat("k", 81)) {
		t.Fatal("invalid request key accepted")
	}
}

// This test only runs against an explicitly provided disposable database; no migrations are applied.
func TestSubscriptionBatchIntegration(t *testing.T) {
	dsn := os.Getenv("SUBSCRIPTION_TEST_DSN")
	if dsn == "" {
		t.Skip("SUBSCRIPTION_TEST_DSN must point to a disposable migrated database")
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
	db = db.Begin()
	defer db.Rollback()
	if db.Error != nil {
		t.Fatal(db.Error)
	}
	var project struct{ ID, TenantID int64 }
	if err = db.Table("projects").Where("deleted_at IS NULL AND status='active'").Order("id").Take(&project).Error; err != nil {
		t.Fatal(err)
	}
	var u struct{ ID int64 }
	if err = db.Raw("INSERT INTO users(username,password_hash,name) VALUES (?,?,?) RETURNING id", fmt.Sprintf("subscription-%d", time.Now().UnixNano()), "test-hash", "test").Scan(&u).Error; err != nil {
		t.Fatal(err)
	}
	sc := scope{Tenant: project.TenantID, Project: project.ID, User: u.ID, Admin: true}
	a := &App{store: &store{db: db}}
	if err = db.Exec("INSERT INTO accounts(tenant_id,balance) VALUES (?,100) ON CONFLICT (tenant_id) DO NOTHING", sc.Tenant).Error; err != nil {
		t.Fatal(err)
	}
	if err = db.Table("accounts").Where("tenant_id=?", sc.Tenant).Update("balance", 100).Error; err != nil {
		t.Fatal(err)
	}
	var account struct {
		ID      int64
		Balance float64
	}
	if err = db.Table("accounts").Where("tenant_id=?", sc.Tenant).Take(&account).Error; err != nil {
		t.Fatal(err)
	}
	var product struct{ ID int64 }
	if err = db.Raw("INSERT INTO products(tenant_id,name,pricing_json,billing_cycle) VALUES (?,'batch test','{\"monthly\":10}','monthly') RETURNING id", sc.Tenant).Scan(&product).Error; err != nil {
		t.Fatal(err)
	}
	makeAsset := func() int64 {
		var row struct{ ID int64 }
		if err := db.Raw("INSERT INTO assets(tenant_id,project_id,name,bmc_address,bmc_user,bmc_secret_ciphertext,bmc_secret_key_id) VALUES (?,?,'subscription test',?,'test',?,'v1') RETURNING id", sc.Tenant, sc.Project, fmt.Sprintf("2001:db8::%x", time.Now().UnixNano()), []byte("test")).Scan(&row).Error; err != nil {
			t.Fatal(err)
		}
		return row.ID
	}
	asset := makeAsset()
	legacy := makeAsset()
	end := time.Now().Add(time.Hour).UTC().Truncate(time.Microsecond)
	var order struct{ ID int64 }
	if err = db.Raw("INSERT INTO orders(tenant_id,project_id,user_id,type,product_id,asset_id,status,paid_at,payable_cents) VALUES (?,?,?,'purchase',?,?,'completed',now(),10000) RETURNING id", sc.Tenant, sc.Project, sc.User, product.ID, asset).Scan(&order).Error; err != nil {
		t.Fatal(err)
	}
	if err = db.Table("assets").Where("id=?", asset).Updates(map[string]any{"subscription_state": "active", "lease_end": end}).Error; err != nil {
		t.Fatal(err)
	}
	if err = db.Exec("INSERT INTO asset_monitor_configs(tenant_id,project_id,asset_id) VALUES (?,?,?)", sc.Tenant, sc.Project, asset).Error; err != nil {
		t.Fatal(err)
	}
	request := func(sc scope, body string, handler gin.HandlerFunc) (int, map[string]any) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Set("scope", sc)
		c.Params = gin.Params{{Key: "id", Value: fmt.Sprint(order.ID)}}
		c.Request = httptest.NewRequest("POST", "/", strings.NewReader(body))
		c.Request.Header.Set("Content-Type", "application/json")
		handler(c)
		var response map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		return w.Code, response
	}
	body := fmt.Sprintf(`{"asset_ids":[%d,%d],"cycle":"monthly","count":2,"request_key":"renew-test"}`, asset, legacy)
	code, response := request(sc, body, a.batchRenewSubscriptions)
	if code != 200 {
		t.Fatalf("renew: %d %v", code, response)
	}
	results := response["data"].(map[string]any)["results"].([]any)
	if results[0].(map[string]any)["status"] != "pending" || results[1].(map[string]any)["status"] != "failed" {
		t.Fatalf("partial renew: %v", results)
	}
	var renew struct {
		ID           int64
		Status       string
		PayableCents int64
		TermCount    int
		TermCycle    string
		PaidAt       *time.Time
	}
	if err = db.Table("orders").Where("asset_id=? AND type='renew'", asset).Take(&renew).Error; err != nil {
		t.Fatal(err)
	}
	if renew.Status != "pending" || renew.PayableCents != 2000 || renew.TermCount != 2 || renew.TermCycle != "monthly" || renew.PaidAt != nil {
		t.Fatalf("renew order %+v", renew)
	}
	request(sc, body, a.batchRenewSubscriptions)
	var count int64
	db.Table("orders").Where("asset_id=? AND type='renew'", asset).Count(&count)
	if count != 1 {
		t.Fatal("duplicate renewal order")
	}

	order.ID = renew.ID
	if err = db.Table("accounts").Where("id=?", account.ID).Update("frozen_amount", 100).Error; err != nil {
		t.Fatal(err)
	}
	code, _ = request(sc, `{}`, a.paySubscriptionOrder)
	if code != 409 {
		t.Fatalf("frozen balance accepted: %d", code)
	}
	db.Table("payments").Where("transaction_id=?", fmt.Sprintf("subscription-order:%d", renew.ID)).Count(&count)
	if count != 0 {
		t.Fatal("failed payment left a ledger")
	}
	if err = db.Table("accounts").Where("id=?", account.ID).Update("frozen_amount", 0).Error; err != nil {
		t.Fatal(err)
	}
	code, response = request(sc, `{"amount":0,"status":"success"}`, a.paySubscriptionOrder)
	if code != 200 {
		t.Fatalf("balance payment: %d %v", code, response)
	}
	code, response = request(sc, `{}`, a.paySubscriptionOrder)
	if code != 200 || response["data"].(map[string]any)["replayed"] != true {
		t.Fatalf("payment replay: %d %v", code, response)
	}
	var paidBalance struct{ Balance float64 }
	db.Table("accounts").Where("id=?", account.ID).Take(&paidBalance)
	if paidBalance.Balance != account.Balance-20 {
		t.Fatalf("payment balance %v", paidBalance.Balance)
	}
	var paymentCount int64
	db.Table("payments").Where("transaction_id=? AND status='success' AND method='balance' AND amount=20", fmt.Sprintf("subscription-order:%d", renew.ID)).Count(&paymentCount)
	if paymentCount != 1 {
		t.Fatal("missing or duplicate actual payment")
	}
	if err = db.Exec("UPDATE subscription_payments SET payment_id=NULL WHERE order_id=?", renew.ID).Error; err != nil {
		t.Fatal(err)
	}
	if _, err = verifiedTerms(db, sc, asset); err == nil {
		t.Fatal("receipt without actual payment accepted")
	}
	if err = db.Exec("UPDATE subscription_payments SET payment_id=(SELECT payment_id FROM orders WHERE id=?) WHERE order_id=?", renew.ID, renew.ID).Error; err != nil {
		t.Fatal(err)
	}
	terms, err := verifiedTerms(db, sc, asset)
	if err != nil || len(terms) != 1 || !terms[0].StartsAt.Equal(end) || !terms[0].EndsAt.Equal(end.AddDate(0, 2, 0)) {
		t.Fatalf("paid lease: %+v %v", terms, err)
	}
	assertNoSubscriptionBill := func() {
		t.Helper()
		body := fmt.Sprintf(`{"tenant_id":%d,"period_start":%q,"period_end":%q}`, sc.Tenant, time.Now().Add(-time.Hour).UTC().Format(time.RFC3339), time.Now().Add(time.Hour).UTC().Format(time.RFC3339))
		// Isolate this fixture from unrelated orders in the disposable tenant.
		if err := db.Table("orders").Where("tenant_id=? AND (asset_id IS NULL OR asset_id<>?)", sc.Tenant, asset).Update("paid_at", nil).Error; err != nil {
			t.Fatal(err)
		}
		status, got := request(sc, body, a.generateBills)
		if status != 200 || got["data"].(map[string]any)["generated"].(float64) != 0 {
			t.Fatalf("subscription billed again: %d %v", status, got)
		}
	}
	assertNoSubscriptionBill()
	noPermission := sc
	noPermission.Admin = false
	var role struct{ ID int64 }
	if err = db.Raw("INSERT INTO roles(tenant_id,name,code) VALUES (?,'subscription test',?) RETURNING id", sc.Tenant, fmt.Sprintf("subscription-%d", u.ID)).Scan(&role).Error; err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []struct {
		sql  string
		args []any
	}{
		{"INSERT INTO tenant_memberships(tenant_id,user_id) VALUES (?,?)", []any{sc.Tenant, u.ID}},
		{"INSERT INTO memberships(tenant_id,project_id,user_id,role_id) VALUES (?,?,?,?)", []any{sc.Tenant, sc.Project, u.ID, role.ID}},
		{"INSERT INTO permissions(code,name,resource,action) VALUES ('orders:write','test','orders','write') ON CONFLICT (code) WHERE deleted_at IS NULL DO NOTHING", nil},
		{"INSERT INTO role_permissions(tenant_id,role_id,permission_id) SELECT ?,?,id FROM permissions WHERE code='orders:write' AND deleted_at IS NULL", []any{sc.Tenant, role.ID}},
	} {
		if err = db.Exec(stmt.sql, stmt.args...).Error; err != nil {
			t.Fatal(err)
		}
	}
	if !a.store.allowed(noPermission, "orders:write") || a.store.allowed(noPermission, "finance:write") {
		t.Fatal("invalid permission fixture")
	}
	for _, handler := range []gin.HandlerFunc{a.paySubscriptionOrder, a.batchUnsubscribeSubscriptions, a.unsubscribeAsset} {
		code, _ = request(noPermission, `{}`, handler)
		if code != 403 {
			t.Fatalf("missing financial permission accepted: %d", code)
		}
	}
	code, _ = request(noPermission, fmt.Sprintf(`{"asset_ids":[%d],"action":"on","idempotency_key":"denied"}`, asset), a.batchActions)
	if code != 403 {
		t.Fatalf("missing bmc permission accepted: %d", code)
	}
	_, partialPower := request(sc, fmt.Sprintf(`{"asset_ids":[%d,%d],"action":"on","idempotency_key":"partial-power"}`, legacy, int64(-1)), a.batchActions)
	powerResults := partialPower["data"].(map[string]any)["results"].([]any)
	if powerResults[0].(map[string]any)["status"] != "accepted" || powerResults[1].(map[string]any)["status"] != "failed" {
		t.Fatalf("partial power: %v", powerResults)
	}
	wrong := sc
	wrong.Project = -1
	_, denied := request(wrong, fmt.Sprintf(`{"asset_ids":[%d],"action":"on","idempotency_key":"wrong-project"}`, asset), a.batchActions)
	if denied["data"].(map[string]any)["results"].([]any)[0].(map[string]any)["status"] != "failed" {
		t.Fatal("cross-project power accepted")
	}
	wrong = sc
	wrong.Tenant = -1
	_, denied = request(wrong, body, a.batchRenewSubscriptions)
	if denied["data"].(map[string]any)["results"].([]any)[0].(map[string]any)["status"] != "failed" {
		t.Fatal("cross-tenant renewal accepted")
	}
	unsub := fmt.Sprintf(`{"asset_ids":[%d,%d],"request_key":"unsubscribe-test"}`, asset, legacy)
	_, response = request(sc, unsub, a.batchUnsubscribeSubscriptions)
	results = response["data"].(map[string]any)["results"].([]any)
	if results[0].(map[string]any)["status"] != "reclaim_pending" || results[1].(map[string]any)["status"] != "failed" {
		t.Fatalf("partial unsubscribe %v", results)
	}
	refund := int64(results[0].(map[string]any)["refund_cents"].(float64))
	if refund != 2000 {
		t.Fatalf("refund=%d", refund)
	}
	_, response = request(sc, unsub, a.batchUnsubscribeSubscriptions)
	if response["data"].(map[string]any)["results"].([]any)[0].(map[string]any)["replayed"] != true {
		t.Fatal("unsubscribe replay missing")
	}
	_, response = request(sc, fmt.Sprintf(`{"asset_ids":[%d],"request_key":"another-key"}`, asset), a.batchUnsubscribeSubscriptions)
	if response["data"].(map[string]any)["results"].([]any)[0].(map[string]any)["status"] != "failed" {
		t.Fatal("duplicate unsubscribe accepted")
	}
	var after struct{ Balance float64 }
	db.Table("accounts").Where("id=?", account.ID).Take(&after)
	if after.Balance != account.Balance-20+float64(refund)/100 {
		t.Fatalf("balance=%v expected=%v", after.Balance, account.Balance-20+float64(refund)/100)
	}
	assertNoSubscriptionBill()
	var monitor struct{ Enabled bool }
	db.Table("asset_monitor_configs").Where("asset_id=?", asset).Take(&monitor)
	if monitor.Enabled {
		t.Fatal("monitoring was not disabled")
	}
	db.Table("subscription_refunds").Where("account_id=? AND term_id IN (SELECT id FROM subscription_terms WHERE asset_id=?)", account.ID, asset).Count(&count)
	if count != 1 {
		t.Fatal("duplicate refund ledger")
	}
	db.Table("operations").Where("asset_id=? AND kind='power_off'", asset).Count(&count)
	if count != 1 {
		t.Fatal("duplicate power-off")
	}
	_, response = request(sc, fmt.Sprintf(`{"asset_ids":[%d],"action":"on","idempotency_key":"after-unsubscribe"}`, asset), a.batchActions)
	if response["data"].(map[string]any)["results"].([]any)[0].(map[string]any)["status"] != "failed" {
		t.Fatal("unsubscribed asset powered on")
	}
}
