package adapter

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"strings"
	"sync"
	"time"

	"baremetal-manager/backend/internal/model"
)

// address 校验 BMC 地址是否为非回环、非未指定且非组播的 IP 地址。
func address(a model.Asset) (string, error) {
	host := a.BMCAddress
	if strings.Contains(host, ":") {
		h, _, err := net.SplitHostPort(host)
		if err != nil {
			return "", err
		}
		host = h
	}
	ip := net.ParseIP(host)
	if ip == nil || ip.IsLoopback() || ip.IsUnspecified() || ip.IsMulticast() {
		return "", errors.New("invalid BMC address")
	}
	return a.BMCAddress, nil
}

// redfish 向设备发送不使用代理且不跟随重定向的 Redfish 请求。
func redfish(ctx context.Context, a model.Asset, method, path string, body string) ([]byte, error) {
	addr, err := address(a)
	if err != nil {
		return nil, err
	}
	u := url.URL{Scheme: "https", Host: addr, Path: path}
	client := &http.Client{Timeout: 12 * time.Second, Transport: &http.Transport{Proxy: nil, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: true}}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(a.BMCUser, a.BMCSecret)
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("redfish status %d", resp.StatusCode)
	}
	var raw json.RawMessage
	err = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&raw)
	if err != nil && !(method == http.MethodPost && errors.Is(err, io.EOF)) {
		return nil, err
	}
	return raw, nil
}

// Power 通过 Redfish 执行电源操作，失败时尝试 IPMI。
func (a *BMC) Power(ctx context.Context, asset model.Asset, action string) error {
	reset := map[string]string{"on": "On", "off": "ForceOff", "restart": "ForceRestart"}[action]
	if reset == "" {
		return errors.New("invalid power action")
	}
	_, err := redfish(ctx, asset, "POST", "/redfish/v1/Systems/1/Actions/ComputerSystem.Reset", fmt.Sprintf(`{"ResetType":%q}`, reset))
	if err == nil {
		return nil
	}
	addr, e := address(asset)
	if e != nil {
		return e
	}
	cmd := exec.CommandContext(ctx, "ipmitool", "-I", "lanplus", "-H", addr, "-U", asset.BMCUser, "-E", "chassis", "power", map[string]string{"on": "on", "off": "off", "restart": "cycle"}[action])
	cmd.Env = append(cmd.Environ(), "IPMI_PASSWORD="+asset.BMCSecret)
	if output, e := cmd.CombinedOutput(); e != nil {
		return fmt.Errorf("redfish failed: %v; ipmi failed: %v (%s)", err, e, strings.TrimSpace(string(output)))
	}
	return nil
}

// BMC 提供设备的 Redfish 与 IPMI 管理操作。
type BMC struct {
	limiter *RateLimiter // BMC 并发限流器
	once    sync.Once    // 延迟初始化限流器
}

// NewBMC 创建 BMC 适配器实例并初始化默认限流器。
func NewBMC() *BMC {
	return &BMC{
		limiter: NewRateLimiter(10, 100*time.Millisecond),
	}
}

// getLimiter 获取限流器，支持延迟初始化。
func (a *BMC) getLimiter() *RateLimiter {
	a.once.Do(func() {
		if a.limiter == nil {
			a.limiter = NewRateLimiter(10, 100*time.Millisecond)
		}
	})
	return a.limiter
}

// Inspect 通过 Redfish 读取设备状态，失败时尝试 IPMI。
func (a *BMC) Inspect(ctx context.Context, asset model.Asset) (json.RawMessage, error) {
	raw, err := redfish(ctx, asset, "GET", "/redfish/v1/Systems/1", "")
	if err == nil {
		return raw, nil
	}
	addr, e := address(asset)
	if e != nil {
		return nil, e
	}
	cmd := exec.CommandContext(ctx, "ipmitool", "-I", "lanplus", "-H", addr, "-U", asset.BMCUser, "-E", "chassis", "status")
	cmd.Env = append(cmd.Environ(), "IPMI_PASSWORD="+asset.BMCSecret)
	out, e := cmd.Output()
	if e != nil {
		return nil, fmt.Errorf("BMC unavailable: %w", e)
	}
	return json.Marshal(map[string]string{"protocol": "ipmi", "output": string(out)})
}
