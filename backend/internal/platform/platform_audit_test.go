package platform

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
)

func TestOpenAPIAuthenticationMetadata(t *testing.T) {
	a := &App{}
	a.router = a.routes()
	w := httptest.NewRecorder()
	a.router.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/docs/openapi.json", nil))
	var spec struct {
		Security []any
		Paths    map[string]map[string]map[string]any
	}
	if err := json.Unmarshal(w.Body.Bytes(), &spec); err != nil {
		t.Fatal(err)
	}
	if len(spec.Security) != 2 {
		t.Fatal("missing default authentication")
	}
	for path, method := range map[string]string{
		"/api/v1/projects/{projectId}/work-orders/{id}/assign":   "put",
		"/api/v1/projects/{projectId}/work-orders/{id}/messages": "post",
	} {
		if spec.Paths[path][method] == nil {
			t.Fatalf("missing work-order collaboration route: %s %s", method, path)
		}
	}
	if _, ok := spec.Paths["/api/v1/projects/{projectId}/assets"]["get"]["security"]; ok {
		t.Fatal("asset route publicly documented")
	}
	if sec, ok := spec.Paths["/api/v1/auth/login"]["post"]["security"]; !ok || len(sec.([]any)) != 0 {
		t.Fatal("login must be documented public")
	}
}
