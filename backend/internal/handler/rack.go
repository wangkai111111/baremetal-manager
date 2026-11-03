package handler

import (
	"baremetal-manager/backend/internal/model"
	"baremetal-manager/backend/internal/repository"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
)

// RegisterRackRoutes 注册机柜管理路由
// 路由前缀: /api/v1/projects/:projectId/racks
func RegisterRackRoutes(rg *gin.RouterGroup, store *repository.Store) {
	racks := rg.Group("/racks")
	{
		racks.GET("", listRacks(store))        // 机柜列表（支持按机房筛选和分页）
		racks.POST("", createRack(store))       // 创建机柜
		racks.GET("/:id", getRack(store))       // 机柜详情
		racks.PUT("/:id", updateRack(store))    // 更新机柜
		racks.DELETE("/:id", deleteRack(store))  // 删除机柜（软删除）
	}
}

// listRacks 分页查询项目下的机柜列表，支持按 datacenter_id 筛选
func listRacks(store *repository.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 从路由参数获取项目 ID
		projectID, err := strconv.ParseUint(c.Param("projectId"), 10, 64)
		if err != nil || projectID == 0 {
			Fail(c, http.StatusBadRequest, "INVALID_PARAM", "无效的项目ID")
			return
		}

		// 可选的机房筛选参数
		datacenterID, _ := strconv.ParseUint(c.Query("datacenter_id"), 10, 64)

		// 解析分页参数
		page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
		pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))

		result, err := store.ListRacks(uint(projectID), uint(datacenterID), page, pageSize)
		if err != nil {
			Fail(c, http.StatusInternalServerError, "DB_ERROR", "查询机柜列表失败")
			return
		}

		PageOK(c, result)
	}
}

// createRack 创建机柜记录
func createRack(store *repository.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 从路由参数获取项目 ID
		projectID, err := strconv.ParseUint(c.Param("projectId"), 10, 64)
		if err != nil || projectID == 0 {
			Fail(c, http.StatusBadRequest, "INVALID_PARAM", "无效的项目ID")
			return
		}

		var rack model.Rack
		if err := c.ShouldBindJSON(&rack); err != nil {
			Fail(c, http.StatusBadRequest, "INVALID_REQUEST", "请求参数错误")
			return
		}

		// 校验必填字段
		if rack.Name == "" {
			Fail(c, http.StatusBadRequest, "INVALID_REQUEST", "机柜名称不能为空")
			return
		}
		if rack.DatacenterID == 0 {
			Fail(c, http.StatusBadRequest, "INVALID_REQUEST", "必须指定所属机房")
			return
		}

		// 设置项目 ID 和创建人
		rack.ProjectID = uint(projectID)
		userID := c.GetUint("userID")
		rack.CreateBy = &userID

		// 默认 U 位高度
		if rack.HeightU == 0 {
			rack.HeightU = 42
		}

		if err := store.CreateRack(&rack); err != nil {
			Fail(c, http.StatusInternalServerError, "DB_ERROR", "创建机柜失败")
			return
		}

		// 记录审计日志
		store.Audit(userID, rack.ProjectID, "rack.create", rack.Name)

		Created(c, rack)
	}
}

// getRack 根据 ID 查询机柜详情
func getRack(store *repository.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 从路由参数获取项目 ID
		projectID, err := strconv.ParseUint(c.Param("projectId"), 10, 64)
		if err != nil || projectID == 0 {
			Fail(c, http.StatusBadRequest, "INVALID_PARAM", "无效的项目ID")
			return
		}

		// 解析机柜 ID
		rackID, err := strconv.ParseUint(c.Param("id"), 10, 64)
		if err != nil || rackID == 0 {
			Fail(c, http.StatusBadRequest, "INVALID_PARAM", "无效的机柜ID")
			return
		}

		rack, err := store.GetRack(uint(rackID))
		if err != nil {
			Fail(c, http.StatusNotFound, "NOT_FOUND", "机柜不存在")
			return
		}

		// 检查项目隔离
		if rack.ProjectID != uint(projectID) {
			Fail(c, http.StatusNotFound, "NOT_FOUND", "机柜不存在")
			return
		}

		OK(c, rack)
	}
}

// updateRack 更新机柜信息
func updateRack(store *repository.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 从路由参数获取项目 ID
		projectID, err := strconv.ParseUint(c.Param("projectId"), 10, 64)
		if err != nil || projectID == 0 {
			Fail(c, http.StatusBadRequest, "INVALID_PARAM", "无效的项目ID")
			return
		}

		// 解析机柜 ID
		rackID, err := strconv.ParseUint(c.Param("id"), 10, 64)
		if err != nil || rackID == 0 {
			Fail(c, http.StatusBadRequest, "INVALID_PARAM", "无效的机柜ID")
			return
		}

		// 查询现有记录
		rack, err := store.GetRack(uint(rackID))
		if err != nil {
			Fail(c, http.StatusNotFound, "NOT_FOUND", "机柜不存在")
			return
		}

		// 检查项目隔离
		if rack.ProjectID != uint(projectID) {
			Fail(c, http.StatusNotFound, "NOT_FOUND", "机柜不存在")
			return
		}

		// 绑定更新字段
		var input struct {
			Name          string `json:"name"`
			DatacenterID  uint   `json:"datacenter_id"`
			HeightU       int    `json:"height_u"`
			Location      string `json:"location"`
			PowerCapacity string `json:"power_capacity"`
		}
		if err := c.ShouldBindJSON(&input); err != nil {
			Fail(c, http.StatusBadRequest, "INVALID_REQUEST", "请求参数错误")
			return
		}

		updates := map[string]interface{}{
			"name":           input.Name,
			"datacenter_id":  input.DatacenterID,
			"height_u":       input.HeightU,
			"location":       input.Location,
			"power_capacity": input.PowerCapacity,
		}
		userID := c.GetUint("userID")
		updates["update_by"] = userID

		store.DB.Model(rack).Updates(updates)

		// 记录审计日志
		store.Audit(userID, rack.ProjectID, "rack.update", rack.Name)

		// 重新查询返回最新数据
		updated, _ := store.GetRack(uint(rackID))
		OK(c, updated)
	}
}

// deleteRack 软删除机柜记录
func deleteRack(store *repository.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 从路由参数获取项目 ID
		projectID, err := strconv.ParseUint(c.Param("projectId"), 10, 64)
		if err != nil || projectID == 0 {
			Fail(c, http.StatusBadRequest, "INVALID_PARAM", "无效的项目ID")
			return
		}

		// 解析机柜 ID
		rackID, err := strconv.ParseUint(c.Param("id"), 10, 64)
		if err != nil || rackID == 0 {
			Fail(c, http.StatusBadRequest, "INVALID_PARAM", "无效的机柜ID")
			return
		}

		// 查询现有记录
		rack, err := store.GetRack(uint(rackID))
		if err != nil {
			Fail(c, http.StatusNotFound, "NOT_FOUND", "机柜不存在")
			return
		}

		// 检查项目隔离
		if rack.ProjectID != uint(projectID) {
			Fail(c, http.StatusNotFound, "NOT_FOUND", "机柜不存在")
			return
		}

		// 执行软删除
		if err := store.DeleteRack(uint(rackID)); err != nil {
			Fail(c, http.StatusInternalServerError, "DB_ERROR", "删除机柜失败")
			return
		}

		// 记录审计日志
		userID := c.GetUint("userID")
		store.Audit(userID, rack.ProjectID, "rack.delete", rack.Name)

		c.Status(http.StatusNoContent)
	}
}
