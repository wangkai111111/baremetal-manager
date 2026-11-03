package middleware

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"baremetal-manager/backend/internal/repository"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

// Auth 保存 JWT 签名密钥和项目权限查询所需的数据存储。
type Auth struct {
	Secret []byte
	Store  *repository.Store
}

// Token 为指定用户签发有效期十二小时的 JWT。
func (a Auth) Token(id uint) (string, time.Time, error) {
	expires := time.Now().Add(12 * time.Hour)
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"sub": strconv.FormatUint(uint64(id), 10), "exp": expires.Unix()})
	text, err := token.SignedString(a.Secret)
	return text, expires, err
}

// Verify 校验 JWT 签名和有效期并返回用户 ID。
func (a Auth) Verify(text string) (uint, error) {
	claims := jwt.MapClaims{}
	token, err := jwt.ParseWithClaims(text, claims, func(t *jwt.Token) (any, error) { return a.Secret, nil }, jwt.WithValidMethods([]string{"HS256"}))
	if err != nil || !token.Valid {
		return 0, fmtError{}
	}
	sub, err := claims.GetSubject()
	if err != nil {
		return 0, err
	}
	n, err := strconv.ParseUint(sub, 10, 64)
	return uint(n), err
}

// fmtError 表示令牌校验失败。
type fmtError struct{}

// Error 返回令牌校验失败的错误文本。
func (fmtError) Error() string { return "invalid token" }

// Guard 校验请求令牌，并将用户的 ID 写入请求上下文。
func (a Auth) Guard() gin.HandlerFunc {
	return func(c *gin.Context) {
		header := strings.Fields(c.GetHeader("Authorization"))
		if strings.HasSuffix(c.Request.URL.Path, "/sol") && len(header) == 0 {
			protocols := strings.Split(c.GetHeader("Sec-WebSocket-Protocol"), ",")
			if len(protocols) == 2 && strings.TrimSpace(protocols[0]) == "bearer" {
				header = []string{"Bearer", strings.TrimSpace(protocols[1])}
			}
		}
		// 如果未携带 Bearer 令牌，检查是否存在 API Key 认证
		if len(header) != 2 || !strings.EqualFold(header[0], "Bearer") {
			// 如果已通过 API Key 认证，直接放行
			if c.GetString("authMethod") == "apikey" {
				c.Next()
				return
			}
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"message": "unauthorized"})
			return
		}
		id, err := a.Verify(header[1])
		if err != nil {
			c.AbortWithStatusJSON(401, gin.H{"message": "unauthorized"})
			return
		}
		c.Set("userID", id)
		c.Next()
	}
}

// Project 校验项目成员身份及可选的写权限，并设置项目 ID。
// write=true 时从请求路径推断资源名称，检查 resource:write 权限。
func (a Auth) Project(write bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, e := strconv.ParseUint(c.Param("projectId"), 10, 64)
		if e != nil || id == 0 {
			c.AbortWithStatusJSON(400, gin.H{"message": "invalid project"})
			return
		}
		projectID := uint(id)
		userID := c.GetUint("userID")

		_, e = a.Store.Role(userID, projectID)
		if e != nil {
			c.AbortWithStatusJSON(403, gin.H{"message": "forbidden"})
			return
		}

		if write {
			// 从路由路径推断资源名称，用于 resource:write 权限检查
			resource := inferResource(c.FullPath())
			perm := fmt.Sprintf("%s:write", resource)
			if !a.Store.HasPermission(userID, projectID, perm) {
				c.AbortWithStatusJSON(403, gin.H{"message": "forbidden"})
				return
			}
		}

		c.Set("projectID", projectID)
		c.Next()
	}
}

// inferResource 从路由完整路径推断资源名称，用于权限检查。
// 例如 "/projects/:projectId/assets" → "assets"
func inferResource(fullPath string) string {
	// 移除前缀的 /api/v1
	fullPath = strings.TrimPrefix(fullPath, "/api/v1")
	// 移除 /projects/:projectId 前缀
	parts := strings.Split(strings.Trim(fullPath, "/"), "/")
	if len(parts) >= 3 && parts[0] == "projects" {
		parts = parts[2:]
	}
	if len(parts) > 0 && parts[0] != "" {
		return parts[0]
	}
	return "general"
}
