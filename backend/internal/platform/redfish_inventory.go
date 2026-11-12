package platform

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"reflect"
	"strings"
	"time"
)

// Only these normalized fields can cross the snapshot/API boundary.
type hardwareComponent struct {
	ID            string  `json:"id,omitempty"`
	Name          string  `json:"name,omitempty"`
	Kind          string  `json:"kind,omitempty"`
	Manufacturer  string  `json:"manufacturer,omitempty"`
	Model         string  `json:"model,omitempty"`
	SerialNumber  string  `json:"serial_number,omitempty"`
	PartNumber    string  `json:"part_number,omitempty"`
	Health        string  `json:"health,omitempty"`
	State         string  `json:"state,omitempty"`
	Cores         float64 `json:"cores,omitempty"`
	Threads       float64 `json:"threads,omitempty"`
	MaxSpeedMHz   float64 `json:"max_speed_mhz,omitempty"`
	CapacityMiB   float64 `json:"capacity_mib,omitempty"`
	CapacityBytes float64 `json:"capacity_bytes,omitempty"`
	MediaType     string  `json:"media_type,omitempty"`
	Protocol      string  `json:"protocol,omitempty"`
	RAIDType      string  `json:"raid_type,omitempty"`
	MAC           string  `json:"mac,omitempty"`
	SpeedMbps     float64 `json:"speed_mbps,omitempty"`
	Link          string  `json:"link,omitempty"`
	Version       string  `json:"version,omitempty"`
	Reading       float64 `json:"reading,omitempty"`
	Units         string  `json:"units,omitempty"`
}
type componentState struct {
	Supported bool     `json:"supported"`
	Partial   bool     `json:"partial"`
	Status    string   `json:"status"`
	Failures  []string `json:"failures,omitempty"`
}
type hardwareDetails struct {
	ComponentDetailsSupported bool                           `json:"component_details_supported,omitempty"`
	Partial                   bool                           `json:"partial,omitempty"`
	Components                map[string][]hardwareComponent `json:"components,omitempty"`
	ComponentStates           map[string]componentState      `json:"component_states,omitempty"`
}
type inventoryCollector struct {
	target                 *targetBMC
	ctx                    context.Context
	cache                  map[string]map[string]any
	requests, bytes, items int
	details                hardwareDetails
}

func object(v any) map[string]any                  { m, _ := v.(map[string]any); return m }
func text(m map[string]any, key string) string     { s, _ := m[key].(string); return s }
func numeric(m map[string]any, key string) float64 { n, _ := m[key].(float64); return n }
func reference(v any) string                       { return text(object(v), "@odata.id") }
func safeInventoryText(s string) string {
	// Inventory text is not a place to return device URLs (including query credentials).
	if strings.Contains(s, "://") || strings.Contains(s, "?") || strings.HasPrefix(s, "/") {
		return ""
	}
	if len(s) > 512 {
		s = s[:512]
	}
	return strings.Map(func(r rune) rune {
		if r < 32 {
			return -1
		}
		return r
	}, s)
}
func normalizedComponent(m map[string]any, kind string) hardwareComponent {
	str := func(k string) string { return safeInventoryText(text(m, k)) }
	status := object(m["Status"])
	c := hardwareComponent{ID: str("Id"), Name: str("Name"), Kind: kind, Manufacturer: str("Manufacturer"), Model: str("Model"), SerialNumber: str("SerialNumber"), PartNumber: str("PartNumber"), Health: safeInventoryText(text(status, "Health")), State: safeInventoryText(text(status, "State")), Cores: numeric(m, "TotalCores"), Threads: numeric(m, "TotalThreads"), MaxSpeedMHz: numeric(m, "MaxSpeedMHz"), CapacityMiB: numeric(m, "CapacityMiB"), CapacityBytes: numeric(m, "CapacityBytes"), MediaType: str("MediaType"), Protocol: str("Protocol"), RAIDType: str("RAIDType"), MAC: str("MACAddress"), SpeedMbps: numeric(m, "SpeedMbps"), Link: str("LinkStatus"), Version: str("Version"), Reading: numeric(m, "Reading"), Units: str("ReadingUnits")}
	if c.Version == "" {
		c.Version = str("FirmwareVersion")
	}
	if c.MAC == "" {
		c.MAC = str("PermanentMACAddress")
	}
	if c.SpeedMbps == 0 {
		c.SpeedMbps = numeric(m, "CurrentLinkSpeedMbps")
	}
	if c.Link == "" {
		c.Link = str("LinkState")
	}
	for _, reading := range []struct{ key, unit string }{{"ReadingCelsius", "°C"}, {"ReadingRPM", "RPM"}, {"PowerConsumedWatts", "W"}, {"PowerOutputWatts", "W"}, {"LineInputVoltage", "V"}} {
		if _, ok := m[reading.key]; ok {
			c.Reading, c.Units = numeric(m, reading.key), reading.unit
			break
		}
	}
	if c.Units == "" {
		c.Units = str("ReadingUnits")
	}
	if addresses, ok := m["AssociatedNetworkAddresses"].([]any); ok && c.MAC == "" && len(addresses) > 0 {
		c.MAC, _ = addresses[0].(string)
		c.MAC = safeInventoryText(c.MAC)
	}
	return c
}
func inventorySerial(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	switch s {
	case "", "unknown", "n/a", "na", "none", "null", "not available", "not applicable", "not specified", "unspecified", "to be filled by o.e.m.":
		return ""
	}
	if strings.Trim(s, "0 -:.") == "" {
		return ""
	}
	return s
}

func inventoryMAC(s string) string {
	mac, err := net.ParseMAC(strings.TrimSpace(s))
	if err != nil || len(mac) != 6 {
		return ""
	}
	if mac.String() == "00:00:00:00:00:00" || mac.String() == "ff:ff:ff:ff:ff:ff" {
		return ""
	}
	return mac.String()
}

func (c *inventoryCollector) appendComponent(group string, item hardwareComponent) {
	for i := range c.details.Components[group] {
		existing := &c.details.Components[group][i]
		if existing.Kind != item.Kind {
			continue
		}
		match := false
		if group == "storage" && item.Kind == "drive" {
			serial := inventorySerial(item.SerialNumber)
			compatible := func(a, b string) bool {
				a, b = strings.TrimSpace(a), strings.TrimSpace(b)
				return a == "" || b == "" || strings.EqualFold(a, b)
			}
			match = serial != "" && serial == inventorySerial(existing.SerialNumber) && compatible(existing.Model, item.Model) && compatible(existing.Manufacturer, item.Manufacturer)
		} else if group == "network" {
			mac := inventoryMAC(item.MAC)
			match = mac != "" && mac == inventoryMAC(existing.MAC)
		}
		if match {
			dst, src := reflect.ValueOf(existing).Elem(), reflect.ValueOf(item)
			for j := 0; j < dst.NumField(); j++ {
				if dst.Field(j).IsZero() {
					dst.Field(j).Set(src.Field(j))
				}
			}
			return
		}
	}
	c.details.Components[group] = append(c.details.Components[group], item)
}

func (c *inventoryCollector) get(path string) (map[string]any, error) {
	u, err := c.target.resource(path)
	if err != nil {
		return nil, errors.New("unsafe_link")
	}
	key := u.RequestURI()
	if m, ok := c.cache[key]; ok {
		return m, nil
	}
	if c.requests >= 256 || c.bytes >= 16<<20 || c.ctx.Err() != nil {
		return nil, errors.New("budget_exhausted")
	}
	c.requests++
	raw, err := c.target.request(c.ctx, http.MethodGet, path, nil)
	if err != nil {
		var status redfishRejection
		if errors.As(err, &status) {
			switch int(status) {
			case 404:
				return nil, errors.New("not_supported")
			case 401, 403:
				return nil, errors.New("access_denied")
			}
		}
		if strings.Contains(err.Error(), "timed out") {
			return nil, errors.New("timeout")
		}
		return nil, errors.New("request_failed")
	}
	c.bytes += len(raw)
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil || m == nil {
		return nil, errors.New("invalid_response")
	}
	c.cache[key] = m
	return m, nil
}
func (c *inventoryCollector) fail(group, reason string) {
	s := c.details.ComponentStates[group]
	if reason != "not_supported" {
		s.Partial = true
	}
	if len(s.Failures) < 8 {
		found := false
		for _, v := range s.Failures {
			if v == reason {
				found = true
			}
		}
		if !found {
			s.Failures = append(s.Failures, reason)
		}
	}
	c.details.ComponentStates[group] = s
}

var inventoryChildren = map[string][]struct{ field, kind string }{
	"storage": {{"StorageControllers", "controller"}, {"Controllers", "controller"}, {"Drives", "drive"}, {"Volumes", "volume"}, {"Devices", "drive"}},
	"network": {{"NetworkAdapters", "adapter"}, {"NetworkPorts", "port"}, {"Ports", "port"}, {"NetworkDeviceFunctions", "network_function"}, {"EthernetInterfaces", "interface"}},
	"sensors": {{"Thermal", "thermal"}, {"Power", "power"}, {"ThermalSubsystem", "thermal"}, {"PowerSubsystem", "power"}, {"Sensors", "sensor"}, {"Temperatures", "temperature"}, {"Fans", "fan"}, {"PowerControl", "power"}, {"PowerSupplies", "power_supply"}, {"Voltages", "voltage"}},
}

func (c *inventoryCollector) walk(group, kind string, value any, visited map[string]bool, depth int) {
	if value == nil {
		return
	}
	if depth > 16 || c.items >= 2048 {
		c.fail(group, "budget_exhausted")
		return
	}
	if list, ok := value.([]any); ok {
		for _, v := range list {
			c.walk(group, kind, v, visited, depth+1)
		}
		return
	}
	m := object(value)
	if m == nil {
		c.fail(group, "invalid_response")
		return
	}
	if path := reference(m); path != "" {
		u, err := c.target.resource(path)
		if err != nil {
			c.fail(group, "unsafe_link")
			return
		}
		key := u.RequestURI()
		if visited[key] {
			return
		}
		visited[key] = true
		m, err = c.get(path)
		if err != nil {
			c.fail(group, err.Error())
			return
		}
	}
	if kind != "chassis" {
		s := c.details.ComponentStates[group]
		s.Supported = true
		c.details.ComponentStates[group] = s
	}
	if members, ok := m["Members"]; ok {
		c.walk(group, kind, members, visited, depth+1)
		if next := text(m, "Members@odata.nextLink"); next != "" {
			u, err := c.target.resource(next)
			if err != nil {
				c.fail(group, "unsafe_link")
			} else if visited[u.RequestURI()] {
				c.fail(group, "pagination_cycle")
			} else {
				c.walk(group, kind, map[string]any{"@odata.id": next}, visited, depth)
			}
		}
		return
	}
	if kind != "chassis" && kind != "thermal" && kind != "power" && (text(m, "Id") != "" || text(m, "Name") != "") {
		c.items++
		c.appendComponent(group, normalizedComponent(m, kind))
	}
	for _, child := range inventoryChildren[group] {
		c.walk(group, child.kind, m[child.field], visited, depth+1)
	}
	if group == "network" {
		c.walk(group, "ethernet", m["Ethernet"], visited, depth+1)
		links := object(m["Links"])
		for _, child := range []struct{ field, kind string }{{"NetworkAdapter", "adapter"}, {"NetworkAdapters", "adapter"}, {"NetworkPorts", "port"}, {"Ports", "port"}} {
			c.walk(group, child.kind, links[child.field], visited, depth+1)
		}
	}
	if group == "storage" {
		links := object(m["Links"])
		c.walk(group, "drive", links["Drives"], visited, depth+1)
	}
}
func (t *targetBMC) collectDetails(ctx context.Context, system json.RawMessage) hardwareDetails {
	c := inventoryCollector{target: t, ctx: ctx, cache: map[string]map[string]any{}, details: hardwareDetails{ComponentDetailsSupported: true, Components: map[string][]hardwareComponent{}, ComponentStates: map[string]componentState{}}}
	var sys map[string]any
	_ = json.Unmarshal(system, &sys)
	for _, g := range []string{"processors", "memory", "storage", "network", "managers", "firmware", "sensors"} {
		c.details.Components[g] = []hardwareComponent{}
		c.details.ComponentStates[g] = componentState{Status: "not_supported"}
	}
	visitedGroups := map[string]map[string]bool{}
	for group := range c.details.ComponentStates {
		visitedGroups[group] = map[string]bool{}
	}
	for _, spec := range []struct{ group, field, kind string }{{"processors", "Processors", "processor"}, {"memory", "Memory", "memory"}, {"storage", "Storage", "storage"}, {"storage", "SimpleStorage", "controller"}, {"network", "EthernetInterfaces", "interface"}, {"network", "NetworkInterfaces", "interface"}} {
		c.walk(spec.group, spec.kind, sys[spec.field], visitedGroups[spec.group], 0)
	}
	// These standard service resources are optional; no address discovery or writes are performed.
	c.walk("managers", "manager", map[string]any{"@odata.id": "/redfish/v1/Managers"}, map[string]bool{}, 0)
	if update, err := c.get("/redfish/v1/UpdateService"); err == nil {
		c.walk("firmware", "firmware", update["FirmwareInventory"], map[string]bool{}, 0)
	} else {
		c.fail("firmware", err.Error())
	}
	// Chassis may expose both adapter inventory and environmental readings.
	chassis, err := c.get("/redfish/v1/Chassis")
	if err != nil {
		c.fail("sensors", err.Error())
		c.fail("network", err.Error())
	} else {
		// Walk the collection using the normal bounded traversal for each applicable category.
		chassis["@odata.id"] = "/redfish/v1/Chassis"
		c.walk("sensors", "chassis", chassis, map[string]bool{}, 0)
		c.walk("network", "chassis", chassis, visitedGroups["network"], 0)
	}
	for group, s := range c.details.ComponentStates {
		if s.Partial {
			if s.Supported {
				s.Status = "partial"
			} else {
				s.Status = "failed"
			}
			c.details.Partial = true
		} else if s.Supported {
			s.Status = "succeeded"
			if len(s.Failures) > 0 {
				s.Partial = true
				s.Status = "partial"
				c.details.Partial = true
			}
		}
		c.details.ComponentStates[group] = s
	}
	return c.details
}

func sanitizeHardwareDetails(d hardwareDetails) hardwareDetails {
	out := hardwareDetails{ComponentDetailsSupported: d.ComponentDetailsSupported, Partial: d.Partial, Components: map[string][]hardwareComponent{}, ComponentStates: map[string]componentState{}}
	remaining := 2048
	for _, group := range []string{"processors", "memory", "storage", "network", "managers", "firmware", "sensors"} {
		if d.ComponentDetailsSupported {
			out.Components[group] = []hardwareComponent{}
		}
		for _, item := range d.Components[group] {
			if remaining == 0 {
				out.Partial = true
				break
			}
			remaining--
			for _, s := range []*string{&item.ID, &item.Name, &item.Kind, &item.Manufacturer, &item.Model, &item.SerialNumber, &item.PartNumber, &item.Health, &item.State, &item.MediaType, &item.Protocol, &item.RAIDType, &item.MAC, &item.Link, &item.Version, &item.Units} {
				*s = safeInventoryText(*s)
			}
			out.Components[group] = append(out.Components[group], item)
		}
		if state, ok := d.ComponentStates[group]; ok {
			switch state.Status {
			case "succeeded", "partial", "failed", "not_supported":
			default:
				state.Status = "failed"
			}
			failures := []string{}
			for _, reason := range state.Failures {
				switch reason {
				case "not_supported", "access_denied", "timeout", "request_failed", "invalid_response", "budget_exhausted", "unsafe_link", "pagination_cycle":
					if len(failures) < 8 {
						failures = append(failures, reason)
					}
				}
			}
			state.Failures = failures
			out.ComponentStates[group] = state
		}
	}
	return out
}

const collectionDeadline = 90 * time.Second
