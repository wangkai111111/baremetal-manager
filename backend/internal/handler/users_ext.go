package handler

import (
	"baremetal-manager/backend/internal/model"
	"baremetal-manager/backend/internal/repository"
	"fmt"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// RegisterUserManagementRoutes 注册用户管理路由（全局，admin 专用）
// 路由前缀: /api/v1/users
func RegisterUserManagementRoutes(rg *gin.RouterGroup, store *repository.Store) {
	users := rg.Group("/users")
	{
		users.GET("", listUsers(store))         // 用户列表（支持分页）
		users.PUT("/:id", updateUser(store))    // 更新用户信息
		users.DELETE("/:id", deleteUser(store)) // 删除用户
	}
}

// RegisterRoleRoutes 注册角色管理路由
// 路由前缀: /api/v1/roles
func RegisterRoleRoutes(rg *gin.RouterGroup, store *repository.Store) {
	roles := rg.Group("/roles")
	{
		roles.GET("", listRoles(store))                       // 角色列表（预定义角色 + 权限矩阵）
		roles.GET("/permissions", getPermissionMatrix(store)) // 获取完整权限矩阵
	}
}

// ============================================================
// 用户管理
// ============================================================

// listUsers 分页查询系统用户列表（仅 admin 可访问）
func listUsers(store *repository.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 校验管理员权限
		if !requireAdmin(c, store) {
			return
		}

		// 解析分页参数
		page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
		pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))

		// 处理搜索参数（模糊匹配用户名、姓名、邮箱）
		search := c.Query("search")

		var users []model.User
		query := store.DB.Model(&model.User{}).Order("id asc")
		if search != "" {
			likePattern := "%" + search + "%"
			query = query.Where("username LIKE ? OR name LIKE ? OR email LIKE ?", likePattern, likePattern, likePattern)
		}
		result, err := store.Paginate(query, page, pageSize, &users)
		if err != nil {
			Fail(c, http.StatusInternalServerError, "DB_ERROR", "查询用户列表失败")
			return
		}

		// 清除密码哈希，避免泄露
		for i := range users {
			users[i].PasswordHash = ""
		}

		PageOK(c, result)
	}
}

// updateUser 更新用户信息（仅 admin 可访问）
func updateUser(store *repository.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 校验管理员权限
		if !requireAdmin(c, store) {
			return
		}

		// 解析用户 ID
		userID, err := strconv.ParseUint(c.Param("id"), 10, 64)
		if err != nil || userID == 0 {
			Fail(c, http.StatusBadRequest, "INVALID_PARAM", "无效的用户ID")
			return
		}

		// 查询目标用户
		var user model.User
		if err := store.DB.First(&user, userID).Error; err != nil {
			Fail(c, http.StatusNotFound, "NOT_FOUND", "用户不存在")
			return
		}

		// 绑定可更新字段
		var input struct {
			Name   string `json:"name"`
			Email  string `json:"email"`
			Role   string `json:"role"`
			Status string `json:"status"`
		}
		if err := c.ShouldBindJSON(&input); err != nil {
			Fail(c, http.StatusBadRequest, "INVALID_REQUEST", "请求参数错误")
			return
		}

		// 构造更新字段
		updates := map[string]interface{}{}
		if input.Name != "" {
			updates["name"] = input.Name
		}
		if input.Email != "" {
			updates["email"] = input.Email
		}
		if input.Role != "" {
			// 仅允许 admin 和 user 两种角色
			if input.Role != "admin" && input.Role != "user" {
				input.Role = "user"
			}
			updates["role"] = input.Role
		}
		if input.Status != "" {
			// 校验状态值合法性
			if input.Status != "active" && input.Status != "disabled" {
				input.Status = "active"
			}
			updates["status"] = input.Status
		}

		if len(updates) > 0 {
			if err := store.DB.Model(&user).Updates(updates).Error; err != nil {
				Fail(c, http.StatusInternalServerError, "DB_ERROR", "更新用户失败")
				return
			}
		}

		// 重新查询返回最新数据
		store.DB.First(&user, userID)
		user.PasswordHash = ""

		OK(c, user)
	}
}

// deleteUser 删除用户（仅 admin 可访问）
func deleteUser(store *repository.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 校验管理员权限
		currentUserID := requireAdminUserID(c, store)
		if currentUserID == 0 {
			return
		}

		// 解析目标用户 ID
		targetID, err := strconv.ParseUint(c.Param("id"), 10, 64)
		if err != nil || targetID == 0 {
			Fail(c, http.StatusBadRequest, "INVALID_PARAM", "无效的用户ID")
			return
		}

		// 不允许删除自己
		if uint(targetID) == currentUserID {
			Fail(c, http.StatusBadRequest, "INVALID_REQUEST", "不能删除当前登录用户")
			return
		}

		// 查询目标用户
		var user model.User
		if err := store.DB.First(&user, targetID).Error; err != nil {
			Fail(c, http.StatusNotFound, "NOT_FOUND", "用户不存在")
			return
		}

		// 物理删除用户及其所有项目成员关系
		if err := store.DB.Transaction(func(tx *gorm.DB) error {
			// 删除成员关系
			tx.Where("user_id = ?", targetID).Delete(&model.Membership{})
			// 删除用户
			return tx.Delete(&model.User{}, targetID).Error
		}); err != nil {
			Fail(c, http.StatusInternalServerError, "DB_ERROR", "删除用户失败")
			return
		}

		c.Status(http.StatusNoContent)
	}
}

// ============================================================
// 角色管理
// ============================================================

// listRoles 返回预定义角色列表及其权限
func listRoles(store *repository.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 构建角色列表
		type roleInfo struct {
			Name        string   `json:"name"`        // 角色名称
			Permissions []string `json:"permissions"` // 权限列表
			Description string   `json:"description"` // 角色描述
		}

		descriptions := map[string]string{
			"admin":    "超级管理员，拥有所有权限",
			"manager":  "项目管理员，拥有项目内全部管理权限",
			"operator": "运维人员，拥有设备操作和查看权限",
			"viewer":   "只读用户，仅拥有查看权限",
		}

		roles := make([]roleInfo, 0, len(repository.RolePermissions))
		for name, perms := range repository.RolePermissions {
			roles = append(roles, roleInfo{
				Name:        name,
				Permissions: perms,
				Description: descriptions[name],
			})
		}

		OK(c, roles)
	}
}

// getPermissionMatrix 返回完整的权限矩阵（所有权限定义）
func getPermissionMatrix(store *repository.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 权限定义列表
		type permDef struct {
			Code        string `json:"code"`        // 权限代码
			Description string `json:"description"` // 权限描述
		}

		matrix := []permDef{
			{repository.PermAssetsRead, "资产查看"},
			{repository.PermAssetsWrite, "资产编辑"},
			{repository.PermAssetsOperate, "资产操作（电源等）"},
			{repository.PermBMCOperate, "BMC 管理"},
			{repository.PermSOLAccess, "SOL 串口访问"},
			{repository.PermImagesRead, "镜像查看"},
			{repository.PermImagesWrite, "镜像编辑"},
			{repository.PermTasksRead, "任务查看"},
			{repository.PermTasksWrite, "任务编辑"},
			{repository.PermAlertsRead, "告警查看"},
			{repository.PermAlertsWrite, "告警编辑"},
			{repository.PermAlertsManage, "告警管理"},
			{repository.PermNetworkRead, "网络查看"},
			{repository.PermNetworkWrite, "网络编辑"},
			{repository.PermFirmwareRead, "固件查看"},
			{repository.PermFirmwareWrite, "固件编辑"},
			{repository.PermIAMRead, "身份认证查看"},
			{repository.PermIAMWrite, "身份认证编辑"},
			{repository.PermAuditRead, "审计日志查看"},
			{repository.PermSettingsRead, "系统设置查看"},
			{repository.PermSettingsWrite, "系统设置编辑"},
			{repository.PermProjectsRead, "项目查看"},
			{repository.PermProjectsWrite, "项目编辑"},
		}

		OK(c, gin.H{
			"permissions": matrix,
			"roles":       repository.RolePermissions,
		})
	}
}

// ============================================================
// 项目成员管理
// ============================================================

// listMembers 查询项目成员列表
func listMembers(store *repository.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 从路由参数获取项目 ID
		projectID, err := strconv.ParseUint(c.Param("projectId"), 10, 64)
		if err != nil || projectID == 0 {
			Fail(c, http.StatusBadRequest, "INVALID_PARAM", "无效的项目ID")
			return
		}

		var members []model.Membership
		if err := store.DB.Where("project_id = ?", projectID).Find(&members).Error; err != nil {
			Fail(c, http.StatusInternalServerError, "DB_ERROR", "查询成员列表失败")
			return
		}

		// 查询关联的用户信息
		if len(members) > 0 {
			userIDs := make([]uint, 0, len(members))
			for _, m := range members {
				userIDs = append(userIDs, m.UserID)
			}

			var users []model.User
			store.DB.Where("id IN ?", userIDs).Find(&users)

			// 构建用户信息映射
			userMap := make(map[uint]model.User)
			for _, u := range users {
				u.PasswordHash = ""
				userMap[u.ID] = u
			}

			// 组装返回结果，附带用户详情
			type memberInfo struct {
				model.Membership
				UserInfo model.User `json:"user_info"` // 用户基本信息
			}

			result := make([]memberInfo, 0, len(members))
			for _, m := range members {
				info := memberInfo{Membership: m}
				if u, ok := userMap[m.UserID]; ok {
					info.UserInfo = u
				}
				result = append(result, info)
			}

			OK(c, result)
			return
		}

		OK(c, []model.Membership{})
	}
}

// addOrUpdateMember 添加或更新项目成员
func addOrUpdateMember(store *repository.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 从路由参数获取项目 ID
		projectID, err := strconv.ParseUint(c.Param("projectId"), 10, 64)
		if err != nil || projectID == 0 {
			Fail(c, http.StatusBadRequest, "INVALID_PARAM", "无效的项目ID")
			return
		}

		// 校验当前用户的项目管理员权限
		userID := c.GetUint("userID")
		role, _ := store.Role(userID, uint(projectID))
		if role != "admin" {
			Fail(c, http.StatusForbidden, "FORBIDDEN", "仅项目管理员可管理成员")
			return
		}

		var input struct {
			UserID      uint   `json:"user_id"`
			Role        string `json:"role"`
			Permissions string `json:"permissions"`
		}
		if err := c.ShouldBindJSON(&input); err != nil {
			Fail(c, http.StatusBadRequest, "INVALID_REQUEST", "请求参数错误")
			return
		}

		// 校验参数
		if input.UserID == 0 {
			Fail(c, http.StatusBadRequest, "INVALID_REQUEST", "用户ID不能为空")
			return
		}
		if input.Role != "admin" && input.Role != "operator" && input.Role != "viewer" {
			Fail(c, http.StatusBadRequest, "INVALID_REQUEST", "角色必须为 admin、operator 或 viewer")
			return
		}
		if len(input.Permissions) > 500 {
			Fail(c, http.StatusBadRequest, "INVALID_REQUEST", "权限列表过长")
			return
		}

		// 校验目标用户存在
		var targetUser model.User
		if err := store.DB.First(&targetUser, input.UserID).Error; err != nil {
			Fail(c, http.StatusNotFound, "NOT_FOUND", "目标用户不存在")
			return
		}

		// 创建或更新成员关系
		membership := model.Membership{
			UserID:      input.UserID,
			ProjectID:   uint(projectID),
			Role:        input.Role,
			Permissions: input.Permissions,
		}

		if err := store.DB.Save(&membership).Error; err != nil {
			Fail(c, http.StatusInternalServerError, "DB_ERROR", "保存成员关系失败")
			return
		}

		// 记录审计日志
		store.Audit(userID, uint(projectID), "member.upsert", fmt.Sprint(input.UserID))

		OK(c, membership)
	}
}

// updateMember 更新项目成员的角色或权限。
func updateMember(store *repository.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		projectID, err := strconv.ParseUint(c.Param("projectId"), 10, 64)
		if err != nil || projectID == 0 {
			Fail(c, http.StatusBadRequest, "INVALID_PARAM", "无效的项目ID")
			return
		}

		// 仅项目管理员可调整成员权限
		userID := c.GetUint("userID")
		role, _ := store.Role(userID, uint(projectID))
		if role != "admin" {
			Fail(c, http.StatusForbidden, "FORBIDDEN", "仅项目管理员可管理成员")
			return
		}

		targetID, err := strconv.ParseUint(c.Param("userId"), 10, 64)
		if err != nil || targetID == 0 {
			Fail(c, http.StatusBadRequest, "INVALID_PARAM", "无效的用户ID")
			return
		}

		var input struct {
			Role        string `json:"role"`
			Permissions string `json:"permissions"`
		}
		if err := c.ShouldBindJSON(&input); err != nil {
			Fail(c, http.StatusBadRequest, "INVALID_REQUEST", "请求参数错误")
			return
		}

		updates := map[string]interface{}{}
		if input.Role != "" {
			if input.Role != "admin" && input.Role != "operator" && input.Role != "viewer" {
				Fail(c, http.StatusBadRequest, "INVALID_REQUEST", "角色必须为 admin、operator 或 viewer")
				return
			}
			updates["role"] = input.Role
		}
		if input.Permissions != "" {
			updates["permissions"] = input.Permissions
		}
		if len(updates) == 0 {
			Fail(c, http.StatusBadRequest, "INVALID_REQUEST", "没有需要更新的字段")
			return
		}

		result := store.DB.Model(&model.Membership{}).
			Where("user_id = ? AND project_id = ?", targetID, projectID).
			Updates(updates)
		if result.Error != nil {
			Fail(c, http.StatusInternalServerError, "DB_ERROR", "更新成员失败")
			return
		}
		if result.RowsAffected == 0 {
			Fail(c, http.StatusNotFound, "NOT_FOUND", "成员关系不存在")
			return
		}

		store.Audit(userID, uint(projectID), "member.update", fmt.Sprint(targetID))
		OK(c, gin.H{"user_id": targetID, "project_id": projectID})
	}
}

// removeMember 移除项目成员
func removeMember(store *repository.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 从路由参数获取项目 ID
		projectID, err := strconv.ParseUint(c.Param("projectId"), 10, 64)
		if err != nil || projectID == 0 {
			Fail(c, http.StatusBadRequest, "INVALID_PARAM", "无效的项目ID")
			return
		}

		// 校验当前用户的项目管理员权限
		userID := c.GetUint("userID")
		role, _ := store.Role(userID, uint(projectID))
		if role != "admin" {
			Fail(c, http.StatusForbidden, "FORBIDDEN", "仅项目管理员可管理成员")
			return
		}

		// 解析目标用户 ID
		targetID, err := strconv.ParseUint(c.Param("userId"), 10, 64)
		if err != nil || targetID == 0 {
			Fail(c, http.StatusBadRequest, "INVALID_PARAM", "无效的用户ID")
			return
		}

		// 不允许移除自己
		if uint(targetID) == userID {
			Fail(c, http.StatusBadRequest, "INVALID_REQUEST", "不能移除当前登录用户")
			return
		}

		// 删除成员关系
		result := store.DB.Where("user_id = ? AND project_id = ?", targetID, projectID).Delete(&model.Membership{})
		if result.RowsAffected == 0 {
			Fail(c, http.StatusNotFound, "NOT_FOUND", "成员关系不存在")
			return
		}

		// 记录审计日志
		store.Audit(userID, uint(projectID), "member.remove", fmt.Sprint(targetID))

		c.Status(http.StatusNoContent)
	}
}

// ============================================================
// 辅助函数
// ============================================================

// requireAdmin 校验当前用户是否为管理员，失败时写入响应并返回 false
func requireAdmin(c *gin.Context, store *repository.Store) bool {
	return requireAdminUserID(c, store) != 0
}

// requireAdminUserID 校验管理员身份并返回用户 ID，失败时返回 0
func requireAdminUserID(c *gin.Context, store *repository.Store) uint {
	userID := c.GetUint("userID")
	var user model.User
	if err := store.DB.First(&user, userID).Error; err != nil {
		Fail(c, http.StatusUnauthorized, "UNAUTHORIZED", "用户不存在")
		return 0
	}
	if user.Role != "admin" {
		Fail(c, http.StatusForbidden, "FORBIDDEN", "仅管理员可执行此操作")
		return 0
	}
	return userID
}
