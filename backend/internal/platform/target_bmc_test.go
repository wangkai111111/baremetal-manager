package platform

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func testTarget(t *testing.T, server *httptest.Server, verify bool) *targetBMC {
	t.Helper()
	u, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	host, port, err := net.SplitHostPort(u.Host)
	if err != nil {
		t.Fatal(err)
	}
	p, _ := strconv.Atoi(port)
	target, err := newTargetBMC(device{BMCAddress: host, BMCPort: p, BMCProtocol: "redfish", BMCTLSVerify: verify, BMCUser: "operator"}, "do-not-leak-password")
	if err != nil {
		t.Fatal(err)
	}
	return target
}

func TestCollectionSummaryWhitelist(t *testing.T) {
	raw := json.RawMessage(`{"Name":"System","Manufacturer":" Vendor ","Model":"Server","SerialNumber":"SN","BiosVersion":"v1","ProcessorSummary":{"Count":2,"Model":"CPU","Secret":"hidden"},"MemorySummary":{"TotalSystemMemoryGiB":128,"Secret":"hidden"},"BMCPassword":"hidden","Actions":{"Secret":"hidden"}}`)
	now := time.Now()
	dto := collectionSummary(collectionRow{BMCProtocol: "redfish", RedfishSnapshot: raw, CollectedAt: &now, CollectionError: "hidden credentials"})
	if !dto.Supported || dto.ComponentDetailsSupported || dto.CPUCount != 2 || dto.CPUModel != "CPU" || dto.MemoryGiB != 128 || dto.BIOSVersion != "v1" || dto.SerialNumber != "SN" || dto.CollectedAt != &now || dto.CollectionError == "" {
		t.Fatalf("incorrect summary: %#v", dto)
	}
	encoded, err := json.Marshal(dto)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"hidden", "Secret", "Password", "snapshot", "Actions"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("leaked %s", forbidden)
		}
	}
	updates, err := collectionAssetUpdates(raw)
	if err != nil || updates["manufacturer"] != "Vendor" || updates["model"] != "Server" || updates["serial_number"] != "SN" {
		t.Fatalf("updates: %#v, %v", updates, err)
	}
	updates, err = collectionAssetUpdates(json.RawMessage(`{"Name":"System","Manufacturer":"  "}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"manufacturer", "model", "serial_number"} {
		if _, ok := updates[key]; ok {
			t.Fatalf("empty %s would overwrite identity", key)
		}
	}
	for _, value := range []string{"", "{}", "null", "{", `{"PowerState":"On"}`, `{"ProcessorSummary":{},"MemorySummary":{}}`} {
		dto := collectionSummary(collectionRow{BMCProtocol: "ipmi", RedfishSnapshot: json.RawMessage(value)})
		if dto.Supported || dto.CPUCount != 0 || dto.MemoryGiB != 0 || dto.Name != "" || dto.CollectedAt != nil {
			t.Fatalf("nonempty DTO for %q", value)
		}
		if _, err := parseComputerSystemSummary(json.RawMessage(value)); err == nil {
			t.Fatalf("accepted empty summary %q", value)
		}
	}
}

func TestCollectionSummaryStatus(t *testing.T) {
	now := time.Now()
	for _, tc := range []struct {
		row  collectionRow
		want string
	}{
		{collectionRow{BMCProtocol: "redfish"}, "not_collected"},
		{collectionRow{BMCProtocol: "ipmi"}, "unsupported"},
		{collectionRow{BMCProtocol: "redfish", RedfishSnapshot: json.RawMessage(`{}`), CollectedAt: &now}, "not_collected"},
		{collectionRow{BMCProtocol: "redfish", RedfishSnapshot: json.RawMessage(`{"Model":"Server"}`), CollectedAt: &now}, "succeeded"},
		{collectionRow{BMCProtocol: "redfish", RedfishSnapshot: json.RawMessage(`{"Model":"Server"}`), CollectedAt: &now, CollectionError: "unsafe secret"}, "failed"},
	} {
		dto := collectionSummary(tc.row)
		if dto.Status != tc.want {
			t.Fatalf("status = %q, want %q", dto.Status, tc.want)
		}
		if tc.want == "not_collected" && dto.CollectedAt != nil {
			t.Fatal("empty legacy snapshot presented as successful collection")
		}
	}
}

func TestTargetCollectEmptySystem(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redfish/v1/Systems" {
			fmt.Fprint(w, `{"Members":[{"@odata.id":"/redfish/v1/Systems/1"}]}`)
		} else {
			fmt.Fprint(w, `{}`)
		}
	}))
	defer server.Close()
	if _, err := testTarget(t, server, false).collect(context.Background()); err == nil {
		t.Fatal("empty system must not succeed")
	}
}

func TestTargetCollectTLS(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s", r.Method)
		}
		user, secret, ok := r.BasicAuth()
		if !ok || user != "operator" || secret != "do-not-leak-password" {
			t.Error("missing target credentials")
		}
		switch r.URL.Path {
		case "/redfish/v1/Systems":
			fmt.Fprint(w, `{"Members":[{"@odata.id":"/redfish/v1/Systems/Server.1"}]}`)
		case "/redfish/v1/Systems/Server.1":
			fmt.Fprint(w, `{"Manufacturer":"Real vendor","Model":"Hardware","SerialNumber":"123","PowerState":"On","BiosVersion":"1.2","ProcessorSummary":{"Count":2,"Model":"CPU"},"MemorySummary":{"TotalSystemMemoryGiB":128},"OEMSecret":"not persisted"}`)
		case "/redfish/v1/Managers", "/redfish/v1/UpdateService", "/redfish/v1/Chassis":
			http.NotFound(w, r)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	verified := testTarget(t, server, true)
	_, err := verified.collect(context.Background())
	if err == nil || !strings.Contains(err.Error(), "certificate verification") {
		t.Fatalf("untrusted TLS: %v", err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	verified.client.Transport.(*http.Transport).TLSClientConfig.RootCAs = roots
	for _, target := range []*targetBMC{verified, testTarget(t, server, false)} {
		raw, err := target.collect(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		var snapshot map[string]any
		if err := json.Unmarshal(raw, &snapshot); err != nil {
			t.Fatal(err)
		}
		if snapshot["Manufacturer"] != "Real vendor" || snapshot["MemorySummary"].(map[string]any)["TotalSystemMemoryGiB"] != float64(128) {
			t.Fatalf("unexpected snapshot %s", raw)
		}
		if _, ok := snapshot["OEMSecret"]; ok {
			t.Fatal("unapproved fields persisted")
		}
	}
}

func TestTargetReset(t *testing.T) {
	for kind, reset := range map[string]string{"power_on": "On", "power_off": "ForceOff", "power_cycle": "ForceRestart"} {
		t.Run(kind, func(t *testing.T) {
			var posts atomic.Int32
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/redfish/v1/Systems":
					fmt.Fprint(w, `{"Members":[{"@odata.id":"/redfish/v1/Systems/1"}]}`)
				case "/redfish/v1/Systems/1":
					fmt.Fprint(w, `{"Actions":{"#ComputerSystem.Reset":{"target":"/redfish/v1/Systems/1/Actions/ComputerSystem.Reset"}}}`)
				case "/redfish/v1/Systems/1/Actions/ComputerSystem.Reset":
					posts.Add(1)
					if r.Method != http.MethodPost {
						t.Errorf("method = %s", r.Method)
					}
					var body map[string]string
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
					}
					if len(body) != 1 || body["ResetType"] != reset {
						t.Errorf("body = %#v", body)
					}
					w.WriteHeader(http.StatusNoContent)
				default:
					t.Errorf("unexpected path %s", r.URL.Path)
				}
			}))
			defer server.Close()
			started, err := testTarget(t, server, false).power(context.Background(), kind)
			if err != nil || !started || posts.Load() != 1 {
				t.Fatalf("started=%v posts=%d error=%v", started, posts.Load(), err)
			}
		})
	}
}

func TestTargetRejectsForeignResources(t *testing.T) {
	var hits atomic.Int32
	foreign := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
	defer foreign.Close()
	for _, stage := range []string{"system", "reset", "redirect"} {
		t.Run(stage, func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if stage == "redirect" {
					http.Redirect(w, r, foreign.URL+"/redfish/v1/Systems", http.StatusTemporaryRedirect)
					return
				}
				if r.URL.Path == "/redfish/v1/Systems" {
					path := "/redfish/v1/Systems/1"
					if stage == "system" {
						path = foreign.URL + "/redfish/v1/Systems/1"
					}
					fmt.Fprintf(w, `{"Members":[{"@odata.id":%q}]}`, path)
				} else {
					fmt.Fprintf(w, `{"Actions":{"#ComputerSystem.Reset":{"target":%q}}}`, foreign.URL+"/redfish/v1/Systems/1/Actions/ComputerSystem.Reset")
				}
			}))
			defer server.Close()
			started, err := testTarget(t, server, false).power(context.Background(), "power_off")
			if err == nil || started {
				t.Fatalf("started=%v error=%v", started, err)
			}
		})
	}
	if hits.Load() != 0 {
		t.Fatal("credentials could reach foreign origin")
	}
	target := testTarget(t, foreign, false)
	for _, path := range []string{"//example.com/redfish/v1/Systems", "https://example.com/redfish/v1/Systems", "/\\example.com/redfish/v1/Systems", "/redfish/v1/Systems?secret=x", "/redfish/v1/Systems#x", "relative"} {
		if _, err := target.resource(path); err == nil {
			t.Errorf("accepted %q", path)
		}
	}
}

func TestTargetFailureIsNotReplayedAndIsRedacted(t *testing.T) {
	for _, code := range []int{http.StatusInternalServerError, http.StatusTemporaryRedirect} {
		t.Run(strconv.Itoa(code), func(t *testing.T) {
			var posts atomic.Int32
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPost {
					posts.Add(1)
					w.Header().Set("Location", "/redfish/v1/Systems/1/Actions/ComputerSystem.Reset")
					w.WriteHeader(code)
					fmt.Fprint(w, "do-not-leak-password")
					return
				}
				if r.URL.Path == "/redfish/v1/Systems" {
					fmt.Fprint(w, `{"Members":[{"@odata.id":"/redfish/v1/Systems/1"}]}`)
				} else {
					fmt.Fprint(w, `{}`)
				}
			}))
			defer server.Close()
			started, err := testTarget(t, server, false).power(context.Background(), "power_cycle")
			wantStarted, wantStatus := code >= 500, "failed"
			if wantStarted {
				wantStatus = "unknown"
			}
			if err == nil || started != wantStarted || posts.Load() != 1 {
				t.Fatalf("started=%v posts=%d error=%v", started, posts.Load(), err)
			}
			status, message, _ := operationOutcome("power_cycle", started, err)
			if status != wantStatus || !strings.Contains(message, strconv.Itoa(code)) || strings.Contains(message, "do-not-leak-password") {
				t.Fatalf("status=%s message=%s", status, message)
			}
		})
	}
}

func TestTargetContextTimeout(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := testTarget(t, server, false).collect(ctx)
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("error=%v", err)
	}
}

func TestOperationOutcomeAndPowerState(t *testing.T) {
	for _, tc := range []struct {
		kind    string
		started bool
		want    string
	}{
		{"collect", false, "failed"}, {"collect", true, "failed"},
		{"power_on", false, "failed"}, {"power_cycle", true, "unknown"},
	} {
		status, _, _ := operationOutcome(tc.kind, tc.started, errors.New("safe error"))
		if status != tc.want {
			t.Errorf("%+v: %s", tc, status)
		}
	}
	for kind, want := range map[string]string{"power_on": "on", "power_off": "off", "power_cycle": "unknown", "reset": "unknown", "cycle": "unknown"} {
		if got := completedPowerState(kind); got != want {
			t.Errorf("%s: %s", kind, got)
		}
	}
}

func TestTargetIPMIAndValidation(t *testing.T) {
	target, err := newTargetBMC(device{BMCAddress: "2001:db8::1", BMCPort: 6623, BMCProtocol: "ipmi"}, "do-not-leak-password")
	if err != nil {
		t.Fatal(err)
	}
	cmd := target.ipmiCommand(context.Background(), "off")
	args := strings.Join(cmd.Args, " ")
	if !strings.Contains(args, "-p 6623") || !strings.Contains(args, "-E") || strings.Contains(args, "-P") || strings.Contains(args, target.secret) {
		t.Fatalf("unsafe arguments %s", args)
	}
	found := false
	for _, entry := range cmd.Env {
		if entry == "IPMI_PASSWORD="+target.secret {
			found = true
		}
	}
	if !found {
		t.Fatal("password environment missing")
	}
	if _, err := target.collect(context.Background()); err == nil {
		t.Fatal("IPMI collection unexpectedly pretended to be Redfish")
	}
	for _, d := range []device{{BMCAddress: "example.com", BMCPort: 443, BMCProtocol: "redfish"}, {BMCAddress: "127.0.0.1", BMCPort: 0, BMCProtocol: "redfish"}, {BMCAddress: "127.0.0.1", BMCPort: 443, BMCProtocol: "auto"}} {
		if _, err := newTargetBMC(d, "secret"); err == nil {
			t.Fatalf("accepted invalid device %#v", d)
		}
	}
	status, message, _ := operationOutcome("power_on", false, errors.New("unsupported BMC protocol"))
	if status != "failed" || !strings.Contains(message, "unsupported BMC protocol") {
		t.Fatalf("%s: %s", status, message)
	}
}
