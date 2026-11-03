package middleware

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"time"

	"baremetal-manager/backend/internal/repository"
	"github.com/gin-gonic/gin"
)

// APIKeyAuth API Key 认证中间件
// 支持通过 X-API-Key 请求头进行认证
// 认证通过后，将关联的用户和项目信息注入 gin.Context
func APIKeyAuth(store *repository.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		key := c.GetHeader("X-API-Key")
		if key == "" {
			c.Next() // 没有 API Key，交给后续的 JWT 中间件处理
			return
		}

		// 计算 key 的 SHA256 哈希
		hash := sha256.Sum256([]byte(key))
		keyHash := hex.EncodeToString(hash[:])

		// 查询数据库
		apiKey, err := store.GetAPIKeyByHash(keyHash)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid api key"})
			return
		}

		// 检查过期时间
		if apiKey.ExpiresAt != nil && apiKey.ExpiresAt.Before(time.Now()) {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "api key expired"})
			return
		}

		// 更新最后使用时间（异步更新，不阻塞请求）
		store.UpdateAPIKeyLastUsed(apiKey.ID)

		// 注入上下文信息
		c.Set("apiKeyUser", apiKey.UserID)
		if apiKey.ProjectID != nil {
			c.Set("apiKeyProject", *apiKey.ProjectID)
		}
		c.Set("apiKeyPermissions", apiKey.Permissions)
		c.Set("authMethod", "apikey")

		// 如果未设置 userID，则使用 API Key 关联的用户 ID
		if c.GetUint("userID") == 0 {
			c.Set("userID", apiKey.UserID)
		}

		c.Next()
	}
}
