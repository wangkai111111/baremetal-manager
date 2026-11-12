package handler

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"baremetal-manager/backend/internal/model"
	"baremetal-manager/backend/internal/repository"
	"baremetal-manager/backend/internal/service"
	"baremetal-manager/backend/internal/task"
	"github.com/gin-gonic/gin"
	"github.com/hibiken/asynq"
)

// RegisterInstallRoutes 注册装机相关路由
func RegisterInstallRoutes(rg *gin.RouterGroup, store *repository.Store, svc *service.Service) {
	// Kickstart 模板管理
	rg.GET("/kickstart-templates", listKickstartTemplates(store))
	rg.POST("/kickstart-templates", createKickstartTemplate(store))
	rg.GET("/kickstart-templates/:id", getKickstartTemplate(store))
	rg.PUT("/kickstart-templates/:id", updateKickstartTemplate(store))
	rg.DELETE("/kickstart-templates/:id", deleteKickstartTemplate(store))

	// 装机任务管理
	rg.GET("/install-plans", listInstallPlans(store))
	rg.POST("/install-plans", createInstallPlan(store, svc))
	rg.GET("/install-plans/:id", getInstallPlan(store))
	rg.PUT("/install-plans/:id/retry", retryInstallPlan(store, svc))
}

// listKickstartTemplates 分页列出项目下的 Kickstart 模板
func listKickstartTemplates(store *repository.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		projectID := pid(c)
		page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
		pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))

		var items []model.KickstartTemplate
		query := store.DB.Where("project_id = ? AND deleted_at IS NULL", projectID).Order("id desc")
		result, err := store.Paginate(query, page, pageSize, &items)
		if err != nil {
			Fail(c, 500, "query_failed", "查询模板列表失败")
			return
		}
		PageOK(c, result)
	}
}

// createKickstartTemplate 创建 Kickstart 模板
func createKickstartTemplate(store *repository.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 接受 JSON: {name, format, content, os_type, partition_scheme, variables, post_script}
		var input struct {
			Name            string `json:"name"`
			Format          string `json:"format"`
			Content         string `json:"content"`
			Variables       string `json:"variables"`
			OSType          string `json:"os_type"`
			PartitionScheme string `json:"partition_scheme"`
			PostScript      string `json:"post_script"`
		}
		if c.ShouldBindJSON(&input) != nil || input.Name == "" {
			Fail(c, 400, "invalid_request", "模板名称不能为空")
			return
		}
		if !service.IsValidInstallFormat(input.Format) {
			Fail(c, 400, "invalid_format", "应答文件格式必须为 kickstart/autoinstall/preseed/unattend")
			return
		}

		userID := uid(c)
		tpl := model.KickstartTemplate{
			Name:            input.Name,
			Format:          strings.ToLower(strings.TrimSpace(input.Format)),
			TemplateContent: input.Content,
			Variables:       input.Variables,
			OSType:          input.OSType,
			PartitionScheme: input.PartitionScheme,
			PostScript:      input.PostScript,
			ProjectID:       pid(c),
			CreateBy:        &userID,
		}
		if err := store.DB.Create(&tpl).Error; err != nil {
			Fail(c, 500, "create_failed", "创建模板失败")
			return
		}
		store.Audit(userID, pid(c), "kickstart_template.create", fmt.Sprint(tpl.ID))
		Created(c, tpl)
	}
}

// getKickstartTemplate 获取 Kickstart 模板详情
func getKickstartTemplate(store *repository.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		templateID := id(c)
		var tpl model.KickstartTemplate
		if err := store.DB.Where("id = ? AND project_id = ? AND deleted_at IS NULL", templateID, pid(c)).First(&tpl).Error; err != nil {
			Fail(c, 404, "not_found", "模板不存在")
			return
		}
		OK(c, tpl)
	}
}

// updateKickstartTemplate 更新 Kickstart 模板
func updateKickstartTemplate(store *repository.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		templateID := id(c)
		var tpl model.KickstartTemplate
		if err := store.DB.Where("id = ? AND project_id = ? AND deleted_at IS NULL", templateID, pid(c)).First(&tpl).Error; err != nil {
			Fail(c, 404, "not_found", "模板不存在")
			return
		}

		// 接受 JSON: {name, format, content, os_type, partition_scheme, variables, post_script}
		var input struct {
			Name            string `json:"name"`
			Format          string `json:"format"`
			Content         string `json:"content"`
			Variables       string `json:"variables"`
			OSType          string `json:"os_type"`
			PartitionScheme string `json:"partition_scheme"`
			PostScript      string `json:"post_script"`
		}
		if c.ShouldBindJSON(&input) != nil {
			Fail(c, 400, "invalid_request", "请求参数错误")
			return
		}
		if !service.IsValidInstallFormat(input.Format) {
			Fail(c, 400, "invalid_format", "应答文件格式必须为 kickstart/autoinstall/preseed/unattend")
			return
		}

		// 仅更新数据库中真实存在的列（name/format/content）；
		// os_type/partition_scheme/variables/post_script 在目标库中无对应列，
		// 只在内存结构上回显，供前端与渲染引擎使用。
		changes := map[string]interface{}{
			"name":    input.Name,
			"format":  strings.ToLower(strings.TrimSpace(input.Format)),
			"content": input.Content,
		}
		if err := store.DB.Model(&tpl).Updates(changes).Error; err != nil {
			Fail(c, 500, "update_failed", "更新模板失败")
			return
		}
		// 同步内存中的非持久化字段，以便重新查询后回显最新值。
		tpl.Variables = input.Variables
		tpl.OSType = input.OSType
		tpl.PartitionScheme = input.PartitionScheme
		tpl.PostScript = input.PostScript
		store.Audit(uid(c), pid(c), "kickstart_template.update", fmt.Sprint(tpl.ID))
		// 重新查询返回最新数据（仅刷新映射列，不覆盖 gorm:"-" 内存字段）
		store.DB.Where("id = ?", tpl.ID).First(&tpl)
		OK(c, tpl)
	}
}

// deleteKickstartTemplate 删除 Kickstart 模板（软删除）
func deleteKickstartTemplate(store *repository.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		templateID := id(c)
		result := store.DB.Where("id = ? AND project_id = ? AND deleted_at IS NULL", templateID, pid(c)).Delete(&model.KickstartTemplate{})
		if result.RowsAffected == 0 {
			Fail(c, 404, "not_found", "模板不存在")
			return
		}
		store.Audit(uid(c), pid(c), "kickstart_template.delete", fmt.Sprint(templateID))
		c.Status(204)
	}
}

// listInstallPlans 分页列出项目下的装机计划
func listInstallPlans(store *repository.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		projectID := pid(c)
		page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
		pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))

		var items []model.InstallPlan
		query := store.DB.Where("project_id = ? AND deleted_at IS NULL", projectID).Order("id desc")
		result, err := store.Paginate(query, page, pageSize, &items)
		if err != nil {
			Fail(c, 500, "query_failed", "查询装机计划列表失败")
			return
		}
		PageOK(c, result)
	}
}

// createInstallPlan 创建装机计划并触发异步装机任务
func createInstallPlan(store *repository.Store, svc *service.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var input struct {
			Name        string `json:"name"`
			TemplateID  uint   `json:"template_id"`
			AssetID     uint   `json:"asset_id"`
			Description string `json:"description"`
			Config      string `json:"config"`
		}
		if c.ShouldBindJSON(&input) != nil || input.Name == "" || input.TemplateID == 0 || input.AssetID == 0 {
			Fail(c, 400, "invalid_request", "装机计划名称、模板ID和设备ID不能为空")
			return
		}

		projectID := pid(c)
		userID := uid(c)

		// 校验设备归属
		var asset model.Asset
		if err := store.DB.Where("project_id = ? AND id = ?", projectID, input.AssetID).First(&asset).Error; err != nil {
			Fail(c, 404, "asset_not_found", "设备不存在")
			return
		}

		// 校验模板存在
		var tpl model.KickstartTemplate
		if err := store.DB.Where("id = ? AND project_id = ? AND deleted_at IS NULL", input.TemplateID, projectID).First(&tpl).Error; err != nil {
			Fail(c, 404, "template_not_found", "装机模板不存在")
			return
		}

		// 创建装机计划记录
		plan := model.InstallPlan{
			Name:        input.Name,
			TemplateID:  input.TemplateID,
			Description: input.Description,
			Config:      input.Config,
			ProjectID:   projectID,
			CreateBy:    userID,
			UpdateBy:    userID,
		}
		if err := store.DB.Create(&plan).Error; err != nil {
			Fail(c, 500, "create_failed", "创建装机计划失败")
			return
		}

		// 创建 Operation 记录（kind="reinstall"）
		payload, _ := json.Marshal(task.ReinstallAction{
			AssetID:    input.AssetID,
			TemplateID: input.TemplateID,
		})
		op := model.Operation{
			UserID:    userID,
			ProjectID: projectID,
			AssetID:   input.AssetID,
			Kind:      "reinstall",
			Payload:   string(payload),
			Status:    "queued",
		}
		if err := store.DB.Create(&op).Error; err != nil {
			Fail(c, 500, "create_operation_failed", "创建操作记录失败")
			return
		}

		// 通过 asynq 入队异步装机任务
		asynqTask, err := task.NewReinstallTask(task.ReinstallAction{
			AssetID:    input.AssetID,
			TemplateID: input.TemplateID,
		})
		if err != nil {
			store.DB.Model(&op).Updates(map[string]interface{}{"status": "failed", "error": "创建任务失败"})
			Fail(c, 500, "enqueue_failed", "创建装机任务失败")
			return
		}
		_, err = svc.Queue.Enqueue(asynqTask, asynq.MaxRetry(3), asynq.Timeout(30*time.Minute))
		if err != nil {
			store.DB.Model(&op).Updates(map[string]interface{}{"status": "failed", "error": "任务入队失败"})
			Fail(c, 503, "queue_unavailable", "任务队列不可用")
			return
		}

		store.Audit(userID, projectID, "install_plan.create", fmt.Sprintf("plan=%d,op=%d", plan.ID, op.ID))
		Created(c, gin.H{
			"plan":      plan,
			"operation": op,
		})
	}
}

// getInstallPlan 获取装机计划详情
func getInstallPlan(store *repository.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		planID := id(c)
		var plan model.InstallPlan
		if err := store.DB.Where("id = ? AND project_id = ? AND deleted_at IS NULL", planID, pid(c)).First(&plan).Error; err != nil {
			Fail(c, 404, "not_found", "装机计划不存在")
			return
		}

		// 同时查询关联的操作记录
		var ops []model.Operation
		store.DB.Where("project_id = ? AND asset_id = ? AND kind = ?", pid(c), plan.ID, "reinstall").
			Order("id desc").Limit(10).Find(&ops)

		OK(c, gin.H{
			"plan":       plan,
			"operations": ops,
		})
	}
}

// retryInstallPlan 重试装机计划
func retryInstallPlan(store *repository.Store, svc *service.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		planID := id(c)
		projectID := pid(c)
		userID := uid(c)

		// 查询装机计划
		var plan model.InstallPlan
		if err := store.DB.Where("id = ? AND project_id = ? AND deleted_at IS NULL", planID, projectID).First(&plan).Error; err != nil {
			Fail(c, 404, "not_found", "装机计划不存在")
			return
		}

		// 查询最近一次失败的 Operation
		var lastOp model.Operation
		if err := store.DB.Where("project_id = ? AND kind = ? AND payload LIKE ?", projectID, "reinstall", fmt.Sprintf("%%%d%%", plan.ID)).
			Order("id desc").First(&lastOp).Error; err != nil {
			Fail(c, 404, "operation_not_found", "未找到关联的操作记录")
			return
		}

		// 解析原始 payload 获取设备信息
		var reinstallPayload task.ReinstallAction
		if err := json.Unmarshal([]byte(lastOp.Payload), &reinstallPayload); err != nil {
			Fail(c, 500, "parse_payload_failed", "解析操作参数失败")
			return
		}

		// 创建新的 Operation 记录
		payload, _ := json.Marshal(reinstallPayload)
		op := model.Operation{
			UserID:     userID,
			ProjectID:  projectID,
			AssetID:    reinstallPayload.AssetID,
			Kind:       "reinstall",
			Payload:    string(payload),
			Status:     "queued",
			RetryCount: lastOp.RetryCount + 1,
		}
		if err := store.DB.Create(&op).Error; err != nil {
			Fail(c, 500, "create_operation_failed", "创建重试操作记录失败")
			return
		}

		// 入队异步任务
		asynqTask, err := task.NewReinstallTask(reinstallPayload)
		if err != nil {
			store.DB.Model(&op).Updates(map[string]interface{}{"status": "failed", "error": "创建重试任务失败"})
			Fail(c, 500, "enqueue_failed", "创建重试任务失败")
			return
		}
		_, err = svc.Queue.Enqueue(asynqTask, asynq.MaxRetry(3), asynq.Timeout(30*time.Minute))
		if err != nil {
			store.DB.Model(&op).Updates(map[string]interface{}{"status": "failed", "error": "任务入队失败"})
			Fail(c, 503, "queue_unavailable", "任务队列不可用")
			return
		}

		store.Audit(userID, projectID, "install_plan.retry", fmt.Sprintf("plan=%d,op=%d", plan.ID, op.ID))
		OK(c, op)
	}
}
