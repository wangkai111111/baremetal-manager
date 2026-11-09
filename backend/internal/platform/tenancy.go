package platform

import (
	"errors"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (a *App) tenants(c *gin.Context) {
	u := current(c)
	q := a.store.db.Table("tenants").Where("tenants.deleted_at IS NULL AND tenants.status='active'")
	if !u.IsPlatformAdmin {
		q = q.Where("EXISTS (SELECT 1 FROM tenant_memberships tm WHERE tm.tenant_id=tenants.id AND tm.user_id=? AND tm.status='active' AND tm.deleted_at IS NULL)", u.ID)
	}
	a.identityList(c, q, "tenants")
}
func (a *App) projects(c *gin.Context) {
	u := current(c)
	q := a.store.db.Table("projects").Where("projects.deleted_at IS NULL AND projects.status='active'").Where("EXISTS (SELECT 1 FROM tenants t WHERE t.id=projects.tenant_id AND t.status='active' AND t.deleted_at IS NULL)")
	if tenant := number(c.Query("tenant_id")); tenant > 0 {
		q = q.Where("tenant_id=?", tenant)
	}
	if !u.IsPlatformAdmin {
		q = q.Where("EXISTS (SELECT 1 FROM memberships m JOIN tenant_memberships tm ON tm.tenant_id=m.tenant_id AND tm.user_id=m.user_id AND tm.status='active' AND tm.deleted_at IS NULL JOIN roles r ON r.id=m.role_id AND r.tenant_id=m.tenant_id AND r.deleted_at IS NULL WHERE m.project_id=projects.id AND m.tenant_id=projects.tenant_id AND m.user_id=? AND m.deleted_at IS NULL)", u.ID)
	}
	a.identityList(c, q, "projects")
}
func (a *App) identityList(c *gin.Context, q *gorm.DB, table string) {
	var total int64
	if q.Count(&total).Error != nil {
		fail(c, 500, "query failed")
		return
	}
	p, n := page(c)
	items, e := decodeRows(q.Order("id DESC").Limit(n).Offset((p-1)*n), resource{table: table})
	if e != nil {
		fail(c, 500, "query failed")
		return
	}
	ok(c, gin.H{"items": items, "total": total, "page": p, "page_size": n})
}
func (a *App) createTenant(c *gin.Context) {
	u := current(c)
	if !u.IsPlatformAdmin {
		fail(c, 403, "platform administrator required")
		return
	}
	var in struct {
		Name string `json:"name"`
		Code string `json:"code"`
	}
	if c.ShouldBindJSON(&in) != nil || in.Name == "" || in.Code == "" || len(in.Name) > 255 || len(in.Code) > 64 {
		fail(c, 400, "name and code required")
		return
	}
	var tenant int64
	e := a.store.db.Transaction(func(tx *gorm.DB) error {
		row := map[string]any{"name": in.Name, "code": in.Code, "create_by": u.ID}
		if e := tx.Table("tenants").Clauses(clause.Returning{Columns: []clause.Column{{Name: "id"}}}).Create(&row).Error; e != nil {
			return e
		}
		tenant = asInt(row["id"])
		if e := tx.Table("tenant_memberships").Create(map[string]any{"tenant_id": tenant, "user_id": u.ID}).Error; e != nil {
			return e
		}
		return initializeRoles(tx, tenant, u.ID)
	})
	if e != nil {
		fail(c, 409, "tenant creation failed")
		return
	}
	ok(c, gin.H{"id": tenant})
}
func initializeRoles(tx *gorm.DB, tenant, actor int64) error {
	for _, code := range []string{"manager", "operator", "viewer"} {
		row := map[string]any{"tenant_id": tenant, "name": code, "code": code, "create_by": actor}
		if e := tx.Table("roles").Clauses(clause.Returning{Columns: []clause.Column{{Name: "id"}}}).Create(&row).Error; e != nil {
			return e
		}
		q := tx.Table("permissions").Select("id").Where("deleted_at IS NULL")
		if code == "viewer" {
			q = q.Where("action='read'")
		}
		if code == "operator" {
			q = q.Where("action IN ('read','write','operate','access') AND resource NOT IN ('iam','settings','projects')")
		}
		var ids []int64
		if e := q.Pluck("id", &ids).Error; e != nil {
			return e
		}
		for _, id := range ids {
			if e := tx.Table("role_permissions").Create(map[string]any{"tenant_id": tenant, "role_id": asInt(row["id"]), "permission_id": id, "create_by": actor}).Error; e != nil {
				return e
			}
		}
	}
	return nil
}
func (a *App) createProject(c *gin.Context) {
	u := current(c)
	var in struct {
		TenantID    int64  `json:"tenant_id"`
		Name        string `json:"name"`
		Code        string `json:"code"`
		Description string `json:"description"`
	}
	if c.ShouldBindJSON(&in) != nil || in.TenantID < 1 || in.Name == "" || in.Code == "" || len(in.Name) > 255 || len(in.Code) > 64 {
		fail(c, 400, "tenant_id, name and code required")
		return
	}
	var id int64
	e := a.store.db.Transaction(func(tx *gorm.DB) error {
		var tenant int64
		if e := tx.Raw("SELECT id FROM tenants WHERE id=? AND status='active' AND deleted_at IS NULL FOR UPDATE", in.TenantID).Scan(&tenant).Error; e != nil {
			return e
		}
		if tenant == 0 {
			return errors.New("tenant missing")
		}
		if !u.IsPlatformAdmin {
			var count int64
			tx.Table("tenant_memberships").Where("tenant_id=? AND user_id=? AND status='active' AND deleted_at IS NULL", tenant, u.ID).Count(&count)
			if count == 0 {
				return errors.New("tenant membership required")
			}
			var allowed int64
			if e := tx.Raw("SELECT count(*) FROM memberships m JOIN roles r ON r.id=m.role_id AND r.tenant_id=m.tenant_id AND r.deleted_at IS NULL JOIN role_permissions rp ON rp.role_id=r.id AND rp.tenant_id=r.tenant_id AND rp.deleted_at IS NULL JOIN permissions p ON p.id=rp.permission_id AND p.deleted_at IS NULL WHERE m.tenant_id=? AND m.user_id=? AND m.deleted_at IS NULL AND p.code IN ('projects:write','projects:manage')", tenant, u.ID).Scan(&allowed).Error; e != nil {
				return e
			}
			if allowed == 0 {
				return errors.New("permission denied")
			}
		}
		row := map[string]any{"tenant_id": tenant, "name": in.Name, "code": in.Code, "description": in.Description, "owner_id": u.ID, "create_by": u.ID}
		if e := tx.Table("projects").Clauses(clause.Returning{Columns: []clause.Column{{Name: "id"}}}).Create(&row).Error; e != nil {
			return e
		}
		id = asInt(row["id"])
		var role int64
		if e := tx.Table("roles").Select("id").Where("tenant_id=? AND code='manager' AND deleted_at IS NULL", tenant).Scan(&role).Error; e != nil {
			return e
		}
		if role == 0 {
			return errors.New("manager role missing")
		}
		if e := tx.Exec("INSERT INTO tenant_memberships(tenant_id,user_id) VALUES (?,?) ON CONFLICT (tenant_id,user_id) DO NOTHING", tenant, u.ID).Error; e != nil {
			return e
		}
		return tx.Table("memberships").Create(map[string]any{"tenant_id": tenant, "project_id": id, "user_id": u.ID, "role_id": role, "create_by": u.ID}).Error
	})
	if e != nil {
		fail(c, 403, "project creation denied or invalid")
		return
	}
	ok(c, gin.H{"id": id, "tenant_id": in.TenantID})
}
func (a *App) switchProject(c *gin.Context) {
	var in struct {
		ProjectID int64 `json:"project_id"`
	}
	if c.ShouldBindJSON(&in) != nil || in.ProjectID < 1 {
		fail(c, 400, "project_id required")
		return
	}
	var p struct{ ID, TenantID int64 }
	e := a.store.db.Table("projects p").Select("p.id,p.tenant_id").Joins("JOIN tenants t ON t.id=p.tenant_id AND t.deleted_at IS NULL AND t.status='active'").Where("p.id=? AND p.deleted_at IS NULL AND p.status='active'", in.ProjectID).Take(&p).Error
	u := current(c)
	sc := scope{p.TenantID, p.ID, u.ID, u.IsPlatformAdmin}
	if e != nil || (!u.IsPlatformAdmin && !a.store.member(sc)) {
		fail(c, 403, "project access denied")
		return
	}
	ok(c, gin.H{"project_id": p.ID, "tenant_id": p.TenantID})
}
