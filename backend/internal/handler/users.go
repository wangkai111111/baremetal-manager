package handler

import (
	"baremetal-manager/backend/internal/model"
	"fmt"
	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
	"time"
)

// ChangePassword 验证当前密码并更新登录用户的密码。
func (h *Handler) ChangePassword(c *gin.Context) {
	var in struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	if c.ShouldBindJSON(&in) != nil || len(in.NewPassword) < 12 {
		fail(c, 400, "new password must be at least 12 characters")
		return
	}
	var u model.User
	if h.S.Store.DB.First(&u, uid(c)).Error != nil || bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(in.CurrentPassword)) != nil {
		fail(c, 401, "invalid credentials")
		return
	}
	hash, _ := bcrypt.GenerateFromPassword([]byte(in.NewPassword), bcrypt.DefaultCost)
	h.S.Store.DB.Model(&u).Update("password_hash", string(hash))
	c.Status(204)
}

// CreateUser 供系统管理员创建用户并设置角色。
func (h *Handler) CreateUser(c *gin.Context) {
	var in struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Role     string `json:"role"`
	}
	if c.ShouldBindJSON(&in) != nil || in.Username == "" || len(in.Password) < 12 {
		fail(c, 400, "invalid user")
		return
	}
	var me model.User
	h.S.Store.DB.First(&me, uid(c))
	if me.Role != "admin" {
		fail(c, 403, "forbidden")
		return
	}
	if in.Role != "admin" && in.Role != "user" {
		in.Role = "user"
	}
	hash, _ := bcrypt.GenerateFromPassword([]byte(in.Password), bcrypt.DefaultCost)
	u := model.User{Username: in.Username, PasswordHash: string(hash), Role: in.Role}
	if h.S.Store.DB.Create(&u).Error != nil {
		fail(c, 409, "username exists")
		return
	}
	u.PasswordHash = ""
	c.JSON(201, u)
}

// SOLSessions 列出当前项目尚未结束且未过期的串口会话。
func (h *Handler) SOLSessions(c *gin.Context) {
	var sessions []model.SOLSession
	h.S.Store.DB.Where("project_id = ? AND ended_at IS NULL AND expires_at > ?", pid(c), time.Now()).Find(&sessions)
	c.JSON(200, sessions)
}

// EndSOLSession 结束当前项目中的指定串口会话。
func (h *Handler) EndSOLSession(c *gin.Context) {
	var session model.SOLSession
	if h.S.Store.DB.Where("project_id = ? AND id = ?", pid(c), c.Param("id")).First(&session).Error != nil {
		fail(c, 404, "session not found")
		return
	}
	now := time.Now()
	h.S.Store.DB.Model(&session).Update("ended_at", &now)
	c.Status(204)
}

// Scan 校验设备归属后批量提交硬件检查任务。
func (h *Handler) Scan(c *gin.Context) {
	var in struct {
		AssetIDs []uint `json:"asset_ids"`
	}
	if c.ShouldBindJSON(&in) != nil || len(in.AssetIDs) == 0 {
		fail(c, 400, "invalid scan")
		return
	}
	ids := make([]uint, 0, len(in.AssetIDs))
	for _, aid := range in.AssetIDs {
		var a model.Asset
		if h.S.Store.DB.Where("project_id=? AND id=?", pid(c), aid).First(&a).Error != nil {
			fail(c, 404, "asset not found")
			return
		}
	}
	for _, aid := range in.AssetIDs {
		if _, err := h.S.Submit(uid(c), pid(c), aid, "inspect", []byte(`{}`)); err != nil {
			fail(c, 503, "queue unavailable")
			return
		}
		ids = append(ids, aid)
	}
	c.JSON(202, gin.H{"submitted": ids})
}

// BatchAction 校验设备归属后批量提交电源操作任务。
func (h *Handler) BatchAction(c *gin.Context) {
	var in struct {
		AssetIDs []uint `json:"asset_ids"`
		Action   string `json:"action"`
	}
	if c.ShouldBindJSON(&in) != nil || len(in.AssetIDs) == 0 {
		fail(c, 400, "invalid batch action")
		return
	}
	if in.Action != "on" && in.Action != "off" && in.Action != "restart" {
		fail(c, 400, "invalid power action")
		return
	}
	ids := make([]uint, 0, len(in.AssetIDs))
	for _, aid := range in.AssetIDs {
		var a model.Asset
		if h.S.Store.DB.Where("project_id=? AND id=?", pid(c), aid).First(&a).Error != nil {
			fail(c, 404, "asset not found")
			return
		}
	}
	for _, aid := range in.AssetIDs {
		if _, err := h.S.Submit(uid(c), pid(c), aid, "power", []byte(fmt.Sprintf(`{"action":%q}`, in.Action))); err != nil {
			fail(c, 503, "queue unavailable")
			return
		}
		ids = append(ids, aid)
	}
	c.JSON(202, gin.H{"submitted": ids})
}
