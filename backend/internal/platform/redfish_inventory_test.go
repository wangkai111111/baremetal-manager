package platform

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"gorm.io/gorm/clause"
)

func TestInventoryPaginationAndPersistence(t *testing.T) {
	var diskReads atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Errorf("unexpected write: %s", r.Method)
		}
		user, password, ok := r.BasicAuth()
		if !ok || user != "operator" || password != "do-not-leak-password" {
			t.Error("missing auth")
		}
		switch r.URL.Path {
		case "/redfish/v1/Systems":
			fmt.Fprint(w, `{"Members":[{"@odata.id":"/redfish/v1/Systems/1"}]}`)
		case "/redfish/v1/Systems/1":
			fmt.Fprint(w, `{"Name":"Server","Processors":{"@odata.id":"/redfish/v1/CPU"},"Memory":{"@odata.id":"/redfish/v1/Memory"},"Storage":{"@odata.id":"/redfish/v1/Storage"},"EthernetInterfaces":{"@odata.id":"/redfish/v1/NIC"},"NetworkInterfaces":{"@odata.id":"/redfish/v1/NIC"}}`)
		case "/redfish/v1/CPU":
			fmt.Fprint(w, `{"Members":[{"Id":"cpu1","Model":"CPU","TotalCores":32}]}`)
		case "/redfish/v1/Memory":
			fmt.Fprint(w, `{"Members":[{"Id":"mem1","CapacityMiB":32768}]}`)
		case "/redfish/v1/Storage":
			fmt.Fprint(w, `{"Members":[{"Id":"ctrl","StorageControllers":[{"Name":"RAID controller","Model":"RAID"}],"Drives":[{"@odata.id":"/redfish/v1/Drive/1"},{"@odata.id":"/redfish/v1/Drive/2"},{"@odata.id":"/redfish/v1/Drive/1"}],"Volumes":{"@odata.id":"/redfish/v1/Volumes"}}]}`)
		case "/redfish/v1/Drive/1", "/redfish/v1/Drive/2":
			diskReads.Add(1)
			fmt.Fprintf(w, `{"Id":%q,"Model":"SSD","CapacityBytes":1000000000000,"SerialNumber":%q,"Status":{"Health":"OK"},"OEM":{"Password":"hidden"}}`, strings.TrimPrefix(r.URL.Path, "/redfish/v1/Drive/"), "disk-serial-"+strings.TrimPrefix(r.URL.Path, "/redfish/v1/Drive/"))
		case "/redfish/v1/Volumes":
			fmt.Fprint(w, `{"Members":[{"Id":"volume1","RAIDType":"RAID1","CapacityBytes":1000000000000}]}`)
		case "/redfish/v1/NIC":
			if r.URL.Query().Get("$skip") == "1" {
				fmt.Fprint(w, `{"Members":[{"Id":"nic2","MACAddress":"00:11:22:33:44:66","SpeedMbps":25000,"LinkStatus":"LinkDown"}]}`)
			} else {
				fmt.Fprint(w, `{"Members":[{"Id":"nic1","MACAddress":"00:11:22:33:44:55","SpeedMbps":10000,"LinkStatus":"LinkUp"}],"Members@odata.nextLink":"/redfish/v1/NIC?$skip=1"}`)
			}
		case "/redfish/v1/Managers":
			fmt.Fprint(w, `{"Members":[{"Id":"bmc","FirmwareVersion":"BMC1"}]}`)
		case "/redfish/v1/UpdateService":
			fmt.Fprint(w, `{"FirmwareInventory":{"@odata.id":"/redfish/v1/Firmware"}}`)
		case "/redfish/v1/Firmware":
			fmt.Fprint(w, `{"Members":[{"Id":"bios","Version":"BIOS1"}]}`)
		case "/redfish/v1/Chassis":
			fmt.Fprint(w, `{"Members":[{"Id":"chassis","Thermal":{"@odata.id":"/redfish/v1/Thermal"}}]}`)
		case "/redfish/v1/Thermal":
			fmt.Fprint(w, `{"Temperatures":[{"Name":"CPU temperature","ReadingCelsius":45}],"Fans":[{"Name":"Fan1","Reading":2000,"ReadingUnits":"RPM"}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	raw, err := testTarget(t, server, false).collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	dto := collectionSummary(collectionRow{BMCProtocol: "redfish", RedfishSnapshot: raw, CollectedAt: &now})
	if !dto.ComponentDetailsSupported || dto.Partial || len(dto.Components["network"]) != 2 || diskReads.Load() != 2 {
		t.Fatalf("inventory mismatch: %s", raw)
	}
	drives := 0
	volumes := 0
	for _, item := range dto.Components["storage"] {
		if item.Kind == "drive" {
			drives++
			if item.CapacityBytes != 1000000000000 {
				t.Fatal("capacity lost")
			}
		}
		if item.Kind == "volume" {
			volumes++
		}
	}
	if drives != 2 || volumes != 1 || len(dto.Components["sensors"]) != 2 {
		t.Fatalf("missing detail: %s", raw)
	}
	updates, err := collectionAssetUpdates(raw)
	if err != nil {
		t.Fatal(err)
	}
	stored := updates["redfish_snapshot"].(clause.Expr).Vars[0].(string)
	persisted := collectionSummary(collectionRow{BMCProtocol: "redfish", RedfishSnapshot: json.RawMessage(stored), CollectedAt: &now})
	if len(persisted.Components["network"]) != 2 || len(persisted.Components["storage"]) != len(dto.Components["storage"]) {
		t.Fatal("detail lost in storage or DTO")
	}
	encoded, _ := json.Marshal(persisted)
	for _, secret := range []string{"hidden", "OEM", "@odata", "Password", "do-not-leak-password"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("leaked %s", secret)
		}
	}
}

func TestInventoryDriveAliases(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/redfish/v1/Drive/1":
			fmt.Fprint(w, `{"Id":"drive1","SerialNumber":"SN-1","Model":"SSD","CapacityBytes":1024,"Status":{"Health":"OK"}}`)
		case "/redfish/v1/Alias/1":
			fmt.Fprint(w, `{"Id":"alias1","SerialNumber":"SN-1","Model":"SSD","Manufacturer":"Vendor","CapacityBytes":2048,"Protocol":"SAS","Status":{"Health":"Warning"}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	c := inventoryCollector{target: testTarget(t, server, false), ctx: context.Background(), cache: map[string]map[string]any{}, details: hardwareDetails{Components: map[string][]hardwareComponent{}, ComponentStates: map[string]componentState{}}}
	var value any
	if err := json.Unmarshal([]byte(`{"Id":"controller","Drives":[{"@odata.id":"/redfish/v1/Drive/1"}],"Devices":[{"Id":"embedded","SerialNumber":"SN-1","PartNumber":"part1"}],"Links":{"Drives":[{"@odata.id":"/redfish/v1/Alias/1"}]}}`), &value); err != nil {
		t.Fatal(err)
	}
	c.walk("storage", "controller", value, map[string]bool{}, 0)
	items := c.details.Components["storage"]
	if len(items) != 2 || items[0].Kind != "controller" {
		t.Fatalf("drive aliases not merged: %+v", items)
	}
	drive := items[1]
	if drive.ID != "drive1" || drive.CapacityBytes != 1024 || drive.Health != "OK" || drive.Manufacturer != "Vendor" || drive.PartNumber != "part1" || drive.Protocol != "SAS" {
		t.Fatalf("missing fields or overwritten original values: %+v", drive)
	}
}

func TestInventoryHardwareIdentity(t *testing.T) {
	for _, tt := range []struct {
		name, group, kind, payload string
		want                       int
	}{
		{"different drive serials", "storage", "drive", `[{"Id":"1","SerialNumber":"SN-1","Model":"SSD"},{"Id":"1","SerialNumber":"SN-2","Model":"SSD"}]`, 2},
		{"conflicting drive model", "storage", "drive", `[{"Id":"1","SerialNumber":"SN-1","Model":"SSD"},{"Id":"2","SerialNumber":"SN-1","Model":"HDD"}]`, 2},
		{"conflicting drive manufacturer", "storage", "drive", `[{"Id":"1","SerialNumber":"SN-1","Manufacturer":"A"},{"Id":"2","SerialNumber":"SN-1","Manufacturer":"B"}]`, 2},
		{"volumes not merged", "storage", "volume", `[{"Id":"1","SerialNumber":"SN-1"},{"Id":"2","SerialNumber":"SN-1"}]`, 2},
		{"multiport shared serial", "network", "port", `[{"Id":"1","SerialNumber":"board1","MACAddress":"00:11:22:33:44:55"},{"Id":"1","SerialNumber":"board1","MACAddress":"00:11:22:33:44:66"}]`, 2},
		{"serial alone not network identity", "network", "adapter", `[{"Id":"1","SerialNumber":"board1"},{"Id":"1","SerialNumber":"board1"}]`, 2},
		{"same layer network aliases", "network", "interface", `[{"Id":"1","MACAddress":"00:11:22:aa:bb:cc"},{"Id":"2","PermanentMACAddress":"00-11-22-AA-BB-CC","SpeedMbps":25000}]`, 1},
		{"network layers preserved", "network", "adapter", `{"Id":"adapter","MACAddress":"00:11:22:33:44:55","Ports":[{"Id":"port","MACAddress":"00:11:22:33:44:55"}]}`, 2},
		{"zero MACs preserved", "network", "port", `[{"Id":"1","MACAddress":"00:00:00:00:00:00"},{"Id":"1","MACAddress":"00:00:00:00:00:00"}]`, 2},
		{"broadcast MACs preserved", "network", "port", `[{"Id":"1","MACAddress":"ff:ff:ff:ff:ff:ff"},{"Id":"1","MACAddress":"FF-FF-FF-FF-FF-FF"}]`, 2},
		{"invalid MACs preserved", "network", "port", `[{"Id":"1","MACAddress":"unknown"},{"Id":"1","MACAddress":"unknown"}]`, 2},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c := inventoryCollector{details: hardwareDetails{Components: map[string][]hardwareComponent{}, ComponentStates: map[string]componentState{}}}
			var value any
			if err := json.Unmarshal([]byte(tt.payload), &value); err != nil {
				t.Fatal(err)
			}
			c.walk(tt.group, tt.kind, value, map[string]bool{}, 0)
			items := c.details.Components[tt.group]
			if len(items) != tt.want {
				t.Fatalf("want %d components, got %+v", tt.want, items)
			}
			if tt.name == "same layer network aliases" && (items[0].ID != "1" || items[0].SpeedMbps != 25000) {
				t.Fatalf("network fields not merged: %+v", items)
			}
		})
	}
	for _, serial := range []string{"", " ", "unknown", " UNKNOWN ", "N/A", "NA", "None", "null", "Not Available", "Not Applicable", "Not Specified", "Unspecified", "To Be Filled By O.E.M.", "00000000", "00-00-00", "---"} {
		t.Run("placeholder serial "+serial, func(t *testing.T) {
			c := inventoryCollector{details: hardwareDetails{Components: map[string][]hardwareComponent{}, ComponentStates: map[string]componentState{}}}
			c.walk("storage", "drive", []any{
				map[string]any{"Id": "1", "Model": "SSD", "SerialNumber": serial},
				map[string]any{"Id": "1", "Model": "SSD", "SerialNumber": serial},
			}, map[string]bool{}, 0)
			if len(c.details.Components["storage"]) != 2 {
				t.Fatalf("placeholder serial merged distinct drives: %+v", c.details.Components["storage"])
			}
		})
	}
}

func TestInventoryNetworkLinkKinds(t *testing.T) {
	c := inventoryCollector{details: hardwareDetails{Components: map[string][]hardwareComponent{}, ComponentStates: map[string]componentState{}}}
	var value any
	if err := json.Unmarshal([]byte(`{"Id":"interface","Links":{"NetworkAdapter":{"Id":"adapter1","MACAddress":"00:11:22:33:44:55"},"NetworkAdapters":[{"Id":"adapter2","MACAddress":"00:11:22:33:44:66"}],"NetworkPorts":[{"Id":"port1","MACAddress":"00:11:22:33:44:55"}],"Ports":[{"Id":"port2","MACAddress":"00:11:22:33:44:66"}]}}`), &value); err != nil {
		t.Fatal(err)
	}
	c.walk("network", "interface", value, map[string]bool{}, 0)
	items := c.details.Components["network"]
	if len(items) != 5 {
		t.Fatalf("network links incorrectly merged: %+v", items)
	}
	for _, item := range items {
		want := "interface"
		if strings.HasPrefix(item.ID, "adapter") {
			want = "adapter"
		} else if strings.HasPrefix(item.ID, "port") {
			want = "port"
		}
		if item.Kind != want {
			t.Fatalf("%s: want kind %s, got %s", item.ID, want, item.Kind)
		}
	}
}

func TestInventoryFailureIsolation(t *testing.T) {
	var outside atomic.Int32
	external := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { outside.Add(1) }))
	defer external.Close()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/redfish/v1/Systems":
			fmt.Fprint(w, `{"Members":[{"@odata.id":"/redfish/v1/Systems/1"}]}`)
		case "/redfish/v1/Systems/1":
			fmt.Fprintf(w, `{"Name":"Server","Processors":{"@odata.id":"/redfish/v1/Denied"},"Memory":{"@odata.id":"/redfish/v1/Slow"},"Storage":{"@odata.id":"%s/redfish/v1/Secret?password=hidden"},"EthernetInterfaces":{"@odata.id":"/redfish/v1/NIC"}}`, external.URL)
		case "/redfish/v1/Denied":
			w.WriteHeader(403)
		case "/redfish/v1/Slow":
			<-r.Context().Done()
		case "/redfish/v1/NIC":
			fmt.Fprint(w, `{"Members":[{"Id":"nic"}],"Members@odata.nextLink":"/redfish/v1/NIC"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	target := testTarget(t, server, false)
	target.client.Timeout = 40 * time.Millisecond
	raw, err := target.collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var report computerSystemSummary
	if json.Unmarshal(raw, &report) != nil {
		t.Fatal("invalid snapshot")
	}
	if !report.Partial || outside.Load() != 0 || report.ComponentStates["processors"].Status != "failed" || report.ComponentStates["memory"].Failures[0] != "timeout" || report.ComponentStates["storage"].Failures[0] != "unsafe_link" || report.ComponentStates["network"].Status != "partial" || len(report.Components["network"]) != 1 || report.ComponentStates["managers"].Status != "not_supported" {
		t.Fatalf("incorrect failure isolation: %s", raw)
	}
	if strings.Contains(string(raw), "hidden") || strings.Contains(string(raw), external.URL) {
		t.Fatal("unsafe URL persisted")
	}
}

func TestInventoryNoLegacyTruncationAndBudget(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/redfish/v1/Systems":
			fmt.Fprint(w, `{"Members":[{"@odata.id":"/redfish/v1/Systems/1"}]}`)
		case "/redfish/v1/Systems/1":
			drives := []map[string]any{}
			for i := 0; i < 12; i++ {
				drives = append(drives, map[string]any{"Id": fmt.Sprint(i), "CapacityBytes": 1024})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"Name": "Server", "Storage": map[string]any{"Members": []any{map[string]any{"Id": "storage", "Drives": drives}}}, "NetworkInterfaces": map[string]any{"Members": []any{map[string]any{"Id": "interface", "Links": map[string]any{"NetworkAdapter": map[string]any{"@odata.id": "/redfish/v1/Adapter"}}}}}})
		case "/redfish/v1/Adapter":
			fmt.Fprint(w, `{"Id":"adapter","Ports":{"Members":[{"Id":"port","CurrentLinkSpeedMbps":25000}]}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	raw, err := testTarget(t, server, false).collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var report computerSystemSummary
	_ = json.Unmarshal(raw, &report)
	if len(report.Components["storage"]) != 13 || len(report.Components["network"]) != 3 {
		t.Fatalf("inventory truncated: %s", raw)
	}
	c := inventoryCollector{target: testTarget(t, server, false), ctx: context.Background(), requests: 256, cache: map[string]map[string]any{}, details: hardwareDetails{Components: map[string][]hardwareComponent{}, ComponentStates: map[string]componentState{}}}
	c.walk("network", "interface", map[string]any{"@odata.id": "/redfish/v1/Adapter"}, map[string]bool{}, 0)
	if !c.details.ComponentStates["network"].Partial || c.details.ComponentStates["network"].Failures[0] != "budget_exhausted" {
		t.Fatal("budget silently truncated")
	}
}

func TestInventoryLegacyAndWhitelist(t *testing.T) {
	dto := collectionSummary(collectionRow{BMCProtocol: "redfish", RedfishSnapshot: json.RawMessage(`{"Name":"Legacy","Secret":"hidden"}`)})
	if dto.ComponentDetailsSupported || len(dto.Components) != 0 {
		t.Fatal("legacy claims details")
	}
	raw := json.RawMessage(`{"Name":"System","component_details_supported":true,"components":{"network":[{"id":"nic","mac":"00:11:22:33:44:55","Secret":"hidden","name":"https://host/?password=hidden"}],"OEM":[{"name":"hidden"}]},"component_states":{"network":{"status":"failed","failures":["https://host/?hidden"]},"hidden":{"status":"failed"}}}`)
	updates, err := collectionAssetUpdates(raw)
	if err != nil {
		t.Fatal(err)
	}
	stored := updates["redfish_snapshot"].(clause.Expr).Vars[0].(string)
	if strings.Contains(stored, "hidden") || strings.Contains(stored, "https://") || strings.Contains(stored, "Secret") {
		t.Fatalf("unsafe snapshot: %s", stored)
	}
}
