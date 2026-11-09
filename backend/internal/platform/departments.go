package platform

import (
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ============================================================
// 部门管理处理器（自用模式）
// ============================================================
//
// 待注册路由（routes.go）：
// GET    /api/v1/projects/:projectId/departments            → listDepartments      → departments:read
// POST   /api/v1/projects/:projectId/departments            → createDepartment     → departments:write
// PUT    /api/v1/projects/:projectId/departments/:id        → updateDepartment     → departments:write
// DELETE /api/v1/projects/:projectId/departments/:id        → deleteDepartment     → departments:write
// GET    /api/v1/projects/:projectId/departments/:id/stats  → getDepartmentStats   → departments:read

// listDepartments 返回当前租户的部门树形列表
func (a *App) listDepartments(c *gin.Context) {
	sc := contextScope(c)
	var rows []map[string]any
	e := a.store.db.Table("departments").
		Select("id, name, parent_id, description, created_at, updated_at").
		Where("tenant_id=? AND deleted_at IS NULL", sc.Tenant).
		Order("parent_id, name").
		Find(&rows).Error
	if e != nil {
		fail(c, 500, "部门查询失败")
		return
	}
	// 构建树形结构
	tree := buildDepartmentTree(rows, 0)
	ok(c, tree)
}

// buildDepartmentTree 递归构建部门树
func buildDepartmentTree(rows []map[string]any, parentID int64) []map[string]any {
	var result []map[string]any
	for _, row := range rows {
		pid := toInt64(row["parent_id"])
		if pid == parentID {
			id := toInt64(row["id"])
			children := buildDepartmentTree(rows, id)
			row["children"] = children
			result = append(result, row)
		}
	}
	return result
}

// toInt64 将任意数值类型安全转换为 int64
func toInt64(v any) int64 {
	if v == nil {
		return 0
	}
	switch x := v.(type) {
	case int64:
		return x
	case int:
		return int64(x)
	case float64:
		return int64(x)
	case int32:
		return int64(x)
	default:
		return 0
	}
}

// createDepartment 创建新部门
func (a *App) createDepartment(c *gin.Context) {
	sc := contextScope(c)
	var in struct {
		Name        string `json:"name"`
		ParentID    *int64 `json:"parent_id"`
		Description string `json:"description"`
	}
	if c.ShouldBindJSON(&in) != nil || strings.TrimSpace(in.Name) == "" {
		fail(c, 400, "部门名称不能为空")
		return
	}
	if len(in.Name) > 128 {
		fail(c, 400, "部门名称过长")
		return
	}
	// 如果指定了上级部门，验证其存在性
	if in.ParentID != nil && *in.ParentID > 0 {
		var count int64
		a.store.db.Table("departments").
			Where("id=? AND tenant_id=? AND deleted_at IS NULL", *in.ParentID, sc.Tenant).
			Count(&count)
		if count == 0 {
			fail(c, 404, "上级部门不存在")
			return
		}
	}
	var id int64
	e := a.store.db.Transaction(func(tx *gorm.DB) error {
		row := map[string]any{
			"tenant_id":   sc.Tenant,
			"name":        strings.TrimSpace(in.Name),
			"description": in.Description,
		}
		if in.ParentID != nil && *in.ParentID > 0 {
			row["parent_id"] = *in.ParentID
		}
		if e := tx.Table("departments").Clauses(clause.Returning{Columns: []clause.Column{{Name: "id"}}}).Create(&row).Error; e != nil {
			return e
		}
		id = asInt(row["id"])
		return a.store.audit(tx, sc, "create", "departments", id)
	})
	if e != nil {
		if strings.Contains(e.Error(), "uq_departments_tenant_name") {
			fail(c, 409, "同租户下部门名称已存在")
			return
		}
		fail(c, 500, "部门创建失败")
		return
	}
	ok(c, gin.H{"id": id})
}

// updateDepartment 更新部门信息
func (a *App) updateDepartment(c *gin.Context) {
	sc := contextScope(c)
	deptID := number(c.Param("id"))
	if deptID <= 0 {
		fail(c, 400, "无效的部门ID")
		return
	}
	// 验证部门存在
	var existing map[string]any
	e := a.store.db.Table("departments").
		Where("id=? AND tenant_id=? AND deleted_at IS NULL", deptID, sc.Tenant).
		Take(&existing).Error
	if e != nil {
		fail(c, 404, "部门不存在")
		return
	}
	var in struct {
		Name        *string `json:"name"`
		ParentID    *int64  `json:"parent_id"`
		Description *string `json:"description"`
	}
	if c.ShouldBindJSON(&in) != nil {
		fail(c, 400, "无效的请求体")
		return
	}
	updates := map[string]any{}
	if in.Name != nil {
		trimmed := strings.TrimSpace(*in.Name)
		if trimmed == "" {
			fail(c, 400, "部门名称不能为空")
			return
		}
		if len(trimmed) > 128 {
			fail(c, 400, "部门名称过长")
			return
		}
		updates["name"] = trimmed
	}
	if in.ParentID != nil {
		if *in.ParentID == deptID {
			fail(c, 400, "不能将部门设为自己的子部门")
			return
		}
		if *in.ParentID > 0 {
			var count int64
			a.store.db.Table("departments").
				Where("id=? AND tenant_id=? AND deleted_at IS NULL", *in.ParentID, sc.Tenant).
				Count(&count)
			if count == 0 {
				fail(c, 404, "上级部门不存在")
				return
			}
		}
		if *in.ParentID > 0 {
			updates["parent_id"] = *in.ParentID
		} else {
			updates["parent_id"] = nil
		}
	}
	if in.Description != nil {
		updates["description"] = *in.Description
	}
	if len(updates) == 0 {
		fail(c, 400, "无更新内容")
		return
	}
	e = a.store.db.Transaction(func(tx *gorm.DB) error {
		res := tx.Table("departments").
			Where("id=? AND tenant_id=? AND deleted_at IS NULL", deptID, sc.Tenant).
			Updates(updates)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		return a.store.audit(tx, sc, "update", "departments", deptID)
	})
	if e != nil {
		if strings.Contains(e.Error(), "uq_departments_tenant_name") {
			fail(c, 409, "同租户下部门名称已存在")
			return
		}
		fail(c, 500, "部门更新失败")
		return
	}
	ok(c, nil)
}

// deleteDepartment 软删除部门（需无子部门且无关联资产）
func (a *App) deleteDepartment(c *gin.Context) {
	sc := contextScope(c)
	deptID := number(c.Param("id"))
	if deptID <= 0 {
		fail(c, 400, "无效的部门ID")
		return
	}
	// 检查是否存在子部门
	var childCount int64
	a.store.db.Table("departments").
		Where("parent_id=? AND deleted_at IS NULL", deptID).
		Count(&childCount)
	if childCount > 0 {
		fail(c, 409, "请先删除或迁移子部门")
		return
	}
	// 检查是否有关联资产
	var assetCount int64
	a.store.db.Table("assets").
		Where("department_id=? AND deleted_at IS NULL", deptID).
		Count(&assetCount)
	if assetCount > 0 {
		fail(c, 409, "请先移除该部门下的资产分配")
		return
	}
	e := a.store.db.Transaction(func(tx *gorm.DB) error {
		res := tx.Table("departments").
			Where("id=? AND tenant_id=? AND deleted_at IS NULL", deptID, sc.Tenant).
			Updates(map[string]any{"deleted_at": time.Now()})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		return a.store.audit(tx, sc, "delete", "departments", deptID)
	})
	if e != nil {
		fail(c, 500, "部门删除失败")
		return
	}
	ok(c, nil)
}

// getDepartmentStats 返回部门统计信息（资产数、在线数等）
func (a *App) getDepartmentStats(c *gin.Context) {
	sc := contextScope(c)
	deptID := number(c.Param("id"))
	if deptID <= 0 {
		fail(c, 400, "无效的部门ID")
		return
	}
	// 验证部门存在
	var dept map[string]any
	e := a.store.db.Table("departments").
		Where("id=? AND tenant_id=? AND deleted_at IS NULL", deptID, sc.Tenant).
		Take(&dept).Error
	if e != nil {
		fail(c, 404, "部门不存在")
		return
	}
	// 统计资产总数
	var assetCount int64
	a.store.db.Table("assets").
		Where("department_id=? AND project_id=? AND deleted_at IS NULL", deptID, sc.Project).
		Count(&assetCount)
	// 统计在线资产数（status='online'）
	var onlineCount int64
	a.store.db.Table("assets").
		Where("department_id=? AND project_id=? AND status='online' AND deleted_at IS NULL", deptID, sc.Project).
		Count(&onlineCount)
	// 统计子部门数
	var subDeptCount int64
	a.store.db.Table("departments").
		Where("parent_id=? AND deleted_at IS NULL", deptID).
		Count(&subDeptCount)
	ok(c, gin.H{
		"asset_count":    assetCount,
		"online_count":   onlineCount,
		"offline_count":  assetCount - onlineCount,
		"sub_dept_count": subDeptCount,
	})
}
