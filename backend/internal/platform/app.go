package platform

import (
	"context"
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
	a.router = a.routes()
	return a, nil
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
	ok(c, gin.H{"token": token, "user": u})
}
func (a *App) password(c *gin.Context) {
	var in struct {
		OldPassword string `json:"old_password"`
		NewPassword string `json:"new_password"`
	}
	if c.ShouldBindJSON(&in) != nil || len(in.NewPassword) < 12 || len(in.NewPassword) > 72 {
		fail(c, 400, "password must contain 12 to 72 bytes")
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
		"initialized": a.setupComplete,
		"mode":        mode,
		"locked":      locked,
	})
}

// setupMode 设置系统部署模式（需要管理员权限）。
// POST /api/v1/setup/mode
func (a *App) setupMode(c *gin.Context) {
	if !current(c).IsPlatformAdmin {
		fail(c, 403, "需要平台管理员权限")
		return
	}
	if a.setupComplete {
		fail(c, 400, "系统模式已初始化，不可重复设置")
		return
	}
	var in struct {
		Mode string `json:"mode"`
	}
	if c.ShouldBindJSON(&in) != nil || in.Mode == "" {
		fail(c, 400, "请提供有效的系统模式")
		return
	}
	if err := InitializeMode(a.store.db, in.Mode); err != nil {
		fail(c, 400, err.Error())
		return
	}
	a.setupComplete = true
	ok(c, gin.H{
		"mode":     in.Mode,
		"features": GetAvailableFeatures(in.Mode),
	})
}

// setupConfig 完成系统初始化配置（需要管理员权限）。
// POST /api/v1/setup/config
// 请求体: {admin_username, admin_password, admin_email, bmc_network, datacenter_name}
func (a *App) setupConfig(c *gin.Context) {
	if !current(c).IsPlatformAdmin {
		fail(c, 403, "需要平台管理员权限")
		return
	}
	var in struct {
		AdminUsername   string `json:"admin_username"`
		AdminPassword   string `json:"admin_password"`
		AdminEmail      string `json:"admin_email"`
		BMCNetwork      string `json:"bmc_network"`
		DatacenterName  string `json:"datacenter_name"`
	}
	if c.ShouldBindJSON(&in) != nil {
		fail(c, 400, "请求参数无效")
		return
	}
	// 验证必填字段
	if in.AdminUsername == "" || in.AdminPassword == "" {
		fail(c, 400, "管理员用户名和密码为必填项")
		return
	}
	if len(in.AdminPassword) < 12 || len(in.AdminPassword) > 72 {
		fail(c, 400, "密码长度必须在12到72字节之间")
		return
	}

	err := a.store.db.Transaction(func(tx *gorm.DB) error {
		// 1. 创建或更新超级管理员用户
		if in.AdminUsername != "" {
			hash, e := bcrypt.GenerateFromPassword([]byte(in.AdminPassword), bcrypt.DefaultCost)
			if e != nil {
				return errors.New("密码加密失败")
			}
			var existingUser struct{ ID int64 }
			e = tx.Table("users").Select("id").Where("lower(username)=lower(?) AND deleted_at IS NULL", in.AdminUsername).Scan(&existingUser.ID).Error
			if e != nil && !errors.Is(e, gorm.ErrRecordNotFound) {
				return e
			}
			if existingUser.ID > 0 {
				// 更新现有管理员密码
				updates := map[string]any{"password_hash": string(hash), "status": "active"}
				if in.AdminEmail != "" {
					updates["email"] = in.AdminEmail
				}
				if e := tx.Table("users").Where("id=?", existingUser.ID).Updates(updates).Error; e != nil {
					return e
				}
			} else {
				// 创建新的超级管理员
				email := in.AdminEmail
				if email == "" {
					email = in.AdminUsername + "@localhost"
				}
				userRow := map[string]any{
					"username":          in.AdminUsername,
					"password_hash":     string(hash),
					"name":              in.AdminUsername,
					"email":             email,
					"role":              "admin",
					"status":            "active",
					"is_platform_admin": true,
				}
				if e := tx.Table("users").Create(&userRow).Error; e != nil {
					return e
				}
			}
		}

		// 2. 存储 BMC 网络配置到 system_settings
		if in.BMCNetwork != "" {
			data, _ := json.Marshal(in.BMCNetwork)
			if e := tx.Exec(
				"INSERT INTO system_settings(key,value,create_by) VALUES (? (?),?) ON CONFLICT (key) WHERE deleted_at IS NULL DO UPDATE SET value=EXCLUDED.value",
				"bmc.default_network", string(data), current(c).ID,
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
					"project_id": project.ID,
					"create_by":  current(c).ID,
				}
				if e := tx.Table("datacenters").Create(&dcRow).Error; e != nil {
					return e
				}
			}
		}

		return nil
	})
	if err != nil {
		fail(c, 500, "系统配置初始化失败: "+err.Error())
		return
	}
	ok(c, gin.H{"message": "系统配置初始化完成"})
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
