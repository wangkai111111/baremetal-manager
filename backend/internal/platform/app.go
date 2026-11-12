package platform

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

type App struct {
	store         *store
	secret        []byte
	node          string
	router        *gin.Engine
	setupComplete bool // 系统是否已完成初始化设置
}
type store struct{ db *gorm.DB }
type user struct {
	ID              int64  `json:"id"`
	Username        string `json:"username"`
	Name            string `json:"name"`
	Email           string `json:"email"`
	PasswordHash    string `json:"-"`
	IsPlatformAdmin bool   `json:"is_platform_admin"`
}
type scope struct {
	Tenant, Project, User int64
	Admin                 bool
}

func New(db *gorm.DB) (*App, error) {
	if db == nil {
		return nil, errors.New("database is required")
	}
	secret := os.Getenv("JWT_SECRET")
	if len(secret) < 32 {
		return nil, errors.New("JWT_SECRET must be at least 32 bytes")
	}
	if len(os.Getenv("BMC_ENCRYPTION_KEY")) != 32 {
		return nil, errors.New("BMC_ENCRYPTION_KEY must be exactly 32 bytes")
	}
	a := &App{store: &store{db}, secret: []byte(secret), node: uuid.NewString()}
	// 检查系统初始化设置是否完成
	if complete, err := IsSetupComplete(db); err == nil {
		a.setupComplete = complete
	}
	// 历史数据兼容：部署模式一经初始化即视为锁定，补齐早期版本未写入的 locked 标记，
	// 否则已初始化的存量系统仍能通过 /settings 改写 platform.operation_mode。
	if a.setupComplete {
		if locked, err := IsModeLocked(db); err == nil && !locked {
			if err := LockMode(db); err == nil {
				InvalidateModeCache()
			}
		}
	}
	// 为存量数据库补齐新增权限条目（幂等，不影响已有数据）
	seedPermissions(db)
	a.router = a.routes()
	return a, nil
}

// seedPermissions 确保新增资源权限条目存在于 permissions 表中。
// 仅对存量数据库有效（全新安装由 bootstrap 负责）；使用 INSERT ... WHERE NOT EXISTS 保证幂等。
func seedPermissions(db *gorm.DB) {
	newResources := []string{"work_orders", "products", "orders", "billing", "finance", "marketing", "departments"}
	actions := []string{"read", "write", "operate", "access", "manage"}
	for _, res := range newResources {
		for _, act := range actions {
			code := res + ":" + act
			db.Exec(
				"INSERT INTO permissions(code,name,resource,action,create_by,update_by) SELECT ?,?,?,?,0,0 WHERE NOT EXISTS (SELECT 1 FROM permissions WHERE code=? AND deleted_at IS NULL)",
				code, code, res, act, code,
			)
		}
	}
	// 将新增权限自动授予 manager 角色（若存在），保证管理员功能不受影响
	var managerRole struct{ ID, TenantID int64 }
	if err := db.Table("roles").Select("id, tenant_id").Where("code=? AND deleted_at IS NULL", "manager").Limit(1).Scan(&managerRole).Error; err == nil && managerRole.ID > 0 {
		for _, res := range newResources {
			for _, act := range actions {
				code := res + ":" + act
				db.Exec(
					"INSERT INTO role_permissions(tenant_id,role_id,permission_id,create_by,update_by) SELECT r.tenant_id, r.id, p.id, 0, 0 FROM roles r JOIN permissions p ON p.code=? AND p.deleted_at IS NULL WHERE r.id=? AND r.deleted_at IS NULL AND NOT EXISTS (SELECT 1 FROM role_permissions rp WHERE rp.role_id=r.id AND rp.permission_id=p.id AND rp.deleted_at IS NULL)",
					code, managerRole.ID,
				)
			}
		}
	}
}
func (a *App) Router() *gin.Engine { return a.router }
func ok(c *gin.Context, data any)  { c.JSON(200, gin.H{"code": 0, "data": data, "message": "success"}) }
func fail(c *gin.Context, status int, message string) {
	c.AbortWithStatusJSON(status, gin.H{"code": status, "message": message})
}
func current(c *gin.Context) user       { return c.MustGet("user").(user) }
func contextScope(c *gin.Context) scope { return c.MustGet("scope").(scope) }
func number(s string) int64             { n, _ := strconv.ParseInt(s, 10, 64); return n }
func (a *App) guard(c *gin.Context) {
	token := strings.TrimPrefix(c.GetHeader("Authorization"), "Bearer ")
	key := c.GetHeader("X-API-Key")
	if key != "" || strings.HasPrefix(token, "bmm_") {
		if key == "" {
			key = token
		}
		a.iamKeyGuard(c, key)
		return
	}
	if token == "" || token == c.GetHeader("Authorization") {
		fail(c, 401, "authentication required")
		return
	}
	claims := jwt.RegisteredClaims{}
	t, err := jwt.ParseWithClaims(token, &claims, func(*jwt.Token) (any, error) { return a.secret, nil }, jwt.WithValidMethods([]string{"HS256"}), jwt.WithExpirationRequired(), jwt.WithIssuer("baremetal-platform"))
	if err != nil || !t.Valid {
		fail(c, 401, "invalid token")
		return
	}
	u, err := a.store.activeUser(number(claims.Subject))
	if err != nil {
		fail(c, 401, "inactive user")
		return
	}
	c.Set("user", u)
	c.Next()
}
func (s *store) activeUser(id int64) (user, error) {
	var u user
	e := s.db.Table("users").Where("id=? AND status='active' AND deleted_at IS NULL", id).Take(&u).Error
	return u, e
}
func (a *App) project(c *gin.Context) {
	u := current(c)
	var p struct{ ID, TenantID int64 }
	err := a.store.db.Table("projects p").Select("p.id,p.tenant_id").Joins("JOIN tenants t ON t.id=p.tenant_id AND t.status='active' AND t.deleted_at IS NULL").Where("p.id=? AND p.status='active' AND p.deleted_at IS NULL", number(c.Param("projectId"))).Take(&p).Error
	if err != nil {
		fail(c, 404, "project not found")
		return
	}
	sc := scope{p.TenantID, p.ID, u.ID, u.IsPlatformAdmin}
	if value, exists := c.Get("api_key"); exists {
		key := value.(iamAuthenticatedKey)
		if key.TenantID != p.TenantID || key.ProjectID != p.ID {
			fail(c, 403, "API key project mismatch")
			return
		}
	}
	if !u.IsPlatformAdmin && !a.store.member(sc) {
		fail(c, 403, "project membership required")
		return
	}
	c.Set("scope", sc)
	c.Next()
}
func (s *store) member(sc scope) bool {
	var n int64
	s.db.Table("memberships m").Joins("JOIN tenant_memberships tm ON tm.tenant_id=m.tenant_id AND tm.user_id=m.user_id AND tm.status='active' AND tm.deleted_at IS NULL").Joins("JOIN roles r ON r.id=m.role_id AND r.tenant_id=m.tenant_id AND r.deleted_at IS NULL").Where("m.tenant_id=? AND m.project_id=? AND m.user_id=? AND m.deleted_at IS NULL", sc.Tenant, sc.Project, sc.User).Count(&n)
	return n > 0
}
func (s *store) allowed(sc scope, code string) bool {
	if sc.Admin {
		return true
	}
	var n int64
	s.db.Table("memberships m").Joins("JOIN roles r ON r.id=m.role_id AND r.tenant_id=m.tenant_id AND r.deleted_at IS NULL").Joins("JOIN role_permissions rp ON rp.role_id=r.id AND rp.tenant_id=r.tenant_id AND rp.deleted_at IS NULL").Joins("JOIN permissions p ON p.id=rp.permission_id AND p.deleted_at IS NULL").Where("m.tenant_id=? AND m.project_id=? AND m.user_id=? AND m.deleted_at IS NULL AND p.code=?", sc.Tenant, sc.Project, sc.User, code).Count(&n)
	return n > 0
}
func (a *App) require(code string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !a.store.allowed(contextScope(c), code) {
			fail(c, 403, "permission denied")
			return
		}
		c.Next()
	}
}
func (a *App) login(c *gin.Context) {
	var in struct{ Username, Password string }
	if c.ShouldBindJSON(&in) != nil || len(in.Password) > 72 {
		fail(c, 400, "invalid credentials")
		return
	}
	var u user
	err := a.store.db.Table("users").Where("lower(username)=lower(?) AND status='active' AND deleted_at IS NULL", in.Username).Take(&u).Error
	if err != nil || bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(in.Password)) != nil {
		fail(c, 401, "invalid credentials")
		return
	}
	now := time.Now()
	claims := jwt.RegisteredClaims{Subject: strconv.FormatInt(u.ID, 10), Issuer: "baremetal-platform", IssuedAt: jwt.NewNumericDate(now), ExpiresAt: jwt.NewNumericDate(now.Add(8 * time.Hour))}
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(a.secret)
	if err != nil {
		fail(c, 500, "token creation failed")
		return
	}
	a.store.db.Table("users").Where("id=?", u.ID).Update("last_login_at", now)
	// 登录响应携带完整上下文（权限/项目/角色），供前端菜单与路由守卫使用；
	// 若仅返回裸 user 行，非平台管理员（租户用户）会因拿不到 permissions 而看到空侧边栏。
	ok(c, gin.H{"token": token, "user": a.buildUserContext(u, 0)})
}

// authMe 返回当前用户身份 + 前端渲染权限化导航所需的全部上下文：
// 可访问项目、当前项目的有效权限码、角色。历史实现只回传裸 user 行，
// 导致运营模式下的新注册租户拿不到任何 permissions，侧边栏被权限过滤清空。
func (a *App) authMe(c *gin.Context) {
	u := current(c)
	projectID := number(c.Query("project_id"))
	if projectID == 0 {
		projectID = number(c.GetHeader("X-Project-ID"))
	}
	ok(c, a.buildUserContext(u, projectID))
}

// buildUserContext 组装用户上下文。平台管理员短路：前端把 is_platform_admin 视为
// “拥有全部权限”，因此无需展开成员/权限联表；其余用户按成员关系解析实际权限。
func (a *App) buildUserContext(u user, projectID int64) gin.H {
	out := gin.H{
		"id":                u.ID,
		"username":          u.Username,
		"name":              u.Name,
		"email":             u.Email,
		"is_platform_admin": u.IsPlatformAdmin,
		"role":              "user",
		"project_ids":       []int64{},
		"permissions":       []string{},
		"memberships":       []gin.H{},
	}
	if u.IsPlatformAdmin {
		out["role"] = "admin"
		return out
	}

	// 租户归属：自助注册的租户用户通常仅属于一个租户，取最早的一条作为默认作用域。
	var tenant struct{ ID int64 }
	if a.store.db.Table("tenant_memberships").Select("tenant_id AS id").
		Where("user_id=? AND status='active' AND deleted_at IS NULL", u.ID).
		Order("tenant_id").Limit(1).Scan(&tenant).Error == nil && tenant.ID > 0 {
		out["tenant_id"] = tenant.ID
	}

	// 可访问项目 + 每项目的角色码，过滤条件与 projects() 的成员判定保持一致。
	type projRow struct {
		ProjectID int64  `gorm:"column:project_id"`
		TenantID  int64  `gorm:"column:tenant_id"`
		Role      string `gorm:"column:role"`
	}
	var projects []projRow
	a.store.db.Raw(`
		SELECT DISTINCT m.project_id, p.tenant_id, r.code AS role
		FROM memberships m
		JOIN projects p ON p.id=m.project_id AND p.tenant_id=m.tenant_id AND p.status='active' AND p.deleted_at IS NULL
		JOIN tenants t ON t.id=p.tenant_id AND t.status='active' AND t.deleted_at IS NULL
		JOIN roles r ON r.id=m.role_id AND r.tenant_id=m.tenant_id AND r.deleted_at IS NULL
		JOIN tenant_memberships tm ON tm.tenant_id=p.tenant_id AND tm.user_id=m.user_id AND tm.status='active' AND tm.deleted_at IS NULL
		WHERE m.user_id=? AND m.deleted_at IS NULL
		ORDER BY m.project_id`, u.ID).Scan(&projects)

	ids := make([]int64, 0, len(projects))
	memberships := make([]gin.H, 0, len(projects))
	for _, pr := range projects {
		ids = append(ids, pr.ProjectID)
		memberships = append(memberships, gin.H{
			"user_id":     u.ID,
			"project_id":  pr.ProjectID,
			"role":        pr.Role,
			"permissions": a.effectivePermissions(pr.TenantID, pr.ProjectID, u.ID),
		})
	}
	out["project_ids"] = ids
	out["memberships"] = memberships

	// 解析当前作用域项目：优先使用请求指定的可访问项目，否则回退首个可访问项目。
	target, targetTenant, role := projectID, int64(0), ""
	if target != 0 {
		for _, pr := range projects {
			if pr.ProjectID == target {
				targetTenant, role = pr.TenantID, pr.Role
				break
			}
		}
		if targetTenant == 0 {
			target = 0 // 请求的项目不可访问，视为未选择
		}
	}
	if target == 0 && len(projects) > 0 {
		target, targetTenant, role = projects[0].ProjectID, projects[0].TenantID, projects[0].Role
	}
	if role != "" {
		out["role"] = role
	}
	out["permissions"] = a.effectivePermissions(targetTenant, target, u.ID)
	return out
}

// effectivePermissions 计算某成员在指定 (tenant, project) 下的有效权限码集合。
func (a *App) effectivePermissions(tenant, project, userID int64) []string {
	codes := []string{}
	if tenant == 0 || project == 0 {
		return codes
	}
	var rows []struct {
		Code string `gorm:"column:code"`
	}
	a.store.db.Raw(`
		SELECT DISTINCT p.code
		FROM memberships m
		JOIN roles r ON r.id=m.role_id AND r.tenant_id=m.tenant_id AND r.deleted_at IS NULL
		JOIN role_permissions rp ON rp.role_id=r.id AND rp.tenant_id=r.tenant_id AND rp.deleted_at IS NULL
		JOIN permissions p ON p.id=rp.permission_id AND p.deleted_at IS NULL
		WHERE m.tenant_id=? AND m.project_id=? AND m.user_id=? AND m.deleted_at IS NULL`,
		tenant, project, userID).Scan(&rows)
	for _, r := range rows {
		codes = append(codes, r.Code)
	}
	return codes
}
func (a *App) password(c *gin.Context) {
	var in struct {
		OldPassword string `json:"old_password"`
		NewPassword string `json:"new_password"`
	}
	if c.ShouldBindJSON(&in) != nil || len(in.NewPassword) < 6 || len(in.NewPassword) > 72 {
		fail(c, 400, "password must contain 6 to 72 bytes")
		return
	}
	u := current(c)
	if bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(in.OldPassword)) != nil {
		fail(c, 403, "invalid current password")
		return
	}
	hash, e := bcrypt.GenerateFromPassword([]byte(in.NewPassword), bcrypt.DefaultCost)
	if e != nil {
		fail(c, 500, "password hashing failed")
		return
	}
	e = a.store.db.Table("users").Where("id=? AND password_hash=?", u.ID, u.PasswordHash).Update("password_hash", string(hash)).Error
	if e != nil {
		fail(c, 500, "password update failed")
		return
	}
	ok(c, nil)
}
func (a *App) readiness(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
	defer cancel()
	db, e := a.store.db.DB()
	if e != nil || db.PingContext(ctx) != nil {
		fail(c, 503, "database unavailable")
		return
	}
	ok(c, gin.H{"status": "ready"})
}

// ============================================================
// 系统初始化设置相关接口
// ============================================================

// setupStatus 返回系统初始化状态。
// GET /api/v1/setup/status
func (a *App) setupStatus(c *gin.Context) {
	mode, err := GetSystemMode(a.store.db)
	if err != nil {
		fail(c, 500, "查询系统模式失败")
		return
	}
	locked, err := IsModeLocked(a.store.db)
	if err != nil {
		fail(c, 500, "查询模式锁定状态失败")
		return
	}
	ok(c, gin.H{
		"initialized": mode != "",
		"mode":        mode,
		"locked":      locked,
	})
}

func setupCredentials(username, password string) (string, string, bool, error) {
	username = strings.TrimSpace(username)
	if username == "" {
		username = "admin"
	}
	if len(username) < 3 {
		return "", "", false, errors.New("用户名至少 3 个字符")
	}
	if password != "" {
		if len(password) < 6 || len(password) > 72 {
			return "", "", false, errors.New("密码必须为 6 到 72 字节")
		}
		return username, password, false, nil
	}
	var random [24]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", "", false, errors.New("安全随机密码生成失败")
	}
	return username, base64.RawURLEncoding.EncodeToString(random[:]), true, nil
}

// setupConfig 原子创建首个管理员并锁定模式；仅未初始化时开放。
func (a *App) setupConfig(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	var in struct {
		Mode           string `json:"mode"`
		AdminUsername  string `json:"admin_username"`
		AdminPassword  string `json:"admin_password"`
		AdminEmail     string `json:"admin_email"`
		BMCNetwork     string `json:"bmc_network"`
		DatacenterName string `json:"datacenter_name"`
	}
	if c.ShouldBindJSON(&in) != nil {
		fail(c, 400, "请求参数无效")
		return
	}
	if in.Mode != ModeSelfUse && in.Mode != ModeExternalOperation {
		fail(c, 400, "请选择有效的系统模式")
		return
	}
	username, password, generated, err := setupCredentials(in.AdminUsername, in.AdminPassword)
	if err != nil {
		fail(c, 400, err.Error())
		return
	}

	var adminID int64
	errSetupClosed := errors.New("系统已初始化或已有管理员，不可重复初始化")
	err = a.store.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SELECT pg_advisory_xact_lock(684219733)").Error; err != nil {
			return err
		}
		complete, err := IsSetupComplete(tx)
		if err != nil {
			return err
		}
		var admins int64
		if err := tx.Table("users").Where("is_platform_admin = true").Count(&admins).Error; err != nil {
			return err
		}
		if complete || admins > 0 {
			return errSetupClosed
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
		if err != nil {
			return err
		}
		email := in.AdminEmail
		if email == "" {
			email = username + "@localhost"
		}
		if err := tx.Raw("INSERT INTO users(username,password_hash,name,email,is_platform_admin) VALUES (?,?,?,?,true) RETURNING id", username, string(hash), username, email).Scan(&adminID).Error; err != nil {
			return err
		}
		var project struct{ ID, TenantID int64 }
		if err := tx.Table("projects").Select("id,tenant_id").Where("deleted_at IS NULL AND status='active'").Order("id").Take(&project).Error; err != nil {
			return err
		}
		var roleID int64
		if err := tx.Table("roles").Select("id").Where("tenant_id=? AND code='manager' AND deleted_at IS NULL", project.TenantID).Take(&roleID).Error; err != nil {
			return err
		}
		if err := tx.Exec("INSERT INTO tenant_memberships(tenant_id,user_id,create_by,update_by) VALUES (?,?,?,?)", project.TenantID, adminID, adminID, adminID).Error; err != nil {
			return err
		}
		if err := tx.Exec("INSERT INTO memberships(tenant_id,project_id,user_id,role_id,create_by,update_by) VALUES (?,?,?,?,?,?)", project.TenantID, project.ID, adminID, roleID, adminID, adminID).Error; err != nil {
			return err
		}
		if err := tx.Table("projects").Where("id=?", project.ID).Update("owner_id", adminID).Error; err != nil {
			return err
		}
		if err := InitializeMode(tx, in.Mode); err != nil {
			return err
		}
		if err := LockMode(tx); err != nil {
			return err
		}
		settingMode := "self_hosted"
		if in.Mode == ModeExternalOperation {
			settingMode = "external"
		}
		if err := tx.Exec("INSERT INTO system_settings(key,value,create_by) VALUES ('platform.operation_mode',to_jsonb(?::text),?) ON CONFLICT (key) WHERE deleted_at IS NULL DO UPDATE SET value=EXCLUDED.value", settingMode, adminID).Error; err != nil {
			return err
		}

		// 2. 存储 BMC 网络配置到 system_settings
		if in.BMCNetwork != "" {
			data, _ := json.Marshal(in.BMCNetwork)
			if e := tx.Exec(
				"INSERT INTO system_settings(key,value,create_by) VALUES (?,?::jsonb,?) ON CONFLICT (key) WHERE deleted_at IS NULL DO UPDATE SET value=EXCLUDED.value",
				"bmc.default_network", string(data), adminID,
			).Error; e != nil {
				return e
			}
		}

		// 3. 创建默认机房（如果提供了名称）
		if in.DatacenterName != "" {
			// 获取第一个可用项目作为机房归属
			var project struct{ ID, TenantID int64 }
			e := tx.Table("projects").Select("id,tenant_id").Where("deleted_at IS NULL AND status='active'").Order("id").Take(&project).Error
			if e == nil {
				dcRow := map[string]any{
					"name":       in.DatacenterName,
					"tenant_id":  project.TenantID,
					"project_id": project.ID,
					"create_by":  adminID,
				}
				if e := tx.Table("datacenters").Create(&dcRow).Error; e != nil {
					return e
				}
			}
		}

		return nil
	})
	if err != nil {
		if errors.Is(err, errSetupClosed) {
			fail(c, 409, errSetupClosed.Error())
		} else {
			fail(c, 500, "系统配置初始化失败")
		}
		return
	}
	InvalidateModeCache()
	out := gin.H{"message": "系统配置初始化完成", "admin_username": username}
	if generated {
		out["generated_password"] = password
	}
	ok(c, out)
}

// requireSetup 中间件：检查系统是否已完成初始化设置。
// 未完成时仅允许 /api/v1/setup/* 路由访问。
func (a *App) requireSetup(c *gin.Context) {
	if !a.setupComplete {
		// 允许 setup 相关路由通过
		path := c.Request.URL.Path
		if strings.HasPrefix(path, "/api/v1/setup/") {
			c.Next()
			return
		}
		fail(c, 403, "系统尚未完成初始化设置，请先访问 /api/v1/setup/* 接口")
		return
	}
	c.Next()
}
