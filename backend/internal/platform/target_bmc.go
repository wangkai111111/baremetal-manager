package platform

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"os/exec"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

type redfishRejection int

func (e redfishRejection) Error() string {
	return fmt.Sprintf("Redfish HTTP status %d", int(e))
}

type targetBMC struct {
	device device
	secret string
	base   *url.URL
	client *http.Client
}

func newTargetBMC(d device, secret string) (*targetBMC, error) {
	ip := net.ParseIP(d.BMCAddress)
	if ip == nil || ip.IsUnspecified() || ip.IsMulticast() {
		return nil, errors.New("invalid BMC IP address")
	}
	if d.BMCProtocol != "redfish" && d.BMCProtocol != "ipmi" {
		return nil, errors.New("unsupported BMC protocol")
	}
	if d.BMCPort < 1 || d.BMCPort > 65535 {
		return nil, errors.New("invalid BMC port")
	}
	t := &targetBMC{device: d, secret: secret, base: &url.URL{Scheme: "https", Host: net.JoinHostPort(ip.String(), strconv.Itoa(d.BMCPort))}}
	t.client = &http.Client{
		Timeout: 12 * time.Second,
		Transport: &http.Transport{
			Proxy:                 nil,
			TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: !d.BMCTLSVerify},
			DisableKeepAlives:     true,
			TLSHandshakeTimeout:   5 * time.Second,
			ResponseHeaderTimeout: 10 * time.Second,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	return t, nil
}

// Only root-relative resource paths are accepted; device-provided URLs never select an origin.
func (t *targetBMC) resource(path string) (*url.URL, error) {
	u, err := url.Parse(path)
	if err != nil || !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") || strings.ContainsAny(path, "\\\r\n") || u.IsAbs() || u.Host != "" || u.User != nil || u.Fragment != "" {
		return nil, errors.New("Redfish resource must be a same-origin path")
	}
	if !strings.HasPrefix(u.Path, "/redfish/v1/") || strings.HasPrefix(u.Path, "//") {
		return nil, errors.New("invalid Redfish resource path")
	}
	query, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return nil, errors.New("invalid Redfish pagination")
	}
	for key, values := range query {
		if (key != "$skip" && key != "$top" && key != "$skiptoken") || len(values) != 1 || len(values[0]) > 256 {
			return nil, errors.New("unsupported Redfish pagination")
		}
	}
	u.Scheme, u.Host = t.base.Scheme, t.base.Host
	return u, nil
}

func transportError(ctx context.Context, err error) error {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
		return errors.New("BMC request timed out")
	}
	if errors.Is(ctx.Err(), context.Canceled) {
		return errors.New("BMC request canceled")
	}
	var certErr *tls.CertificateVerificationError
	if errors.As(err, &certErr) {
		return errors.New("Redfish TLS certificate verification failed")
	}
	return errors.New("Redfish transport failed")
}

func (t *targetBMC) request(ctx context.Context, method, path string, body []byte) (json.RawMessage, error) {
	u, err := t.resource(path)
	if err != nil {
		return nil, err
	}
	// A non-rewindable body and no idempotency headers prevent automatic POST replay.
	var reader io.Reader
	if body != nil {
		reader = io.LimitReader(bytes.NewReader(body), int64(len(body)))
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), reader)
	if err != nil {
		return nil, errors.New("invalid Redfish request")
	}
	req.SetBasicAuth(t.device.BMCUser, t.secret)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := t.client.Do(req)
	if err != nil {
		return nil, transportError(ctx, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if resp.StatusCode < 500 {
			return nil, redfishRejection(resp.StatusCode)
		}
		return nil, fmt.Errorf("Redfish HTTP status %d", resp.StatusCode)
	}
	if method == http.MethodPost {
		return nil, nil
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
	if err != nil {
		return nil, transportError(ctx, err)
	}
	if len(raw) > 1<<20 || !json.Valid(raw) {
		return nil, errors.New("invalid Redfish JSON response")
	}
	return raw, nil
}

func (t *targetBMC) system(ctx context.Context) (string, json.RawMessage, error) {
	raw, err := t.request(ctx, http.MethodGet, "/redfish/v1/Systems", nil)
	if err != nil {
		return "", nil, err
	}
	var collection struct {
		Members []struct {
			ID string `json:"@odata.id"`
		} `json:"Members"`
	}
	if json.Unmarshal(raw, &collection) != nil || len(collection.Members) == 0 || collection.Members[0].ID == "" {
		return "", nil, errors.New("Redfish computer system not found")
	}
	path := collection.Members[0].ID
	raw, err = t.request(ctx, http.MethodGet, path, nil)
	return path, raw, err
}

type computerSystemSummary struct {
	hardwareDetails
	Name             string
	Manufacturer     string
	Model            string
	SerialNumber     string
	PowerState       string
	BiosVersion      string
	Status           *struct{ Health, HealthRollup string } `json:",omitempty"`
	ProcessorSummary *struct {
		Count int
		Model string
	} `json:",omitempty"`
	MemorySummary *struct{ TotalSystemMemoryGiB float64 } `json:",omitempty"`
}

func parseComputerSystemSummary(raw json.RawMessage) (computerSystemSummary, error) {
	var s computerSystemSummary
	if json.Unmarshal(raw, &s) != nil {
		return s, errors.New("invalid Redfish computer system response")
	}
	if strings.TrimSpace(s.Name+s.Manufacturer+s.Model+s.SerialNumber+s.BiosVersion) == "" &&
		(s.ProcessorSummary == nil || (s.ProcessorSummary.Count <= 0 && strings.TrimSpace(s.ProcessorSummary.Model) == "")) &&
		(s.MemorySummary == nil || s.MemorySummary.TotalSystemMemoryGiB <= 0) {
		return s, errors.New("Redfish computer system summary is empty")
	}
	s.Name, s.Manufacturer, s.Model = safeInventoryText(s.Name), safeInventoryText(s.Manufacturer), safeInventoryText(s.Model)
	s.SerialNumber, s.BiosVersion = safeInventoryText(s.SerialNumber), safeInventoryText(s.BiosVersion)
	s.PowerState = safeInventoryText(s.PowerState)
	if s.Status != nil {
		s.Status.Health, s.Status.HealthRollup = safeInventoryText(s.Status.Health), safeInventoryText(s.Status.HealthRollup)
	}
	if s.ProcessorSummary != nil {
		s.ProcessorSummary.Model = safeInventoryText(s.ProcessorSummary.Model)
	}
	s.hardwareDetails = sanitizeHardwareDetails(s.hardwareDetails)
	return s, nil
}

func (t *targetBMC) collect(ctx context.Context) (json.RawMessage, error) {
	if t.device.BMCProtocol != "redfish" {
		return nil, errors.New("hardware collection requires Redfish protocol")
	}
	ctx, cancel := context.WithTimeout(ctx, collectionDeadline)
	defer cancel()
	_, raw, err := t.system(ctx)
	if err != nil {
		return nil, err
	}
	snapshot, err := parseComputerSystemSummary(raw)
	if err != nil {
		return nil, err
	}
	snapshot.hardwareDetails = t.collectDetails(ctx, raw)
	return json.Marshal(snapshot)
}

func (t *targetBMC) ipmiCommand(ctx context.Context, action string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "ipmitool", "-I", "lanplus", "-H", t.device.BMCAddress, "-p", strconv.Itoa(t.device.BMCPort), "-U", t.device.BMCUser, "-E", "chassis", "power", action)
	cmd.Env = append(cmd.Environ(), "IPMI_PASSWORD="+t.secret)
	return cmd
}

// started is set only at the irreversible command boundary; failures after it require reconciliation.
func (t *targetBMC) power(ctx context.Context, kind string) (started bool, err error) {
	reset := map[string]string{"power_on": "On", "power_off": "ForceOff", "power_cycle": "ForceRestart"}[kind]
	if reset == "" {
		return false, errors.New("unsupported power operation")
	}
	if t.device.BMCProtocol == "ipmi" {
		cmd := t.ipmiCommand(ctx, map[string]string{"power_on": "on", "power_off": "off", "power_cycle": "cycle"}[kind])
		if err := cmd.Start(); err != nil {
			return false, errors.New("IPMI command could not start")
		}
		if err := cmd.Wait(); err != nil {
			if ctx.Err() != nil {
				return true, transportError(ctx, err)
			}
			return true, errors.New("IPMI command failed")
		}
		return true, nil
	}
	path, raw, err := t.system(ctx)
	if err != nil {
		return false, err
	}
	var system struct {
		Actions map[string]struct {
			Target string `json:"target"`
		}
	}
	if json.Unmarshal(raw, &system) != nil || string(raw) == "null" {
		return false, errors.New("invalid Redfish computer system response")
	}
	target := system.Actions["#ComputerSystem.Reset"].Target
	if target == "" {
		target = strings.TrimSuffix(path, "/") + "/Actions/ComputerSystem.Reset"
	}
	if _, err := t.resource(target); err != nil {
		return false, err
	}
	body, _ := json.Marshal(map[string]string{"ResetType": reset})
	var sent atomic.Bool
	ctx = httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{
		WroteHeaders: func() { sent.Store(true) },
	})
	_, err = t.request(ctx, http.MethodPost, target, body)
	var rejection redfishRejection
	if errors.As(err, &rejection) {
		return false, err
	}
	return sent.Load(), err
}

func completedPowerState(kind string) string {
	switch kind {
	case "power_on":
		return "on"
	case "power_off":
		return "off"
	default:
		return "unknown"
	}
}

func operationOutcome(kind string, started bool, err error) (status, message, level string) {
	if err == nil {
		return "succeeded", "device operation completed", "info"
	}
	if started && kind != "collect" {
		return "unknown", "device outcome unknown; manual reconciliation required: " + err.Error(), "error"
	}
	return "failed", "device operation failed: " + err.Error(), "error"
}
