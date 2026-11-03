package handler

import (
	"baremetal-manager/backend/internal/model"
	"baremetal-manager/backend/internal/repository"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

// RegisterSettingsRoutes 注册系统设置路由（全局，仅 admin 可操作）
// 路由前缀: /api/v1/settings
func RegisterSettingsRoutes(rg *gin.RouterGroup, store *repository.Store) {
	settings := rg.Group("/settings")
	{
		settings.GET("", listSettings(store))                      // 所有设置项
		settings.GET("/category/:category", getByCategory(store))   // 按分类查询
		settings.PUT("/:key", updateSetting(store))                 // 更新设置项
	}
}

// listSettings 查询所有系统设置项
func listSettings(store *repository.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 校验管理员权限
		if !isAdmin(c, store) {
			return
		}

		settings, err := store.ListSystemSettings()
		if err != nil {
			Fail(c, http.StatusInternalServerError, "DB_ERROR", "查询系统设置失败")
			return
		}

		OK(c, settings)
	}
}

// getByCategory 按分类查询系统设置项
func getByCategory(store *repository.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 校验管理员权限
		if !isAdmin(c, store) {
			return
		}

		category := c.Param("category")
		if category == "" {
			Fail(c, http.StatusBadRequest, "INVALID_PARAM", "分类名称不能为空")
			return
		}

		settings, err := store.GetSystemSettingsByCategory(category)
		if err != nil {
			Fail(c, http.StatusInternalServerError, "DB_ERROR", "查询分类设置失败")
			return
		}

		OK(c, settings)
	}
}

// updateSetting 更新指定键名的系统设置项
func updateSetting(store *repository.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 校验管理员权限
		userID := c.GetUint("userID")
		if !checkAdmin(c, store, userID) {
			return
		}

		key := c.Param("key")
		if key == "" {
			Fail(c, http.StatusBadRequest, "INVALID_PARAM", "设置键名不能为空")
			return
		}

		var input struct {
			Value       string `json:"value"`
			Description string `json:"description"`
			Category    string `json:"category"`
		}
		if err := c.ShouldBindJSON(&input); err != nil {
			Fail(c, http.StatusBadRequest, "INVALID_REQUEST", "请求参数错误")
			return
		}

		// 构造设置对象用于 upsert
		setting := &model.SystemSetting{
			Key:         key,
			Value:       input.Value,
			Description: input.Description,
			Category:    input.Category,
			UpdatedBy:   &userID,
			UpdatedAt:   time.Now(),
		}

		if err := store.UpsertSystemSetting(setting); err != nil {
			Fail(c, http.StatusInternalServerError, "DB_ERROR", "更新系统设置失败")
			return
		}

		// 查询并返回最新值
		updated, err := store.GetSystemSettingByKey(key)
		if err != nil {
			Fail(c, http.StatusInternalServerError, "DB_ERROR", "查询更新结果失败")
			return
		}

		OK(c, updated)
	}
}

// isAdmin 校验当前用户是否为管理员，失败时直接写入响应并返回 false
func isAdmin(c *gin.Context, store *repository.Store) bool {
	userID := c.GetUint("userID")
	return checkAdmin(c, store, userID)
}

// checkAdmin 根据用户 ID 校验管理员角色
func checkAdmin(c *gin.Context, store *repository.Store, userID uint) bool {
	var user model.User
	if err := store.DB.First(&user, userID).Error; err != nil {
		Fail(c, http.StatusUnauthorized, "UNAUTHORIZED", "用户不存在")
		return false
	}
	if user.Role != "admin" {
		Fail(c, http.StatusForbidden, "FORBIDDEN", "仅管理员可操作系统设置")
		return false
	}
	return true
}
