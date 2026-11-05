package handler

import (
	"strconv"
	"time"

	"baremetal-manager/backend/internal/model"
	"baremetal-manager/backend/internal/repository"
	"github.com/gin-gonic/gin"
)

// RegisterSensorRoutes 注册传感器数据路由。
func RegisterSensorRoutes(rg *gin.RouterGroup, store *repository.Store) {
	// 使用 :id 与其他路由保持一致，避免 Gin 通配符冲突
	rg.GET("/assets/:id/sensors", sensorListHandler(store))
	rg.GET("/assets/:id/sensors/latest", sensorLatestHandler(store))
}

// sensorListHandler 传感器历史数据查询。
// 支持按 sensor_type、start_time、end_time 筛选。
func sensorListHandler(store *repository.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 解析资产 ID
		assetID, err := strconv.ParseUint(c.Param("id"), 10, 64)
		if err != nil {
			fail(c, 400, "invalid asset id")
			return
		}

		// 验证资产归属当前项目
		var asset model.Asset
		if store.DB.Where("project_id = ? AND id = ?", pid(c), uint(assetID)).First(&asset).Error != nil {
			fail(c, 404, "asset not found")
			return
		}

		// 解析查询参数
		sensorType := c.Query("sensor_type")
		var startTime, endTime *time.Time

		if st := c.Query("start_time"); st != "" {
			t, err := time.Parse(time.RFC3339, st)
			if err != nil {
				// 尝试 Unix 时间戳格式
				if ts, tsErr := strconv.ParseInt(st, 10, 64); tsErr == nil {
					parsed := time.Unix(ts, 0)
					startTime = &parsed
				} else {
					fail(c, 400, "invalid start_time format, use RFC3339 or Unix timestamp")
					return
				}
			} else {
				startTime = &t
			}
		}

		if et := c.Query("end_time"); et != "" {
			t, err := time.Parse(time.RFC3339, et)
			if err != nil {
				if ts, tsErr := strconv.ParseInt(et, 10, 64); tsErr == nil {
					parsed := time.Unix(ts, 0)
					endTime = &parsed
				} else {
					fail(c, 400, "invalid end_time format, use RFC3339 or Unix timestamp")
					return
				}
			} else {
				endTime = &t
			}
		}

		// 查询传感器记录
		records, err := store.ListSensorRecords(asset.ID, sensorType, startTime, endTime)
		if err != nil {
			fail(c, 500, "query sensor records failed")
			return
		}

		OK(c, records)
	}
}

// sensorLatestHandler 获取指定资产的最新传感器记录（每种类型各一条）。
func sensorLatestHandler(store *repository.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 解析资产 ID
		assetID, err := strconv.ParseUint(c.Param("id"), 10, 64)
		if err != nil {
			fail(c, 400, "invalid asset id")
			return
		}

		// 验证资产归属当前项目
		var asset model.Asset
		if store.DB.Where("project_id = ? AND id = ?", pid(c), uint(assetID)).First(&asset).Error != nil {
			fail(c, 404, "asset not found")
			return
		}

		// 查询最新传感器记录
		records, err := store.LatestSensorRecordsByAsset(asset.ID)
		if err != nil {
			fail(c, 500, "query latest sensor records failed")
			return
		}

		// 按传感器类型组织数据
		result := make(map[string]interface{})
		for _, r := range records {
			result[r.SensorType] = map[string]interface{}{
				"value":        r.Value,
				"unit":         r.Unit,
				"collected_at": r.CollectedAt,
			}
		}

		OK(c, result)
	}
}
