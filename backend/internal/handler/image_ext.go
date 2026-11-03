package handler

import (
	"fmt"

	"baremetal-manager/backend/internal/model"
	"baremetal-manager/backend/internal/repository"
	"github.com/gin-gonic/gin"
	"github.com/minio/minio-go/v7"
)

// RegisterImageExtRoutes 注册镜像管理增强路由
func RegisterImageExtRoutes(rg *gin.RouterGroup, store *repository.Store, minioClient *minio.Client, bucket string) {
	rg.DELETE("/images/:id", deleteImage(store, minioClient, bucket)) // 删除镜像
}

// deleteImage 删除镜像记录并清理对象存储中的文件
func deleteImage(store *repository.Store, minioClient *minio.Client, bucket string) gin.HandlerFunc {
	return func(c *gin.Context) {
		imageID := id(c)
		projectID := pid(c)

		// 查询镜像记录，确保归属当前项目
		var image model.Image
		if err := store.DB.Where("project_id = ? AND id = ?", projectID, imageID).First(&image).Error; err != nil {
			Fail(c, 404, "not_found", "镜像不存在")
			return
		}

		// 先删除对象存储中的文件
		if image.ObjectKey != "" {
			err := minioClient.RemoveObject(c.Request.Context(), bucket, image.ObjectKey, minio.RemoveObjectOptions{})
			if err != nil {
				// 对象存储删除失败不阻断流程，仅记录日志继续
				// 避免存储中存在但数据库已删除的孤立记录
				_ = err
			}
		}

		// 删除数据库中的镜像记录
		if err := store.DB.Delete(&image).Error; err != nil {
			Fail(c, 500, "delete_failed", "删除镜像记录失败")
			return
		}

		store.Audit(uid(c), projectID, "image.delete", fmt.Sprint(imageID))
		c.Status(204)
	}
}
