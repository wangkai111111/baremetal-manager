package adapter

import (
	"baremetal-manager/backend/internal/model"
	"testing"
)

// TestAddress 验证 BMC 地址校验对有效地址和禁用地址的判断。
func TestAddress(t *testing.T) {
	for _, v := range []struct {
		addr  string
		valid bool
	}{{"192.0.2.10", true}, {"127.0.0.1", false}, {"localhost", false}, {"0.0.0.0", false}, {"224.0.0.1", false}} {
		_, err := address(model.Asset{BMCAddress: v.addr})
		if (err == nil) != v.valid {
			t.Errorf("address %q validity unexpected: %v", v.addr, err)
		}
	}
}
