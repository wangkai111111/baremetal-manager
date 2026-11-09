package platform

import (
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ============================================================
// 租户注册处理器（运营模式）
// ============================================================
//
// 待注册路由（routes.go）：
// POST /api/v1/auth/register → registerTenant → (公开接口，无需认证)

// 邮箱和手机号正则校验
var (
	emailRegex = regexp.MustCompile(`^[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}$`)
	phoneRegex = regexp.MustCompile(`^1[3-9]\d{9}$`)
)

// registerTenant 租户自助注册
// 支持手机号或邮箱注册，自动创建租户、账户、默认项目和用户
func (a *App) registerTenant(c *gin.Context) {
	var in struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Name     string `json:"name"`
		Email    string `json:"email"`
		Phone    string `json:"phone"`
		Company  string `json:"company_name"`
	}
	if c.ShouldBindJSON(&in) != nil {
		fail(c, 400, "请求参数无效")
		return
	}
	// 校验必填字段
	if strings.TrimSpace(in.Username) == "" || len(in.Username) > 255 {
		fail(c, 400, "用户名不能为空且长度不超过255")
		return
	}
	if len(in.Password) < 12 || len(in.Password) > 72 {
		fail(c, 400, "密码长度必须在12到72字节之间")
		return
	}
	// 至少提供一种联系方式
	hasEmail := strings.TrimSpace(in.Email) != ""
	hasPhone := strings.TrimSpace(in.Phone) != ""
	if !hasEmail && !hasPhone {
		fail(c, 400, "请提供邮箱或手机号")
		return
	}
	if hasEmail && !emailRegex.MatchString(in.Email) {
		fail(c, 400, "邮箱格式无效")
		return
	}
	if hasPhone && !phoneRegex.MatchString(in.Phone) {
		fail(c, 400, "手机号格式无效")
		return
	}
	// 检查用户名唯一性
	var existingUser int64
	a.store.db.Table("users").
		Where("lower(username)=lower(?) AND deleted_at IS NULL", strings.TrimSpace(in.Username)).
		Count(&existingUser)
	if existingUser > 0 {
		fail(c, 409, "用户名已被注册")
		return
	}
	// 检查邮箱唯一性（如果提供）
	if hasEmail {
		var existingEmail int64
		a.store.db.Table("users").
			Where("lower(email)=lower(?) AND deleted_at IS NULL", in.Email).
			Count(&existingEmail)
		if existingEmail > 0 {
			fail(c, 409, "邮箱已被注册")
			return
		}
	}
	// 生成租户编码（基于时间戳+随机数）
	tenantCode := generateTenantCode(in.Username)
	tenantName := in.Company
	if tenantName == "" {
		tenantName = in.Username + " 的工作空间"
	}
	// 密码哈希
	hash, err := bcrypt.GenerateFromPassword([]byte(in.Password), bcrypt.DefaultCost)
	if err != nil {
		fail(c, 500, "密码加密失败")
		return
	}
	var (
		userID   int64
		tenantID int64
		token    string
	)
	err = a.store.db.Transaction(func(tx *gorm.DB) error {
		// 1. 创建租户
		tenantRow := map[string]any{
			"name":   tenantName,
			"code":   tenantCode,
			"status": "active",
		}
		if e := tx.Table("tenants").Clauses(clause.Returning{Columns: []clause.Column{{Name: "id"}}}).Create(&tenantRow).Error; e != nil {
			return e
		}
		tenantID = asInt(tenantRow["id"])
		// 2. 创建用户
		userRow := map[string]any{
			"username":          strings.TrimSpace(in.Username),
			"password_hash":     string(hash),
			"name":              in.Name,
			"email":             in.Email,
			"role":              "admin",
			"status":            "active",
			"is_platform_admin": false,
		}
		if userRow["name"] == "" {
			userRow["name"] = userRow["username"]
		}
		if e := tx.Table("users").Clauses(clause.Returning{Columns: []clause.Column{{Name: "id"}}}).Create(&userRow).Error; e != nil {
			return e
		}
		userID = asInt(userRow["id"])
		// 3. 创建租户成员关系
		if e := tx.Table("tenant_memberships").Create(map[string]any{
			"tenant_id": tenantID,
			"user_id":   userID,
			"status":    "active",
		}).Error; e != nil {
			return e
		}
		// 4. 初始化租户角色和权限
		if e := initializeRoles(tx, tenantID, userID); e != nil {
			return e
		}
		// 5. 创建租户档案（运营模式）
		profileRow := map[string]any{
			"tenant_id":    tenantID,
			"contact_name": in.Name,
		}
		if hasPhone {
			profileRow["contact_phone"] = in.Phone
		}
		if hasEmail {
			profileRow["contact_email"] = in.Email
		}
		if in.Company != "" {
			profileRow["company_name"] = in.Company
		}
		if e := tx.Table("tenant_profiles").Create(&profileRow).Error; e != nil {
			// 档案创建失败不阻断注册流程
			_ = e
		}
		// 6. 创建租户账户（运营模式，初始余额0）
		if e := tx.Table("accounts").Create(map[string]any{
			"tenant_id":     tenantID,
			"balance":       0,
			"frozen_amount": 0,
			"credit_limit":  0,
		}).Error; e != nil {
			// 账户创建失败不阻断注册流程
			_ = e
		}
		// 7. 创建默认项目
		projectRow := map[string]any{
			"tenant_id":   tenantID,
			"name":        "默认项目",
			"code":        "default",
			"description": "注册时自动创建的默认项目",
			"owner_id":    userID,
			"status":      "active",
		}
		if e := tx.Table("projects").Clauses(clause.Returning{Columns: []clause.Column{{Name: "id"}}}).Create(&projectRow).Error; e != nil {
			return e
		}
		projectID := asInt(projectRow["id"])
		// 8. 将用户加入默认项目（manager 角色）
		var managerRole int64
		if e := tx.Table("roles").Select("id").
			Where("tenant_id=? AND code='manager' AND deleted_at IS NULL", tenantID).
			Scan(&managerRole).Error; e != nil {
			return e
		}
		if managerRole > 0 {
			if e := tx.Table("memberships").Create(map[string]any{
				"tenant_id":  tenantID,
				"project_id": projectID,
				"user_id":    userID,
				"role_id":    managerRole,
			}).Error; e != nil {
				return e
			}
		}
		return nil
	})
	if err != nil {
		fail(c, 500, "注册失败: "+err.Error())
		return
	}
	// 9. 生成 JWT Token（自动登录）
	now := time.Now()
	claims := jwt.RegisteredClaims{
		Subject:   strconv.FormatInt(userID, 10),
		Issuer:    "baremetal-platform",
		IssuedAt:  jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(now.Add(8 * time.Hour)),
	}
	token, err = jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(a.secret)
	if err != nil {
		fail(c, 500, "令牌生成失败")
		return
	}
	// 更新最后登录时间
	a.store.db.Table("users").Where("id=?", userID).Update("last_login_at", now)
	ok(c, gin.H{
		"token":     token,
		"user_id":   userID,
		"tenant_id": tenantID,
		"username":  in.Username,
		"message":   "注册成功",
	})
}

// generateTenantCode 基于用户名生成唯一租户编码
func generateTenantCode(username string) string {
	sanitized := strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			return r
		}
		return '-'
	}, username)
	sanitized = strings.ToLower(strings.Trim(sanitized, "-"))
	if len(sanitized) > 20 {
		sanitized = sanitized[:20]
	}
	// 追加时间戳后4位确保唯一
	ts := time.Now().Unix()
	return sanitized + "-" + strconv.FormatInt(ts%10000, 10)
}
