package handler

import (
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"baremetal-manager/backend/internal/adapter"
	"baremetal-manager/backend/internal/model"
	"baremetal-manager/backend/internal/repository"
	"github.com/gin-gonic/gin"
)

// RegisterAlertExtRoutes 注册告警管理增强路由
func RegisterAlertExtRoutes(rg *gin.RouterGroup, store *repository.Store) {
	// 告警规则管理
	rg.GET("/alert-rules", listAlertRules(store))
	rg.POST("/alert-rules", createAlertRule(store))
	rg.GET("/alert-rules/:id", getAlertRule(store))
	rg.PUT("/alert-rules/:id", updateAlertRule(store))
	rg.DELETE("/alert-rules/:id", deleteAlertRule(store))

	// 告警推送渠道管理
	rg.GET("/alert-channels", listAlertChannels(store))
	rg.POST("/alert-channels", createAlertChannel(store))
	rg.PUT("/alert-channels/:id", updateAlertChannel(store))
	rg.DELETE("/alert-channels/:id", deleteAlertChannel(store))
	rg.POST("/alert-channels/:id/test", testAlertChannel(store))

	// 告警确认
	rg.POST("/alerts/:id/acknowledge", acknowledgeAlert(store))
}

// listAlertRules 列出项目下的所有告警规则
func listAlertRules(store *repository.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		projectID := pid(c)
		rules, err := store.ListAlertRules(projectID)
		if err != nil {
			Fail(c, 500, "query_failed", "查询告警规则列表失败")
			return
		}
		OK(c, rules)
	}
}

// createAlertRule 创建告警规则
func createAlertRule(store *repository.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		var input struct {
			Name       string  `json:"name"`
			Metric     string  `json:"metric"`
			Operator   string  `json:"operator"`
			Threshold  float64 `json:"threshold"`
			Severity   string  `json:"severity"`
			Enabled    bool    `json:"enabled"`
			TimeWindow int     `json:"time_window"`
		}
		if c.ShouldBindJSON(&input) != nil || input.Name == "" || input.Metric == "" {
			Fail(c, 400, "invalid_request", "规则名称和监控指标不能为空")
			return
		}

		// 校验告警等级
		if input.Severity != "INFO" && input.Severity != "WARNING" && input.Severity != "CRITICAL" {
			input.Severity = "WARNING"
		}
		// 校验比较运算符
		if input.Operator != ">" && input.Operator != ">=" && input.Operator != "<" && input.Operator != "<=" && input.Operator != "=" {
			input.Operator = ">"
		}
		if input.TimeWindow <= 0 {
			input.TimeWindow = 300
		}

		userID := uid(c)
		rule := model.AlertRule{
			Name:       input.Name,
			ProjectID:  pid(c),
			Metric:     input.Metric,
			Operator:   input.Operator,
			Threshold:  input.Threshold,
			Severity:   input.Severity,
			Enabled:    input.Enabled,
			TimeWindow: input.TimeWindow,
			CreateBy:   &userID,
		}
		if err := store.CreateAlertRule(&rule); err != nil {
			Fail(c, 500, "create_failed", "创建告警规则失败")
			return
		}
		store.Audit(userID, pid(c), "alert_rule.create", fmt.Sprint(rule.ID))
		Created(c, rule)
	}
}

// getAlertRule 获取告警规则详情
func getAlertRule(store *repository.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		ruleID := id(c)
		rule, err := store.GetAlertRule(ruleID)
		if err != nil {
			Fail(c, 404, "not_found", "告警规则不存在")
			return
		}
		// 校验项目归属
		if rule.ProjectID != pid(c) {
			Fail(c, 404, "not_found", "告警规则不存在")
			return
		}
		OK(c, rule)
	}
}

// updateAlertRule 更新告警规则
func updateAlertRule(store *repository.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		ruleID := id(c)
		projectID := pid(c)

		var rule model.AlertRule
		if err := store.DB.Where("id = ? AND project_id = ? AND deleted_at IS NULL", ruleID, projectID).First(&rule).Error; err != nil {
			Fail(c, 404, "not_found", "告警规则不存在")
			return
		}

		var input struct {
			Name       string  `json:"name"`
			Metric     string  `json:"metric"`
			Operator   string  `json:"operator"`
			Threshold  float64 `json:"threshold"`
			Severity   string  `json:"severity"`
			Enabled    *bool   `json:"enabled"`
			TimeWindow int     `json:"time_window"`
		}
		if c.ShouldBindJSON(&input) != nil {
			Fail(c, 400, "invalid_request", "请求参数错误")
			return
		}

		changes := map[string]interface{}{}
		if input.Name != "" {
			changes["name"] = input.Name
		}
		if input.Metric != "" {
			changes["metric"] = input.Metric
		}
		if input.Operator != "" {
			changes["operator"] = input.Operator
		}
		if input.Threshold != 0 {
			changes["threshold"] = input.Threshold
		}
		if input.Severity != "" {
			changes["severity"] = input.Severity
		}
		if input.Enabled != nil {
			changes["enabled"] = *input.Enabled
		}
		if input.TimeWindow > 0 {
			changes["time_window"] = input.TimeWindow
		}

		if len(changes) > 0 {
			if err := store.DB.Model(&rule).Updates(changes).Error; err != nil {
				Fail(c, 500, "update_failed", "更新告警规则失败")
				return
			}
		}
		store.Audit(uid(c), projectID, "alert_rule.update", fmt.Sprint(rule.ID))
		store.DB.Where("id = ?", rule.ID).First(&rule)
		OK(c, rule)
	}
}

// deleteAlertRule 删除告警规则（软删除）
func deleteAlertRule(store *repository.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		ruleID := id(c)
		projectID := pid(c)

		result := store.DB.Where("id = ? AND project_id = ? AND deleted_at IS NULL", ruleID, projectID).Delete(&model.AlertRule{})
		if result.RowsAffected == 0 {
			Fail(c, 404, "not_found", "告警规则不存在")
			return
		}
		store.Audit(uid(c), projectID, "alert_rule.delete", fmt.Sprint(ruleID))
		c.Status(204)
	}
}

// listAlertChannels 列出项目下的所有告警推送渠道
func listAlertChannels(store *repository.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		projectID := pid(c)
		channels, err := store.ListAlertChannels(projectID)
		if err != nil {
			Fail(c, 500, "query_failed", "查询告警渠道列表失败")
			return
		}
		OK(c, channels)
	}
}

// createAlertChannel 创建告警推送渠道
func createAlertChannel(store *repository.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		var input struct {
			Name    string `json:"name"`
			Type    string `json:"type"`
			Config  string `json:"config"`
			Enabled bool   `json:"enabled"`
		}
		if c.ShouldBindJSON(&input) != nil || input.Name == "" || input.Type == "" {
			Fail(c, 400, "invalid_request", "渠道名称和类型不能为空")
			return
		}

		// 校验渠道类型
		if input.Type != "wechat" && input.Type != "dingtalk" && input.Type != "webhook" && input.Type != "email" {
			Fail(c, 400, "invalid_type", "不支持的推送渠道类型，仅支持 wechat/dingtalk/webhook/email")
			return
		}

		// 校验配置 JSON 格式
		if input.Config != "" {
			var cfgMap map[string]interface{}
			if err := json.Unmarshal([]byte(input.Config), &cfgMap); err != nil {
				Fail(c, 400, "invalid_config", "渠道配置必须为有效的JSON格式")
				return
			}
		}

		channel := model.AlertChannel{
			Name:      input.Name,
			Type:      input.Type,
			Config:    input.Config,
			ProjectID: pid(c),
			Enabled:   input.Enabled,
		}
		if err := store.CreateAlertChannel(&channel); err != nil {
			Fail(c, 500, "create_failed", "创建告警渠道失败")
			return
		}
		store.Audit(uid(c), pid(c), "alert_channel.create", fmt.Sprint(channel.ID))
		Created(c, channel)
	}
}

// updateAlertChannel 更新告警推送渠道
func updateAlertChannel(store *repository.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		channelID := id(c)
		projectID := pid(c)

		var channel model.AlertChannel
		if err := store.DB.Where("id = ? AND project_id = ?", channelID, projectID).First(&channel).Error; err != nil {
			Fail(c, 404, "not_found", "告警渠道不存在")
			return
		}

		var input struct {
			Name    string `json:"name"`
			Type    string `json:"type"`
			Config  string `json:"config"`
			Enabled *bool  `json:"enabled"`
		}
		if c.ShouldBindJSON(&input) != nil {
			Fail(c, 400, "invalid_request", "请求参数错误")
			return
		}

		changes := map[string]interface{}{}
		if input.Name != "" {
			changes["name"] = input.Name
		}
		if input.Type != "" {
			changes["type"] = input.Type
		}
		if input.Config != "" {
			var cfgMap map[string]interface{}
			if err := json.Unmarshal([]byte(input.Config), &cfgMap); err != nil {
				Fail(c, 400, "invalid_config", "渠道配置必须为有效的JSON格式")
				return
			}
			changes["config"] = input.Config
		}
		if input.Enabled != nil {
			changes["enabled"] = *input.Enabled
		}

		if len(changes) > 0 {
			if err := store.DB.Model(&channel).Updates(changes).Error; err != nil {
				Fail(c, 500, "update_failed", "更新告警渠道失败")
				return
			}
		}
		store.Audit(uid(c), projectID, "alert_channel.update", fmt.Sprint(channel.ID))
		store.DB.Where("id = ?", channel.ID).First(&channel)
		OK(c, channel)
	}
}

// deleteAlertChannel 删除告警推送渠道
func deleteAlertChannel(store *repository.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		channelID := id(c)
		projectID := pid(c)

		result := store.DB.Where("id = ? AND project_id = ?", channelID, projectID).Delete(&model.AlertChannel{})
		if result.RowsAffected == 0 {
			Fail(c, 404, "not_found", "告警渠道不存在")
			return
		}
		store.Audit(uid(c), projectID, "alert_channel.delete", fmt.Sprint(channelID))
		c.Status(204)
	}
}

// testAlertChannel 测试告警推送渠道
func testAlertChannel(store *repository.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		channelID := id(c)
		projectID := pid(c)

		// 查询渠道配置
		var channel model.AlertChannel
		if err := store.DB.Where("id = ? AND project_id = ?", channelID, projectID).First(&channel).Error; err != nil {
			Fail(c, 404, "not_found", "告警渠道不存在")
			return
		}

		// 解析渠道配置
		var configMap map[string]interface{}
		if channel.Config != "" {
			if err := json.Unmarshal([]byte(channel.Config), &configMap); err != nil {
				Fail(c, 500, "invalid_config", "渠道配置JSON解析失败")
				return
			}
		}

		// 构造测试推送数据
		payload := adapter.NotifyPayload{
			AlertID:   0,
			Severity:  "INFO",
			Message:   "这是一条测试告警消息，请忽略。",
			AssetName: "测试设备",
			AssetID:   0,
			ProjectID: projectID,
			RuleName:  "测试规则",
			CreatedAt: time.Now().Format(time.RFC3339),
		}

		// 创建对应的推送适配器并发送测试消息
		notifier, err := adapter.NewNotifier(channel.Type, configMap)
		if err != nil {
			Fail(c, 500, "create_notifier_failed", fmt.Sprintf("创建推送适配器失败: %s", err.Error()))
			return
		}

		if err := notifier.Send(payload); err != nil {
			Fail(c, 502, "send_failed", fmt.Sprintf("发送测试消息失败: %s", err.Error()))
			return
		}

		store.Audit(uid(c), projectID, "alert_channel.test", fmt.Sprint(channelID))
		OK(c, gin.H{"message": "测试消息发送成功"})
	}
}

// acknowledgeAlert 确认告警
func acknowledgeAlert(store *repository.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		alertID := id(c)
		projectID := pid(c)
		userID := uid(c)

		var alert model.Alert
		if err := store.DB.Where("id = ? AND project_id = ?", alertID, projectID).First(&alert).Error; err != nil {
			Fail(c, 404, "not_found", "告警不存在")
			return
		}

		// 检查告警是否已确认
		if alert.AcknowledgedAt != nil {
			Fail(c, 409, "already_acknowledged", "该告警已被确认")
			return
		}

		now := time.Now()
		if err := store.DB.Model(&alert).Updates(map[string]interface{}{
			"acknowledged_at": &now,
			"acknowledged_by": userID,
		}).Error; err != nil {
			Fail(c, 500, "acknowledge_failed", "确认告警失败")
			return
		}

		store.Audit(userID, projectID, "alert.acknowledge", strconv.FormatUint(uint64(alertID), 10))
		store.DB.Where("id = ?", alertID).First(&alert)
		OK(c, alert)
	}
}
