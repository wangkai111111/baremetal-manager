package handler

import (
	"fmt"
	"net"
	"strconv"
	"time"

	"baremetal-manager/backend/internal/model"
	"baremetal-manager/backend/internal/repository"
	"baremetal-manager/backend/internal/service"
	"github.com/gin-gonic/gin"
)

// RegisterNetworkExtRoutes 注册网络管理增强路由
func RegisterNetworkExtRoutes(rg *gin.RouterGroup, store *repository.Store, svc *service.Service) {
	// IP 地址池管理
	rg.GET("/ip-pools", listIPPools(store))
	rg.POST("/ip-pools", createIPPool(store))
	rg.PUT("/ip-pools/:id", updateIPPool(store))
	rg.DELETE("/ip-pools/:id", deleteIPPool(store))

	// 自动分配 IP
	rg.POST("/networks/:networkId/auto-assign", autoAssignIP(store, svc))

	// 释放 IP
	rg.POST("/ip-allocations/:id/release", releaseIP(store))

	// PXE 配置
	rg.GET("/pxe-config", getPXEConfig(store))
	rg.PUT("/pxe-config", updatePXEConfig(store))
}

// listIPPools 分页列出项目下的 IP 地址池
func listIPPools(store *repository.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		projectID := pid(c)
		page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
		pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))

		var items []model.IPPool
		query := store.DB.Where("project_id = ? AND deleted_at IS NULL", projectID).Order("id desc")
		result, err := store.Paginate(query, page, pageSize, &items)
		if err != nil {
			Fail(c, 500, "query_failed", "查询IP地址池列表失败")
			return
		}
		PageOK(c, result)
	}
}

// createIPPool 创建 IP 地址池
func createIPPool(store *repository.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		var input struct {
			Name      string `json:"name"`
			NetworkID uint   `json:"network_id"`
			CIDR      string `json:"cidr"`
			Gateway   string `json:"gateway"`
		}
		if c.ShouldBindJSON(&input) != nil || input.Name == "" || input.NetworkID == 0 || input.CIDR == "" {
			Fail(c, 400, "invalid_request", "地址池名称、网络ID和CIDR不能为空")
			return
		}

		// 校验 CIDR 格式
		if _, _, err := net.ParseCIDR(input.CIDR); err != nil {
			Fail(c, 400, "invalid_cidr", "CIDR格式不正确")
			return
		}

		projectID := pid(c)
		userID := uid(c)

		// 校验网络归属
		var network model.Network
		if err := store.DB.Where("project_id = ? AND id = ?", projectID, input.NetworkID).First(&network).Error; err != nil {
			Fail(c, 404, "network_not_found", "网络不存在")
			return
		}

		pool := model.IPPool{
			Name:      input.Name,
			NetworkID: input.NetworkID,
			CIDR:      input.CIDR,
			Gateway:   input.Gateway,
			ProjectID: projectID,
			CreateBy:  userID,
			UpdateBy:  userID,
		}
		if err := store.DB.Create(&pool).Error; err != nil {
			Fail(c, 500, "create_failed", "创建IP地址池失败")
			return
		}
		store.Audit(userID, projectID, "ip_pool.create", fmt.Sprint(pool.ID))
		Created(c, pool)
	}
}

// updateIPPool 更新 IP 地址池
func updateIPPool(store *repository.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		poolID := id(c)
		projectID := pid(c)

		var pool model.IPPool
		if err := store.DB.Where("id = ? AND project_id = ? AND deleted_at IS NULL", poolID, projectID).First(&pool).Error; err != nil {
			Fail(c, 404, "not_found", "IP地址池不存在")
			return
		}

		var input struct {
			Name    string `json:"name"`
			CIDR    string `json:"cidr"`
			Gateway string `json:"gateway"`
		}
		if c.ShouldBindJSON(&input) != nil {
			Fail(c, 400, "invalid_request", "请求参数错误")
			return
		}

		changes := map[string]interface{}{
			"name":      input.Name,
			"cidr":      input.CIDR,
			"gateway":   input.Gateway,
			"update_by": uid(c),
		}
		if err := store.DB.Model(&pool).Updates(changes).Error; err != nil {
			Fail(c, 500, "update_failed", "更新IP地址池失败")
			return
		}
		store.Audit(uid(c), projectID, "ip_pool.update", fmt.Sprint(pool.ID))
		store.DB.Where("id = ?", pool.ID).First(&pool)
		OK(c, pool)
	}
}

// deleteIPPool 删除 IP 地址池（软删除）
func deleteIPPool(store *repository.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		poolID := id(c)
		projectID := pid(c)

		result := store.DB.Where("id = ? AND project_id = ? AND deleted_at IS NULL", poolID, projectID).Delete(&model.IPPool{})
		if result.RowsAffected == 0 {
			Fail(c, 404, "not_found", "IP地址池不存在")
			return
		}
		store.Audit(uid(c), projectID, "ip_pool.delete", fmt.Sprint(poolID))
		c.Status(204)
	}
}

// autoAssignIP 从指定子网中自动分配一个空闲 IP 地址
func autoAssignIP(store *repository.Store, svc *service.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		networkIDStr := c.Param("networkId")
		networkID, _ := strconv.ParseUint(networkIDStr, 10, 64)
		projectID := pid(c)
		userID := uid(c)

		var input struct {
			AssetID uint `json:"asset_id"`
		}
		_ = c.ShouldBindJSON(&input)

		// 查询目标网络
		var network model.Network
		if err := store.DB.Where("project_id = ? AND id = ?", projectID, uint(networkID)).First(&network).Error; err != nil {
			Fail(c, 404, "network_not_found", "网络不存在")
			return
		}

		// 解析网络 CIDR
		ip, ipNet, err := net.ParseCIDR(network.CIDR)
		if err != nil {
			Fail(c, 500, "invalid_cidr", "网络CIDR格式错误")
			return
		}

		// 查询该网络下已分配的 IP 地址
		var allocated []model.IPAllocation
		store.DB.Where("network_id = ? AND project_id = ? AND deleted_at IS NULL", uint(networkID), projectID).Find(&allocated)
		usedIPs := make(map[string]bool, len(allocated))
		for _, a := range allocated {
			usedIPs[a.Address] = true
		}

		// 排除网关地址
		if network.Gateway != "" {
			usedIPs[network.Gateway] = true
		}

		// 遍历子网中的可用地址，找到第一个未使用的 IP
		var availableIP string
		for addr := ip.Mask(ipNet.Mask); ipNet.Contains(addr); incIP(addr) {
			addrStr := addr.String()
			// 跳过网络地址和广播地址
			if addr.Equal(ip) || isBroadcastAddr(addr, ipNet) {
				continue
			}
			if !usedIPs[addrStr] {
				availableIP = addrStr
				break
			}
		}

		if availableIP == "" {
			Fail(c, 409, "no_available_ip", "子网中无可用IP地址")
			return
		}

		// 如果指定了设备，校验设备归属
		if input.AssetID != 0 {
			var asset model.Asset
			if err := store.DB.Where("project_id = ? AND id = ?", projectID, input.AssetID).First(&asset).Error; err != nil {
				Fail(c, 404, "asset_not_found", "设备不存在")
				return
			}
		}

		// 创建 IP 分配记录
		allocation := model.IPAllocation{
			ProjectID: projectID,
			NetworkID: uint(networkID),
			AssetID:   input.AssetID,
			Address:   availableIP,
			CreateBy:  userID,
			UpdateBy:  userID,
		}
		if err := store.DB.Create(&allocation).Error; err != nil {
			Fail(c, 500, "create_failed", "自动分配IP失败")
			return
		}

		store.Audit(userID, projectID, "ip.auto_assign", availableIP)
		Created(c, allocation)
	}
}

// incIP 将 IP 地址递增到下一个地址
func incIP(ip net.IP) {
	for j := len(ip) - 1; j >= 0; j-- {
		ip[j]++
		if ip[j] > 0 {
			break
		}
	}
}

// isBroadcastAddr 判断是否为广播地址
func isBroadcastAddr(ip net.IP, network *net.IPNet) bool {
	for i := range ip {
		if ip[i] != ^network.Mask[i] {
			return false
		}
	}
	return true
}

// releaseIP 释放 IP 地址分配（软删除）
func releaseIP(store *repository.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		allocationID := id(c)
		projectID := pid(c)

		var allocation model.IPAllocation
		if err := store.DB.Where("id = ? AND project_id = ? AND deleted_at IS NULL", allocationID, projectID).First(&allocation).Error; err != nil {
			Fail(c, 404, "not_found", "IP分配记录不存在")
			return
		}

		if err := store.DB.Delete(&allocation).Error; err != nil {
			Fail(c, 500, "release_failed", "释放IP失败")
			return
		}
		store.Audit(uid(c), projectID, "ip.release", allocation.Address)
		c.Status(204)
	}
}

// getPXEConfig 获取 PXE 全局配置
func getPXEConfig(store *repository.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		projectID := pid(c)

		// 从 system_settings 表读取 category="pxe" 的配置
		key := fmt.Sprintf("pxe_config_%d", projectID)
		var setting model.SystemSetting
		if err := store.DB.Where("\"key\" = ?", key).First(&setting).Error; err != nil {
			// 不存在则返回默认配置
			OK(c, gin.H{
				"config":      "",
				"updated_at":  nil,
				"description": "PXE全局配置",
			})
			return
		}
		OK(c, gin.H{
			"config":      setting.Value,
			"updated_at":  setting.UpdatedAt,
			"description": setting.Description,
		})
	}
}

// updatePXEConfig 更新 PXE 全局配置
func updatePXEConfig(store *repository.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		projectID := pid(c)
		userID := uid(c)

		var input struct {
			Config string `json:"config"`
		}
		if c.ShouldBindJSON(&input) != nil {
			Fail(c, 400, "invalid_request", "请求参数错误")
			return
		}

		key := fmt.Sprintf("pxe_config_%d", projectID)
		setting := model.SystemSetting{
			Key:         key,
			Value:       input.Config,
			Description: "PXE全局配置",
			Category:    "pxe",
			UpdatedBy:   &userID,
			UpdatedAt:   time.Now(),
		}
		if err := store.UpsertSystemSetting(&setting); err != nil {
			Fail(c, 500, "update_failed", "更新PXE配置失败")
			return
		}
		store.Audit(userID, projectID, "pxe_config.update", key)
		OK(c, setting)
	}
}


