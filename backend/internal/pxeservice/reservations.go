package pxeservice

import (
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Relay IDs are exact byte matches. Text is always rendered as hexadecimal so
// dnsmasq cannot misinterpret numeric text as hex or treat it as configuration.
type DHCPReservation struct {
	Name          string `json:"name"`
	Enabled       bool   `json:"enabled"`
	CircuitID     string `json:"circuit_id"`
	CircuitFormat string `json:"circuit_format"`
	RemoteID      string `json:"remote_id"`
	RemoteFormat  string `json:"remote_format"`
	MAC           string `json:"mac"`
	IP            string `json:"ip"`
}

func relayID(value, format string) ([]byte, error) {
	if format != "" && format != "text" && format != "hex" {
		return nil, errors.New("标识格式只能选择文本或十六进制")
	}
	if value == "" {
		return nil, nil
	}
	var data []byte
	if format == "hex" {
		parts := strings.Split(value, ":")
		for _, part := range parts {
			if len(part) != 2 {
				return nil, errors.New("十六进制标识需按字节用冒号分隔，例如 00:04:00:64")
			}
		}
		var err error
		data, err = hex.DecodeString(strings.Join(parts, ""))
		if err != nil {
			return nil, errors.New("标识包含无效十六进制字节")
		}
	} else {
		if strings.TrimSpace(value) == "" || strings.IndexFunc(value, unicode.IsControl) >= 0 {
			return nil, errors.New("文本标识不能为空白或包含控制字符")
		}
		data = []byte(value)
	}
	if len(data) > 128 {
		return nil, errors.New("每个 Relay 标识最多 128 字节")
	}
	return data, nil
}

func reservationMAC(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	mac, err := net.ParseMAC(value)
	if err != nil || len(mac) != 6 || mac[0]&1 != 0 || bytes.Equal(mac, make([]byte, 6)) {
		return "", errors.New("MAC 必须是完整、非零的单播 MAC 地址，不支持通配符")
	}
	return mac.String(), nil
}

func (c Config) validateReservations(subnet netip.Prefix, broadcast netip.Addr) error {
	if len(c.Reservations) > 128 {
		return errors.New("标签与固定 IP 规则最多 128 条")
	}
	if c.RelayProxy && c.DHCPMode != "relay" {
		return errors.New("续租 Relay 代理只能在三层 DHCP Relay 模式启用")
	}
	if c.Interface == TestVMBridge && (len(c.Reservations) > 0 || c.TaggedOnly || c.RelayProxy) {
		return errors.New("隔离测试 VM 使用内置固定地址，不支持自定义标签规则")
	}
	type match struct {
		circuit, remote []byte
		mac, ip, name   string
	}
	var active []match
	for i, rule := range c.Reservations {
		fail := func(message string) error {
			return fmt.Errorf("第 %d 条规则【%s】：%s", i+1, rule.Name, message)
		}
		if strings.TrimSpace(rule.Name) == "" || utf8.RuneCountInString(rule.Name) > 80 || strings.IndexFunc(rule.Name, unicode.IsControl) >= 0 {
			return fail("规则名称必填，最多 80 个字符且不含控制字符")
		}
		circuit, err := relayID(rule.CircuitID, rule.CircuitFormat)
		if err != nil {
			return fail("Circuit-ID：" + err.Error())
		}
		remote, err := relayID(rule.RemoteID, rule.RemoteFormat)
		if err != nil {
			return fail("Remote-ID：" + err.Error())
		}
		if len(circuit)+len(remote) > 251 {
			return fail("两个 Relay 标识合计不能超过 251 字节")
		}
		mac, err := reservationMAC(rule.MAC)
		if err != nil {
			return fail(err.Error())
		}
		if len(circuit)+len(remote) == 0 && mac == "" {
			return fail("至少填写 Circuit-ID、Remote-ID 或 MAC 中的一项")
		}
		if len(circuit)+len(remote) > 0 && c.DHCPMode != "relay" {
			return fail("Option 82 标签规则需要三层 DHCP Relay 模式")
		}
		ip, err := netip.ParseAddr(rule.IP)
		if err != nil || !ip.Is4() || !ip.IsGlobalUnicast() || !subnet.Contains(ip) || ip == subnet.Addr() || ip == broadcast || rule.IP == c.Gateway || rule.IP == c.Server {
			return fail("固定 IP 必须属于装机子网，且不能使用网络、广播、网关或 PXE 服务器地址")
		}
		if !rule.Enabled {
			continue
		}
		for _, old := range active {
			if old.ip == ip.String() {
				return fail("固定 IP 已由规则【" + old.name + "】使用")
			}
			if (len(circuit) == 0 || len(old.circuit) == 0 || bytes.Equal(circuit, old.circuit)) && (len(remote) == 0 || len(old.remote) == 0 || bytes.Equal(remote, old.remote)) && (mac == "" || old.mac == "" || mac == old.mac) {
				return fail("匹配条件与规则【" + old.name + "】可能同时命中，请用 Remote-ID / Circuit-ID / MAC 区分设备")
			}
		}
		active = append(active, match{circuit, remote, mac, ip.String(), rule.Name})
	}
	if c.TaggedOnly && len(active) == 0 {
		return errors.New("仅按规则分配时，至少需要一条已启用的规则")
	}
	return nil
}

func hexRelayID(value, format string) string {
	data, _ := relayID(value, format) // Validate is required before rendering/starting.
	parts := make([]string, len(data))
	for i, b := range data {
		parts[i] = fmt.Sprintf("%02x", b)
	}
	if len(parts) == 1 {
		return parts[0] + ":" // A colon makes dnsmasq parse a single byte as hex, not text.
	}
	return strings.Join(parts, ":")
}

func (c Config) renderReservations() string {
	var lines []string
	if c.RelayProxy {
		lines = append(lines, "dhcp-proxy")
	}
	for i, rule := range c.Reservations {
		if !rule.Enabled {
			continue
		}
		var filters []string
		for _, field := range []struct{ kind, value, format string }{{"circuit", rule.CircuitID, rule.CircuitFormat}, {"remote", rule.RemoteID, rule.RemoteFormat}} {
			if field.value == "" {
				continue
			}
			tag := fmt.Sprintf("bmm-r%d-%s", i+1, field.kind)
			lines = append(lines, fmt.Sprintf("dhcp-%sid=set:%s,%s", field.kind, tag, hexRelayID(field.value, field.format)))
			filters = append(filters, "tag:"+tag)
		}
		mac, _ := reservationMAC(rule.MAC)
		if mac == "" {
			mac = "*:*:*:*:*:*"
		}
		filters = append(filters, mac, "id:*", "set:bmm-reserved", rule.IP)
		lines = append(lines, "dhcp-host="+strings.Join(filters, ","))
	}
	if c.TaggedOnly {
		lines = append(lines, "dhcp-ignore=tag:!bmm-reserved")
	}
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n") + "\n"
}
