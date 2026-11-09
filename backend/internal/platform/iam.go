package platform

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type iamAuthenticatedKey struct{ ID, TenantID, ProjectID, UserID int64 }

// Key requests are restricted to audited project business routes. The permission
// gate runs before handlers, including handlers that otherwise trust platform admins.
func (a *App) iamKeyGuard(c *gin.Context, secret string) {
	if len(secret) != 68 || !strings.HasPrefix(secret, "bmm_") {
		fail(c, 401, "invalid API key")
		return
	}
	sum := sha256.Sum256([]byte(secret))
	var key iamAuthenticatedKey
	if e := a.store.db.Table("api_keys").Select("id,tenant_id,project_id,user_id").Where("secret_hash=? AND expires_at>now() AND revoked_at IS NULL AND deleted_at IS NULL", hex.EncodeToString(sum[:])).Limit(1).Find(&key).Error; e != nil || key.ID == 0 {
		fail(c, 401, "invalid API key")
		return
	}
	u, e := a.store.activeUser(key.UserID)
	if e != nil {
		fail(c, 401, "inactive API key owner")
		return
	}
	sc := scope{Tenant: key.TenantID, Project: key.ProjectID, User: u.ID, Admin: u.IsPlatformAdmin}
	var active int64
	e = a.store.db.Table("projects p").Joins("JOIN tenants t ON t.id=p.tenant_id AND t.status='active' AND t.deleted_at IS NULL").Where("p.id=? AND p.tenant_id=? AND p.status='active' AND p.deleted_at IS NULL", key.ProjectID, key.TenantID).Count(&active).Error
	if e != nil || active != 1 || !a.store.member(sc) {
		fail(c, 401, "inactive API key membership")
		return
	}
	parts := strings.Split(strings.Trim(c.FullPath(), "/"), "/")
	if len(parts) < 5 || parts[0] != "api" || parts[1] != "v1" || parts[2] != "projects" || parts[3] != ":projectId" {
		fail(c, 403, "API keys require project business routes")
		return
	}
	if number(c.Param("projectId")) != key.ProjectID {
		fail(c, 403, "API key project mismatch")
		return
	}
	resourceName := parts[4]
	code := ""
	for _, r := range resources {
		if r.path == resourceName {
			action := "read"
			if c.Request.Method != "GET" {
				action = "write"
			}
			code = permissionResource(r.table) + ":" + action
			break
		}
	}
	if resourceName == "assets" && len(parts) == 7 {
		switch parts[6] {
		case "collect":
			code = "assets:operate"
		case "actions":
			body, err := io.ReadAll(io.LimitReader(c.Request.Body, 1<<20))
			if err != nil {
				fail(c, 400, "invalid action")
				return
			}
			c.Request.Body = io.NopCloser(bytes.NewReader(body))
			var in struct{ Action string }
			if json.Unmarshal(body, &in) != nil {
				fail(c, 400, "invalid action")
				return
			}
			code = "bmc:operate"
			if in.Action == "collect" || in.Action == "inspect" {
				code = "assets:operate"
			}
		case "install":
			code = "install:operate"
		case "firmware-upgrade":
			code = "firmware:operate"
		default:
			code = ""
		}
	}
	if resourceName == "sol-sessions" {
		code = ""
	}
	if code == "" {
		fail(c, 403, "API key route is not supported")
		return
	}
	var granted int64
	e = a.store.db.Table("api_key_permissions kp").Joins("JOIN permissions p ON p.id=kp.permission_id AND p.deleted_at IS NULL").Where("kp.tenant_id=? AND kp.project_id=? AND kp.api_key_id=? AND kp.deleted_at IS NULL AND p.code=?", key.TenantID, key.ProjectID, key.ID, code).Count(&granted).Error
	if e != nil || granted == 0 || !a.store.allowed(sc, code) {
		fail(c, 403, "API key permission denied")
		return
	}
	if e := a.store.db.Table("api_keys").Where("id=? AND tenant_id=? AND project_id=? AND revoked_at IS NULL AND deleted_at IS NULL AND expires_at>now()", key.ID, key.TenantID, key.ProjectID).Update("last_used_at", time.Now()); e.Error != nil || e.RowsAffected != 1 {
		fail(c, 401, "API key no longer active")
		return
	}
	c.Set("api_key", key)
	c.Set("user", u)
	c.Next()
}

// Global compatibility endpoints require an explicit scope when more than one project is accessible.
func (a *App) iamScope(c *gin.Context) {
	if _, exists := c.Get("scope"); exists {
		c.Next()
		return
	}
	id := number(c.Query("project_id"))
	if id == 0 {
		id = number(c.GetHeader("X-Project-ID"))
	}
	u := current(c)
	q := a.store.db.Table("projects p").Select("p.id,p.tenant_id").Joins("JOIN tenants t ON t.id=p.tenant_id AND t.status='active' AND t.deleted_at IS NULL").Where("p.status='active' AND p.deleted_at IS NULL")
	if id > 0 {
		q = q.Where("p.id=?", id)
	}
	if !u.IsPlatformAdmin {
		q = q.Where("EXISTS (SELECT 1 FROM memberships m JOIN tenant_memberships tm ON tm.tenant_id=m.tenant_id AND tm.user_id=m.user_id AND tm.status='active' AND tm.deleted_at IS NULL JOIN roles r ON r.id=m.role_id AND r.tenant_id=m.tenant_id AND r.deleted_at IS NULL WHERE m.project_id=p.id AND m.tenant_id=p.tenant_id AND m.user_id=? AND m.deleted_at IS NULL)", u.ID)
	}
	var projects []struct{ ID, TenantID int64 }
	if q.Limit(2).Find(&projects).Error != nil {
		fail(c, 500, "scope query failed")
		return
	}
	if len(projects) != 1 {
		fail(c, 400, "provide project_id or X-Project-ID for an accessible project")
		return
	}
	c.Set("scope", scope{projects[0].TenantID, projects[0].ID, u.ID, u.IsPlatformAdmin})
	c.Next()
}

func (a *App) iamRoutes(auth, p *gin.RouterGroup) {
	auth.GET("/users", a.iamUsers)
	auth.POST("/auth/users", a.iamAdmin, a.iamScope, a.iamWriteUser)
	auth.POST("/users", a.iamAdmin, a.iamScope, a.iamWriteUser)
	auth.PUT("/users/:id", a.iamAdmin, a.iamScope, a.iamWriteUser)
	auth.DELETE("/users/:id", a.iamAdmin, a.iamScope, a.iamWriteUser)
	for _, group := range []*gin.RouterGroup{auth.Group("", a.iamScope), p} {
		group.GET("/roles", a.require("iam:read"), a.iamRoles)
		group.POST("/roles", a.require("iam:manage"), a.iamWriteRole)
		group.PUT("/roles/:id", a.require("iam:manage"), a.iamWriteRole)
		group.DELETE("/roles/:id", a.require("iam:manage"), a.iamWriteRole)
		group.GET("/api-keys", a.require("iam:read"), a.iamKeys)
		group.POST("/api-keys", a.require("iam:write"), a.iamCreateKey)
		group.DELETE("/api-keys/:id", a.require("iam:write"), a.iamRevokeKey)
	}
	p.GET("/users", a.require("iam:read"), a.iamUsers)
	p.GET("/members", a.require("iam:read"), a.iamMembers)
	p.POST("/members", a.require("iam:manage"), a.iamWriteMember)
	p.PUT("/members/:id", a.require("iam:manage"), a.iamWriteMember)
	p.DELETE("/members/:id", a.require("iam:manage"), a.iamWriteMember)
}
func (a *App) iamAdmin(c *gin.Context) {
	if !current(c).IsPlatformAdmin {
		fail(c, 403, "platform administrator required")
		return
	}
	c.Next()
}
func (a *App) iamUsers(c *gin.Context) {
	q := a.store.db.Table("users").Select("users.id,username,name,email,status,is_platform_admin,created_at,updated_at,CASE WHEN is_platform_admin THEN 'admin' ELSE 'user' END AS role").Where("users.deleted_at IS NULL")
	if !current(c).IsPlatformAdmin || c.Param("projectId") != "" {
		if _, exists := c.Get("scope"); !exists {
			a.iamScope(c)
			if c.IsAborted() {
				return
			}
		}
		sc := contextScope(c)
		if !a.store.allowed(sc, "iam:read") {
			fail(c, 403, "permission denied")
			return
		}
		q = q.Where("EXISTS (SELECT 1 FROM tenant_memberships tm WHERE tm.tenant_id=? AND tm.user_id=users.id AND tm.status='active' AND tm.deleted_at IS NULL)", sc.Tenant)
	}
	if search := strings.TrimSpace(c.Query("search")); search != "" {
		q = q.Where("(lower(username) LIKE lower(?) OR lower(name) LIKE lower(?))", "%"+search+"%", "%"+search+"%")
	}
	a.iamList(c, q, "users")
}
func iamRows(db, q *gorm.DB) ([]map[string]any, error) {
	var rows []struct{ Doc string }
	if e := db.Table("(?) AS iam_rows", q).Select("to_jsonb(iam_rows)::text AS doc").Scan(&rows).Error; e != nil {
		return nil, e
	}
	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		var item map[string]any
		if e := json.Unmarshal([]byte(row.Doc), &item); e != nil {
			return nil, e
		}
		out = append(out, item)
	}
	return out, nil
}
func (a *App) iamList(c *gin.Context, q *gorm.DB, _ string) {
	var total int64
	if q.Count(&total).Error != nil {
		fail(c, 500, "query failed")
		return
	}
	p, n := page(c)
	items, e := iamRows(a.store.db, q.Order("id DESC").Limit(n).Offset((p-1)*n))
	if e != nil {
		fail(c, 500, "query failed")
		return
	}
	ok(c, gin.H{"items": items, "total": total, "page": p, "page_size": n})
}
func validIAMStatus(s string) bool { return s == "active" || s == "disabled" || s == "locked" }
func (a *App) iamWriteUser(c *gin.Context) {
	sc := contextScope(c)
	id := number(c.Param("id"))
	create := c.Request.Method == "POST"
	var in struct {
		Username, Password, Name, Email, Role, Status string
		Admin                                         *bool `json:"is_platform_admin"`
	}
	values := map[string]any{"update_by": sc.User}
	if c.Request.Method == "DELETE" {
		if id == sc.User {
			fail(c, 403, "cannot delete yourself")
			return
		}
		values["deleted_at"] = time.Now()
		values["status"] = "disabled"
	} else {
		if c.ShouldBindJSON(&in) != nil || (in.Status != "" && !validIAMStatus(in.Status)) || (in.Role != "" && in.Role != "user" && in.Role != "admin") {
			fail(c, 400, "invalid user")
			return
		}
		values["name"] = in.Name
		values["email"] = in.Email
		if in.Status != "" {
			values["status"] = in.Status
		}
		if in.Admin != nil {
			values["is_platform_admin"] = *in.Admin
		} else if in.Role != "" {
			values["is_platform_admin"] = in.Role == "admin"
		}
		if id == sc.User && (in.Status != "" && in.Status != "active" || in.Admin != nil && !*in.Admin || in.Role == "user") {
			fail(c, 403, "cannot disable or demote yourself")
			return
		}
		if create || in.Password != "" {
			if len(in.Password) < 12 || len(in.Password) > 72 {
				fail(c, 400, "password must contain 12 to 72 bytes")
				return
			}
			hash, e := bcrypt.GenerateFromPassword([]byte(in.Password), bcrypt.DefaultCost)
			if e != nil {
				fail(c, 500, "password hashing failed")
				return
			}
			values["password_hash"] = string(hash)
		}
		if create {
			if strings.TrimSpace(in.Username) == "" || len(in.Username) > 255 {
				fail(c, 400, "username required")
				return
			}
			values["username"] = strings.TrimSpace(in.Username)
			values["create_by"] = sc.User
		}
	}
	err := a.store.db.Transaction(func(tx *gorm.DB) error {
		if create {
			if e := tx.Table("users").Clauses(clause.Returning{Columns: []clause.Column{{Name: "id"}}}).Create(&values).Error; e != nil {
				return e
			}
			id = asInt(values["id"])
		} else {
			r := tx.Table("users").Where("id=? AND deleted_at IS NULL", id).Updates(values)
			if r.Error != nil {
				return r.Error
			}
			if r.RowsAffected == 0 {
				return gorm.ErrRecordNotFound
			}
		}
		return a.store.audit(tx, sc, "iam:write", "users", id)
	})
	if err != nil {
		fail(c, 409, "user mutation failed")
		return
	}
	ok(c, gin.H{"id": id})
}
func (a *App) iamRoles(c *gin.Context) {
	sc := contextScope(c)
	a.iamList(c, a.store.db.Table("roles").Select("roles.*,COALESCE((SELECT jsonb_agg(p.code ORDER BY p.code) FROM role_permissions rp JOIN permissions p ON p.id=rp.permission_id AND p.deleted_at IS NULL WHERE rp.tenant_id=roles.tenant_id AND rp.role_id=roles.id AND rp.deleted_at IS NULL),'[]'::jsonb) AS permissions").Where("tenant_id=? AND deleted_at IS NULL", sc.Tenant), "roles")
}

// Tenant-wide roles may affect other projects; only platform administrators may mutate them.
func (a *App) iamWriteRole(c *gin.Context) {
	sc := contextScope(c)
	if !sc.Admin {
		fail(c, 403, "platform administrator required for tenant-wide roles")
		return
	}
	id := number(c.Param("id"))
	create := c.Request.Method == "POST"
	var in struct {
		Name, Code, Description string
		Permissions             []string
	}
	if c.Request.Method != "DELETE" && (c.ShouldBindJSON(&in) != nil || strings.TrimSpace(in.Name) == "" || create && strings.TrimSpace(in.Code) == "") {
		fail(c, 400, "name and code required")
		return
	}
	err := a.store.db.Transaction(func(tx *gorm.DB) error {
		if c.Request.Method == "DELETE" {
			var n int64
			if e := tx.Table("memberships").Where("tenant_id=? AND role_id=? AND deleted_at IS NULL", sc.Tenant, id).Count(&n).Error; e != nil {
				return e
			}
			if n > 0 {
				return errors.New("role in use")
			}
			r := tx.Table("roles").Where("tenant_id=? AND id=? AND deleted_at IS NULL", sc.Tenant, id).Updates(map[string]any{"deleted_at": time.Now(), "update_by": sc.User})
			if r.Error != nil {
				return r.Error
			}
			if r.RowsAffected == 0 {
				return gorm.ErrRecordNotFound
			}
		} else {
			ids, e := a.iamPermissionIDs(tx, sc, in.Permissions)
			if e != nil {
				return e
			}
			row := map[string]any{"name": in.Name, "description": in.Description, "update_by": sc.User}
			if create {
				row["tenant_id"] = sc.Tenant
				row["code"] = in.Code
				row["create_by"] = sc.User
				if e := tx.Table("roles").Clauses(clause.Returning{Columns: []clause.Column{{Name: "id"}}}).Create(&row).Error; e != nil {
					return e
				}
				id = asInt(row["id"])
			} else {
				r := tx.Table("roles").Where("tenant_id=? AND id=? AND deleted_at IS NULL", sc.Tenant, id).Updates(row)
				if r.Error != nil {
					return r.Error
				}
				if r.RowsAffected == 0 {
					return gorm.ErrRecordNotFound
				}
			}
			if e := tx.Table("role_permissions").Where("tenant_id=? AND role_id=? AND deleted_at IS NULL", sc.Tenant, id).Update("deleted_at", time.Now()).Error; e != nil {
				return e
			}
			for _, pid := range ids {
				if e := tx.Table("role_permissions").Create(map[string]any{"tenant_id": sc.Tenant, "role_id": id, "permission_id": pid, "create_by": sc.User}).Error; e != nil {
					return e
				}
			}
		}
		return a.store.audit(tx, sc, "iam:manage", "roles", id)
	})
	if err != nil {
		fail(c, 409, "role mutation failed")
		return
	}
	ok(c, gin.H{"id": id})
}
func (a *App) iamPermissionIDs(tx *gorm.DB, sc scope, codes []string) ([]int64, error) {
	ids := []int64{}
	seen := map[string]bool{}
	for _, code := range codes {
		if seen[code] {
			continue
		}
		seen[code] = true
		var p struct{ ID int64 }
		if e := tx.Table("permissions").Select("id").Where("code=? AND deleted_at IS NULL", code).Take(&p).Error; e != nil {
			return nil, e
		}
		if !(&store{tx}).allowed(sc, code) {
			return nil, errors.New("cannot grant unavailable permission")
		}
		ids = append(ids, p.ID)
	}
	return ids, nil
}
func (a *App) iamMembers(c *gin.Context) {
	sc := contextScope(c)
	q := a.store.db.Table("memberships m").Select("m.id,m.user_id,m.role_id,u.username,u.name,u.email,r.code AS role,m.created_at").Joins("JOIN users u ON u.id=m.user_id AND u.status='active' AND u.deleted_at IS NULL").Joins("JOIN tenant_memberships tm ON tm.tenant_id=m.tenant_id AND tm.user_id=m.user_id AND tm.status='active' AND tm.deleted_at IS NULL").Joins("JOIN roles r ON r.tenant_id=m.tenant_id AND r.id=m.role_id AND r.deleted_at IS NULL").Where("m.tenant_id=? AND m.project_id=? AND m.deleted_at IS NULL", sc.Tenant, sc.Project)
	var total int64
	if q.Count(&total).Error != nil {
		fail(c, 500, "query failed")
		return
	}
	p, n := page(c)
	items, e := iamRows(a.store.db, q.Order("m.id DESC").Limit(n).Offset((p-1)*n))
	if e != nil {
		fail(c, 500, "query failed")
		return
	}
	ok(c, gin.H{"items": items, "total": total, "page": p, "page_size": n})
}
func (a *App) iamWriteMember(c *gin.Context) {
	sc := contextScope(c)
	id := number(c.Param("id"))
	var in struct {
		UserID int64  `json:"user_id"`
		RoleID int64  `json:"role_id"`
		Role   string `json:"role"`
	}
	if c.Request.Method != "DELETE" {
		if c.ShouldBindJSON(&in) != nil {
			fail(c, 400, "invalid member")
			return
		}
		if c.Request.Method == "POST" {
			id = in.UserID
		}
	}
	if id < 1 || id == sc.User {
		fail(c, 403, "invalid member or self membership change")
		return
	}
	err := a.store.db.Transaction(func(tx *gorm.DB) error {
		if c.Request.Method != "DELETE" {
			var u user
			if e := tx.Table("users").Where("id=? AND status='active' AND deleted_at IS NULL", id).Take(&u).Error; e != nil {
				return e
			}
			if sc.Admin && c.Request.Method == "POST" {
				if e := tx.Table("tenant_memberships").Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "tenant_id"}, {Name: "user_id"}}, DoNothing: true}).Create(map[string]any{"tenant_id": sc.Tenant, "user_id": id, "create_by": sc.User}).Error; e != nil {
					return e
				}
			}
			var tm struct{ ID int64 }
			if e := tx.Table("tenant_memberships").Where("tenant_id=? AND user_id=? AND status='active' AND deleted_at IS NULL", sc.Tenant, id).Take(&tm).Error; e != nil {
				return e
			}
			var role struct{ ID int64 }
			q := tx.Table("roles").Where("tenant_id=? AND deleted_at IS NULL", sc.Tenant)
			if in.RoleID > 0 {
				q = q.Where("id=?", in.RoleID)
			} else {
				q = q.Where("code=?", in.Role)
			}
			if e := q.Take(&role).Error; e != nil {
				return e
			}
			in.RoleID = role.ID
			var codes []string
			if e := tx.Table("role_permissions rp").Select("p.code").Joins("JOIN permissions p ON p.id=rp.permission_id AND p.deleted_at IS NULL").Where("rp.tenant_id=? AND rp.role_id=? AND rp.deleted_at IS NULL", sc.Tenant, role.ID).Scan(&codes).Error; e != nil {
				return e
			}
			if _, e := a.iamPermissionIDs(tx, sc, codes); e != nil {
				return e
			}
		}
		q := tx.Table("memberships").Where("tenant_id=? AND project_id=? AND user_id=? AND deleted_at IS NULL", sc.Tenant, sc.Project, id)
		if c.Request.Method == "POST" {
			var n int64
			if e := q.Count(&n).Error; e != nil {
				return e
			}
			if n > 0 {
				return errors.New("member exists")
			}
		} else {
			r := q.Updates(map[string]any{"deleted_at": time.Now(), "update_by": sc.User})
			if r.Error != nil {
				return r.Error
			}
			if r.RowsAffected == 0 {
				return gorm.ErrRecordNotFound
			}
		}
		if c.Request.Method != "DELETE" {
			if e := tx.Table("memberships").Create(map[string]any{"tenant_id": sc.Tenant, "project_id": sc.Project, "user_id": id, "role_id": in.RoleID, "create_by": sc.User}).Error; e != nil {
				return e
			}
		}
		return a.store.audit(tx, sc, "iam:manage", "memberships", id)
	})
	if err != nil {
		fail(c, 409, "member mutation failed; active same-tenant membership and grantable role required")
		return
	}
	ok(c, nil)
}
func newIAMKey() (string, string, error) {
	b := make([]byte, 32)
	if _, e := rand.Read(b); e != nil {
		return "", "", e
	}
	key := "bmm_" + hex.EncodeToString(b)
	sum := sha256.Sum256([]byte(key))
	return key, hex.EncodeToString(sum[:]), nil
}
func (a *App) iamKeys(c *gin.Context) {
	sc := contextScope(c)
	q := a.store.db.Table("api_keys k").Select("k.id,k.name,k.user_id,k.key_prefix,k.expires_at,k.last_used_at,k.created_at,k.revoked_at,CASE WHEN k.revoked_at IS NOT NULL THEN 'revoked' WHEN k.expires_at<=now() THEN 'expired' ELSE 'active' END AS status,COALESCE((SELECT jsonb_agg(p.code ORDER BY p.code) FROM api_key_permissions kp JOIN permissions p ON p.id=kp.permission_id AND p.deleted_at IS NULL WHERE kp.tenant_id=k.tenant_id AND kp.project_id=k.project_id AND kp.api_key_id=k.id AND kp.deleted_at IS NULL),'[]'::jsonb) AS permissions").Where("k.tenant_id=? AND k.project_id=? AND k.deleted_at IS NULL", sc.Tenant, sc.Project)
	if !sc.Admin {
		q = q.Where("k.user_id=?", sc.User)
	}
	var total int64
	if q.Count(&total).Error != nil {
		fail(c, 500, "query failed")
		return
	}
	p, n := page(c)
	items, e := iamRows(a.store.db, q.Order("k.id DESC").Limit(n).Offset((p-1)*n))
	if e != nil {
		fail(c, 500, "query failed")
		return
	}
	ok(c, gin.H{"items": items, "total": total, "page": p, "page_size": n, "external_auth_supported": true})
}
func (a *App) iamCreateKey(c *gin.Context) {
	sc := contextScope(c)
	var in struct {
		Name        string
		Permissions []string
		ExpiresAt   *time.Time `json:"expires_at"`
	}
	if c.ShouldBindJSON(&in) != nil || strings.TrimSpace(in.Name) == "" || len(in.Name) > 255 {
		fail(c, 400, "invalid key")
		return
	}
	expires := time.Now().AddDate(1, 0, 0)
	if in.ExpiresAt != nil {
		expires = *in.ExpiresAt
	}
	if !expires.After(time.Now()) {
		fail(c, 400, "expiration must be in the future")
		return
	}
	key, hash, e := newIAMKey()
	if e != nil {
		fail(c, 500, "key generation failed")
		return
	}
	var id int64
	e = a.store.db.Transaction(func(tx *gorm.DB) error {
		ids, e := a.iamPermissionIDs(tx, sc, in.Permissions)
		if e != nil {
			return e
		}
		row := map[string]any{"tenant_id": sc.Tenant, "project_id": sc.Project, "user_id": sc.User, "name": in.Name, "key_prefix": key[:12], "secret_hash": hash, "expires_at": expires, "create_by": sc.User}
		if e := tx.Table("api_keys").Clauses(clause.Returning{Columns: []clause.Column{{Name: "id"}}}).Create(&row).Error; e != nil {
			return e
		}
		id = asInt(row["id"])
		for _, pid := range ids {
			if e := tx.Table("api_key_permissions").Create(map[string]any{"tenant_id": sc.Tenant, "project_id": sc.Project, "api_key_id": id, "permission_id": pid, "create_by": sc.User}).Error; e != nil {
				return e
			}
		}
		return a.store.audit(tx, sc, "iam:write", "api_keys", id)
	})
	if e != nil {
		fail(c, 409, "key creation failed or permission unavailable")
		return
	}
	ok(c, gin.H{"id": id, "key": key, "expires_at": expires, "external_auth_supported": true})
}
func (a *App) iamRevokeKey(c *gin.Context) {
	sc := contextScope(c)
	id := number(c.Param("id"))
	e := a.store.db.Transaction(func(tx *gorm.DB) error {
		q := tx.Table("api_keys").Where("tenant_id=? AND project_id=? AND id=? AND deleted_at IS NULL AND revoked_at IS NULL", sc.Tenant, sc.Project, id)
		if !sc.Admin {
			q = q.Where("user_id=?", sc.User)
		}
		r := q.Updates(map[string]any{"revoked_at": time.Now(), "update_by": sc.User})
		if r.Error != nil {
			return r.Error
		}
		if r.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		return a.store.audit(tx, sc, "iam:write", "api_keys", id)
	})
	if e != nil {
		fail(c, 404, "active key not found")
		return
	}
	ok(c, nil)
}
