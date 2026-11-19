package platform

import (
	"encoding/json"
	"fmt"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"strings"
)

func nullableID(id int64) any {
	if id > 0 {
		return id
	}
	return nil
}

type auditAudienceEntry struct {
	TenantID       int64  `json:"tenant_id"`
	DepartmentID   *int64 `json:"department_id"`
	DepartmentName string `json:"department_name"`
}

// Capture attribution at event time; never reattribute evidence using a moved
// asset's current department. Unattributed legacy records remain org-admin only.
func auditAudience(tx *gorm.DB, sc scope, action, target string, id any) ([]auditAudienceEntry, error) {
	entries := []auditAudienceEntry{}
	if strings.HasPrefix(action, "auth:") {
		err := tx.Raw(`SELECT g.tenant_id,g.department_id,concat_ws(' / ', t.name, COALESCE(d.name,'组织范围')) AS department_name FROM user_access_grants g JOIN tenants t ON t.id=g.tenant_id JOIN tenant_memberships tm ON tm.tenant_id=g.tenant_id AND tm.user_id=g.user_id AND tm.status='active' AND tm.deleted_at IS NULL LEFT JOIN departments d ON d.id=g.department_id AND d.tenant_id=g.tenant_id WHERE g.user_id=? AND (g.department_id IS NULL OR d.deleted_at IS NULL)`, sc.User).Scan(&entries).Error
		return entries, err
	}
	if target == "operations" || target == "installations" {
		var original string
		if err := tx.Raw(`SELECT COALESCE(after_snapshot->'audience','[]'::jsonb)::text FROM audits WHERE tenant_id=? AND project_id=? AND target_type=? AND target_id=? AND action='submit' ORDER BY id LIMIT 1`, sc.Tenant, sc.Project, target, fmt.Sprint(id)).Scan(&original).Error; err != nil {
			return entries, err
		}
		if original != "" {
			if err := json.Unmarshal([]byte(original), &entries); err != nil {
				return entries, err
			}
			return entries, nil
		}
	}
	if sc.Tenant == 0 {
		return entries, nil
	}
	var dept *int64
	var row struct{ DepartmentID *int64 }
	targetID := number(fmt.Sprint(id))
	var err error
	switch target {
	case "assets":
		err = tx.Table("assets").Select("department_id").Where("tenant_id=? AND project_id=? AND id=?", sc.Tenant, sc.Project, targetID).Scan(&row).Error
	case "departments":
		row.DepartmentID = &targetID
	case "operations", "installations", "asset_monitor_configs", "alerts", "asset_tags", "sol_sessions":
		err = tx.Table(target+" r").Select("a.department_id").Joins("JOIN assets a ON a.id=r.asset_id AND a.tenant_id=r.tenant_id AND a.project_id=r.project_id").Where("r.tenant_id=? AND r.project_id=? AND r.id=?", sc.Tenant, sc.Project, targetID).Scan(&row).Error
	}
	if err != nil {
		return entries, err
	}
	dept = row.DepartmentID
	entry := auditAudienceEntry{TenantID: sc.Tenant, DepartmentID: dept, DepartmentName: "组织范围"}
	if dept != nil {
		if err = tx.Table("departments").Select("name").Where("tenant_id=? AND id=?", sc.Tenant, *dept).Scan(&entry.DepartmentName).Error; err != nil {
			return entries, err
		}
	}
	var tenantName string
	if err = tx.Table("tenants").Select("name").Where("id=?", sc.Tenant).Scan(&tenantName).Error; err != nil {
		return entries, err
	}
	entry.DepartmentName = tenantName + " / " + entry.DepartmentName
	return append(entries, entry), nil
}

// Both audience and current administrator grants are required. Authentication
// events may have multiple audiences, but resource events have only their target.
func auditManagedSQL(sc scope) string {
	if sc.Admin {
		return "TRUE"
	}
	if sc.User <= 0 || sc.Tenant <= 0 {
		return "FALSE"
	}
	return fmt.Sprintf(`EXISTS(SELECT 1 FROM tenant_memberships tm WHERE tm.tenant_id=%d AND tm.user_id=%d AND tm.status='active' AND tm.deleted_at IS NULL) AND (
 EXISTS(SELECT 1 FROM jsonb_array_elements(COALESCE(audits.after_snapshot->'audience','[]'::jsonb)) audience WHERE (audience->>'tenant_id')::bigint=%d AND (%s))
 OR (audits.tenant_id=%d AND NOT (COALESCE(audits.after_snapshot,'{}'::jsonb) ? 'audience') AND EXISTS(SELECT 1 FROM user_access_grants g WHERE g.tenant_id=%d AND g.user_id=%d AND g.is_admin AND g.department_id IS NULL)))`, sc.Tenant, sc.User, sc.Tenant, departmentAccessSQL(sc, "(audience->>'department_id')::bigint", true), sc.Tenant, sc.Tenant, sc.User)
}

func (a *App) auditScope(c *gin.Context) {
	u := current(c)
	if u.IsPlatformAdmin {
		sc := scope{User: u.ID, Admin: true}
		project := number(c.Query("project_id"))
		if project > 0 {
			var p struct{ TenantID int64 }
			if a.store.db.Table("projects").Select("tenant_id").Where("id=? AND deleted_at IS NULL", project).Take(&p).Error != nil {
				fail(c, 404, "项目不存在")
				return
			}
			sc.Project = project
			sc.Tenant = p.TenantID
		}
		// Department links identify their organization even when the feed spans projects.
		if sc.Tenant == 0 && number(c.Query("department_id")) > 0 {
			var d struct{ TenantID int64 }
			if a.store.db.Table("departments").Select("tenant_id").Where("id=? AND deleted_at IS NULL", number(c.Query("department_id"))).Take(&d).Error != nil {
				fail(c, 404, "部门不存在")
				return
			}
			sc.Tenant = d.TenantID
		}
		c.Set("scope", sc)
		c.Next()
		return
	}
	project := number(c.Query("project_id"))
	if project == 0 {
		project = number(c.GetHeader("X-Project-ID"))
	}
	var p struct{ ID, TenantID int64 }
	err := a.store.db.Table("projects p").Select("p.id,p.tenant_id").Joins("JOIN tenants t ON t.id=p.tenant_id AND t.status='active' AND t.deleted_at IS NULL").Where("p.id=? AND p.status='active' AND p.deleted_at IS NULL", project).Take(&p).Error
	if err != nil {
		fail(c, 404, "请先选择可访问的组织 / 项目")
		return
	}
	sc := scope{Tenant: p.TenantID, Project: p.ID, User: u.ID}
	if !a.store.member(sc) {
		fail(c, 403, "project membership required")
		return
	}
	sc.Project = 0
	// The current project identifies the organization. The global audit feed spans
	// that organization's projects; an explicit project filter narrows it.
	if number(c.Query("project_id")) > 0 {
		sc.Project = p.ID
	}
	c.Set("scope", sc)
	c.Next()
}

func (a *App) getAuditLog(c *gin.Context) {
	sc := contextScope(c)
	if !a.store.allowed(sc, "audit:read") {
		fail(c, 403, "permission denied")
		return
	}
	items, err := decodeAuditRows(a.auditQuery(c, sc).Where("audits.id=?", number(c.Param("id"))).Limit(1), sc)
	if err != nil {
		fail(c, 500, "审计查询失败")
		return
	}
	if len(items) == 0 {
		fail(c, 404, "日志不存在或无权访问")
		return
	}
	ok(c, items[0])
}

// Filter human scope labels too: a multi-department login must not reveal names
// of sibling organizations/departments to a scoped administrator.
func scopedAuditDisplaySQL(sc scope) string {
	display := auditDisplaySQL("audits")
	if sc.Admin {
		return display
	}
	return strings.Replace(display, " au), '平台范围')", fmt.Sprintf(" au WHERE (au->>'tenant_id')::bigint=%d AND (%s)), '历史范围未记录')", sc.Tenant, departmentAccessSQL(sc, "(au->>'department_id')::bigint", true)), 1)
}
func decodeAuditRows(q *gorm.DB, sc scope) ([]map[string]any, error) {
	doc := strings.Replace(resourceDocumentSQL(resource{table: "audits"}), auditDisplaySQL("audits"), scopedAuditDisplaySQL(sc), 1)
	var rows []struct{ Doc string }
	err := q.Select(doc).Scan(&rows).Error
	out := make([]map[string]any, 0, len(rows))
	for _, r := range rows {
		var item map[string]any
		if e := json.Unmarshal([]byte(r.Doc), &item); e != nil {
			return nil, e
		}
		out = append(out, item)
	}
	return out, err
}

// One event per changed department avoids exposing another branch's grants in
// the description, and preserves evidence when the subject's grant is revoked.
func (s *store) auditAccessChanges(tx *gorm.DB, sc scope, userID int64, before, after []accessGrant) error {
	old, newGrants := map[int64]bool{}, map[int64]bool{}
	for _, g := range before {
		id := int64(0)
		if g.DepartmentID != nil {
			id = *g.DepartmentID
		}
		old[id] = g.IsAdmin
	}
	for _, g := range after {
		id := int64(0)
		if g.DepartmentID != nil {
			id = *g.DepartmentID
		}
		newGrants[id] = g.IsAdmin
	}
	keys := map[int64]bool{}
	for id := range old {
		keys[id] = true
	}
	for id := range newGrants {
		keys[id] = true
	}
	var tenantName string
	if err := tx.Table("tenants").Select("name").Where("id=?", sc.Tenant).Scan(&tenantName).Error; err != nil {
		return err
	}
	for id := range keys {
		oldAdmin, had := old[id]
		newAdmin, has := newGrants[id]
		if had == has && oldAdmin == newAdmin {
			continue
		}
		entry := auditAudienceEntry{TenantID: sc.Tenant, DepartmentName: tenantName + " / 组织范围"}
		if id > 0 {
			var name string
			if err := tx.Table("departments").Select("name").Where("tenant_id=? AND id=?", sc.Tenant, id).Scan(&name).Error; err != nil {
				return err
			}
			dept := id
			entry.DepartmentID = &dept
			entry.DepartmentName = tenantName + " / " + name
		}
		message := "撤销访问权限"
		if has {
			message = "授予普通运维访问权限"
			if newAdmin {
				message = "授予管理员权限"
			}
		}
		meta := sc.Audit
		meta.Audience = []auditAudienceEntry{entry}
		if err := s.recordAuditNote(tx, sc, meta, "access:manage", "users", userID, "success", map[string]any{"message": entry.DepartmentName + "：" + message}); err != nil {
			return err
		}
	}
	return nil
}
