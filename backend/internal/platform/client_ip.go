package platform

import (
	"fmt"
	"net/netip"
	"strings"
	"unicode"
)

// Trust only explicitly configured ingress addresses; an empty value disables
// forwarding headers, so direct callers cannot forge their operation/audit IP.
func trustedProxyCIDRs(value string) ([]string, error) {
	var proxies []string
	for _, entry := range strings.FieldsFunc(value, func(r rune) bool { return r == ',' || unicode.IsSpace(r) }) {
		if addr, err := netip.ParseAddr(entry); err == nil && addr.Zone() == "" {
			proxies = append(proxies, addr.Unmap().String())
			continue
		}
		prefix, err := netip.ParsePrefix(entry)
		if err != nil || prefix.Addr().Is4In6() {
			return nil, fmt.Errorf("TRUSTED_PROXY_CIDRS contains invalid IP/CIDR: %q", entry)
		}
		proxies = append(proxies, prefix.Masked().String())
	}
	return proxies, nil
}
