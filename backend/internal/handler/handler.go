package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"baremetal-manager/backend/internal/middleware"
	"baremetal-manager/backend/internal/model"
	"baremetal-manager/backend/internal/repository"
	"baremetal-manager/backend/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

// ============================================================
// 统一返回体
// ============================================================

// Response 统一成功返回结构
type Response struct {
	Code    int         `json:"code"`           // 状态码
	Data    interface{} `json:"data,omitempty"` // 数据
	Message string      `json:"message"`        // 消息
}

// ErrorResponse 统一错误返回结构
type ErrorResponse struct {
	Code    int    `json:"code"`    // 错误码
	Error   string `json:"error"`   // 错误类型
	Message string `json:"message"` // 错误描述
}

// OK 返回 200 成功响应
func OK(c *gin.Context, data interface{}) {
	c.JSON(200, Response{Code: 0, Data: data, Message: "success"})
}

// Created 返回 201 创建成功响应
func Created(c *gin.Context, data interface{}) {
	c.JSON(201, Response{Code: 0, Data: data, Message: "created"})
}

// PageOK 返回分页数据
func PageOK(c *gin.Context, result *repository.PageResult) {
	c.JSON(200, Response{Code: 0, Data: result, Message: "success"})
}

// Fail 返回统一错误响应
func Fail(c *gin.Context, httpCode int, errType, msg string) {
	c.JSON(httpCode, ErrorResponse{Code: httpCode, Error: errType, Message: msg})
}

// Handler 持有 HTTP 接口所需的服务、认证和对象存储客户端。
type Handler struct {
	S      *service.Service
	Auth   middleware.Auth
	Minio  *minio.Client
	Bucket string
}

// New 初始化 HTTP 处理器及对象存储客户端。
func New(s *service.Service) (*Handler, error) {
	endpoint := strings.TrimPrefix(strings.TrimPrefix(os.Getenv("MINIO_ENDPOINT"), "http://"), "https://")
	m, err := minio.New(endpoint, &minio.Options{Creds: credentials.NewStaticV4(os.Getenv("MINIO_ACCESS_KEY"), os.Getenv("MINIO_SECRET_KEY"), ""), Secure: strings.HasPrefix(os.Getenv("MINIO_ENDPOINT"), "https://")})
	if err != nil {
		return nil, err
	}
	return &Handler{S: s, Auth: middleware.Auth{Secret: []byte(service.RequiredSecret()), Store: s.Store}, Minio: m, Bucket: os.Getenv("MINIO_BUCKET")}, nil
}

// fail 返回包含错误消息的 JSON 响应。
func fail(c *gin.Context, code int, msg string) { c.JSON(code, gin.H{"message": msg}) }

// pid 从请求上下文取得当前项目 ID。
func pid(c *gin.Context) uint { return c.GetUint("projectID") }

// uid 从请求上下文取得登录用户 ID。
func uid(c *gin.Context) uint { return c.GetUint("userID") }

// id 将路由中的资源 ID 解析为无符号整数。
func id(c *gin.Context) uint { v, _ := strconv.ParseUint(c.Param("id"), 10, 64); return uint(v) }

// Router 注册健康检查、认证及项目资源路由。
func (h *Handler) Router() *gin.Engine {
	r := gin.Default()
	r.GET("/health", func(c *gin.Context) { c.String(200, "ok") })
	v := r.Group("/api/v1")
	v.POST("/auth/login", h.Login)

	// === 公开品牌信息，供登录页与侧边栏读取 ===
	RegisterBrandRoutes(v, h.S.Store)
	RegisterDocsRoutes(v)
	authorized := v.Group("")
	authorized.Use(h.Auth.Guard())
	authorized.GET("/auth/me", h.Me)
	authorized.PUT("/auth/password", h.ChangePassword)
	authorized.POST("/auth/users", h.CreateUser)
	authorized.GET("/projects", h.Projects)
	authorized.POST("/projects", h.CreateProject)
	p := authorized.Group("/projects/:projectId")
	p.Use(h.Auth.Project(false))
	p.GET("/assets", h.Assets)
	p.GET("/assets/:id", h.Asset)
	p.GET("/assets/:id/monitor", h.Monitor)
	p.GET("/assets/:id/sol", h.SOL)
	p.GET("/tasks", h.Tasks)
	p.GET("/tasks/:id", h.Task)
	p.GET("/images", h.Images)
	p.POST("/images", h.UploadImage)
	p.GET("/images/:id/download", h.DownloadImage)
	p.GET("/alerts", h.Alerts)
	p.GET("/audit", h.Audits)
	p.GET("/networks", h.Networks)
	p.GET("/ip-allocations", h.IPs)
	p.GET("/members", h.Members)
	p.GET("/sol-sessions", h.SOLSessions)
	w := authorized.Group("/projects/:projectId")
	w.Use(h.Auth.Project(true))
	w.POST("/assets", h.CreateAsset)
	w.PUT("/assets/:id", h.UpdateAsset)
	w.DELETE("/assets/:id", h.DeleteAsset)
	w.POST("/assets/:id/actions", h.Action)
	w.POST("/alerts/:id/resolve", h.ResolveAlert)
	w.POST("/networks", h.CreateNetwork)
	w.POST("/ip-allocations", h.CreateIP)
	w.POST("/members", h.AddMember)
	w.PUT("/members/:userId", updateMember(h.S.Store))
	w.DELETE("/members/:userId", removeMember(h.S.Store))
	w.POST("/assets/batch-actions", h.BatchAction)
	w.POST("/assets/scan", h.Scan)
	w.DELETE("/sol-sessions/:id", h.EndSOLSession)
	// 注册传感器数据路由
	RegisterSensorRoutes(p, h.S.Store)

	// === 机房管理路由 ===
	RegisterDatacenterRoutes(p, h.S.Store)

	// === 机柜管理路由 ===
	RegisterRackRoutes(p, h.S.Store)

	// === 标签管理路由 ===
	RegisterTagRoutes(p, h.S.Store)

	// === 装机模块路由 ===
	RegisterInstallRoutes(p, h.S.Store, h.S)

	// === 网络管理增强路由 ===
	RegisterNetworkExtRoutes(p, h.S.Store, h.S)

	// === 告警管理增强路由 ===
	RegisterAlertExtRoutes(p, h.S.Store)

	// === 固件管理路由 ===
	RegisterFirmwareRoutes(p, h.S.Store, h.S, h.Minio, h.Bucket)

	// === 镜像管理增强路由 ===
	RegisterImageExtRoutes(p, h.S.Store, h.Minio, h.Bucket)

	// === 资产管理增强路由 ===
	RegisterAssetExtRoutes(p, h.S.Store, h.S.BMC)

	// === 增强资产列表路由 ===
	RegisterEnhancedAssetListRoutes(p, h.S.Store)

	// === 系统设置路由（全局，不需要 projectId） ===
	RegisterSettingsRoutes(authorized, h.S.Store)

	// === 用户管理路由（全局，admin 专用） ===
	RegisterUserManagementRoutes(authorized, h.S.Store)

	// === 角色管理路由 ===
	RegisterRoleRoutes(authorized, h.S.Store)

	// === 开放 API 密钥管理路由 ===
	RegisterAPIKeyRoutes(authorized, h.S.Store)

	// === 开放 API 路由 ===
	apiKeyAuth := middleware.APIKeyAuth(h.S.Store)
	RegisterOpenAPIRoutes(r, h.S.Store, apiKeyAuth)

	return r
}

// Login 验证用户名和密码并返回 JWT 及用户项目列表。
func (h *Handler) Login(c *gin.Context) {
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if c.ShouldBindJSON(&body) != nil {
		fail(c, 400, "invalid request")
		return
	}
	var user model.User
	if h.S.Store.DB.Where("username = ?", body.Username).First(&user).Error != nil || bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(body.Password)) != nil {
		fail(c, 401, "invalid credentials")
		return
	}
	token, expires, err := h.Auth.Token(user.ID)
	if err != nil {
		fail(c, 500, "token failed")
		return
	}
	var members []model.Membership
	h.S.Store.DB.Where("user_id = ?", user.ID).Find(&members)
	projects := make([]uint, 0, len(members))
	for _, m := range members {
		projects = append(projects, m.ProjectID)
	}
	c.JSON(200, gin.H{"token": token, "token_type": "Bearer", "expires_at": expires, "user": gin.H{"id": user.ID, "username": user.Username, "role": user.Role, "project_ids": projects}})
}

// Me 返回当前登录用户的信息。
func (h *Handler) Me(c *gin.Context) {
	var u model.User
	h.S.Store.DB.First(&u, uid(c))
	c.JSON(200, u)
}

// Projects 列出当前用户可访问的项目。
func (h *Handler) Projects(c *gin.Context) {
	var user model.User
	if h.S.Store.DB.First(&user, uid(c)).Error != nil {
		fail(c, 401, "unauthorized")
		return
	}
	var list []model.Project
	if user.Role == "admin" {
		h.S.Store.DB.Find(&list)
	} else {
		h.S.Store.DB.Joins("JOIN memberships ON memberships.project_id = projects.id").Where("memberships.user_id = ?", uid(c)).Find(&list)
	}
	c.JSON(200, list)
}

// CreateProject 供系统管理员创建项目并自动加入项目管理员。
func (h *Handler) CreateProject(c *gin.Context) {
	var user model.User
	if h.S.Store.DB.First(&user, uid(c)).Error != nil || user.Role != "admin" {
		fail(c, 403, "forbidden")
		return
	}
	var p model.Project
	if c.ShouldBindJSON(&p) != nil || p.Name == "" {
		fail(c, 400, "invalid project")
		return
	}
	err := h.S.Store.DB.Transaction(func(tx *gorm.DB) error {
		if e := tx.Create(&p).Error; e != nil {
			return e
		}
		return tx.Create(&model.Membership{UserID: uid(c), ProjectID: p.ID, Role: "admin"}).Error
	})
	if err != nil {
		fail(c, 409, "project exists")
		return
	}
	h.S.Store.Audit(uid(c), p.ID, "project.create", p.Name)
	c.JSON(201, p)
}

// Assets 列出当前项目的设备。
func (h *Handler) Assets(c *gin.Context) {
	var a []model.Asset
	h.S.Store.DB.Where("project_id = ?", pid(c)).Order("id desc").Find(&a)
	c.JSON(200, a)
}

// Asset 返回当前项目中指定设备的信息。
func (h *Handler) Asset(c *gin.Context) {
	var a model.Asset
	if h.S.Store.DB.Where("project_id = ?", pid(c)).First(&a, id(c)).Error != nil {
		fail(c, 404, "asset not found")
		return
	}
	c.JSON(200, a)
}

// CreateAsset 创建项目设备并加密保存 BMC 凭据。
func (h *Handler) CreateAsset(c *gin.Context) {
	var input struct {
		model.Asset
		BMCSecretInput string `json:"bmc_secret"`
	}
	if c.ShouldBindJSON(&input) != nil || input.Name == "" || input.BMCAddress == "" {
		fail(c, 400, "invalid asset")
		return
	}
	a := input.Asset
	a.BMCSecret = input.BMCSecretInput
	a.ID = 0
	a.ProjectID = pid(c)
	if a.BMCSecret != "" {
		var err error
		a.BMCSecret, err = service.EncryptCredential(a.BMCSecret)
		if err != nil {
			fail(c, 500, err.Error())
			return
		}
	}
	if h.S.Store.DB.Create(&a).Error != nil {
		fail(c, 500, "create failed")
		return
	}
	h.S.Store.Audit(uid(c), pid(c), "asset.create", fmt.Sprint(a.ID))
	c.JSON(201, a)
}

// UpdateAsset 更新项目设备信息并加密新提交的 BMC 凭据。
func (h *Handler) UpdateAsset(c *gin.Context) {
	var a model.Asset
	if h.S.Store.DB.Where("project_id = ?", pid(c)).First(&a, id(c)).Error != nil {
		fail(c, 404, "asset not found")
		return
	}
	var input struct {
		Name       string `json:"name"`
		BMCAddress string `json:"bmc_address"`
		BMCUser    string `json:"bmc_user"`
		BMCSecret  string `json:"bmc_secret"`
		IPAddress  string `json:"ip_address"`
		MACAddress string `json:"mac_address"`
	}
	if c.ShouldBindJSON(&input) != nil {
		fail(c, 400, "invalid request")
		return
	}
	changes := map[string]any{"name": input.Name, "bmc_address": input.BMCAddress, "bmc_user": input.BMCUser, "ip_address": input.IPAddress, "mac_address": input.MACAddress}
	if input.BMCSecret != "" {
		secret, err := service.EncryptCredential(input.BMCSecret)
		if err != nil {
			fail(c, 500, err.Error())
			return
		}
		changes["bmc_secret"] = secret
	}
	if input.Name == "" || input.BMCAddress == "" {
		fail(c, 400, "invalid asset")
		return
	}
	h.S.Store.DB.Model(&a).Updates(changes)
	h.S.Store.Audit(uid(c), pid(c), "asset.update", fmt.Sprint(a.ID))
	c.JSON(200, a)
}

// DeleteAsset 软删除当前项目中的指定设备并记录审计日志。
func (h *Handler) DeleteAsset(c *gin.Context) {
	q := h.S.Store.DB.Where("project_id = ? AND id = ?", pid(c), id(c)).Delete(&model.Asset{})
	if q.RowsAffected == 0 {
		fail(c, 404, "asset not found")
		return
	}
	h.S.Store.Audit(uid(c), pid(c), "asset.delete", c.Param("id"))
	c.Status(204)
}

// Action 校验设备和操作类型后提交电源或检查任务。
func (h *Handler) Action(c *gin.Context) {
	var a model.Asset
	if h.S.Store.DB.Where("project_id = ?", pid(c)).First(&a, id(c)).Error != nil {
		fail(c, 404, "asset not found")
		return
	}
	var input struct {
		Kind    string `json:"kind"`
		Action  string `json:"action"`
		ImageID uint   `json:"image_id"`
	}
	if c.ShouldBindJSON(&input) != nil {
		fail(c, 400, "invalid request")
		return
	}
	if input.Kind != "power" && input.Kind != "inspect" && input.Kind != "reinstall" && input.Kind != "firmware" {
		fail(c, 400, "invalid action")
		return
	}
	if input.Kind == "power" && input.Action != "on" && input.Action != "off" && input.Action != "restart" {
		fail(c, 400, "invalid power action")
		return
	}
	if input.Kind == "reinstall" || input.Kind == "firmware" {
		fail(c, 501, "operation rejected: reinstall and firmware execution is not supported")
		return
	}
	data, _ := json.Marshal(input)
	op, err := h.S.Submit(uid(c), pid(c), a.ID, input.Kind, data)
	if err != nil {
		if strings.Contains(err.Error(), "not supported") {
			fail(c, 501, err.Error())
		} else {
			fail(c, 503, "queue unavailable")
		}
		return
	}
	c.JSON(202, op)
}

// Tasks 列出当前项目最近的设备操作任务。
func (h *Handler) Tasks(c *gin.Context) {
	var v []model.Operation
	h.S.Store.DB.Where("project_id = ?", pid(c)).Order("id desc").Limit(100).Find(&v)
	c.JSON(200, v)
}

// Task 返回当前项目中的指定设备操作任务。
func (h *Handler) Task(c *gin.Context) {
	var v model.Operation
	if h.S.Store.DB.Where("project_id = ?", pid(c)).First(&v, id(c)).Error != nil {
		fail(c, 404, "task not found")
		return
	}
	c.JSON(200, v)
}

// Monitor 实时读取指定设备的 BMC 状态并返回结构化数据。
func (h *Handler) Monitor(c *gin.Context) {
	var a model.Asset
	if h.S.Store.DB.Where("project_id = ?", pid(c)).First(&a, id(c)).Error != nil {
		fail(c, 404, "asset not found")
		return
	}
	secret, err := service.DecryptCredential(a.BMCSecret)
	if err != nil {
		fail(c, 500, "BMC credential unavailable")
		return
	}
	a.BMCSecret = secret
	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()
	info, err := h.S.BMC.Inspect(ctx, a)
	if err != nil {
		fail(c, 502, "BMC unavailable")
		return
	}
	// 将原始 Redfish 数据转换为结构化格式
	result := TransformMonitorData(info)
	// 补充传感器最新数据
	sensorRecords, _ := h.S.Store.LatestSensorRecordsByAsset(a.ID)
	sensors := make(map[string]interface{})
	for _, r := range sensorRecords {
		sensors[r.SensorType] = map[string]interface{}{
			"value":        r.Value,
			"unit":         r.Unit,
			"collected_at": r.CollectedAt,
		}
	}
	result.MergeMonitorSensors(sensors)
	OK(c, result)
}

// Images 列出当前项目的镜像元数据。
func (h *Handler) Images(c *gin.Context) {
	var v []model.Image
	h.S.Store.DB.Where("project_id = ?", pid(c)).Find(&v)
	c.JSON(200, v)
}

// UploadImage 上传镜像至对象存储并保存项目镜像记录。
func (h *Handler) UploadImage(c *gin.Context) {
	file, err := c.FormFile("file")
	if err != nil || file.Size > 20<<30 {
		fail(c, 400, "invalid file")
		return
	}
	f, err := file.Open()
	if err != nil {
		fail(c, 400, "invalid file")
		return
	}
	defer f.Close()
	key := fmt.Sprintf("%d/%s", pid(c), uuid.NewString())
	_, err = h.Minio.PutObject(c.Request.Context(), h.Bucket, key, f, file.Size, minio.PutObjectOptions{ContentType: "application/octet-stream"})
	if err != nil {
		fail(c, 502, "storage unavailable")
		return
	}
	image := model.Image{ProjectID: pid(c), Name: file.Filename, ObjectKey: key, Size: file.Size}
	if h.S.Store.DB.Create(&image).Error != nil {
		h.Minio.RemoveObject(c.Request.Context(), h.Bucket, key, minio.RemoveObjectOptions{})
		fail(c, 500, "create failed")
		return
	}
	h.S.Store.Audit(uid(c), pid(c), "image.upload", fmt.Sprint(image.ID))
	c.JSON(201, image)
}

// DownloadImage 从对象存储下载当前项目中的指定镜像。
func (h *Handler) DownloadImage(c *gin.Context) {
	var image model.Image
	if h.S.Store.DB.Where("project_id = ?", pid(c)).First(&image, id(c)).Error != nil {
		fail(c, 404, "image not found")
		return
	}
	obj, err := h.Minio.GetObject(c.Request.Context(), h.Bucket, image.ObjectKey, minio.GetObjectOptions{})
	if err != nil {
		fail(c, 502, "storage unavailable")
		return
	}
	defer obj.Close()
	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=%q", strings.ReplaceAll(image.Name, "\"", "")))
	c.DataFromReader(200, image.Size, "application/octet-stream", obj, nil)
}

// Alerts 列出当前项目最近的设备告警。
func (h *Handler) Alerts(c *gin.Context) {
	var v []model.Alert
	h.S.Store.DB.Where("project_id = ?", pid(c)).Order("id desc").Limit(200).Find(&v)
	c.JSON(200, v)
}

// ResolveAlert 将当前项目中的指定告警标记为已解决。
func (h *Handler) ResolveAlert(c *gin.Context) {
	q := h.S.Store.DB.Model(&model.Alert{}).Where("project_id = ? AND id = ?", pid(c), id(c)).Update("resolved", true)
	if q.RowsAffected == 0 {
		fail(c, 404, "alert not found")
		return
	}
	h.S.Store.Audit(uid(c), pid(c), "alert.resolve", c.Param("id"))
	c.Status(204)
}

// Audits 列出当前项目最近的操作审计记录。
func (h *Handler) Audits(c *gin.Context) {
	var v []model.Audit
	h.S.Store.DB.Where("project_id = ?", pid(c)).Order("id desc").Limit(200).Find(&v)
	c.JSON(200, v)
}

// Networks 列出当前项目的网络。
func (h *Handler) Networks(c *gin.Context) {
	var v []model.Network
	h.S.Store.DB.Where("project_id = ?", pid(c)).Find(&v)
	c.JSON(200, v)
}

// CreateNetwork 校验地址范围并创建项目网络。
func (h *Handler) CreateNetwork(c *gin.Context) {
	var v model.Network
	if c.ShouldBindJSON(&v) != nil || v.Name == "" || v.CIDR == "" {
		fail(c, 400, "invalid network")
		return
	}
	if _, err := netip.ParsePrefix(v.CIDR); err != nil {
		fail(c, 400, "invalid network")
		return
	}
	v.ID = 0
	v.ProjectID = pid(c)
	if h.S.Store.DB.Create(&v).Error != nil {
		fail(c, 500, "create failed")
		return
	}
	h.S.Store.Audit(uid(c), pid(c), "network.create", fmt.Sprint(v.ID))
	c.JSON(201, v)
}

// IPs 列出当前项目的 IP 地址分配记录。
func (h *Handler) IPs(c *gin.Context) {
	var v []model.IPAllocation
	h.S.Store.DB.Where("project_id = ?", pid(c)).Find(&v)
	c.JSON(200, v)
}

// CreateIP 校验地址属于项目网络及关联设备后创建分配记录。
func (h *Handler) CreateIP(c *gin.Context) {
	var v model.IPAllocation
	if c.ShouldBindJSON(&v) != nil || v.Address == "" {
		fail(c, 400, "invalid IP")
		return
	}
	var network model.Network
	if h.S.Store.DB.Where("project_id = ?", pid(c)).First(&network, v.NetworkID).Error != nil {
		fail(c, 404, "network not found")
		return
	}
	prefix, err := netip.ParsePrefix(network.CIDR)
	address, addressErr := netip.ParseAddr(v.Address)
	if err != nil || addressErr != nil || !prefix.Contains(address) {
		fail(c, 400, "IP outside network")
		return
	}
	if v.AssetID != 0 {
		var asset model.Asset
		if h.S.Store.DB.Where("project_id = ?", pid(c)).First(&asset, v.AssetID).Error != nil {
			fail(c, 404, "asset not found")
			return
		}
	}
	v.ID = 0
	v.ProjectID = pid(c)
	if h.S.Store.DB.Create(&v).Error != nil {
		fail(c, 409, "IP exists")
		return
	}
	h.S.Store.Audit(uid(c), pid(c), "ip.allocate", v.Address)
	c.JSON(201, v)
}

// Members 列出当前项目的成员及其权限。
func (h *Handler) Members(c *gin.Context) {
	var v []model.Membership
	h.S.Store.DB.Where("project_id = ?", pid(c)).Find(&v)
	c.JSON(200, v)
}

// AddMember 供项目管理员新增或更新项目成员及权限。
func (h *Handler) AddMember(c *gin.Context) {
	role, e := h.S.Store.Role(uid(c), pid(c))
	if e != nil || role != "admin" {
		fail(c, 403, "forbidden")
		return
	}
	var v model.Membership
	if c.ShouldBindJSON(&v) != nil || v.UserID == 0 || (v.Role != "admin" && v.Role != "operator" && v.Role != "viewer") || len(v.Permissions) > 500 {
		fail(c, 400, "invalid member")
		return
	}
	var user model.User
	if h.S.Store.DB.First(&user, v.UserID).Error != nil {
		fail(c, 404, "user not found")
		return
	}
	v.ProjectID = pid(c)
	if h.S.Store.DB.Save(&v).Error != nil {
		fail(c, 500, "save failed")
		return
	}
	h.S.Store.Audit(uid(c), pid(c), "member.upsert", fmt.Sprint(v.UserID))
	c.JSON(200, v)
}

// SOL 验证设备与令牌后建立限时 WebSocket 串口会话。
func (h *Handler) SOL(c *gin.Context) {
	var a model.Asset
	if h.S.Store.DB.Where("project_id = ?", pid(c)).First(&a, id(c)).Error != nil {
		fail(c, 404, "asset not found")
		return
	}
	var existing int64
	h.S.Store.DB.Model(&model.SOLSession{}).Where("asset_id = ? AND ended_at IS NULL AND expires_at > ?", a.ID, time.Now()).Count(&existing)
	if existing > 0 {
		fail(c, 409, "SOL session already active")
		return
	}
	secret, err := service.DecryptCredential(a.BMCSecret)
	if err != nil {
		fail(c, 500, "BMC credential unavailable")
		return
	}
	a.BMCSecret = secret
	protocols := websocket.Subprotocols(c.Request)
	if len(protocols) != 2 || protocols[0] != "bearer" {
		fail(c, 401, "unauthorized")
		return
	}
	tokenID, err := h.Auth.Verify(protocols[1])
	if err != nil || tokenID != uid(c) {
		fail(c, 401, "unauthorized")
		return
	}
	host := a.BMCAddress
	if net.ParseIP(host) == nil || net.ParseIP(host).IsLoopback() || net.ParseIP(host).IsUnspecified() || net.ParseIP(host).IsMulticast() {
		fail(c, 400, "invalid BMC address")
		return
	}
	conn, err := (&websocket.Upgrader{Subprotocols: []string{"bearer"}, CheckOrigin: func(r *http.Request) bool {
		origin := r.Header.Get("Origin")
		if origin == "" {
			return false
		}
		// 显式配置的来源优先放行
		if allowed := os.Getenv("SOL_ALLOWED_ORIGIN"); allowed != "" && origin == allowed {
			return true
		}
		// 通过 IP、127.0.0.1 或自定义域名访问时，Origin 与请求 Host 同源即放行
		parsed, err := url.Parse(origin)
		return err == nil && parsed.Host == r.Host
	}}).Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	session := model.SOLSession{ID: uuid.NewString(), ProjectID: pid(c), AssetID: a.ID, UserID: uid(c), StartedAt: time.Now(), ExpiresAt: time.Now().Add(15 * time.Minute)}
	if h.S.Store.DB.Create(&session).Error != nil {
		return
	}
	defer func() { now := time.Now(); h.S.Store.DB.Model(&session).Updates(map[string]any{"ended_at": &now}) }()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	// 先清理 BMC 端残留的 SOL 会话，避免 "already active on another session" 错误。
	deactivateCtx, deactivateCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer deactivateCancel()
	deactivateCmd := exec.CommandContext(deactivateCtx, "ipmitool", "-I", "lanplus", "-H", a.BMCAddress, "-U", a.BMCUser, "-E", "sol", "deactivate")
	deactivateCmd.Env = append(os.Environ(), "IPMI_PASSWORD="+a.BMCSecret)
	dErr := deactivateCmd.Run()
	if dErr != nil {
		log.Printf("[SOL][asset=%d] deactivate: %v", a.ID, dErr)
	}
	// 给 BMC 一点时间完成状态切换，避免紧随其后的 activate 失败
	time.Sleep(300 * time.Millisecond)
	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				conn.Close()
				return
			case <-ticker.C:
				var current model.SOLSession
				if h.S.Store.DB.First(&current, "id = ?", session.ID).Error != nil || current.EndedAt != nil {
					cancel()
					conn.Close()
					return
				}
			}
		}
	}()
	cmd := exec.CommandContext(ctx, "ipmitool", "-I", "lanplus", "-H", a.BMCAddress, "-U", a.BMCUser, "-E", "sol", "activate")
	cmd.Env = append(cmd.Environ(), "IPMI_PASSWORD="+a.BMCSecret)
	stdin, e := cmd.StdinPipe()
	if e != nil {
		log.Printf("[SOL][asset=%d] stdin pipe: %v", a.ID, e)
		return
	}
	stdout, e := cmd.StdoutPipe()
	if e != nil {
		log.Printf("[SOL][asset=%d] stdout pipe: %v", a.ID, e)
		return
	}
	stderr, _ := cmd.StderrPipe()
	if se := cmd.Start(); se != nil {
		log.Printf("[SOL][asset=%d] ipmitool start: %v", a.ID, se)
		return
	}
	// 丢弃 stderr（BMC 连接过程中的提示信息，不影响 SOL 数据流）
	go func() {
		buf := make([]byte, 4096)
		for {
			stderr.Read(buf)
		}
	}()
	go func() {
		if err := cmd.Wait(); err != nil && ctx.Err() == nil {
			log.Printf("[SOL][asset=%d] ipmitool: %v", a.ID, err)
		}
	}()
	go func() {
		buf := make([]byte, 4096)
		for {
			n, e := stdout.Read(buf)
			if n > 0 {
				if conn.WriteMessage(websocket.BinaryMessage, buf[:n]) != nil {
					cancel()
					return
				}
			}
			if e != nil {
				cancel()
				return
			}
		}
	}()
	conn.SetReadLimit(4096)
	for {
		_, data, e := conn.ReadMessage()
		if e != nil {
			break
		}
		if _, e = stdin.Write(data); e != nil {
			break
		}
	}
	cancel()
	stdin.Close()
}
