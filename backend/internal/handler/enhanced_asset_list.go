package handler

import (
	"net/http"
	"strconv"

	"baremetal-manager/backend/internal/model"
	"baremetal-manager/backend/internal/repository"
	"github.com/gin-gonic/gin"
)

// RegisterEnhancedAssetListRoutes 注册增强资产列表路由
// 注意：不覆盖现有的 GET /assets 路由，而是在同一路由组下添加筛选能力
func RegisterEnhancedAssetListRoutes(rg *gin.RouterGroup, store *repository.Store) {
	// 增强列表（支持标签/状态/机房筛选）
	// 筛选参数通过 query string 传递: ?tag_id=1&status=online&datacenter_id=2&search=keyword&page=1&page_size=20
	rg.GET("/assets/enhanced", func(c *gin.Context) {
		EnhancedListAssets(c, store)
	})
}

// EnhancedListAssets 增强版资产列表查询
// 支持按标签、状态、机房筛选，支持关键字搜索和分页
func EnhancedListAssets(c *gin.Context, store *repository.Store) {
	projectID, _ := c.Get("projectID")
	if projectID == nil {
		// 尝试从 API Key 认证获取项目 ID
		projectID, _ = c.Get("apiKeyProject")
	}
	if projectID == nil {
		Fail(c, http.StatusUnauthorized, "UNAUTHORIZED", "未授权访问")
		return
	}
	pid := projectID.(uint)

	// 解析查询参数
	tagIDStr := c.Query("tag_id")
	status := c.Query("status")
	datacenterIDStr := c.Query("datacenter_id")
	search := c.Query("search")
	pageStr := c.DefaultQuery("page", "1")
	pageSizeStr := c.DefaultQuery("page_size", "20")

	page, _ := strconv.Atoi(pageStr)
	pageSize, _ := strconv.Atoi(pageSizeStr)
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}

	// 构建基础查询条件（按项目过滤）
	query := store.DB.Where("project_id = ?", pid)

	// 按状态筛选
	if status != "" {
		query = query.Where("status = ?", status)
	}

	// 按机房筛选
	if datacenterIDStr != "" {
		dcID, err := strconv.ParseUint(datacenterIDStr, 10, 64)
		if err == nil {
			query = query.Where("datacenter_id = ?", uint(dcID))
		}
	}

	// 按标签筛选（通过子查询关联 asset_tags 表）
	if tagIDStr != "" {
		tagID, err := strconv.ParseUint(tagIDStr, 10, 64)
		if err == nil {
			query = query.Where("id IN (SELECT asset_id FROM asset_tags WHERE tag_id = ?)", uint(tagID))
		}
	}

	// 关键字搜索（匹配资产名称、BMC地址、业务IP）
	if search != "" {
		like := "%" + search + "%"
		query = query.Where("name LIKE ? OR bmc_address LIKE ? OR ip_address LIKE ?", like, like, like)
	}

	// 执行分页查询
	var assets []model.Asset
	result, err := store.Paginate(query.Order("id desc"), page, pageSize, &assets)
	if err != nil {
		Fail(c, http.StatusInternalServerError, "QUERY_ERROR", "查询失败: "+err.Error())
		return
	}

	PageOK(c, result)
}
