package adapter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"strings"

	"baremetal-manager/backend/internal/model"
)

// SetBootOrder 设置启动顺序。
// bootDevice: "pxe" (PXE启动), "disk" (硬盘启动), "cdrom" (光盘启动)
// persistent: 是否持久化设置
func (a *BMC) SetBootOrder(ctx context.Context, asset model.Asset, bootDevice string, persistent bool) error {
	enabled := "Once"
	if persistent {
		enabled = "Continuous"
	}
	body := fmt.Sprintf(`{"Boot":{"BootSourceOverrideTarget":%q,"BootSourceOverrideEnabled":%q}}`, bootDevice, enabled)

	// 优先使用 Redfish
	_, err := redfish(ctx, asset, http.MethodPatch, "/redfish/v1/Systems/1", body)
	if err == nil {
		return nil
	}

	// Redfish 失败时回退到 ipmitool
	addr, e := address(asset)
	if e != nil {
		return fmt.Errorf("redfish failed: %v; ipmi address error: %v", err, e)
	}
	args := []string{"-I", "lanplus", "-H", addr, "-U", asset.BMCUser, "-E", "chassis", "bootdev", bootDevice}
	cmd := exec.CommandContext(ctx, "ipmitool", args...)
	cmd.Env = append(cmd.Environ(), "IPMI_PASSWORD="+asset.BMCSecret)
	if output, e := cmd.CombinedOutput(); e != nil {
		return fmt.Errorf("redfish failed: %v; ipmi failed: %v (%s)", err, e, strings.TrimSpace(string(output)))
	}
	return nil
}

// ManageBMCUser 管理 BMC 本地用户。
// action: "create" / "modify" / "delete"
// targetUser/targetPass: 目标用户名和密码
// role: 角色 ID（1=Administrator, 2=Operator, 3=ReadOnly, 4=None）
func (a *BMC) ManageBMCUser(ctx context.Context, asset model.Asset, action string, targetUser, targetPass string, role int) error {
	// 优先使用 Redfish（仅支持 modify）
	if action == "modify" {
		body := fmt.Sprintf(`{"UserName":%q,"Password":%q,"RoleId":"%d"}`, targetUser, targetPass, role)
		_, err := redfish(ctx, asset, http.MethodPatch, "/redfish/v1/AccountService/Accounts/"+targetUser, body)
		if err == nil {
			return nil
		}
	}

	// Redfish 失败或不支持时回退到 ipmitool
	addr, e := address(asset)
	if e != nil {
		return e
	}

	var cmdArgs []string
	switch action {
	case "create":
		cmdArgs = []string{"user", "add", targetUser, targetPass}
	case "modify":
		cmdArgs = []string{"user", "set", "name", targetUser, targetPass}
	case "delete":
		cmdArgs = []string{"user", "delete", targetUser}
	default:
		return fmt.Errorf("unsupported user action: %s", action)
	}

	args := append([]string{"-I", "lanplus", "-H", addr, "-U", asset.BMCUser, "-E"}, cmdArgs...)
	cmd := exec.CommandContext(ctx, "ipmitool", args...)
	cmd.Env = append(cmd.Environ(), "IPMI_PASSWORD="+asset.BMCSecret)
	if output, e := cmd.CombinedOutput(); e != nil {
		return fmt.Errorf("ipmi user %s failed: %v (%s)", action, e, strings.TrimSpace(string(output)))
	}
	return nil
}

// SetBMCNetwork 修改 BMC 网络配置。
// ip/gateway: 新的 IP 地址和网关
// vlanID: VLAN ID（0 表示不设置）
func (a *BMC) SetBMCNetwork(ctx context.Context, asset model.Asset, ip, gateway string, vlanID int) error {
	// 优先使用 Redfish
	body := fmt.Sprintf(`{"IPv4StaticAddresses":[{"Address":%q,"Gateway":%q,"SubnetMask":"255.255.255.0"}]}`, ip, gateway)
	if vlanID > 0 {
		body = fmt.Sprintf(`{"VLAN":{"VLANEnable":true,"VLANId":%d},%s`, vlanID, body[1:])
	}
	_, err := redfish(ctx, asset, http.MethodPatch, "/redfish/v1/Managers/1/EthernetInterfaces/1", body)
	if err == nil {
		return nil
	}

	// Redfish 失败时回退到 ipmitool
	addr, e := address(asset)
	if e != nil {
		return e
	}

	// 设置 IP 地址
	cmd := exec.CommandContext(ctx, "ipmitool", "-I", "lanplus", "-H", addr, "-U", asset.BMCUser, "-E", "lan", "set", "1", "ipaddr", ip)
	cmd.Env = append(cmd.Environ(), "IPMI_PASSWORD="+asset.BMCSecret)
	if output, e := cmd.CombinedOutput(); e != nil {
		return fmt.Errorf("set ip failed: %v (%s)", e, strings.TrimSpace(string(output)))
	}

	// 设置网关
	cmd = exec.CommandContext(ctx, "ipmitool", "-I", "lanplus", "-H", addr, "-U", asset.BMCUser, "-E", "lan", "set", "1", "defgw", "ipaddr", gateway)
	cmd.Env = append(cmd.Environ(), "IPMI_PASSWORD="+asset.BMCSecret)
	if output, e := cmd.CombinedOutput(); e != nil {
		return fmt.Errorf("set gateway failed: %v (%s)", e, strings.TrimSpace(string(output)))
	}

	// 设置 VLAN（如果指定）
	if vlanID > 0 {
		cmd = exec.CommandContext(ctx, "ipmitool", "-I", "lanplus", "-H", addr, "-U", asset.BMCUser, "-E", "lan", "set", "1", "vlan", "id", fmt.Sprintf("%d", vlanID))
		cmd.Env = append(cmd.Environ(), "IPMI_PASSWORD="+asset.BMCSecret)
		if output, e := cmd.CombinedOutput(); e != nil {
			return fmt.Errorf("set vlan failed: %v (%s)", e, strings.TrimSpace(string(output)))
		}
	}

	return nil
}

// GetRAIDInfo 采集 RAID 卡信息。
// 返回 RAID 级别、物理盘列表、虚拟盘信息等。
func (a *BMC) GetRAIDInfo(ctx context.Context, asset model.Asset) (map[string]interface{}, error) {
	// 优先使用 Redfish
	raw, err := redfish(ctx, asset, http.MethodGet, "/redfish/v1/Systems/1/Storage", "")
	if err == nil {
		var result map[string]interface{}
		if e := json.Unmarshal(raw, &result); e != nil {
			return nil, e
		}
		return result, nil
	}

	// Redfish 失败时回退到 ipmitool
	addr, e := address(asset)
	if e != nil {
		return nil, e
	}
	cmd := exec.CommandContext(ctx, "ipmitool", "-I", "lanplus", "-H", addr, "-U", asset.BMCUser, "-E", "raid", "list")
	cmd.Env = append(cmd.Environ(), "IPMI_PASSWORD="+asset.BMCSecret)
	output, e := cmd.Output()
	if e != nil {
		return nil, fmt.Errorf("redfish failed: %v; ipmi failed: %v", err, e)
	}

	return map[string]interface{}{
		"protocol": "ipmi",
		"output":   string(output),
	}, nil
}

// GetSensors 采集传感器数据（温度/风扇/电源/电压）。
// 返回各类传感器的当前读数。
func (a *BMC) GetSensors(ctx context.Context, asset model.Asset) (map[string]interface{}, error) {
	result := make(map[string]interface{})

	// 优先使用 Redfish 获取温度数据
	thermalRaw, thermalErr := redfish(ctx, asset, http.MethodGet, "/redfish/v1/Chassis/1/Thermal", "")
	if thermalErr == nil {
		var thermal map[string]interface{}
		if e := json.Unmarshal(thermalRaw, &thermal); e == nil {
			result["thermal"] = parseRedfishThermal(thermal)
		}
	}

	// 获取电源数据
	powerRaw, powerErr := redfish(ctx, asset, http.MethodGet, "/redfish/v1/Chassis/1/Power", "")
	if powerErr == nil {
		var power map[string]interface{}
		if e := json.Unmarshal(powerRaw, &power); e == nil {
			result["power"] = parseRedfishPower(power)
		}
	}

	// 如果 Redfish 全部失败，回退到 ipmitool
	if thermalErr != nil && powerErr != nil {
		addr, e := address(asset)
		if e != nil {
			return nil, e
		}

		// 获取所有传感器数据
		cmd := exec.CommandContext(ctx, "ipmitool", "-I", "lanplus", "-H", addr, "-U", asset.BMCUser, "-E", "sdr")
		cmd.Env = append(cmd.Environ(), "IPMI_PASSWORD="+asset.BMCSecret)
		output, e := cmd.Output()
		if e != nil {
			return nil, fmt.Errorf("redfish thermal: %v, power: %v; ipmi sdr: %v", thermalErr, powerErr, e)
		}

		result["sdr"] = parseIPMISdrOutput(string(output))
	}

	return result, nil
}

// parseRedfishThermal 解析 Redfish Thermal 响应中的温度数据。
func parseRedfishThermal(data map[string]interface{}) map[string]interface{} {
	result := make(map[string]interface{})
	if temperatures, ok := data["Temperatures"].([]interface{}); ok {
		sensors := make([]map[string]interface{}, 0, len(temperatures))
		for _, t := range temperatures {
			if tm, ok := t.(map[string]interface{}); ok {
				sensor := map[string]interface{}{
					"name":   getMapString(tm, "Name"),
					"value":  getMapFloat(tm, "ReadingCelsius"),
					"unit":   "celsius",
					"status": getMapString(tm, "Status"),
				}
				sensors = append(sensors, sensor)
			}
		}
		result["temperatures"] = sensors
	}
	if fans, ok := data["Fans"].([]interface{}); ok {
		fanList := make([]map[string]interface{}, 0, len(fans))
		for _, f := range fans {
			if fm, ok := f.(map[string]interface{}); ok {
				fan := map[string]interface{}{
					"name":   getMapString(fm, "Name"),
					"value":  getMapFloat(fm, "CurrentReading"),
					"unit":   "rpm",
					"status": getMapString(fm, "Status"),
				}
				fanList = append(fanList, fan)
			}
		}
		result["fans"] = fanList
	}
	return result
}

// parseRedfishPower 解析 Redfish Power 响应中的电源数据。
func parseRedfishPower(data map[string]interface{}) map[string]interface{} {
	result := make(map[string]interface{})
	if powerControl, ok := data["PowerControl"].([]interface{}); ok && len(powerControl) > 0 {
		if pc, ok := powerControl[0].(map[string]interface{}); ok {
			result["power_consumed"] = getMapFloat(pc, "PowerConsumedWatts")
			result["power_capacity"] = getMapFloat(pc, "PowerCapacityWatts")
			result["unit"] = "watts"
		}
	}
	if voltages, ok := data["Voltages"].([]interface{}); ok {
		vList := make([]map[string]interface{}, 0, len(voltages))
		for _, v := range voltages {
			if vm, ok := v.(map[string]interface{}); ok {
				voltage := map[string]interface{}{
					"name":   getMapString(vm, "Name"),
					"value":  getMapFloat(vm, "ReadingVolts"),
					"unit":   "volts",
					"status": getMapString(vm, "Status"),
				}
				vList = append(vList, voltage)
			}
		}
		result["voltages"] = vList
	}
	return result
}

// parseIPMISdrOutput 解析 ipmitool sdr 命令输出。
func parseIPMISdrOutput(output string) map[string]interface{} {
	result := make(map[string]interface{})
	lines := strings.Split(output, "\n")
	sensors := make([]map[string]interface{}, 0)
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "|", 4)
		if len(parts) >= 3 {
			sensor := map[string]interface{}{
				"name":   strings.TrimSpace(parts[0]),
				"value":  strings.TrimSpace(parts[1]),
				"status": strings.TrimSpace(parts[2]),
			}
			sensors = append(sensors, sensor)
		}
	}
	result["sensors"] = sensors
	return result
}

// getMapString 安全地从 map 中获取字符串值。
func getMapString(m map[string]interface{}, key string) string {
	if v, ok := m[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// getMapFloat 安全地从 map 中获取浮点数值。
func getMapFloat(m map[string]interface{}, key string) float64 {
	if v, ok := m[key]; ok {
		switch n := v.(type) {
		case float64:
			return n
		case int:
			return float64(n)
		case json.Number:
			f, _ := n.Float64()
			return f
		}
	}
	return 0
}
