package repository

import (
	"strings"
	"time"

	"baremetal-manager/backend/internal/model"
	"baremetal-manager/backend/migrations"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// Store 封装数据库连接及项目权限和审计操作。
type Store struct{ DB *gorm.DB }

// Open 连接 PostgreSQL，串行迁移数据模型并写入中文表字段注释。
func Open(dsn string) (*Store, error) {
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		return nil, err
	}
	err = db.Transaction(func(tx *gorm.DB) error {
		// 事务级咨询锁防止多个 API、worker 与 scheduler 同时修改表结构。
		if e := tx.Exec("SELECT pg_advisory_xact_lock(684219731)").Error; e != nil {
			return e
		}
		// 早期 GORM 将 CIDR 命名为 c_id_r，先重命名以保留已有网络数据。
		for _, table := range []string{"networks", "ip_pools"} {
			if tx.Migrator().HasColumn(table, "c_id_r") && !tx.Migrator().HasColumn(table, "cidr") {
				if e := tx.Migrator().RenameColumn(table, "c_id_r", "cidr"); e != nil {
					return e
				}
			}
		}
		if e := tx.AutoMigrate(
			&model.User{}, &model.Project{}, &model.Membership{}, &model.Asset{},
			&model.SOLSession{}, &model.Operation{}, &model.Image{}, &model.Alert{},
			&model.Audit{}, &model.Network{}, &model.IPAllocation{},
			// 002_expansion 新增模型
			&model.Datacenter{}, &model.Rack{}, &model.Tag{}, &model.AssetTag{},
			&model.SensorRecord{}, &model.AlertRule{}, &model.AlertChannel{},
			&model.APIKey{}, &model.SystemSetting{}, &model.KickstartTemplate{},
		); e != nil {
			return e
		}
		if e := tx.Exec(migrations.InitialSQL).Error; e != nil {
			return e
		}
		// 执行 002 扩展迁移（新增机房、机柜、标签等表）
		return tx.Exec(migrations.ExpansionSQL).Error
	})
	if err != nil {
		return nil, err
	}
	return &Store{db}, nil
}

// ============================================================
// 通用分页
// ============================================================

// PageResult 通用分页结果
type PageResult struct {
	Items    interface{} `json:"items"`     // 数据列表
	Total    int64       `json:"total"`     // 总记录数
	Page     int         `json:"page"`      // 当前页码
	PageSize int         `json:"page_size"` // 每页条数
}

// Paginate 执行分页查询
// query: GORM 查询对象（已设置 WHERE 条件）
// page: 页码（从 1 开始）
// pageSize: 每页条数（默认 20，最大 100）
func (s *Store) Paginate(query *gorm.DB, page, pageSize int, dest interface{}) (*PageResult, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}

	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, err
	}

	offset := (page - 1) * pageSize
	if err := query.Offset(offset).Limit(pageSize).Find(dest).Error; err != nil {
		return nil, err
	}

	return &PageResult{
		Items:    dest,
		Total:    total,
		Page:     page,
		PageSize: pageSize,
	}, nil
}

// ============================================================
// 权限模型
// ============================================================

// 权限常量定义（resource:action 格式）
const (
	PermAssetsRead    = "assets:read"    // 资产查看
	PermAssetsWrite   = "assets:write"   // 资产编辑
	PermAssetsOperate = "assets:operate" // 资产操作（电源等）
	PermBMCOperate    = "bmc:operate"    // BMC 管理
	PermSOLAccess     = "sol:access"     // SOL 串口访问
	PermImagesRead    = "images:read"    // 镜像查看
	PermImagesWrite   = "images:write"   // 镜像编辑
	PermTasksRead     = "tasks:read"     // 任务查看
	PermTasksWrite    = "tasks:write"    // 任务编辑
	PermAlertsRead    = "alerts:read"    // 告警查看
	PermAlertsWrite   = "alerts:write"   // 告警编辑
	PermAlertsManage  = "alerts:manage"  // 告警管理
	PermNetworkRead   = "network:read"   // 网络查看
	PermNetworkWrite  = "network:write"  // 网络编辑
	PermFirmwareRead  = "firmware:read"  // 固件查看
	PermFirmwareWrite = "firmware:write" // 固件编辑
	PermIAMRead       = "iam:read"       // 身份认证查看
	PermIAMWrite      = "iam:write"      // 身份认证编辑
	PermAuditRead     = "audit:read"     // 审计日志查看
	PermSettingsRead  = "settings:read"  // 系统设置查看
	PermSettingsWrite = "settings:write" // 系统设置编辑
	PermProjectsRead  = "projects:read"  // 项目查看
	PermProjectsWrite = "projects:write" // 项目编辑
)

// RolePermissions 角色默认权限映射
var RolePermissions = map[string][]string{
	"admin": {"*"}, // 超级管理员拥有所有权限
	"manager": { // 项目管理员
		PermAssetsRead, PermAssetsWrite, PermAssetsOperate, PermBMCOperate, PermSOLAccess,
		PermImagesRead, PermImagesWrite, PermTasksRead, PermTasksWrite,
		PermAlertsRead, PermAlertsWrite, PermAlertsManage,
		PermNetworkRead, PermNetworkWrite, PermFirmwareRead, PermFirmwareWrite,
		PermIAMRead, PermAuditRead, PermProjectsRead, PermProjectsWrite,
	},
	"operator": { // 运维人员
		PermAssetsRead, PermAssetsOperate, PermBMCOperate, PermSOLAccess,
		PermImagesRead, PermTasksRead, PermAlertsRead,
		PermNetworkRead, PermFirmwareRead, PermAuditRead, PermProjectsRead,
	},
	"viewer": { // 只读用户
		PermAssetsRead, PermImagesRead, PermTasksRead, PermAlertsRead,
		PermNetworkRead, PermFirmwareRead, PermAuditRead, PermProjectsRead,
	},
}

// Role 返回用户在项目中的角色；系统管理员对所有项目拥有管理员角色。
func (s *Store) Role(userID, projectID uint) (string, error) {
	var u model.User
	if err := s.DB.First(&u, userID).Error; err != nil {
		return "", err
	}
	if u.Role == "admin" {
		return "admin", nil
	}
	var m model.Membership
	err := s.DB.Where("user_id = ? AND project_id = ?", userID, projectID).First(&m).Error
	return m.Role, err
}

// HasPermission 根据项目角色及显式权限判断用户是否获准执行指定操作。
// 支持 resource:action 格式权限字符串，支持 * 通配符匹配。
func (s *Store) HasPermission(userID, projectID uint, permission string) bool {
	role, err := s.Role(userID, projectID)
	if err != nil {
		return false
	}
	// admin 角色直接返回 true
	if role == "admin" {
		return true
	}

	// 检查角色默认权限
	if defaults, ok := RolePermissions[role]; ok {
		for _, p := range defaults {
			if p == "*" || p == permission {
				return true
			}
			// 支持 resource:* 通配符
			if strings.HasSuffix(p, ":*") {
				prefix := strings.TrimSuffix(p, ":*")
				if strings.HasPrefix(permission, prefix+":") {
					return true
				}
			}
		}
	}

	// 检查 Membership.Permissions 逗号分隔列表
	var m model.Membership
	if s.DB.Where("user_id = ? AND project_id = ?", userID, projectID).First(&m).Error != nil {
		return false
	}
	if m.Permissions != "" {
		for _, p := range strings.Split(m.Permissions, ",") {
			p = strings.TrimSpace(p)
			if p == "*" || p == permission {
				return true
			}
			// 支持 resource:* 通配符
			if strings.HasSuffix(p, ":*") {
				prefix := strings.TrimSuffix(p, ":*")
				if strings.HasPrefix(permission, prefix+":") {
					return true
				}
			}
		}
	}

	return false
}

// Audit 写入用户在项目中对指定目标执行的操作记录。
func (s *Store) Audit(userID, projectID uint, action, target string) {
	s.DB.Create(&model.Audit{UserID: userID, ProjectID: projectID, Action: action, Target: target})
}

// ============================================================
// Datacenter CRUD
// ============================================================

// ListDatacenters 分页列出项目下的机房列表
func (s *Store) ListDatacenters(projectID uint, page, pageSize int) (*PageResult, error) {
	var items []model.Datacenter
	query := s.DB.Where("project_id = ? AND deleted_at IS NULL", projectID).Order("id desc")
	return s.Paginate(query, page, pageSize, &items)
}

// GetDatacenter 根据 ID 获取机房详情
func (s *Store) GetDatacenter(id uint) (*model.Datacenter, error) {
	var dc model.Datacenter
	if err := s.DB.Where("id = ? AND deleted_at IS NULL", id).First(&dc).Error; err != nil {
		return nil, err
	}
	return &dc, nil
}

// CreateDatacenter 创建机房记录
func (s *Store) CreateDatacenter(dc *model.Datacenter) error {
	return s.DB.Create(dc).Error
}

// UpdateDatacenter 更新机房信息
func (s *Store) UpdateDatacenter(dc *model.Datacenter) error {
	return s.DB.Save(dc).Error
}

// DeleteDatacenter 软删除机房记录
func (s *Store) DeleteDatacenter(id uint) error {
	return s.DB.Where("id = ?", id).Delete(&model.Datacenter{}).Error
}

// ============================================================
// Rack CRUD
// ============================================================

// ListRacks 分页列出项目下的机柜列表，可按机房筛选
func (s *Store) ListRacks(projectID, datacenterID uint, page, pageSize int) (*PageResult, error) {
	var items []model.Rack
	query := s.DB.Where("project_id = ? AND deleted_at IS NULL", projectID)
	if datacenterID > 0 {
		query = query.Where("datacenter_id = ?", datacenterID)
	}
	query = query.Order("id desc")
	return s.Paginate(query, page, pageSize, &items)
}

// GetRack 根据 ID 获取机柜详情
func (s *Store) GetRack(id uint) (*model.Rack, error) {
	var rack model.Rack
	if err := s.DB.Where("id = ? AND deleted_at IS NULL", id).First(&rack).Error; err != nil {
		return nil, err
	}
	return &rack, nil
}

// CreateRack 创建机柜记录
func (s *Store) CreateRack(rack *model.Rack) error {
	return s.DB.Create(rack).Error
}

// UpdateRack 更新机柜信息
func (s *Store) UpdateRack(rack *model.Rack) error {
	return s.DB.Save(rack).Error
}

// DeleteRack 软删除机柜记录
func (s *Store) DeleteRack(id uint) error {
	return s.DB.Where("id = ?", id).Delete(&model.Rack{}).Error
}

// ============================================================
// Tag CRUD
// ============================================================

// ListTags 列出项目下的所有标签
func (s *Store) ListTags(projectID uint) ([]model.Tag, error) {
	var tags []model.Tag
	if err := s.DB.Where("project_id = ?", projectID).Order("name").Find(&tags).Error; err != nil {
		return nil, err
	}
	return tags, nil
}

// CreateTag 创建标签
func (s *Store) CreateTag(tag *model.Tag) error {
	return s.DB.Create(tag).Error
}

// UpdateTag 更新标签
func (s *Store) UpdateTag(tag *model.Tag) error {
	return s.DB.Save(tag).Error
}

// DeleteTag 删除标签
func (s *Store) DeleteTag(id uint) error {
	return s.DB.Delete(&model.Tag{}, id).Error
}

// ============================================================
// AssetTag 资产标签关联
// ============================================================

// AddAssetTag 为资产添加标签关联
func (s *Store) AddAssetTag(assetID, tagID uint) error {
	return s.DB.Create(&model.AssetTag{AssetID: assetID, TagID: tagID}).Error
}

// RemoveAssetTag 移除资产的标签关联
func (s *Store) RemoveAssetTag(assetID, tagID uint) error {
	return s.DB.Where("asset_id = ? AND tag_id = ?", assetID, tagID).Delete(&model.AssetTag{}).Error
}

// ListAssetTagsByAsset 查询资产关联的所有标签
func (s *Store) ListAssetTagsByAsset(assetID uint) ([]model.AssetTag, error) {
	var list []model.AssetTag
	if err := s.DB.Where("asset_id = ?", assetID).Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

// ListAssetTagsByTag 查询标签关联的所有资产
func (s *Store) ListAssetTagsByTag(tagID uint) ([]model.AssetTag, error) {
	var list []model.AssetTag
	if err := s.DB.Where("tag_id = ?", tagID).Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

// ============================================================
// SensorRecord 传感器数据
// ============================================================

// CreateSensorRecord 写入传感器采集记录
func (s *Store) CreateSensorRecord(record *model.SensorRecord) error {
	return s.DB.Create(record).Error
}

// ListSensorRecords 按资产、传感器类型和时间范围查询传感器记录
func (s *Store) ListSensorRecords(assetID uint, sensorType string, startTime, endTime *time.Time) ([]model.SensorRecord, error) {
	var records []model.SensorRecord
	query := s.DB.Where("asset_id = ?", assetID)
	if sensorType != "" {
		query = query.Where("sensor_type = ?", sensorType)
	}
	if startTime != nil {
		query = query.Where("collected_at >= ?", *startTime)
	}
	if endTime != nil {
		query = query.Where("collected_at <= ?", *endTime)
	}
	if err := query.Order("collected_at desc").Find(&records).Error; err != nil {
		return nil, err
	}
	return records, nil
}

// LatestSensorRecordsByAsset 获取资产最新的传感器记录（每种类型最新一条）
func (s *Store) LatestSensorRecordsByAsset(assetID uint) ([]model.SensorRecord, error) {
	var records []model.SensorRecord
	// 使用子查询获取每种传感器类型的最新记录
	err := s.DB.Raw(`
		SELECT sr.* FROM sensor_records sr
		INNER JOIN (
			SELECT sensor_type, MAX(collected_at) AS max_time
			FROM sensor_records WHERE asset_id = ? GROUP BY sensor_type
		) latest ON sr.sensor_type = latest.sensor_type AND sr.collected_at = latest.max_time
		WHERE sr.asset_id = ?
		ORDER BY sr.sensor_type
	`, assetID, assetID).Scan(&records).Error
	return records, err
}

// ============================================================
// AlertRule CRUD
// ============================================================

// ListAlertRules 列出项目下的所有告警规则
func (s *Store) ListAlertRules(projectID uint) ([]model.AlertRule, error) {
	var rules []model.AlertRule
	if err := s.DB.Where("project_id = ? AND deleted_at IS NULL", projectID).Order("id desc").Find(&rules).Error; err != nil {
		return nil, err
	}
	return rules, nil
}

// GetAlertRule 根据 ID 获取告警规则详情
func (s *Store) GetAlertRule(id uint) (*model.AlertRule, error) {
	var rule model.AlertRule
	if err := s.DB.Where("id = ? AND deleted_at IS NULL", id).First(&rule).Error; err != nil {
		return nil, err
	}
	return &rule, nil
}

// CreateAlertRule 创建告警规则
func (s *Store) CreateAlertRule(rule *model.AlertRule) error {
	return s.DB.Create(rule).Error
}

// UpdateAlertRule 更新告警规则
func (s *Store) UpdateAlertRule(rule *model.AlertRule) error {
	return s.DB.Save(rule).Error
}

// DeleteAlertRule 软删除告警规则
func (s *Store) DeleteAlertRule(id uint) error {
	return s.DB.Where("id = ?", id).Delete(&model.AlertRule{}).Error
}

// ============================================================
// AlertChannel CRUD
// ============================================================

// ListAlertChannels 列出项目下的所有告警渠道
func (s *Store) ListAlertChannels(projectID uint) ([]model.AlertChannel, error) {
	var channels []model.AlertChannel
	if err := s.DB.Where("project_id = ?", projectID).Order("id desc").Find(&channels).Error; err != nil {
		return nil, err
	}
	return channels, nil
}

// GetAlertChannel 根据 ID 获取告警渠道详情
func (s *Store) GetAlertChannel(id uint) (*model.AlertChannel, error) {
	var ch model.AlertChannel
	if err := s.DB.First(&ch, id).Error; err != nil {
		return nil, err
	}
	return &ch, nil
}

// CreateAlertChannel 创建告警渠道
func (s *Store) CreateAlertChannel(ch *model.AlertChannel) error {
	return s.DB.Create(ch).Error
}

// UpdateAlertChannel 更新告警渠道
func (s *Store) UpdateAlertChannel(ch *model.AlertChannel) error {
	return s.DB.Save(ch).Error
}

// DeleteAlertChannel 删除告警渠道
func (s *Store) DeleteAlertChannel(id uint) error {
	return s.DB.Delete(&model.AlertChannel{}, id).Error
}

// ============================================================
// APIKey 密钥管理
// ============================================================

// GetAPIKeyByHash 根据密钥哈希查询 API Key
func (s *Store) GetAPIKeyByHash(keyHash string) (*model.APIKey, error) {
	var key model.APIKey
	if err := s.DB.Where("key_hash = ? AND deleted_at IS NULL", keyHash).First(&key).Error; err != nil {
		return nil, err
	}
	return &key, nil
}

// CreateAPIKey 创建 API Key
func (s *Store) CreateAPIKey(key *model.APIKey) error {
	return s.DB.Create(key).Error
}

// ListAPIKeys 列出用户的所有 API Key
func (s *Store) ListAPIKeys(userID uint) ([]model.APIKey, error) {
	var keys []model.APIKey
	if err := s.DB.Where("user_id = ? AND deleted_at IS NULL", userID).Order("id desc").Find(&keys).Error; err != nil {
		return nil, err
	}
	return keys, nil
}

// DeleteAPIKey 软删除 API Key
func (s *Store) DeleteAPIKey(id uint) error {
	return s.DB.Where("id = ?", id).Delete(&model.APIKey{}).Error
}

// UpdateAPIKeyLastUsed 更新 API Key 最后使用时间
func (s *Store) UpdateAPIKeyLastUsed(id uint) {
	now := time.Now()
	s.DB.Model(&model.APIKey{}).Where("id = ?", id).Update("last_used_at", &now)
}

// ============================================================
// SystemSetting 系统设置
// ============================================================

// ListSystemSettings 列出所有系统设置
func (s *Store) ListSystemSettings() ([]model.SystemSetting, error) {
	var settings []model.SystemSetting
	if err := s.DB.Order("category, key").Find(&settings).Error; err != nil {
		return nil, err
	}
	return settings, nil
}

// GetSystemSettingByKey 根据键名获取系统设置
func (s *Store) GetSystemSettingByKey(key string) (*model.SystemSetting, error) {
	var setting model.SystemSetting
	if err := s.DB.Where("\"key\" = ?", key).First(&setting).Error; err != nil {
		return nil, err
	}
	return &setting, nil
}

// GetSystemSettingsByCategory 根据分类获取系统设置列表
func (s *Store) GetSystemSettingsByCategory(category string) ([]model.SystemSetting, error) {
	var settings []model.SystemSetting
	if err := s.DB.Where("category = ?", category).Order("\"key\"").Find(&settings).Error; err != nil {
		return nil, err
	}
	return settings, nil
}

// UpsertSystemSetting 创建或更新系统设置
func (s *Store) UpsertSystemSetting(setting *model.SystemSetting) error {
	var existing model.SystemSetting
	err := s.DB.Where("\"key\" = ?", setting.Key).First(&existing).Error
	if err == nil {
		// 已存在则更新
		existing.Value = setting.Value
		existing.Description = setting.Description
		existing.Category = setting.Category
		existing.UpdatedBy = setting.UpdatedBy
		existing.UpdatedAt = time.Now()
		return s.DB.Save(&existing).Error
	}
	// 不存在则创建
	return s.DB.Create(setting).Error
}

// ============================================================
// KickstartTemplate CRUD
// ============================================================

// ListKickstartTemplates 列出项目下的所有装机模板
func (s *Store) ListKickstartTemplates(projectID uint) ([]model.KickstartTemplate, error) {
	var templates []model.KickstartTemplate
	if err := s.DB.Where("project_id = ? AND deleted_at IS NULL", projectID).Order("id desc").Find(&templates).Error; err != nil {
		return nil, err
	}
	return templates, nil
}

// GetKickstartTemplate 根据 ID 获取装机模板详情
func (s *Store) GetKickstartTemplate(id uint) (*model.KickstartTemplate, error) {
	var tpl model.KickstartTemplate
	if err := s.DB.Where("id = ? AND deleted_at IS NULL", id).First(&tpl).Error; err != nil {
		return nil, err
	}
	return &tpl, nil
}

// CreateKickstartTemplate 创建装机模板
func (s *Store) CreateKickstartTemplate(tpl *model.KickstartTemplate) error {
	return s.DB.Create(tpl).Error
}

// UpdateKickstartTemplate 更新装机模板
func (s *Store) UpdateKickstartTemplate(tpl *model.KickstartTemplate) error {
	return s.DB.Save(tpl).Error
}

// DeleteKickstartTemplate 软删除装机模板
func (s *Store) DeleteKickstartTemplate(id uint) error {
	return s.DB.Where("id = ?", id).Delete(&model.KickstartTemplate{}).Error
}
