package platform

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"baremetal-manager/backend/internal/pxeservice"
)

func readPXENetworkState(ctx context.Context) (pxeservice.Status, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	data, status, err := pxeservice.Call(ctx, "GET", "/status", nil)
	var state pxeservice.Status
	if err != nil || status != 200 {
		return state, fmt.Errorf("无法确认 PXE 实际运行状态，请恢复控制服务后再调整网络参数")
	}
	if err = json.Unmarshal(data, &state); err != nil {
		return state, err
	}
	return state, nil
}

func (a *App) guardPXENetworkChange(ctx context.Context, sc scope, r resource, id int64, in map[string]any, remove bool) error {
	if r.table != "networks" && r.table != "ip_pools" {
		return nil
	}
	changed := remove
	if !remove {
		var old map[string]any
		if err := queryScope(a.store.db.WithContext(ctx), r, sc).Where("id=?", id).Take(&old).Error; err != nil {
			return err
		}
		keys := []string{"subnet", "gateway", "vlan_id", "dhcp_enabled"}
		if r.table == "ip_pools" {
			keys = []string{"network_id", "start_address", "end_address"}
		}
		for _, key := range keys {
			value, exists := in[key]
			if !exists {
				continue
			}
			before, after := fmt.Sprint(old[key]), fmt.Sprint(value)
			if key == "gateway" || key == "start_address" || key == "end_address" {
				x, _ := hostAddress(old[key])
				y, _ := hostAddress(value)
				before = x.String()
				after = y.String()
			}
			if before != after {
				changed = true
			}
		}
	}
	if !changed {
		return nil
	}
	reader := a.networkState
	if reader == nil {
		reader = readPXENetworkState
	}
	state, err := reader(ctx)
	if err != nil {
		return networkError("无法确认 PXE 实际运行状态，请恢复控制服务后再调整网络参数")
	}
	if (state.DHCP || state.TFTP) && state.Config.TenantID == sc.Tenant && state.Config.ProjectID == sc.Project && ((r.table == "networks" && state.Config.NetworkID == id) || (r.table == "ip_pools" && state.Config.PoolID == id)) {
		return networkError("此子网或地址池正被运行中的 PXE 服务使用，请先停止 DHCP/TFTP 再调整")
	}
	return nil
}
