package handler

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
	"time"

	"baremetal-manager/backend/internal/model"
	"baremetal-manager/backend/internal/repository"

	"github.com/gin-gonic/gin"
)

// apiKeyView 是返回给前端的密钥视图，权限字段以数组形式输出。
type apiKeyView struct {
	ID          uint       `json:"id"`
	Name        string     `json:"name"`
	ProjectID   *uint      `json:"project_id,omitempty"`
	Permissions []string   `json:"permissions"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
	LastUsedAt  *time.Time `json:"last_used_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
}

// splitKeyPermissions 将以逗号分隔的权限字符串切分为数组。
func splitKeyPermissions(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return []string{}
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if v := strings.TrimSpace(part); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// toAPIKeyView 将数据库模型转换为前端使用的视图结构。
func toAPIKeyView(key model.APIKey) apiKeyView {
	return apiKeyView{
		ID:          key.ID,
		Name:        key.Name,
		ProjectID:   key.ProjectID,
		Permissions: splitKeyPermissions(key.Permissions),
		ExpiresAt:   key.ExpiresAt,
		LastUsedAt:  key.LastUsedAt,
		CreatedAt:   key.CreatedAt,
	}
}

// RegisterAPIKeyRoutes 注册当前用户开放 API 密钥管理路由。
// 路由前缀: /api/v1/api-keys
func RegisterAPIKeyRoutes(rg *gin.RouterGroup, store *repository.Store) {
	keys := rg.Group("/api-keys")
	{
		keys.GET("", listAPIKeys(store))
		keys.POST("", createAPIKey(store))
		keys.DELETE("/:id", deleteAPIKey(store))
	}
}

// listAPIKeys 列出当前登录用户创建的 API 密钥。
func listAPIKeys(store *repository.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		keys, err := store.ListAPIKeys(uid(c))
		if err != nil {
			Fail(c, http.StatusInternalServerError, "DB_ERROR", "failed to list api keys")
			return
		}
		items := make([]apiKeyView, 0, len(keys))
		for _, key := range keys {
			items = append(items, toAPIKeyView(key))
		}
		PageOK(c, &repository.PageResult{Items: items, Total: int64(len(items))})
	}
}

// createAPIKey 生成新的 API 密钥并仅在本次响应中返回明文。
func createAPIKey(store *repository.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		var in struct {
			Name        string     `json:"name"`
			Permissions []string   `json:"permissions"`
			ExpiresAt   *time.Time `json:"expires_at"`
		}
		if err := c.ShouldBindJSON(&in); err != nil || strings.TrimSpace(in.Name) == "" {
			Fail(c, http.StatusBadRequest, "INVALID_REQUEST", "name is required")
			return
		}
		if in.ExpiresAt != nil && in.ExpiresAt.Before(time.Now()) {
			Fail(c, http.StatusBadRequest, "INVALID_REQUEST", "expires_at must be in the future")
			return
		}

		buf := make([]byte, 32)
		if _, err := rand.Read(buf); err != nil {
			Fail(c, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to generate api key")
			return
		}
		secret := "bmm_" + hex.EncodeToString(buf)
		hash := sha256.Sum256([]byte(secret))

		key := &model.APIKey{
			Name:        strings.TrimSpace(in.Name),
			KeyHash:     hex.EncodeToString(hash[:]),
			UserID:      uid(c),
			Permissions: strings.Join(in.Permissions, ","),
			ExpiresAt:   in.ExpiresAt,
		}
		if err := store.CreateAPIKey(key); err != nil {
			Fail(c, http.StatusConflict, "CREATE_FAILED", "failed to create api key")
			return
		}
		store.Audit(uid(c), 0, "apikey.create", key.Name)

		view := toAPIKeyView(*key)
		Created(c, gin.H{
			"id":          view.ID,
			"name":        view.Name,
			"permissions": view.Permissions,
			"expires_at":  view.ExpiresAt,
			"created_at":  view.CreatedAt,
			"key":         secret,
		})
	}
}

// deleteAPIKey 软删除当前用户拥有的 API 密钥。
func deleteAPIKey(store *repository.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		var key model.APIKey
		if err := store.DB.First(&key, id(c)).Error; err != nil || key.UserID != uid(c) {
			Fail(c, http.StatusNotFound, "NOT_FOUND", "api key not found")
			return
		}
		if err := store.DeleteAPIKey(key.ID); err != nil {
			Fail(c, http.StatusInternalServerError, "DB_ERROR", "failed to delete api key")
			return
		}
		store.Audit(uid(c), 0, "apikey.revoke", key.Name)
		c.Status(http.StatusNoContent)
	}
}
