package platform

import (
	"errors"
	"fmt"
	"strings"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// SQL fragments only interpolate typed IDs and internal, fixed column names.
// UNION (not UNION ALL) also makes legacy cyclic department trees terminate.
func departmentAccessSQL(sc scope, column string, management bool) string {
	if sc.Admin || sc.User == 0 {
		return "TRUE"
	}
	admin := ""
	if management {
		admin = " AND g.is_admin"
	}
	return fmt.Sprintf(`(EXISTS(SELECT 1 FROM user_access_grants g WHERE g.tenant_id=%d AND g.user_id=%d AND g.department_id IS NULL%s)
 OR %s IN (WITH RECURSIVE permitted(id) AS (
 SELECT g.department_id FROM user_access_grants g JOIN departments d ON d.id=g.department_id AND d.tenant_id=g.tenant_id AND d.deleted_at IS NULL
 WHERE g.tenant_id=%d AND g.user_id=%d%s
 UNION SELECT d.id FROM departments d JOIN permitted p ON d.parent_id=p.id WHERE d.tenant_id=%d AND d.deleted_at IS NULL
 ) SELECT id FROM permitted))`, sc.Tenant, sc.User, admin, column, sc.Tenant, sc.User, admin, sc.Tenant)
}
func assetAccessSQL(sc scope, assetColumn string, management bool) string {
	return fmt.Sprintf(`EXISTS (SELECT 1 FROM assets access_asset WHERE access_asset.id=%s AND access_asset.tenant_id=%d AND access_asset.project_id=%d AND access_asset.deleted_at IS NULL AND (%s))`, assetColumn, sc.Tenant, sc.Project, departmentAccessSQL(sc, "access_asset.department_id", management))
}
func (s *store) hasAccess(sc scope, admins bool) bool {
	if sc.Admin {
		return true
	}
	var n int64
	q := s.db.Table("user_access_grants g").Joins("JOIN tenant_memberships tm ON tm.tenant_id=g.tenant_id AND tm.user_id=g.user_id AND tm.status='active' AND tm.deleted_at IS NULL").Where("g.tenant_id=? AND g.user_id=?", sc.Tenant, sc.User).
		Where("g.department_id IS NULL OR EXISTS(SELECT 1 FROM departments d WHERE d.id=g.department_id AND d.tenant_id=g.tenant_id AND d.deleted_at IS NULL)")
	if admins {
		q = q.Where("g.is_admin")
	}
	return q.Count(&n).Error == nil && n > 0
}
func (s *store) managesDepartment(sc scope, id int64) bool {
	if sc.Admin {
		return true
	}
	var yes bool
	return s.db.Raw("SELECT "+departmentAccessSQL(sc, fmt.Sprint(id), true)).Scan(&yes).Error == nil && yes
}
func (s *store) managesAsset(sc scope, id int64) bool {
	var n int64
	return queryScope(s.db, resource{table: "assets"}, sc).Where("assets.id=?", id).
		Where(departmentAccessSQL(sc, "assets.department_id", true)).Count(&n).Error == nil && n == 1
}

var scopedUserPermissions = strings.Fields(`assets:read assets:operate bmc:operate sol:read sol:access tasks:read monitor:read alerts:read departments:read datacenters:read tags:read images:read install:read install:operate network:read firmware:read work_orders:read work_orders:write`)
var scopedAdminPermissions = strings.Fields(`assets:write departments:write monitor:write iam:read iam:manage`)

func containsCode(codes []string, code string) bool {
	for _, s := range codes {
		if s == code {
			return true
		}
	}
	return false
}

// Project membership is a navigation/operation role, never a grant to all assets.
// Hard caps ensure old manager/operator roles cannot bypass the new data policy.
func (s *store) accessPermission(sc scope, code string) bool {
	if sc.Admin {
		return true
	}
	if containsCode(scopedAdminPermissions, code) {
		return s.hasAccess(sc, true)
	}
	if !containsCode(scopedUserPermissions, code) || !s.hasAccess(sc, false) {
		return false
	}
	roles, codes, err := s.projectRolePermissions(sc)
	return err == nil && (roles == 0 || containsCode(codes, code))
}

// All asset-related generic resources, including indirect log/event tables, use
// this filter for lists, details, export, reference validation and mutation.
func filterAssetResource(q *gorm.DB, table string, sc scope) *gorm.DB {
	if sc.Admin || sc.User == 0 {
		return q
	}
	switch table {
	case "assets":
		return q.Where(departmentAccessSQL(sc, "assets.department_id", false))
	case "asset_tags", "asset_hardwares", "firmware_inventories", "asset_monitor_configs", "sensor_records", "operations", "installations", "firmware_upgrades", "alerts", "sol_sessions", "maintenance_window_assets", "alert_shields":
		return q.Where(assetAccessSQL(sc, table+".asset_id", false))
	case "operation_logs":
		return q.Where(`EXISTS(SELECT 1 FROM operations access_op WHERE access_op.id=operation_logs.operation_id AND access_op.tenant_id=operation_logs.tenant_id AND access_op.project_id=operation_logs.project_id AND access_op.deleted_at IS NULL AND (` + assetAccessSQL(sc, "access_op.asset_id", false) + `))`)
	case "installation_events":
		return q.Where(`EXISTS(SELECT 1 FROM installations access_inst WHERE access_inst.id=installation_events.installation_id AND access_inst.tenant_id=installation_events.tenant_id AND access_inst.project_id=installation_events.project_id AND access_inst.deleted_at IS NULL AND (` + assetAccessSQL(sc, "access_inst.asset_id", false) + `))`)
	case "alert_events":
		return q.Where(`EXISTS(SELECT 1 FROM alerts access_alert WHERE access_alert.id=alert_events.alert_id AND access_alert.tenant_id=alert_events.tenant_id AND access_alert.project_id=alert_events.project_id AND access_alert.deleted_at IS NULL AND (` + assetAccessSQL(sc, "access_alert.asset_id", false) + `))`)
	case "notification_deliveries", "audits", "discovery_candidates", "maintenance_windows":
		return q.Where("FALSE")
	}
	return q
}

type accessGrant struct {
	DepartmentID *int64 `json:"department_id"`
	IsAdmin      bool   `json:"is_admin"`
}

func (a *App) userAccessGrants(c *gin.Context) {
	sc := contextScope(c)
	if !sc.Admin && !a.store.hasAccess(sc, true) {
		fail(c, 403, "仅管理员可以管理授权")
		return
	}
	id := number(c.Param("id"))
	if id < 1 {
		fail(c, 400, "无效用户")
		return
	}
	if c.Request.Method == "GET" {
		q := a.store.db.Table("user_access_grants").Select("department_id,is_admin,created_at,(SELECT d.name FROM departments d WHERE d.id=user_access_grants.department_id AND d.tenant_id=user_access_grants.tenant_id AND d.deleted_at IS NULL) AS department_name").Where("tenant_id=? AND user_id=?", sc.Tenant, id)
		if !sc.Admin {
			q = q.Where(departmentAccessSQL(sc, "user_access_grants.department_id", true))
		}
		items := []map[string]any{}
		if q.Order("id").Find(&items).Error != nil {
			fail(c, 500, "授权查询失败")
			return
		}
		ok(c, gin.H{"items": items, "total": len(items)})
		return
	}
	var in struct {
		Grants []accessGrant `json:"grants"`
	}
	if c.ShouldBindJSON(&in) != nil || in.Grants == nil || len(in.Grants) > 200 {
		fail(c, 400, "请提供完整授权列表（最多200项）")
		return
	}
	if id == sc.User && !sc.Admin {
		fail(c, 403, "不能修改自己的授权")
		return
	}
	err := a.store.db.WithContext(c.Request.Context()).Transaction(func(tx *gorm.DB) error {
		if err := lockDepartments(tx, sc); err != nil {
			return err
		}
		var u struct {
			ID              int64
			IsPlatformAdmin bool
		}
		if err := tx.Table("users").Where("id=? AND deleted_at IS NULL AND status='active'", id).Take(&u).Error; err != nil {
			return err
		}
		if u.IsPlatformAdmin {
			return errors.New("平台管理员无需配置数据授权")
		}
		st := &store{tx}
		if !sc.Admin {
			var member int64
			if err := tx.Table("tenant_memberships").Where("tenant_id=? AND user_id=? AND status='active' AND deleted_at IS NULL", sc.Tenant, id).Count(&member).Error; err != nil {
				return err
			}
			if member != 1 {
				return errors.New("仅可授权本组织已有用户，请由平台管理员先添加组织访问权限")
			}
		}
		if !sc.Admin && !st.hasAccess(sc, true) {
			return errors.New("管理授权已撤销")
		}
		seen := map[int64]bool{}
		for _, g := range in.Grants {
			dept := int64(0)
			if g.DepartmentID != nil {
				dept = *g.DepartmentID
				if dept <= 0 {
					return errors.New("无效部门")
				}
			}
			if seen[dept] {
				return errors.New("授权范围重复")
			}
			seen[dept] = true
			if err := validateDepartment(tx, sc, dept); err != nil {
				return err
			}
			if !st.managesDepartment(sc, dept) {
				return errors.New("不能授予超出自己管理范围的权限")
			}
		}
		q := tx.Table("user_access_grants").Where("tenant_id=? AND user_id=?", sc.Tenant, id)
		if !sc.Admin {
			q = q.Where(departmentAccessSQL(sc, "user_access_grants.department_id", true))
		}
		if err := q.Delete(&accessGrant{}).Error; err != nil {
			return err
		}
		for _, g := range in.Grants {
			if err := tx.Table("user_access_grants").Create(map[string]any{"tenant_id": sc.Tenant, "user_id": id, "department_id": g.DepartmentID, "is_admin": g.IsAdmin, "create_by": sc.User}).Error; err != nil {
				return err
			}
		}
		// Grants automatically enable organization/project selection; no invisible
		// second project-role setup step is required. Revocation takes effect live.
		if len(in.Grants) > 0 {
			if err := tx.Exec(`INSERT INTO tenant_memberships(tenant_id,user_id,status,create_by) VALUES (?,?,'active',?) ON CONFLICT(tenant_id,user_id) DO UPDATE SET deleted_at=NULL,status='active'`, sc.Tenant, id, sc.User).Error; err != nil {
				return err
			}
		}
		return st.audit(tx, sc, "access:manage", "users", id)
	})
	if err != nil {
		fail(c, 409, "保存授权失败："+err.Error())
		return
	}
	ok(c, nil)
}

func projectAccessSQL(alias string, userID int64) string {
	return fmt.Sprintf(`(EXISTS(SELECT 1 FROM user_access_grants g JOIN tenant_memberships tm ON tm.tenant_id=g.tenant_id AND tm.user_id=g.user_id AND tm.status='active' AND tm.deleted_at IS NULL WHERE g.tenant_id=%s.tenant_id AND g.user_id=%d AND (g.department_id IS NULL OR EXISTS(SELECT 1 FROM departments d WHERE d.id=g.department_id AND d.tenant_id=g.tenant_id AND d.deleted_at IS NULL))) OR EXISTS(SELECT 1 FROM memberships m JOIN tenant_memberships tm ON tm.tenant_id=m.tenant_id AND tm.user_id=m.user_id AND tm.status='active' AND tm.deleted_at IS NULL JOIN roles r ON r.id=m.role_id AND r.tenant_id=m.tenant_id AND r.deleted_at IS NULL WHERE m.project_id=%s.id AND m.tenant_id=%s.tenant_id AND m.user_id=%d AND m.deleted_at IS NULL))`, alias, userID, alias, alias, userID)
}

var errAccessDenied = errors.New("无权管理该组织/部门范围")

// Include ancestor labels so paths remain understandable, but never include
// sibling branches. Actual statistics/asset queries remain independently scoped.
func departmentTreeSQL(sc scope) string {
	if sc.Admin || sc.User == 0 {
		return "TRUE"
	}
	return fmt.Sprintf(`departments.id IN (WITH RECURSIVE visible(id,parent_id) AS (
 SELECT d.id,d.parent_id FROM departments d WHERE d.tenant_id=%d AND d.deleted_at IS NULL AND (%s)
 UNION SELECT d.id,d.parent_id FROM departments d JOIN visible v ON v.parent_id=d.id WHERE d.tenant_id=%d AND d.deleted_at IS NULL
 ) SELECT id FROM visible)`, sc.Tenant, departmentAccessSQL(sc, "d.id", false), sc.Tenant)
}

func (s *store) projectRolePermissions(sc scope) (int64, []string, error) {
	var roles int64
	q := s.db.Table("memberships m").Joins("JOIN roles r ON r.id=m.role_id AND r.tenant_id=m.tenant_id AND r.deleted_at IS NULL").Where("m.tenant_id=? AND m.project_id=? AND m.user_id=? AND m.deleted_at IS NULL", sc.Tenant, sc.Project, sc.User)
	if err := q.Count(&roles).Error; err != nil {
		return 0, nil, err
	}
	codes := []string{}
	if roles == 0 {
		return roles, codes, nil
	}
	err := q.Joins("JOIN role_permissions rp ON rp.role_id=r.id AND rp.tenant_id=r.tenant_id AND rp.deleted_at IS NULL").Joins("JOIN permissions p ON p.id=rp.permission_id AND p.deleted_at IS NULL").Distinct().Pluck("p.code", &codes).Error
	return roles, codes, err
}
