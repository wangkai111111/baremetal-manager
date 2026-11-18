package platform

import (
	"errors"
	"fmt"
	"net/netip"
	"strings"

	"gorm.io/gorm"
)

type networkValidationError struct{ message string }

func (e *networkValidationError) Error() string { return e.message }
func networkError(message string) error         { return &networkValidationError{message} }
func isNetworkResource(table string) bool {
	return table == "networks" || table == "ip_pools"
}
func hostAddress(value any) (netip.Addr, error) {
	return netip.ParseAddr(strings.Split(fmt.Sprint(value), "/")[0])
}
func usableAddress(prefix netip.Prefix, ip netip.Addr) bool {
	if !ip.IsValid() || !prefix.Contains(ip) || ip.IsUnspecified() || ip.IsLoopback() || ip.IsMulticast() {
		return false
	}
	if !ip.Is4() || prefix.Bits() >= 31 {
		return true
	}
	if ip == prefix.Masked().Addr() {
		return false
	}
	b := ip.As4()
	n := uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
	mask := ^uint32(0) >> prefix.Bits()
	return n&mask != mask
}

// Validate before SQL casts so malformed input returns an actionable message.
func normalizeNetworkInput(table string, in map[string]any) error {
	if !isNetworkResource(table) {
		return nil
	}
	for _, key := range []string{"gateway", "start_address", "end_address"} {
		v, exists := in[key]
		if !exists {
			continue
		}
		if key == "gateway" && (v == nil || v == "") {
			in[key] = nil
			continue
		}
		str, ok := v.(string)
		if !ok {
			return networkError("请输入有效的主机 IP 地址：" + key)
		}
		ip, err := netip.ParseAddr(strings.TrimSpace(str))
		if err != nil || ip.IsUnspecified() || ip.IsLoopback() || ip.IsMulticast() || ip.Is4In6() {
			return networkError("请输入有效的主机 IP 地址（不带掩码）：" + key)
		}
		in[key] = ip.String()
	}
	if v, exists := in["subnet"]; exists {
		str, ok := v.(string)
		if !ok {
			return networkError("子网必须为 CIDR 网段")
		}
		p, err := netip.ParsePrefix(strings.TrimSpace(str))
		if err != nil || p.Addr().Is4In6() || p.Addr().IsMulticast() || p.Addr().IsLoopback() || p.Bits() == 0 {
			return networkError("子网格式不正确，例如 192.168.10.0/24")
		}
		if p != p.Masked() {
			return networkError("请填写网络地址，当前网段应为 " + p.Masked().String())
		}
		in["subnet"] = p.String()
	}
	if v, exists := in["vlan_id"]; exists && v != nil {
		n, ok := v.(float64)
		if !ok || n != float64(int(n)) || n < 1 || n > 4094 {
			return networkError("VLAN ID 必须为 1–4094 的整数；不打标请留空")
		}
	}
	for _, key := range []string{"dhcp_enabled"} {
		if v, exists := in[key]; exists {
			if _, ok := v.(bool); !ok {
				return networkError(key + " 必须为布尔值")
			}
		}
	}
	if v, exists := in["dns_servers"]; exists {
		values, ok := v.([]any)
		if !ok || len(values) > 8 {
			return networkError("DNS 必须是 IP 地址数组，最多 8 项；清空请传 []")
		}
		ips := []string{}
		for _, value := range values {
			str, ok := value.(string)
			if !ok {
				return networkError("DNS 必须为 IP 地址")
			}
			ip, e := netip.ParseAddr(strings.TrimSpace(str))
			if e != nil || ip.IsUnspecified() || ip.IsMulticast() || ip.IsLoopback() || ip.Is4In6() {
				return networkError("DNS 必须为有效主机 IP 地址")
			}
			ips = append(ips, ip.String())
		}
		in["dns_servers"] = gorm.Expr("?::inet[]", "{"+strings.Join(ips, ",")+"}")
	}
	return nil
}

// Called under the project lock; all network writers share this serialization.
func validateNetworkRelations(tx *gorm.DB, r resource, sc scope, id int64, in, old map[string]any) error {
	if !isNetworkResource(r.table) {
		return nil
	}
	data := map[string]any{}
	for k, v := range old {
		data[k] = v
	}
	for k, v := range in {
		data[k] = v
	}
	scopeDB := func(table string) *gorm.DB {
		return tx.Table(table).Where("tenant_id=? AND project_id=? AND deleted_at IS NULL", sc.Tenant, sc.Project)
	}
	if r.table == "networks" {
		p, err := netip.ParsePrefix(fmt.Sprint(data["subnet"]))
		if err != nil {
			return networkError("子网 CIDR 不正确")
		}
		var gateway netip.Addr
		if data["gateway"] != nil {
			gateway, err = hostAddress(data["gateway"])
			if err != nil || !usableAddress(p, gateway) {
				return networkError("网关必须是子网内可用主机地址")
			}
		}
		if data["dhcp_enabled"] == true && (!p.Addr().Is4() || p.Bits() > 30) {
			return networkError("当前 DHCP 服务仅支持 IPv4 且前缀不大于 /30 的子网")
		}
		var n int64
		if err = scopeDB("networks").Where("id<>? AND subnet && ?::cidr AND COALESCE(vlan_id,0)=?", id, p.String(), asInt(data["vlan_id"])).Count(&n).Error; err != nil {
			return err
		}
		if n > 0 {
			return networkError("同一项目和 VLAN 内的子网不能重叠")
		}
		var addresses []struct{ StartAddress, EndAddress string }
		if err = scopeDB("ip_pools").Select("host(start_address) AS start_address,host(end_address) AS end_address").Where("network_id=?", id).Scan(&addresses).Error; err != nil {
			return err
		}
		for _, item := range addresses {
			start, _ := hostAddress(item.StartAddress)
			end, _ := hostAddress(item.EndAddress)
			if !usableAddress(p, start) || !usableAddress(p, end) {
				return networkError("修改后的子网不再包含已有地址池，请先调整地址池")
			}
			if gateway.IsValid() && gateway.Compare(start) >= 0 && gateway.Compare(end) <= 0 {
				return networkError("网关不能落在已有地址池内")
			}
		}
		return nil
	}
	var network struct {
		Subnet  string
		Gateway *string
	}
	if err := scopeDB("networks").Select("subnet::text,host(gateway) AS gateway").Where("id=?", data["network_id"]).Take(&network).Error; err != nil {
		return networkError("所选子网不存在或不属于当前项目")
	}
	prefix, err := netip.ParsePrefix(network.Subnet)
	if err != nil {
		return err
	}
	var gateway netip.Addr
	if network.Gateway != nil {
		gateway, _ = hostAddress(*network.Gateway)
	}
	start, e1 := hostAddress(data["start_address"])
	end, e2 := hostAddress(data["end_address"])
	if e1 != nil || e2 != nil || !usableAddress(prefix, start) || !usableAddress(prefix, end) || start.Compare(end) > 0 {
		return networkError("地址池起止地址必须是子网内可用主机地址，且起始地址不能大于结束地址")
	}
	if gateway.IsValid() && gateway.Compare(start) >= 0 && gateway.Compare(end) <= 0 {
		return networkError("地址池不能包含网关地址")
	}
	var n int64
	if err = scopeDB("ip_pools").Where("id<>? AND network_id=? AND start_address<=?::inet AND end_address>=?::inet", id, data["network_id"], end.String(), start.String()).Count(&n).Error; err != nil {
		return err
	}
	if n > 0 {
		return networkError("地址池与同一子网内的已有地址池重叠")
	}
	return nil
}

func networkValidationMessage(err error) (string, bool) {
	var validation *networkValidationError
	if errors.As(err, &validation) {
		return validation.Error(), true
	}
	return "", false
}
