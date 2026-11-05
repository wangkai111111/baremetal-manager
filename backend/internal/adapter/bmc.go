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

// redfishClient 复用到 BMC 的 HTTP 连接，避免每次子资源请求重复 TLS 握手。
var redfishClient = &http.Client{
	Timeout: 12 * time.Second,
	Transport: &http.Transport{
		Proxy:               nil,
		MaxIdleConns:        32,
		MaxIdleConnsPerHost: 32,
		IdleConnTimeout:     60 * time.Second,
		TLSClientConfig:     &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: true},
	},
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

// redfish 向设备发送不使用代理且不跟随重定向的 Redfish 请求。
func redfish(ctx context.Context, a model.Asset, method, path string, body string) ([]byte, error) {
	addr, err := address(a)
	if err != nil {
		return nil, err
	}
	u := url.URL{Scheme: "https", Host: addr, Path: path}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(a.BMCUser, a.BMCSecret)
	req.Header.Set("Content-Type", "application/json")
	resp, err := redfishClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("redfish status %d", resp.StatusCode)
	}
	var raw json.RawMessage
	err = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&raw)
	if err != nil && !(method == http.MethodPost && errors.Is(err, io.EOF)) {
		return nil, err
	}
	return raw, nil
}

// systemPath 通过 Redfish 集合接口解析主机系统资源路径。
// 不同厂商的系统资源标识不同（如 Dell iDRAC 为 System.Embedded.1），
// 因此不能写死 /redfish/v1/Systems/1。
func systemPath(ctx context.Context, a model.Asset) (string, error) {
	raw, err := redfish(ctx, a, "GET", "/redfish/v1/Systems", "")
	if err == nil {
		var col struct {
			Members []struct {
				ODataID string `json:"@odata.id"`
			} `json:"Members"`
		}
		if json.Unmarshal(raw, &col) == nil && len(col.Members) > 0 && col.Members[0].ODataID != "" {
			return col.Members[0].ODataID, nil
		}
		if _, e := redfish(ctx, a, "GET", "/redfish/v1/Systems/1", ""); e == nil {
			return "/redfish/v1/Systems/1", nil
		}
		return "", errors.New("no computer system resource")
	}
	// 集合接口不可用时回退到标准路径
	if _, e := redfish(ctx, a, "GET", "/redfish/v1/Systems/1", ""); e == nil {
		return "/redfish/v1/Systems/1", nil
	}
	return "", err
}

// resetTarget 解析系统资源的开机/关机/重启动作地址。
func resetTarget(ctx context.Context, a model.Asset) (string, error) {
	path, err := systemPath(ctx, a)
	if err != nil {
		return "", err
	}
	raw, err := redfish(ctx, a, "GET", path, "")
	if err != nil {
		return "", err
	}
	var system struct {
		Actions map[string]struct {
			Target string `json:"target"`
		} `json:"Actions"`
	}
	if json.Unmarshal(raw, &system) == nil {
		if act, ok := system.Actions["#ComputerSystem.Reset"]; ok && act.Target != "" {
			return act.Target, nil
		}
	}
	return path + "/Actions/ComputerSystem.Reset", nil
}

// Power 通过 Redfish 执行电源操作，失败时尝试 IPMI。
func (a *BMC) Power(ctx context.Context, asset model.Asset, action string) error {
	reset := map[string]string{"on": "On", "off": "ForceOff", "restart": "ForceRestart"}[action]
	if reset == "" {
		return errors.New("invalid power action")
	}
	target, err := resetTarget(ctx, asset)
	if err == nil {
		if _, e := redfish(ctx, asset, "POST", target, fmt.Sprintf(`{"ResetType":%q}`, reset)); e == nil {
			return nil
		} else {
			err = e
		}
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

// collectionIDs 读取 Redfish 集合资源的成员路径。
func collectionIDs(ctx context.Context, a model.Asset, path string) []string {
	raw, err := redfish(ctx, a, "GET", path, "")
	if err != nil {
		return nil
	}
	var col struct {
		Members []struct {
			ODataID string `json:"@odata.id"`
		} `json:"Members"`
	}
	if json.Unmarshal(raw, &col) != nil {
		return nil
	}
	ids := make([]string, 0, len(col.Members))
	for _, m := range col.Members {
		if m.ODataID != "" {
			ids = append(ids, m.ODataID)
		}
	}
	return ids
}

// getObject 读取单个 Redfish 资源并解析为 map，失败时返回 nil。
func getObject(ctx context.Context, a model.Asset, path string) map[string]interface{} {
	raw, err := redfish(ctx, a, "GET", path, "")
	if err != nil {
		return nil
	}
	var m map[string]interface{}
	if json.Unmarshal(raw, &m) != nil {
		return nil
	}
	return m
}

// objectIDs 从资源中提取指定数组字段里的 @odata.id 列表。
func objectIDs(res map[string]interface{}, field string) []string {
	items, ok := res[field].([]interface{})
	if !ok {
		return nil
	}
	ids := make([]string, 0, len(items))
	for _, item := range items {
		if obj, ok := item.(map[string]interface{}); ok {
			if id, ok := obj["@odata.id"].(string); ok && id != "" {
				ids = append(ids, id)
			}
		}
	}
	return ids
}

// enrichSystem 采集网卡、存储、BMC 管理器等子资源并附加到系统数据中。
// 集合资源取前若干项，避免在慢速 BMC 上请求过多。
func enrichSystem(ctx context.Context, a model.Asset, path string, system map[string]interface{}) {
	var wg sync.WaitGroup
	var mu sync.Mutex

	// 网卡信息
	wg.Add(1)
	go func() {
		defer wg.Done()
		ifaces := make([]map[string]interface{}, 0, 4)
		ids := collectionIDs(ctx, a, path+"/EthernetInterfaces")
		if len(ids) > 4 {
			ids = ids[:4]
		}
		for _, id := range ids {
			res := getObject(ctx, a, id)
			if res == nil {
				continue
			}
			ifaces = append(ifaces, map[string]interface{}{
				"id":         res["Id"],
				"name":       res["Name"],
				"mac":        res["MACAddress"],
				"speed_mbps": res["SpeedMbps"],
				"link":       res["LinkStatus"],
				"status":     res["Status"],
			})
		}
		if len(ifaces) > 0 {
			mu.Lock()
			system["NetworkInterfaces"] = ifaces
			mu.Unlock()
		}
	}()

	// 存储与硬盘信息
	wg.Add(1)
	go func() {
		defer wg.Done()
		drives := make([]map[string]interface{}, 0, 8)
		storageIDs := collectionIDs(ctx, a, path+"/Storage")
		if len(storageIDs) > 2 {
			storageIDs = storageIDs[:2]
		}
		for _, sid := range storageIDs {
			res := getObject(ctx, a, sid)
			if res == nil {
				continue
			}
			for _, did := range objectIDs(res, "Drives") {
				if len(drives) >= 8 {
					break
				}
				d := getObject(ctx, a, did)
				if d == nil {
					continue
				}
				drives = append(drives, map[string]interface{}{
					"id":             d["Id"],
					"name":           d["Name"],
					"model":          d["Model"],
					"serial_number":  d["SerialNumber"],
					"capacity_bytes": d["CapacityBytes"],
					"media_type":     d["MediaType"],
					"protocol":       d["Protocol"],
					"status":         d["Status"],
				})
			}
		}
		if len(drives) > 0 {
			mu.Lock()
			system["Drives"] = drives
			mu.Unlock()
		}
	}()

	// BMC 管理器信息
	wg.Add(1)
	go func() {
		defer wg.Done()
		ids := collectionIDs(ctx, a, "/redfish/v1/Managers")
		if len(ids) == 0 {
			return
		}
		mg := getObject(ctx, a, ids[0])
		if mg == nil {
			return
		}
		mu.Lock()
		system["BmcDetail"] = map[string]interface{}{
			"id":               mg["Id"],
			"model":            mg["Model"],
			"manager_type":     mg["ManagerType"],
			"firmware_version": mg["FirmwareVersion"],
			"status":           mg["Status"],
		}
		mu.Unlock()
	}()

	wg.Wait()
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
	var err error
	if path, pErr := systemPath(ctx, asset); pErr == nil {
		if raw, rErr := redfish(ctx, asset, "GET", path, ""); rErr == nil {
			var system map[string]interface{}
			if json.Unmarshal(raw, &system) == nil {
				enrichSystem(ctx, asset, path, system)
				if enriched, mErr := json.Marshal(system); mErr == nil {
					return enriched, nil
				}
			}
			return raw, nil
		} else {
			err = rErr
		}
	} else {
		err = pErr
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
	return json.Marshal(map[string]string{"protocol": "ipmi", "output": string(out), "redfish_error": err.Error()})
}
