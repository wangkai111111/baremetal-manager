package handler

import (
	"encoding/json"
)

// MonitorResult 结构化监控数据（前端期望的格式）。
type MonitorResult struct {
	System   map[string]interface{} `json:"system"`   // 系统概要（型号、SN、UUID）
	CPU      map[string]interface{} `json:"cpu"`      // CPU 信息（型号、核心数、频率）
	Memory   map[string]interface{} `json:"memory"`   // 内存信息（容量、类型、插槽）
	Network  map[string]interface{} `json:"network"`  // 网卡信息（MAC、速率、状态）
	Storage  map[string]interface{} `json:"storage"`  // 存储信息（硬盘、RAID）
	BMC      map[string]interface{} `json:"bmc"`      // BMC 信息（固件版本、IP）
	Power    map[string]interface{} `json:"power"`    // 电源信息（功耗、状态）
	Firmware map[string]interface{} `json:"firmware"` // 固件版本清单
	Sensors  map[string]interface{} `json:"sensors"`  // 传感器数据
}

// TransformMonitorData 将 Redfish 原始 JSON 转换为结构化格式。
// 解析 Redfish ComputerSystem 响应，提取各子系统信息。
// 如果某个 Redfish 字段不存在，返回空 map 而不是报错。
func TransformMonitorData(rawData json.RawMessage) *MonitorResult {
	var data map[string]interface{}
	if err := json.Unmarshal(rawData, &data); err != nil {
		// 无法解析时返回空结构
		return &MonitorResult{
			System:   make(map[string]interface{}),
			CPU:      make(map[string]interface{}),
			Memory:   make(map[string]interface{}),
			Network:  make(map[string]interface{}),
			Storage:  make(map[string]interface{}),
			BMC:      make(map[string]interface{}),
			Power:    make(map[string]interface{}),
			Firmware: make(map[string]interface{}),
			Sensors:  make(map[string]interface{}),
		}
	}

	result := &MonitorResult{
		System:   extractSystem(data),
		CPU:      extractCPU(data),
		Memory:   extractMemory(data),
		Network:  make(map[string]interface{}),
		Storage:  make(map[string]interface{}),
		BMC:      extractBMC(data),
		Power:    extractPower(data),
		Firmware: extractFirmware(data),
		Sensors:  make(map[string]interface{}),
	}

	return result
}

// extractSystem 提取系统概要信息。
func extractSystem(data map[string]interface{}) map[string]interface{} {
	return map[string]interface{}{
		"manufacturer":  safeString(data, "Manufacturer"),
		"model":         safeString(data, "Model"),
		"serial_number": safeString(data, "SerialNumber"),
		"uuid":          safeString(data, "UUID"),
		"sku":           safeString(data, "SKU"),
		"part_number":   safeString(data, "PartNumber"),
		"status":        extractStatus(data),
	}
}

// extractCPU 提取 CPU 信息。
func extractCPU(data map[string]interface{}) map[string]interface{} {
	result := make(map[string]interface{})
	if ps, ok := data["ProcessorSummary"].(map[string]interface{}); ok {
		result["model"] = safeString(ps, "Model")
		result["count"] = safeNumber(ps, "Count")
		result["cores"] = safeNumber(ps, "CoreCount")
		result["threads"] = safeNumber(ps, "ThreadCount")
		result["max_speed_mhz"] = safeNumber(ps, "MaxSpeedMHz")
	}
	return result
}

// extractMemory 提取内存信息。
func extractMemory(data map[string]interface{}) map[string]interface{} {
	result := make(map[string]interface{})
	if ms, ok := data["MemorySummary"].(map[string]interface{}); ok {
		result["total_capacity_mib"] = safeNumber(ms, "TotalSystemMemoryGiB")
		result["status"] = extractNestedStatus(ms)
	}
	// 尝试获取详细内存信息
	if mem, ok := data["Memory"].(map[string]interface{}); ok {
		result["detail"] = mem
	}
	return result
}

// extractBMC 提取 BMC 信息。
func extractBMC(data map[string]interface{}) map[string]interface{} {
	result := make(map[string]interface{})
	// 从 Links.Manager 中提取 BMC 引用
	if links, ok := data["Links"].(map[string]interface{}); ok {
		if mgr, ok := links["Manager"].(map[string]interface{}); ok {
			result["manager_ref"] = safeString(mgr, "@odata.id")
		}
	}
	return result
}

// extractPower 提取电源信息。
func extractPower(data map[string]interface{}) map[string]interface{} {
	result := make(map[string]interface{})
	if ps, ok := data["PowerState"].(string); ok {
		result["state"] = ps
	}
	if actions, ok := data["Actions"].(map[string]interface{}); ok {
		result["actions"] = actions
	}
	return result
}

// extractFirmware 提取固件版本清单。
func extractFirmware(data map[string]interface{}) map[string]interface{} {
	result := make(map[string]interface{})
	if bv, ok := data["BiosVersion"].(string); ok && bv != "" {
		result["bios_version"] = bv
	}
	// 从 Bios 字段中提取更多固件信息
	if bios, ok := data["Bios"].(map[string]interface{}); ok {
		result["bios"] = map[string]interface{}{
			"version": safeString(bios, "Version"),
			"date":    safeString(bios, "ReleaseDate"),
		}
	}
	return result
}

// extractStatus 从顶层数据中提取设备状态。
func extractStatus(data map[string]interface{}) map[string]interface{} {
	if st, ok := data["Status"].(map[string]interface{}); ok {
		return map[string]interface{}{
			"state":  safeString(st, "State"),
			"health": safeString(st, "Health"),
		}
	}
	return map[string]interface{}{}
}

// extractNestedStatus 从嵌套 map 中提取状态信息。
func extractNestedStatus(data map[string]interface{}) map[string]interface{} {
	if st, ok := data["Status"].(map[string]interface{}); ok {
		return map[string]interface{}{
			"state":  safeString(st, "State"),
			"health": safeString(st, "Health"),
		}
	}
	return make(map[string]interface{})
}

// safeString 安全地从 map 中获取字符串值，不存在时返回空字符串。
func safeString(m map[string]interface{}, key string) string {
	if v, ok := m[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// safeNumber 安全地从 map 中获取数值，不存在时返回 0。
func safeNumber(m map[string]interface{}, key string) interface{} {
	if v, ok := m[key]; ok {
		return v
	}
	return 0
}

// MergeMonitorSensors 将传感器数据合并到监控结果中。
func (r *MonitorResult) MergeMonitorSensors(sensors map[string]interface{}) {
	if r.Sensors == nil {
		r.Sensors = make(map[string]interface{})
	}
	for k, v := range sensors {
		r.Sensors[k] = v
	}
}
