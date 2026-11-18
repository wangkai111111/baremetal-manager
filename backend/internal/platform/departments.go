package platform

import (
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// listDepartments 返回当前租户的部门树形列表
func (a *App) listDepartments(c *gin.Context) {
	sc := contextScope(c)
	var rows []map[string]any
	e := a.store.db.Table("departments").
		Select("id, name, parent_id, description, created_at, updated_at").
		Where("tenant_id=? AND deleted_at IS NULL", sc.Tenant).
		Where(departmentTreeSQL(sc)).
		Order("parent_id, name").
		Find(&rows).Error
	if e != nil {
		fail(c, 500, "部门查询失败")
		return
	}
	// 构建树形结构
	for _, row := range rows {
		row["can_manage"] = a.store.managesDepartment(sc, toInt64(row["id"]))
	}
	tree := buildDepartmentTree(rows, 0)
	ok(c, tree)
}

// buildDepartmentTree 递归构建部门树
func buildDepartmentTree(rows []map[string]any, parentID int64) []map[string]any {
	result := make([]map[string]any, 0)
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
	var id int64
	e := a.store.db.WithContext(c.Request.Context()).Transaction(func(tx *gorm.DB) error {
		if err := lockDepartments(tx, sc); err != nil {
			return err
		}
		parent := int64(0)
		if in.ParentID != nil {
			parent = *in.ParentID
		}
		if !(&store{tx}).managesDepartment(sc, parent) {
			return errAccessDenied
		}
		if in.ParentID != nil {
			if *in.ParentID < 0 {
				return errDepartmentMissing
			}
			if err := validateDepartment(tx, sc, *in.ParentID); err != nil {
				return err
			}
		}
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
		if errors.Is(e, errAccessDenied) {
			fail(c, 403, e.Error())
			return
		}
		if errors.Is(e, errDepartmentMissing) {
			fail(c, 404, "上级部门不存在或已删除")
			return
		}
		if errors.Is(e, gorm.ErrRecordNotFound) {
			fail(c, 404, "部门不存在")
			return
		}
		if errors.Is(e, errDepartmentCycle) {
			fail(c, 409, "不能将部门移动到自己的下级部门")
			return
		}
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
		Name        *string         `json:"name"`
		ParentID    json.RawMessage `json:"parent_id"`
		Description *string         `json:"description"`
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
	var parentID int64
	if len(in.ParentID) > 0 {
		var err error
		parentID, err = departmentTarget(in.ParentID)
		if err != nil {
			fail(c, 400, err.Error())
			return
		}
		if parentID == deptID {
			fail(c, 400, "不能将部门设为自己的子部门")
			return
		}
		if parentID > 0 {
			updates["parent_id"] = parentID
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
	e = a.store.db.WithContext(c.Request.Context()).Transaction(func(tx *gorm.DB) error {
		// Serialize hierarchy changes within a tenant, including reciprocal moves.
		if err := lockDepartments(tx, sc); err != nil {
			return err
		}
		if !(&store{tx}).managesDepartment(sc, deptID) {
			return errAccessDenied
		}
		// Moving a tree changes inherited grants. Only organization admins can
		// change hierarchy; department admins can edit labels/create children.
		if len(in.ParentID) > 0 && parentID != toInt64(existing["parent_id"]) && !(&store{tx}).managesDepartment(sc, 0) {
			return errAccessDenied
		}
		if len(in.ParentID) > 0 && parentID > 0 {
			if err := validateDepartment(tx, sc, parentID); err != nil {
				return err
			}
			var cycle bool
			if err := tx.Raw(`WITH RECURSIVE ancestors AS (
            SELECT id,parent_id FROM departments WHERE id=? AND tenant_id=? AND deleted_at IS NULL
            UNION SELECT d.id,d.parent_id FROM departments d JOIN ancestors p ON d.id=p.parent_id WHERE d.tenant_id=? AND d.deleted_at IS NULL
          ) SELECT EXISTS(SELECT 1 FROM ancestors WHERE id=?)`, parentID, sc.Tenant, sc.Tenant, deptID).Scan(&cycle).Error; err != nil {
				return err
			}
			if cycle {
				return errDepartmentCycle
			}
		}
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
		if errors.Is(e, errAccessDenied) {
			fail(c, 403, e.Error())
			return
		}
		if errors.Is(e, errDepartmentMissing) {
			fail(c, 404, "上级部门不存在或已删除")
			return
		}
		if errors.Is(e, gorm.ErrRecordNotFound) {
			fail(c, 404, "部门不存在")
			return
		}
		if errors.Is(e, errDepartmentCycle) {
			fail(c, 409, "不能将部门移动到自己的下级部门")
			return
		}
		if strings.Contains(e.Error(), "uq_departments_tenant_name") {
			fail(c, 409, "同租户下部门名称已存在")
			return
		}
		fail(c, 500, "部门更新失败")
		return
	}
	ok(c, nil)
}

// deleteDepartment protects references across every project in the organization.
func (a *App) deleteDepartment(c *gin.Context) {
	sc := contextScope(c)
	id := number(c.Param("id"))
	if id <= 0 {
		fail(c, 400, "无效的部门ID")
		return
	}
	err := a.store.db.WithContext(c.Request.Context()).Transaction(func(tx *gorm.DB) error {
		if err := lockDepartments(tx, sc); err != nil {
			return err
		}
		if !(&store{tx}).managesDepartment(sc, id) {
			return errAccessDenied
		}
		var grants int64
		if err := tx.Table("user_access_grants").Where("tenant_id=? AND department_id=?", sc.Tenant, id).Count(&grants).Error; err != nil {
			return err
		}
		if grants > 0 {
			return errors.New("该部门仍有用户授权，请先撤销授权")
		}
		if err := validateDepartment(tx, sc, id); err != nil {
			return err
		}
		var count int64
		if err := tx.Table("departments").Where("tenant_id=? AND parent_id=? AND deleted_at IS NULL", sc.Tenant, id).Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			return errDepartmentChildren
		}
		if err := tx.Table("assets").Where("tenant_id=? AND department_id=? AND deleted_at IS NULL", sc.Tenant, id).Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			return errDepartmentAssets
		}
		res := tx.Table("departments").Where("id=? AND tenant_id=? AND deleted_at IS NULL", id, sc.Tenant).Update("deleted_at", time.Now())
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected != 1 {
			return gorm.ErrRecordNotFound
		}
		return a.store.audit(tx, sc, "delete", "departments", id)
	})
	if errors.Is(err, errDepartmentMissing) || errors.Is(err, gorm.ErrRecordNotFound) {
		fail(c, 404, "部门不存在")
		return
	}
	if errors.Is(err, errDepartmentChildren) || errors.Is(err, errDepartmentAssets) {
		fail(c, 409, err.Error())
		return
	}
	if err != nil {
		fail(c, 409, "部门删除失败："+err.Error())
		return
	}
	ok(c, nil)
}

// Asset counts concern the current project; hierarchy belongs to the organization.
// Online means the last known power state is on, matching the server views.
func (a *App) getDepartmentStats(c *gin.Context) {
	sc := contextScope(c)
	id := number(c.Param("id"))
	if id <= 0 {
		fail(c, 400, "无效的部门ID")
		return
	}
	db := a.store.db.WithContext(c.Request.Context())
	if err := validateDepartment(db, sc, id); err != nil {
		if errors.Is(err, errDepartmentMissing) {
			fail(c, 404, err.Error())
		} else {
			fail(c, 500, "部门查询失败")
		}
		return
	}
	var counts struct {
		AssetCount   int64
		OnlineCount  int64
		SubDeptCount int64
	}
	err := queryScope(db, resource{table: "assets"}, sc).Where("department_id=?", id).
		Select("count(*) AS asset_count,count(*) FILTER(WHERE power_state='on') AS online_count").Scan(&counts).Error
	if err == nil {
		err = db.Table("departments").Where("tenant_id=? AND parent_id=? AND deleted_at IS NULL", sc.Tenant, id).Where(departmentAccessSQL(sc, "departments.id", false)).Count(&counts.SubDeptCount).Error
	}

	if err != nil {
		fail(c, 500, "部门统计失败")
		return
	}
	ok(c, gin.H{"asset_count": counts.AssetCount, "online_count": counts.OnlineCount, "offline_count": counts.AssetCount - counts.OnlineCount, "sub_dept_count": counts.SubDeptCount})
}
