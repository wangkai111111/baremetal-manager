package platform

import (
	"context"
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
	store  *store
	secret []byte
	node   string
	router *gin.Engine
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
