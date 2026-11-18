package platform

import (
	"net/http/httptest"
	"testing"
)

func TestCommercialRoutesRemoved(t *testing.T) {
	a := &App{}
	router := a.routes()
	for _, path := range []string{
		"/api/v1/auth/register", "/api/v1/projects/1/products", "/api/v1/projects/1/orders",
		"/api/v1/projects/1/bills", "/api/v1/projects/1/accounts/recharge", "/api/v1/projects/1/payments",
		"/api/v1/projects/1/invoices", "/api/v1/projects/1/coupons", "/api/v1/projects/1/admin/operations-dashboard",
		"/api/v1/projects/1/admin/tenants", "/api/v1/projects/1/admin/risk-items",
		"/api/v1/projects/1/assets/1/subscription/preview", "/api/v1/projects/1/assets/subscription/batch-renew",
	} {
		for _, method := range []string{"GET", "POST", "PUT", "DELETE"} {
			w := httptest.NewRecorder()
			router.ServeHTTP(w, httptest.NewRequest(method, path, nil))
			if w.Code != 404 {
				t.Errorf("%s %s: got %d, want 404", method, path, w.Code)
			}
		}
	}
	for _, key := range []string{"platform.operation_mode", "registration.enabled", "registration.email_verification", "billing.enabled", "billing.model_prices"} {
		if validSetting(key, true) {
			t.Errorf("removed setting accepted: %s", key)
		}
	}
}
