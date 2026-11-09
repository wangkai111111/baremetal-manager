// Package migrations 内嵌数据库初始化结构与中文表字段注释，供服务启动时执行。
package migrations

import _ "embed"

// InitialSQL 定义可重复执行的初始表结构、兼容补列及中文注释。
//
//go:embed 001_initial.sql
var InitialSQL string

// ExpansionSQL 定义 002 扩展迁移：新增机房、机柜、标签、传感器、告警规则、API密钥、系统设置、装机模板等表。
//
//go:embed 002_expansion.sql
var ExpansionSQL string

// TargetSQL 是已批准的目标空库结构，不允许在旧版业务表上重放。
//
//go:embed 003_target_schema.sql
var TargetSQL string
