package handler

import (
	"fmt"
	"net"
	"strconv"

	"baremetal-manager/backend/internal/model"
	"baremetal-manager/backend/internal/repository"
	"github.com/gin-gonic/gin"
)

// RegisterNetworkExtRoutes 注册网络管理增强路由
func RegisterNetworkExtRoutes(rg *gin.RouterGroup, store *repository.Store) {
	// IP 地址池管理
	rg.GET("/ip-pools", listIPPools(store))
	rg.POST("/ip-pools", createIPPool(store))
	rg.PUT("/ip-pools/:id", updateIPPool(store))
	rg.DELETE("/ip-pools/:id", deleteIPPool(store))
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
