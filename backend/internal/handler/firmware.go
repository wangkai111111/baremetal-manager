package handler

import (
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"baremetal-manager/backend/internal/model"
	"baremetal-manager/backend/internal/repository"
	"baremetal-manager/backend/internal/service"
	"baremetal-manager/backend/internal/task"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/minio/minio-go/v7"
)

// RegisterFirmwareRoutes 注册固件管理路由
func RegisterFirmwareRoutes(rg *gin.RouterGroup, store *repository.Store, svc *service.Service, minioClient *minio.Client, bucket string) {
	rg.GET("/firmware", listFirmware(store))                   // 固件版本列表
	rg.POST("/firmware/upload", uploadFirmware(store, svc, minioClient, bucket)) // 上传固件文件
	rg.POST("/firmware/upgrade", triggerFirmwareUpgrade(store, svc))             // 批量触发升级
	rg.GET("/firmware/:id/progress", getUpgradeProgress(store))                  // 升级进度
}

// listFirmware 查询固件清单列表，支持按设备 ID 筛选和分页
func listFirmware(store *repository.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		projectID := pid(c)
		page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
		pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
		assetIDStr := c.Query("asset_id")

		query := store.DB.Where("project_id = ? AND deleted_at IS NULL", projectID)
		if assetIDStr != "" {
			assetID, _ := strconv.ParseUint(assetIDStr, 10, 64)
			if assetID > 0 {
				query = query.Where("asset_id = ?", uint(assetID))
			}
		}
		query = query.Order("id desc")

		var items []model.FirmwareInventory
		result, err := store.Paginate(query, page, pageSize, &items)
		if err != nil {
			Fail(c, 500, "query_failed", "查询固件列表失败")
			return
		}
		PageOK(c, result)
	}
}

// uploadFirmware 上传固件文件到 MinIO 并创建镜像记录
func uploadFirmware(store *repository.Store, svc *service.Service, minioClient *minio.Client, bucket string) gin.HandlerFunc {
	return func(c *gin.Context) {
		projectID := pid(c)
		userID := uid(c)

		file, err := c.FormFile("file")
		if err != nil || file.Size > 5<<30 {
			Fail(c, 400, "invalid_file", "无效的文件或文件超过5GB限制")
			return
		}

		// 读取表单中的可选字段
		name := c.PostForm("name")
		if name == "" {
			name = file.Filename
		}
		version := c.PostForm("version")
		componentType := c.PostForm("component_type")

		f, err := file.Open()
		if err != nil {
			Fail(c, 400, "invalid_file", "无法打开上传的文件")
			return
		}
		defer f.Close()

		// 上传到 MinIO 对象存储
		key := fmt.Sprintf("%d/firmware/%s", projectID, uuid.NewString())
		_, err = minioClient.PutObject(c.Request.Context(), bucket, key, f, file.Size, minio.PutObjectOptions{ContentType: "application/octet-stream"})
		if err != nil {
			Fail(c, 502, "storage_unavailable", "对象存储上传失败")
			return
		}

		// 创建 Image 记录（category="firmware"）
		image := model.Image{
			ProjectID: projectID,
			Name:      name,
			ObjectKey: key,
			Size:      file.Size,
			Category:  "firmware",
			Version:   version,
			OSType:    componentType,
		}
		if err := store.DB.Create(&image).Error; err != nil {
			// 上传失败时清理已上传的文件
			minioClient.RemoveObject(c.Request.Context(), bucket, key, minio.RemoveObjectOptions{})
			Fail(c, 500, "create_failed", "创建固件记录失败")
			return
		}

		store.Audit(userID, projectID, "firmware.upload", fmt.Sprint(image.ID))
		Created(c, image)
	}
}

// triggerFirmwareUpgrade 批量触发固件升级任务
func triggerFirmwareUpgrade(store *repository.Store, svc *service.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		projectID := pid(c)
		userID := uid(c)

		var input struct {
			AssetIDs []uint `json:"asset_ids"` // 目标设备 ID 列表
			ImageID  uint   `json:"image_id"`  // 固件镜像 ID
		}
		if c.ShouldBindJSON(&input) != nil || len(input.AssetIDs) == 0 || input.ImageID == 0 {
			Fail(c, 400, "invalid_request", "设备ID列表和固件镜像ID不能为空")
			return
		}

		// 校验固件镜像存在
		var image model.Image
		if err := store.DB.Where("id = ? AND project_id = ? AND category = ?", input.ImageID, projectID, "firmware").First(&image).Error; err != nil {
			Fail(c, 404, "firmware_not_found", "固件镜像不存在")
			return
		}

		// 校验所有设备归属当前项目
		for _, assetID := range input.AssetIDs {
			var asset model.Asset
			if err := store.DB.Where("project_id = ? AND id = ?", projectID, assetID).First(&asset).Error; err != nil {
				Fail(c, 404, "asset_not_found", fmt.Sprintf("设备 %d 不存在", assetID))
				return
			}
		}

		// 为每个设备创建 Operation 记录并入队升级任务
		operationIDs := make([]uint, 0, len(input.AssetIDs))
		for _, assetID := range input.AssetIDs {
			payload, _ := json.Marshal(task.FirmwareUpgradeAction{
				AssetIDs: []uint{assetID},
				FileKey:  image.ObjectKey,
			})
			op := model.Operation{
				UserID:    userID,
				ProjectID: projectID,
				AssetID:   assetID,
				Kind:      "firmware",
				Payload:   string(payload),
				Status:    "queued",
			}
			if err := store.DB.Create(&op).Error; err != nil {
				Fail(c, 500, "create_operation_failed", "创建操作记录失败")
				return
			}

			// 入队异步固件升级任务
			asynqTask, err := task.NewFirmwareTask(task.FirmwareUpgradeAction{
				AssetIDs: []uint{assetID},
				FileKey:  image.ObjectKey,
			})
			if err != nil {
				store.DB.Model(&op).Updates(map[string]interface{}{"status": "failed", "error": "创建任务失败"})
				Fail(c, 500, "enqueue_failed", "创建固件升级任务失败")
				return
			}
			_, err = svc.Queue.Enqueue(asynqTask, asynq.MaxRetry(3), asynq.Timeout(60*time.Minute))
			if err != nil {
				store.DB.Model(&op).Updates(map[string]interface{}{"status": "failed", "error": "任务入队失败"})
				Fail(c, 503, "queue_unavailable", "任务队列不可用")
				return
			}
			operationIDs = append(operationIDs, op.ID)
		}

		store.Audit(userID, projectID, "firmware.upgrade", fmt.Sprintf("image=%d,ops=%v", image.ID, operationIDs))
		Created(c, gin.H{
			"image_id":    image.ID,
			"operation_ids": operationIDs,
			"asset_count": len(input.AssetIDs),
		})
	}
}

// getUpgradeProgress 查询固件升级进度
func getUpgradeProgress(store *repository.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		operationID := id(c)
		projectID := pid(c)

		var op model.Operation
		if err := store.DB.Where("id = ? AND project_id = ?", operationID, projectID).First(&op).Error; err != nil {
			Fail(c, 404, "not_found", "操作记录不存在")
			return
		}

		// 只返回固件相关的操作
		if op.Kind != "firmware" {
			Fail(c, 400, "invalid_type", "该操作不是固件升级任务")
			return
		}

		// 解析 payload 获取详细信息
		var payloadInfo map[string]interface{}
		_ = json.Unmarshal([]byte(op.Payload), &payloadInfo)

		OK(c, gin.H{
			"operation_id": op.ID,
			"asset_id":     op.AssetID,
			"status":       op.Status,
			"error":        op.Error,
			"attempts":     op.Attempts,
			"retry_count":  op.RetryCount,
			"started_at":   op.StartedAt,
			"completed_at": op.CompletedAt,
			"result":       op.Result,
			"created_at":   op.CreatedAt,
			"updated_at":   op.UpdatedAt,
		})
	}
}
