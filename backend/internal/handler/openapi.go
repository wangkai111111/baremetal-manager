package handler

import (
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"baremetal-manager/backend/internal/model"
	"baremetal-manager/backend/internal/repository"
	"baremetal-manager/backend/internal/service"
	"github.com/gin-gonic/gin"
)

// RegisterOpenAPIRoutes 注册开放 API 路由
// 这些路由使用 API Key 认证（通过 middleware.APIKeyAuth）
// 路由前缀: /api/v1/open
func RegisterOpenAPIRoutes(r *gin.Engine, store *repository.Store, authMiddleware gin.HandlerFunc) {
	h := &Handler{S: &service.Service{Store: store}}

	open := r.Group("/api/v1/open")
	open.Use(authMiddleware) // 使用 API Key 中间件
	{
		// 资产查询
		open.GET("/assets", func(c *gin.Context) { h.openListAssets(c, store) })
		open.GET("/assets/:id", func(c *gin.Context) { h.openGetAsset(c, store) })

		// 电源操作
		open.POST("/assets/:id/power", func(c *gin.Context) { h.openPowerAction(c, store) })

		// 告警查询
		open.GET("/alerts", func(c *gin.Context) { h.openListAlerts(c, store) })

		// 触发装机
		open.POST("/assets/:id/reinstall", func(c *gin.Context) { h.openTriggerReinstall(c, store) })
	}
}

// getOpenAPIProjectID 从 API Key 认证上下文中获取项目 ID
func getOpenAPIProjectID(c *gin.Context) (uint, bool) {
	projID, exists := c.Get("apiKeyProject")
	if !exists || projID == nil {
		Fail(c, 403, "FORBIDDEN", "API Key 未关联项目")
		return 0, false
	}
	return projID.(uint), true
}

// hasOpenAPIPermission 检查 API Key 是否拥有指定权限
func hasOpenAPIPermission(c *gin.Context, permission string) bool {
	permsStr, _ := c.Get("apiKeyPermissions")
	perms, ok := permsStr.(string)
	if !ok || perms == "" {
		return false
	}
	// 支持 * 通配符
	if perms == "*" {
		return true
	}
	for _, p := range splitPermissions(perms) {
		if p == "*" || p == permission {
			return true
		}
	}
	return false
}

// splitPermissions 将逗号分隔的权限字符串拆分为切片
func splitPermissions(perms string) []string {
	var result []string
	for _, p := range splitString(perms, ",") {
		p = trimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}

// splitString 简单的字符串分割
func splitString(s, sep string) []string {
	var parts []string
	for {
		i := indexOf(s, sep)
		if i < 0 {
			parts = append(parts, s)
			break
		}
		parts = append(parts, s[:i])
		s = s[i+len(sep):]
	}
	return parts
}

// indexOf 查找子串位置
func indexOf(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}

// trimSpace 去除首尾空白
func trimSpace(s string) string {
	for len(s) > 0 && (s[0] == ' ' || s[0] == '\t' || s[0] == '\n' || s[0] == '\r') {
		s = s[1:]
	}
	for len(s) > 0 && (s[len(s)-1] == ' ' || s[len(s)-1] == '\t' || s[len(s)-1] == '\n' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s
}

// openListAssets 开放 API — 分页列出资产
func (h *Handler) openListAssets(c *gin.Context, store *repository.Store) {
	projectID, ok := getOpenAPIProjectID(c)
	if !ok {
		return
	}
	if !hasOpenAPIPermission(c, "assets:read") {
		Fail(c, 403, "FORBIDDEN", "无权限访问资产")
		return
	}

	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))

	var assets []model.Asset
	query := store.DB.Where("project_id = ?", projectID).Order("id desc")
	result, err := store.Paginate(query, page, pageSize, &assets)
	if err != nil {
		Fail(c, 500, "QUERY_ERROR", "查询失败")
		return
	}

	PageOK(c, result)
}

// openGetAsset 开放 API — 获取单个资产详情
func (h *Handler) openGetAsset(c *gin.Context, store *repository.Store) {
	projectID, ok := getOpenAPIProjectID(c)
	if !ok {
		return
	}
	if !hasOpenAPIPermission(c, "assets:read") {
		Fail(c, 403, "FORBIDDEN", "无权限访问资产")
		return
	}

	assetID, _ := strconv.ParseUint(c.Param("id"), 10, 64)
	var asset model.Asset
	if err := store.DB.Where("project_id = ? AND id = ?", projectID, uint(assetID)).First(&asset).Error; err != nil {
		Fail(c, 404, "NOT_FOUND", "资产不存在")
		return
	}

	OK(c, asset)
}

// openPowerAction 开放 API — 执行电源操作
func (h *Handler) openPowerAction(c *gin.Context, store *repository.Store) {
	projectID, ok := getOpenAPIProjectID(c)
	if !ok {
		return
	}
	if !hasOpenAPIPermission(c, "assets:operate") {
		Fail(c, 403, "FORBIDDEN", "无权限操作资产")
		return
	}

	assetID, _ := strconv.ParseUint(c.Param("id"), 10, 64)
	var asset model.Asset
	if err := store.DB.Where("project_id = ? AND id = ?", projectID, uint(assetID)).First(&asset).Error; err != nil {
		Fail(c, 404, "NOT_FOUND", "资产不存在")
		return
	}

	var req struct {
		Action string `json:"action"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Action == "" {
		Fail(c, 400, "INVALID_REQUEST", "请提供操作类型")
		return
	}
	if req.Action != "on" && req.Action != "off" && req.Action != "restart" {
		Fail(c, 400, "INVALID_ACTION", "无效的操作类型，仅支持 on/off/restart")
		return
	}

	// 创建操作记录并直接返回
	userID, _ := c.Get("userID")
	payload, _ := json.Marshal(map[string]string{"action": req.Action})
	op := &model.Operation{
		UserID:    userID.(uint),
		ProjectID: projectID,
		AssetID:   asset.ID,
		Kind:      "power",
		Payload:   string(payload),
		Status:    "queued",
	}
	if err := store.DB.Create(op).Error; err != nil {
		Fail(c, 500, "SUBMIT_FAILED", "创建操作记录失败")
		return
	}

	// 记录审计日志
	store.Audit(userID.(uint), projectID, "openapi.power", fmt.Sprintf("资产 %d 执行 %s", asset.ID, req.Action))

	c.JSON(202, Response{Code: 0, Data: op, Message: "操作已提交"})
}

// openListAlerts 开放 API — 列出告警
func (h *Handler) openListAlerts(c *gin.Context, store *repository.Store) {
	projectID, ok := getOpenAPIProjectID(c)
	if !ok {
		return
	}
	if !hasOpenAPIPermission(c, "alerts:read") {
		Fail(c, 403, "FORBIDDEN", "无权限查看告警")
		return
	}

	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))

	var alerts []model.Alert
	query := store.DB.Where("project_id = ?", projectID).Order("id desc")
	result, err := store.Paginate(query, page, pageSize, &alerts)
	if err != nil {
		Fail(c, 500, "QUERY_ERROR", "查询失败")
		return
	}

	PageOK(c, result)
}

// openTriggerReinstall 开放 API — 触发装机任务
func (h *Handler) openTriggerReinstall(c *gin.Context, store *repository.Store) {
	projectID, ok := getOpenAPIProjectID(c)
	if !ok {
		return
	}
	if !hasOpenAPIPermission(c, "assets:operate") {
		Fail(c, 403, "FORBIDDEN", "无权限操作资产")
		return
	}

	assetID, _ := strconv.ParseUint(c.Param("id"), 10, 64)
	var asset model.Asset
	if err := store.DB.Where("project_id = ? AND id = ?", projectID, uint(assetID)).First(&asset).Error; err != nil {
		Fail(c, 404, "NOT_FOUND", "资产不存在")
		return
	}

	var req struct {
		TemplateID uint `json:"template_id"`
		ImageID    uint `json:"image_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.TemplateID == 0 {
		Fail(c, 400, "INVALID_REQUEST", "请提供装机模板 ID")
		return
	}

	// 验证装机模板存在且属于当前项目
	var tpl model.KickstartTemplate
	if err := store.DB.Where("project_id = ? AND id = ? AND deleted_at IS NULL", projectID, req.TemplateID).First(&tpl).Error; err != nil {
		Fail(c, 404, "TEMPLATE_NOT_FOUND", "装机模板不存在")
		return
	}

	// 验证镜像存在（如果指定了镜像 ID）
	if req.ImageID > 0 {
		var img model.Image
		if err := store.DB.Where("project_id = ? AND id = ?", projectID, req.ImageID).First(&img).Error; err != nil {
			Fail(c, 404, "IMAGE_NOT_FOUND", "镜像不存在")
			return
		}
	}

	// 创建操作记录
	userID, _ := c.Get("userID")
	op := &model.Operation{
		UserID:    userID.(uint),
		ProjectID: projectID,
		AssetID:   asset.ID,
		Kind:      "reinstall",
		Payload:   fmt.Sprintf(`{"template_id":%d,"image_id":%d}`, req.TemplateID, req.ImageID),
		Status:    "queued",
	}
	if err := store.DB.Create(op).Error; err != nil {
		Fail(c, 500, "CREATE_FAILED", "创建操作记录失败")
		return
	}

	// 记录审计日志
	store.Audit(userID.(uint), projectID, "openapi.reinstall",
		fmt.Sprintf("资产 %d 触发装机，模板 %d", asset.ID, req.TemplateID))

	c.JSON(202, Response{
		Code: 0,
		Data: map[string]interface{}{
			"operation_id": op.ID,
			"status":       op.Status,
			"asset_id":     asset.ID,
			"template_id":  req.TemplateID,
			"created_at":   time.Now(),
		},
		Message: "装机任务已提交",
	})
}
