package handler

import (
	"baremetal-manager/backend/internal/model"
	"baremetal-manager/backend/internal/repository"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
)

// RegisterTagRoutes 注册标签管理路由
// 标签路由前缀: /api/v1/projects/:projectId/tags
// 资产标签绑定路由前缀: /api/v1/projects/:projectId/assets/:assetId/tags
func RegisterTagRoutes(rg *gin.RouterGroup, store *repository.Store) {
	tags := rg.Group("/tags")
	{
		tags.GET("", listTags(store))         // 项目标签列表
		tags.POST("", createTag(store))        // 创建标签
		tags.PUT("/:id", updateTag(store))     // 更新标签
		tags.DELETE("/:id", deleteTag(store))   // 删除标签
	}

	// 资产-标签绑定路由
	assets := rg.Group("/assets")
	{
		assets.POST("/:assetId/tags", bindAssetTag(store))             // 绑定标签到资产
		assets.DELETE("/:assetId/tags/:tagId", unbindAssetTag(store))   // 解绑资产标签
		assets.GET("/:assetId/tags", listAssetTags(store))             // 查询资产标签
	}
}

// listTags 查询项目下的所有标签列表
func listTags(store *repository.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 从路由参数获取项目 ID
		projectID, err := strconv.ParseUint(c.Param("projectId"), 10, 64)
		if err != nil || projectID == 0 {
			Fail(c, http.StatusBadRequest, "INVALID_PARAM", "无效的项目ID")
			return
		}

		tags, err := store.ListTags(uint(projectID))
		if err != nil {
			Fail(c, http.StatusInternalServerError, "DB_ERROR", "查询标签列表失败")
			return
		}

		OK(c, tags)
	}
}

// createTag 创建新标签
func createTag(store *repository.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 从路由参数获取项目 ID
		projectID, err := strconv.ParseUint(c.Param("projectId"), 10, 64)
		if err != nil || projectID == 0 {
			Fail(c, http.StatusBadRequest, "INVALID_PARAM", "无效的项目ID")
			return
		}

		var tag model.Tag
		if err := c.ShouldBindJSON(&tag); err != nil {
			Fail(c, http.StatusBadRequest, "INVALID_REQUEST", "请求参数错误")
			return
		}

		// 校验必填字段
		if tag.Name == "" {
			Fail(c, http.StatusBadRequest, "INVALID_REQUEST", "标签名称不能为空")
			return
		}

		// 设置项目 ID
		tag.ProjectID = uint(projectID)

		if err := store.CreateTag(&tag); err != nil {
			// 检查是否唯一索引冲突
			Fail(c, http.StatusConflict, "DUPLICATE", "同项目下标签名称已存在")
			return
		}

		Created(c, tag)
	}
}

// updateTag 更新标签信息
func updateTag(store *repository.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 从路由参数获取项目 ID
		projectID, err := strconv.ParseUint(c.Param("projectId"), 10, 64)
		if err != nil || projectID == 0 {
			Fail(c, http.StatusBadRequest, "INVALID_PARAM", "无效的项目ID")
			return
		}

		// 解析标签 ID
		tagID, err := strconv.ParseUint(c.Param("id"), 10, 64)
		if err != nil || tagID == 0 {
			Fail(c, http.StatusBadRequest, "INVALID_PARAM", "无效的标签ID")
			return
		}

		// 查询现有标签，确保属于当前项目
		var tag model.Tag
		if err := store.DB.Where("id = ? AND project_id = ?", tagID, projectID).First(&tag).Error; err != nil {
			Fail(c, http.StatusNotFound, "NOT_FOUND", "标签不存在")
			return
		}

		// 绑定更新字段
		var input struct {
			Name  string `json:"name"`
			Color string `json:"color"`
		}
		if err := c.ShouldBindJSON(&input); err != nil {
			Fail(c, http.StatusBadRequest, "INVALID_REQUEST", "请求参数错误")
			return
		}

		if input.Name != "" {
			tag.Name = input.Name
		}
		tag.Color = input.Color

		if err := store.UpdateTag(&tag); err != nil {
			Fail(c, http.StatusInternalServerError, "DB_ERROR", "更新标签失败")
			return
		}

		OK(c, tag)
	}
}

// deleteTag 删除标签
func deleteTag(store *repository.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 从路由参数获取项目 ID
		projectID, err := strconv.ParseUint(c.Param("projectId"), 10, 64)
		if err != nil || projectID == 0 {
			Fail(c, http.StatusBadRequest, "INVALID_PARAM", "无效的项目ID")
			return
		}

		// 解析标签 ID
		tagID, err := strconv.ParseUint(c.Param("id"), 10, 64)
		if err != nil || tagID == 0 {
			Fail(c, http.StatusBadRequest, "INVALID_PARAM", "无效的标签ID")
			return
		}

		// 查询现有标签，确保属于当前项目
		var tag model.Tag
		if err := store.DB.Where("id = ? AND project_id = ?", tagID, projectID).First(&tag).Error; err != nil {
			Fail(c, http.StatusNotFound, "NOT_FOUND", "标签不存在")
			return
		}

		// 先删除该标签的所有资产关联
		store.DB.Where("tag_id = ?", tagID).Delete(&model.AssetTag{})

		// 删除标签
		if err := store.DeleteTag(uint(tagID)); err != nil {
			Fail(c, http.StatusInternalServerError, "DB_ERROR", "删除标签失败")
			return
		}

		c.Status(http.StatusNoContent)
	}
}

// bindAssetTag 为资产绑定标签
func bindAssetTag(store *repository.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 从路由参数获取项目 ID
		projectID, err := strconv.ParseUint(c.Param("projectId"), 10, 64)
		if err != nil || projectID == 0 {
			Fail(c, http.StatusBadRequest, "INVALID_PARAM", "无效的项目ID")
			return
		}

		// 解析资产 ID
		assetID, err := strconv.ParseUint(c.Param("assetId"), 10, 64)
		if err != nil || assetID == 0 {
			Fail(c, http.StatusBadRequest, "INVALID_PARAM", "无效的资产ID")
			return
		}

		// 校验资产属于当前项目
		var asset model.Asset
		if err := store.DB.Where("id = ? AND project_id = ?", assetID, projectID).First(&asset).Error; err != nil {
			Fail(c, http.StatusNotFound, "NOT_FOUND", "资产不存在")
			return
		}

		// 绑定请求体
		var input struct {
			TagID uint `json:"tag_id"`
		}
		if err := c.ShouldBindJSON(&input); err != nil || input.TagID == 0 {
			Fail(c, http.StatusBadRequest, "INVALID_REQUEST", "请提供有效的标签ID")
			return
		}

		// 校验标签属于当前项目
		var tag model.Tag
		if err := store.DB.Where("id = ? AND project_id = ?", input.TagID, projectID).First(&tag).Error; err != nil {
			Fail(c, http.StatusNotFound, "NOT_FOUND", "标签不存在")
			return
		}

		// 创建关联（忽略重复绑定错误）
		if err := store.AddAssetTag(uint(assetID), input.TagID); err != nil {
			Fail(c, http.StatusConflict, "DUPLICATE", "该资产已绑定此标签")
			return
		}

		Created(c, gin.H{"asset_id": assetID, "tag_id": input.TagID})
	}
}

// unbindAssetTag 解绑资产的标签
func unbindAssetTag(store *repository.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 从路由参数获取项目 ID
		projectID, err := strconv.ParseUint(c.Param("projectId"), 10, 64)
		if err != nil || projectID == 0 {
			Fail(c, http.StatusBadRequest, "INVALID_PARAM", "无效的项目ID")
			return
		}

		// 解析资产 ID
		assetID, err := strconv.ParseUint(c.Param("assetId"), 10, 64)
		if err != nil || assetID == 0 {
			Fail(c, http.StatusBadRequest, "INVALID_PARAM", "无效的资产ID")
			return
		}

		// 解析标签 ID
		tagID, err := strconv.ParseUint(c.Param("tagId"), 10, 64)
		if err != nil || tagID == 0 {
			Fail(c, http.StatusBadRequest, "INVALID_PARAM", "无效的标签ID")
			return
		}

		// 校验资产属于当前项目
		var asset model.Asset
		if err := store.DB.Where("id = ? AND project_id = ?", assetID, projectID).First(&asset).Error; err != nil {
			Fail(c, http.StatusNotFound, "NOT_FOUND", "资产不存在")
			return
		}

		// 执行解绑
		if err := store.RemoveAssetTag(uint(assetID), uint(tagID)); err != nil {
			Fail(c, http.StatusInternalServerError, "DB_ERROR", "解绑标签失败")
			return
		}

		c.Status(http.StatusNoContent)
	}
}

// listAssetTags 查询资产关联的所有标签
func listAssetTags(store *repository.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 从路由参数获取项目 ID
		projectID, err := strconv.ParseUint(c.Param("projectId"), 10, 64)
		if err != nil || projectID == 0 {
			Fail(c, http.StatusBadRequest, "INVALID_PARAM", "无效的项目ID")
			return
		}

		// 解析资产 ID
		assetID, err := strconv.ParseUint(c.Param("assetId"), 10, 64)
		if err != nil || assetID == 0 {
			Fail(c, http.StatusBadRequest, "INVALID_PARAM", "无效的资产ID")
			return
		}

		// 校验资产属于当前项目
		var asset model.Asset
		if err := store.DB.Where("id = ? AND project_id = ?", assetID, projectID).First(&asset).Error; err != nil {
			Fail(c, http.StatusNotFound, "NOT_FOUND", "资产不存在")
			return
		}

		// 查询关联记录
		assetTags, err := store.ListAssetTagsByAsset(uint(assetID))
		if err != nil {
			Fail(c, http.StatusInternalServerError, "DB_ERROR", "查询资产标签失败")
			return
		}

		// 如果有关联，查询标签详情
		if len(assetTags) == 0 {
			OK(c, []model.Tag{})
			return
		}

		tagIDs := make([]uint, 0, len(assetTags))
		for _, at := range assetTags {
			tagIDs = append(tagIDs, at.TagID)
		}

		var tags []model.Tag
		store.DB.Where("id IN ? AND project_id = ?", tagIDs, projectID).Find(&tags)

		OK(c, tags)
	}
}
