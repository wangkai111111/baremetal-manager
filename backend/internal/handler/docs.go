package handler

import (
	"encoding/json"
	"net/http"
	"sync"

	"baremetal-manager/backend/config"

	"github.com/gin-gonic/gin"
	"gopkg.in/yaml.v3"
)

var (
	openAPIJSONOnce sync.Once
	openAPIJSON     []byte
	openAPIJSONErr  error
)

// RegisterDocsRoutes 注册公开的接口文档路由。
// 路由前缀: /api/v1，无需登录即可读取，便于集成方查阅。
func RegisterDocsRoutes(rg *gin.RouterGroup) {
	rg.GET("/openapi.yaml", serveOpenAPIYAML)
	rg.GET("/openapi.json", serveOpenAPIJSON)
}

// serveOpenAPIYAML 返回原始 OpenAPI 规范文件。
func serveOpenAPIYAML(c *gin.Context) {
	spec, err := config.OpenAPISpec()
	if err != nil {
		Fail(c, http.StatusInternalServerError, "internal", "failed to load openapi spec")
		return
	}
	c.Data(http.StatusOK, "application/yaml; charset=utf-8", spec)
}

// serveOpenAPIJSON 将 YAML 规范转换为 JSON，供前端文档页直接使用。
func serveOpenAPIJSON(c *gin.Context) {
	openAPIJSONOnce.Do(func() {
		spec, err := config.OpenAPISpec()
		if err != nil {
			openAPIJSONErr = err
			return
		}
		var document map[string]any
		if err := yaml.Unmarshal(spec, &document); err != nil {
			openAPIJSONErr = err
			return
		}
		openAPIJSON, openAPIJSONErr = json.Marshal(document)
	})
	if openAPIJSONErr != nil {
		Fail(c, http.StatusInternalServerError, "internal", "failed to parse openapi spec")
		return
	}
	c.Data(http.StatusOK, "application/json; charset=utf-8", openAPIJSON)
}
