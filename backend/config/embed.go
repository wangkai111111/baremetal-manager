// Package config 暴露编译期嵌入的接口规范文件。
package config

import "embed"

// openapiFS 嵌入 OpenAPI 规范，避免运行时依赖外部文件路径。
//
//go:embed openapi.yaml
var openapiFS embed.FS

// OpenAPISpec 返回原始 OpenAPI YAML 内容。
func OpenAPISpec() ([]byte, error) {
	return openapiFS.ReadFile("openapi.yaml")
}
