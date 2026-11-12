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

// ModeSystemSQL 是第 4 阶段迁移：系统部署模式、部门、资产折旧、运营体系及增强审计日志。
//
//go:embed 004_mode_system.sql
var ModeSystemSQL string

//go:embed 005_asset_datacenter.sql
var AssetDatacenterSQL string

//go:embed 006_bmc_tls_verify_default.sql
var BMCTLSVerifyDefaultSQL string

//go:embed 007_redfish_monitoring.sql
var RedfishMonitoringSQL string

//go:embed 008_asset_subscriptions.sql
var AssetSubscriptionsSQL string

//go:embed 009_subscription_safety.sql
var SubscriptionSafetySQL string

//go:embed 010_subscription_balance_payment.sql
var SubscriptionBalancePaymentSQL string

// GovernanceSQL 是第 11 阶段迁移：租户审核状态机、告警四级分级与屏蔽、工单回复、批量运维任务框架。
//
//go:embed 011_governance_phase56.sql
var GovernanceSQL string
