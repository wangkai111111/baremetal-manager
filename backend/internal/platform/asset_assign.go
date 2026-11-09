package platform

import (
	"fmt"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// ============================================================
// 资产分配至部门处理器（自用模式）
// ============================================================
//
// 待注册路由（routes.go）：
// POST /api/v1/projects/:projectId/assets/:id/assign       → assignAssetToDepartment  → assets:write
// POST /api/v1/projects/:projectId/assets/batch-assign     → batchAssignAssets        → assets:write

// assignAssetToDepartment 将单个资产分配到指定部门
func (a *App) assignAssetToDepartment(c *gin.Context) {
	sc := contextScope(c)
	assetID := number(c.Param("id"))
	if assetID <= 0 {
		fail(c, 400, "无效的资产ID")
		return
	}
	var in struct {
		DepartmentID *int64 `json:"department_id"`
	}
	if c.ShouldBindJSON(&in) != nil {
		fail(c, 400, "无效的请求体")
		return
	}
	// 验证资产存在且属于当前项目
	var assetCount int64
	a.store.db.Table("assets").
		Where("id=? AND project_id=? AND deleted_at IS NULL", assetID, sc.Project).
		Count(&assetCount)
	if assetCount == 0 {
		fail(c, 404, "资产不存在")
		return
	}
	// 验证部门存在（如果指定了部门）
	if in.DepartmentID != nil && *in.DepartmentID > 0 {
		var deptCount int64
		a.store.db.Table("departments").
			Where("id=? AND tenant_id=? AND deleted_at IS NULL", *in.DepartmentID, sc.Tenant).
			Count(&deptCount)
		if deptCount == 0 {
			fail(c, 404, "部门不存在")
			return
		}
	}
	// 更新资产的部门归属
	var updateValue any
	if in.DepartmentID != nil && *in.DepartmentID > 0 {
		updateValue = *in.DepartmentID
	} else {
		updateValue = nil
	}
	e := a.store.db.Transaction(func(tx *gorm.DB) error {
		res := tx.Table("assets").
			Where("id=? AND project_id=? AND deleted_at IS NULL", assetID, sc.Project).
			Update("department_id", updateValue)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		// 记录审计日志
		return tx.Table("audits").Create(map[string]any{
			"tenant_id":   sc.Tenant,
			"project_id":  sc.Project,
			"user_id":     sc.User,
			"request_id":  fmt.Sprintf("%d-%d", time.Now().UnixNano(), sc.User),
			"action":      "assign",
			"target_type": "assets",
			"target_id":   fmt.Sprint(assetID),
			"outcome":     "success",
		}).Error
	})
	if e != nil {
		if e == gorm.ErrRecordNotFound {
			fail(c, 404, "资产不存在")
			return
		}
		fail(c, 500, "资产分配失败")
		return
	}
	ok(c, gin.H{"asset_id": assetID, "department_id": updateValue})
}

// batchAssignAssets 批量将多个资产分配到指定部门
func (a *App) batchAssignAssets(c *gin.Context) {
	sc := contextScope(c)
	var in struct {
		AssetIDs     []int64 `json:"asset_ids"`
		DepartmentID *int64  `json:"department_id"`
	}
	if c.ShouldBindJSON(&in) != nil || len(in.AssetIDs) == 0 {
		fail(c, 400, "asset_ids 不能为空")
		return
	}
	if len(in.AssetIDs) > 500 {
		fail(c, 400, "单次批量操作不能超过500个资产")
		return
	}
	// 验证部门存在（如果指定了部门）
	if in.DepartmentID != nil && *in.DepartmentID > 0 {
		var deptCount int64
		a.store.db.Table("departments").
			Where("id=? AND tenant_id=? AND deleted_at IS NULL", *in.DepartmentID, sc.Tenant).
			Count(&deptCount)
		if deptCount == 0 {
			fail(c, 404, "部门不存在")
			return
		}
	}
	// 验证所有资产存在且属于当前项目
	var validCount int64
	a.store.db.Table("assets").
		Where("id IN ? AND project_id=? AND deleted_at IS NULL", in.AssetIDs, sc.Project).
		Count(&validCount)
	if validCount != int64(len(in.AssetIDs)) {
		fail(c, 404, "部分资产不存在或不属于当前项目")
		return
	}
	// 批量更新
	var updateValue any
	if in.DepartmentID != nil && *in.DepartmentID > 0 {
		updateValue = *in.DepartmentID
	} else {
		updateValue = nil
	}
	e := a.store.db.Transaction(func(tx *gorm.DB) error {
		res := tx.Table("assets").
			Where("id IN ? AND project_id=? AND deleted_at IS NULL", in.AssetIDs, sc.Project).
			Update("department_id", updateValue)
		if res.Error != nil {
			return res.Error
		}
		// 批量记录审计日志
		for _, assetID := range in.AssetIDs {
			if err := tx.Table("audits").Create(map[string]any{
				"tenant_id":   sc.Tenant,
				"project_id":  sc.Project,
				"user_id":     sc.User,
				"request_id":  fmt.Sprintf("%d-%d", time.Now().UnixNano(), sc.User),
				"action":      "batch_assign",
				"target_type": "assets",
				"target_id":   fmt.Sprint(assetID),
				"outcome":     "success",
			}).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if e != nil {
		fail(c, 500, "批量分配失败")
		return
	}
	ok(c, gin.H{
		"updated_count": validCount,
		"department_id": updateValue,
	})
}
