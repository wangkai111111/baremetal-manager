package handler

import (
	"baremetal-manager/backend/internal/model"
	"baremetal-manager/backend/internal/repository"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
)

// RegisterDatacenterRoutes 注册机房管理路由
// 路由前缀: /api/v1/projects/:projectId/datacenters
func RegisterDatacenterRoutes(rg *gin.RouterGroup, store *repository.Store) {
	dc := rg.Group("/datacenters")
	{
		dc.GET("", listDatacenters(store))         // 机房列表（支持分页）
		dc.POST("", createDatacenter(store))       // 创建机房
		dc.GET("/:id", getDatacenter(store))       // 机房详情
		dc.PUT("/:id", updateDatacenter(store))    // 更新机房
		dc.DELETE("/:id", deleteDatacenter(store)) // 删除机房（软删除）
	}
}

// listDatacenters 分页查询项目下的机房列表
func listDatacenters(store *repository.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 从路由参数获取项目 ID
		projectID, err := strconv.ParseUint(c.Param("projectId"), 10, 64)
		if err != nil || projectID == 0 {
			Fail(c, http.StatusBadRequest, "INVALID_PARAM", "无效的项目ID")
			return
		}

		// 解析分页参数
		page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
		pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))

		result, err := store.ListDatacenters(uint(projectID), page, pageSize)
		if err != nil {
			Fail(c, http.StatusInternalServerError, "DB_ERROR", "查询机房列表失败")
			return
		}

		PageOK(c, result)
	}
}

// createDatacenter 创建机房记录
func createDatacenter(store *repository.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 从路由参数获取项目 ID
		projectID, err := strconv.ParseUint(c.Param("projectId"), 10, 64)
		if err != nil || projectID == 0 {
			Fail(c, http.StatusBadRequest, "INVALID_PARAM", "无效的项目ID")
			return
		}

		var dc model.Datacenter
		if err := c.ShouldBindJSON(&dc); err != nil {
			Fail(c, http.StatusBadRequest, "INVALID_REQUEST", "请求参数错误")
			return
		}

		// 校验必填字段
		if dc.Name == "" {
			Fail(c, http.StatusBadRequest, "INVALID_REQUEST", "机房名称不能为空")
			return
		}

		// 设置项目 ID 和创建人
		dc.ProjectID = uint(projectID)
		userID := c.GetUint("userID")
		dc.CreateBy = &userID

		if err := store.CreateDatacenter(&dc); err != nil {
			Fail(c, http.StatusInternalServerError, "DB_ERROR", "创建机房失败")
			return
		}

		// 记录审计日志
		store.Audit(userID, dc.ProjectID, "datacenter.create", dc.Name)

		Created(c, dc)
	}
}

// getDatacenter 根据 ID 查询机房详情
func getDatacenter(store *repository.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 从路由参数获取项目 ID
		projectID, err := strconv.ParseUint(c.Param("projectId"), 10, 64)
		if err != nil || projectID == 0 {
			Fail(c, http.StatusBadRequest, "INVALID_PARAM", "无效的项目ID")
			return
		}

		// 解析机房 ID
		dcID, err := strconv.ParseUint(c.Param("id"), 10, 64)
		if err != nil || dcID == 0 {
			Fail(c, http.StatusBadRequest, "INVALID_PARAM", "无效的机房ID")
			return
		}

		dc, err := store.GetDatacenter(uint(dcID))
		if err != nil {
			Fail(c, http.StatusNotFound, "NOT_FOUND", "机房不存在")
			return
		}

		// 检查项目隔离
		if dc.ProjectID != uint(projectID) {
			Fail(c, http.StatusNotFound, "NOT_FOUND", "机房不存在")
			return
		}

		OK(c, dc)
	}
}

// updateDatacenter 更新机房信息
func updateDatacenter(store *repository.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 从路由参数获取项目 ID
		projectID, err := strconv.ParseUint(c.Param("projectId"), 10, 64)
		if err != nil || projectID == 0 {
			Fail(c, http.StatusBadRequest, "INVALID_PARAM", "无效的项目ID")
			return
		}

		// 解析机房 ID
		dcID, err := strconv.ParseUint(c.Param("id"), 10, 64)
		if err != nil || dcID == 0 {
			Fail(c, http.StatusBadRequest, "INVALID_PARAM", "无效的机房ID")
			return
		}

		// 查询现有记录
		dc, err := store.GetDatacenter(uint(dcID))
		if err != nil {
			Fail(c, http.StatusNotFound, "NOT_FOUND", "机房不存在")
			return
		}

		// 检查项目隔离
		if dc.ProjectID != uint(projectID) {
			Fail(c, http.StatusNotFound, "NOT_FOUND", "机房不存在")
			return
		}

		// 绑定更新字段
		var input struct {
			Name        string `json:"name"`
			Code        string `json:"code"`
			Location    string `json:"location"`
			Description string `json:"description"`
			Contact     string `json:"contact"`
		}
		if err := c.ShouldBindJSON(&input); err != nil {
			Fail(c, http.StatusBadRequest, "INVALID_REQUEST", "请求参数错误")
			return
		}

		// 更新字段
		updates := map[string]interface{}{
			"name":        input.Name,
			"code":        input.Code,
			"location":    input.Location,
			"description": input.Description,
			"contact":     input.Contact,
		}
		userID := c.GetUint("userID")
		updates["update_by"] = userID

		store.DB.Model(dc).Updates(updates)

		// 记录审计日志
		store.Audit(userID, dc.ProjectID, "datacenter.update", dc.Name)

		// 重新查询返回最新数据
		updated, _ := store.GetDatacenter(uint(dcID))
		OK(c, updated)
	}
}

// deleteDatacenter 软删除机房记录
func deleteDatacenter(store *repository.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 从路由参数获取项目 ID
		projectID, err := strconv.ParseUint(c.Param("projectId"), 10, 64)
		if err != nil || projectID == 0 {
			Fail(c, http.StatusBadRequest, "INVALID_PARAM", "无效的项目ID")
			return
		}

		// 解析机房 ID
		dcID, err := strconv.ParseUint(c.Param("id"), 10, 64)
		if err != nil || dcID == 0 {
			Fail(c, http.StatusBadRequest, "INVALID_PARAM", "无效的机房ID")
			return
		}

		// 查询现有记录
		dc, err := store.GetDatacenter(uint(dcID))
		if err != nil {
			Fail(c, http.StatusNotFound, "NOT_FOUND", "机房不存在")
			return
		}

		// 检查项目隔离
		if dc.ProjectID != uint(projectID) {
			Fail(c, http.StatusNotFound, "NOT_FOUND", "机房不存在")
			return
		}

		// 执行软删除
		if err := store.DeleteDatacenter(uint(dcID)); err != nil {
			Fail(c, http.StatusInternalServerError, "DB_ERROR", "删除机房失败")
			return
		}

		// 记录审计日志
		userID := c.GetUint("userID")
		store.Audit(userID, dc.ProjectID, "datacenter.delete", dc.Name)

		c.Status(http.StatusNoContent)
	}
}
