package platform

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"baremetal-manager/backend/config"
	"baremetal-manager/backend/internal/model"
	"baremetal-manager/backend/internal/pxeservice"
	"baremetal-manager/backend/internal/service"
	"github.com/gin-gonic/gin"
	"gopkg.in/yaml.v3"
)

func TestIPXENoCloudKernelArgument(t *testing.T) {
	cfg := &config.Config{}
	cfg.PXE.HTTPBase = testInstallHTTPBase
	script, err := NewIPXEScriptGenerator(cfg).UbuntuScript("52:54:00:77:00:10", "", "", "", DefaultISOFileName)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(script, "ds=nocloud-net;s=${http-base}/nocloud/525400770010/") || strings.Contains(script, `nocloud-net\;`) {
		t.Fatal("iPXE must forward the NoCloud semicolon without a literal backslash")
	}
}

func TestTestVMOfflineInstallAndBootVerification(t *testing.T) {
	apt := testVMOfflineAPT()
	if apt["fallback"] != "offline-install" || apt["geoip"] != false || len(apt["mirror-selection"].(map[string]any)["primary"].([]any)) != 0 {
		t.Fatal("lab install must work without internet access")
	}
	commands := fmt.Sprint(testVMFirstBootCommands("http://192.168.77.1:18080/api/v1/provision/install-callback/test_token"))
	for _, required := range []string{"/target/usr/local/sbin/bmm-pxe-verify.py", "multi-user.target.wants/bmm-pxe-verify.service", `"stage":"first_boot"`, "urlopen", "BMM_PXE_BOOT_VERIFIED", "console=ttyS0", "update-grub"} {
		if !strings.Contains(commands, required) {
			t.Fatalf("missing first disk boot verification: %s", required)
		}
	}
}

func TestTestVMDefaultSeed(t *testing.T) {
	content, err := service.RenderInstallConfig(model.KickstartTemplate{Format: "autoinstall", TemplateContent: testVMAutoinstall}, service.InstallVars{Hostname: "bmm-test-vm", MAC: "52:54:00:77:00:10", PasswordHash: "!", HTTPBase: testInstallHTTPBase})
	if err != nil {
		t.Fatal(err)
	}
	var seed map[string]any
	if err := yaml.Unmarshal([]byte(content), &seed); err != nil {
		t.Fatal(err)
	}
	if _, ok := seed["autoinstall"].(map[string]any); !ok {
		t.Fatal("缺少 autoinstall 根节点")
	}
}

func TestTestVMOwner(t *testing.T) {
	for _, tc := range []struct {
		name            string
		owner           pxeservice.TestVMConfig
		tenant, project int64
		required, valid bool
	}{
		{"unclaimed-create", pxeservice.TestVMConfig{}, 1, 2, false, true},
		{"unclaimed-reboot", pxeservice.TestVMConfig{}, 1, 2, true, false},
		{"same-scope", pxeservice.TestVMConfig{TenantID: 1, ProjectID: 2}, 1, 2, true, true},
		{"other-project", pxeservice.TestVMConfig{TenantID: 1, ProjectID: 3}, 1, 2, false, false},
		{"other-tenant", pxeservice.TestVMConfig{TenantID: 3, ProjectID: 2}, 1, 2, true, false},
		{"invalid-scope", pxeservice.TestVMConfig{}, 0, 0, false, false},
		{"partial-owner", pxeservice.TestVMConfig{TenantID: 1}, 1, 2, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := testVMOwner(pxeservice.TestVMStatus{TestVMConfig: tc.owner}, tc.tenant, tc.project, tc.required)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v, error=%v", tc.valid, err)
			}
		})
	}
}

func TestTestVMGuards(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name, method, action string
		admin                bool
		sc                   scope
		status               int
	}{
		{"non-admin-read", "GET", "", false, scope{}, 403},
		{"non-admin-create", "POST", "", false, scope{}, 403},
		{"non-admin-reboot", "POST", "reboot", false, scope{}, 403},
		{"non-admin-delete", "DELETE", "", false, scope{}, 403},
		{"delete-needs-confirmation", "DELETE", "", true, scope{Tenant: 1, Project: 2}, 400},
		{"invalid-scope", "POST", "", true, scope{}, 400},
		{"unknown-action", "POST", "delete", true, scope{Tenant: 1, Project: 2}, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(tc.method, "/assets/test-vm", strings.NewReader(`{"firmware":"uefi"}`))
			c.Set("user", user{IsPlatformAdmin: tc.admin})
			c.Set("scope", tc.sc)
			c.Params = gin.Params{{Key: "action", Value: tc.action}}
			(&App{}).testVM(c)
			if w.Code != tc.status {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
		})
	}
}

func TestTestVMControllerUnavailable(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, status, err := testVMCall(ctx, http.MethodGet, "/test-vm", nil); err == nil || status != http.StatusServiceUnavailable {
		t.Fatalf("cancelled controller call: status=%d error=%v", status, err)
	}
	if err := testVMReboot(ctx, 1, 2); err == nil {
		t.Fatal("reboot must not succeed without controller status")
	}
}

func TestTestVMRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := (&App{}).routes()
	for _, req := range []struct{ method, path string }{
		{"GET", "/api/v1/projects/1/assets/test-vm"},
		{"POST", "/api/v1/projects/1/assets/test-vm"},
		{"DELETE", "/api/v1/projects/1/assets/test-vm"},
		{"POST", "/api/v1/projects/1/assets/test-vm/reboot"},
	} {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(req.method, req.path, nil))
		if w.Code != 401 {
			t.Fatalf("%s %s status=%d", req.method, req.path, w.Code)
		}
	}
}
