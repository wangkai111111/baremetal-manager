package model

import (
	"time"

	"gorm.io/gorm"
)

// User 表示可登录系统的用户及其角色。
type User struct {
	ID          uint       `json:"id" gorm:"primaryKey"`
	Username    string     `json:"username" gorm:"uniqueIndex;not null"`
	PasswordHash string   `json:"-" gorm:"not null"`
	Role        string     `json:"role" gorm:"not null"`
	Name        string     `json:"name" gorm:"size:200;comment:用户姓名"`
	Email       string     `json:"email" gorm:"size:255;comment:用户邮箱"`
	Status      string     `json:"status" gorm:"size:20;default:active;comment:用户状态"`
	LastLoginAt *time.Time `json:"last_login_at,omitempty" gorm:"comment:最后登录时间"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

// Project 表示用于隔离资源和成员的项目。
type Project struct {
	ID             uint       `json:"id" gorm:"primaryKey"`
	Name           string     `json:"name" gorm:"uniqueIndex;not null"`
	Description    string     `json:"description"`
	Owner          string     `json:"owner" gorm:"size:200;comment:负责人"`
	Notes          string     `json:"notes" gorm:"type:text;comment:备注"`
	WarrantyExpiry *time.Time `json:"warranty_expiry,omitempty" gorm:"comment:维保到期日期"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

// Membership 记录用户在项目中的角色和权限。
type Membership struct {
	UserID      uint      `json:"user_id" gorm:"primaryKey"`
	ProjectID   uint      `json:"project_id" gorm:"primaryKey"`
	Role        string    `json:"role" gorm:"not null"`
	Permissions string    `json:"permissions" gorm:"type:text"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// Asset 表示项目中的裸金属设备及其 BMC 连接信息。
type Asset struct {
	ID             uint           `json:"id" gorm:"primaryKey"`
	ProjectID      uint           `json:"project_id" gorm:"index;not null"`
	Name           string         `json:"name" gorm:"not null"`
	BMCAddress     string         `json:"bmc_address" gorm:"not null"`
	BMCUser        string         `json:"bmc_user"`
	BMCSecret      string         `json:"-"`
	IPAddress      string         `json:"ip_address"`
	MACAddress     string         `json:"mac_address"`
	Status         string         `json:"status"`
	SerialNumber   string         `json:"serial_number" gorm:"size:200;comment:序列号"`
	Manufacturer   string         `json:"manufacturer" gorm:"size:200;comment:制造商"`
	Model          string         `json:"model" gorm:"size:200;comment:型号"`
	DatacenterID   *uint          `json:"datacenter_id,omitempty" gorm:"index;comment:所属机房"`
	RackID         *uint          `json:"rack_id,omitempty" gorm:"index;comment:所属机柜"`
	RackUPosition  *int           `json:"rack_u_position,omitempty" gorm:"comment:U位位置"`
	PurchaseDate   *time.Time     `json:"purchase_date,omitempty" gorm:"comment:采购日期"`
	WarrantyExpiry *time.Time     `json:"warranty_expiry,omitempty" gorm:"comment:维保到期日期"`
	Vendor         string         `json:"vendor" gorm:"size:200;comment:供应商"`
	CreatedAt      time.Time      `json:"created_at"`
	UpdatedAt      time.Time      `json:"updated_at"`
	DeletedAt      gorm.DeletedAt `json:"-" gorm:"index"`
}

// SOLSession 记录设备串口远程会话及其有效期。
type SOLSession struct {
	ID        string     `json:"id" gorm:"primaryKey"`
	ProjectID uint       `json:"project_id" gorm:"index;not null"`
	AssetID   uint       `json:"asset_id" gorm:"index;not null"`
	UserID    uint       `json:"user_id" gorm:"index;not null"`
	StartedAt time.Time  `json:"started_at"`
	ExpiresAt time.Time  `json:"expires_at"`
	EndedAt   *time.Time `json:"ended_at,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
}

// Operation 记录针对设备提交的异步操作及执行状态。
type Operation struct {
	ID          uint       `json:"id" gorm:"primaryKey"`
	ProjectID   uint       `json:"project_id" gorm:"index"`
	AssetID     uint       `json:"asset_id"`
	UserID      uint       `json:"user_id"`
	Kind        string     `json:"kind"`
	Payload     string     `json:"-"`
	Status      string     `json:"status"`
	Error       string     `json:"error"`
	Attempts    int        `json:"attempts"`
	StartedAt   *time.Time `json:"started_at,omitempty" gorm:"comment:执行开始时间"`
	CompletedAt *time.Time `json:"completed_at,omitempty" gorm:"comment:执行完成时间"`
	Result      string     `json:"result" gorm:"type:jsonb;comment:执行结果（JSON）"`
	RetryCount  int        `json:"retry_count" gorm:"default:0;comment:重试次数"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

// Image 记录项目镜像在对象存储中的元数据。
type Image struct {
	ID        uint      `json:"id" gorm:"primaryKey"`
	ProjectID uint      `json:"project_id" gorm:"index"`
	Name      string    `json:"name"`
	ObjectKey string    `json:"-"`
	Size      int64     `json:"size"`
	SHA256    string    `json:"sha256" gorm:"size:64;comment:SHA256校验值"`
	Category  string    `json:"category" gorm:"size:50;comment:镜像分类"`
	OSType    string    `json:"os_type" gorm:"size:50;comment:操作系统类型"`
	Version   string    `json:"version" gorm:"size:100;comment:镜像版本号"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Alert 记录设备告警及其处理状态。
type Alert struct {
	ID             uint       `json:"id" gorm:"primaryKey"`
	ProjectID      uint       `json:"project_id" gorm:"index"`
	AssetID        uint       `json:"asset_id"`
	Severity       string     `json:"severity"`
	Message        string     `json:"message"`
	Resolved       bool       `json:"resolved"`
	RuleID         *uint      `json:"rule_id,omitempty" gorm:"index;comment:关联告警规则"`
	AcknowledgedAt *time.Time `json:"acknowledged_at,omitempty" gorm:"comment:告警确认时间"`
	AcknowledgedBy *uint      `json:"acknowledged_by,omitempty" gorm:"comment:告警确认用户"`
	ResolvedAt     *time.Time `json:"resolved_at,omitempty" gorm:"comment:告警解决时间"`
	ResolvedBy     *uint      `json:"resolved_by,omitempty" gorm:"comment:告警解决用户"`
	DedupKey       string     `json:"dedup_key" gorm:"size:255;index;comment:告警去重键"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

// Audit 记录用户对项目资源的操作审计信息。
type Audit struct {
	ID        uint      `json:"id" gorm:"primaryKey"`
	ProjectID uint      `json:"project_id" gorm:"index"`
	UserID    uint      `json:"user_id"`
	Action    string    `json:"action"`
	Target    string    `json:"target"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Network 表示项目网络及其地址范围和网关。
type Network struct {
	ID           uint      `json:"id" gorm:"primaryKey"`
	ProjectID    uint      `json:"project_id" gorm:"index"`
	Name         string    `json:"name"`
	CIDR         string    `json:"cidr" gorm:"column:cidr"`
	Gateway      string    `json:"gateway"`
	VlanID       *int      `json:"vlan_id,omitempty" gorm:"comment:VLAN ID"`
	DNSServers   string    `json:"dns_servers" gorm:"size:500;comment:DNS服务器地址"`
	DHCPEnabled  bool      `json:"dhcp_enabled" gorm:"default:false;comment:DHCP开关"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// IPAllocation 记录网络地址向设备的分配信息。
type IPAllocation struct {
	ID        uint           `json:"id" gorm:"primaryKey"`
	ProjectID uint           `json:"project_id" gorm:"index;not null"`
	NetworkID uint           `json:"network_id" gorm:"index;not null"`
	AssetID   uint           `json:"asset_id" gorm:"index"`
	Address   string         `json:"address" gorm:"uniqueIndex;not null"`
	CreateBy  uint           `json:"create_by" gorm:"index;not null"`
	UpdateBy  uint           `json:"update_by" gorm:"index;not null"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `json:"-" gorm:"index"`
}

// RackLabel 表示项目中附加到机架或机柜的标签。
type RackLabel struct {
	ID        uint           `json:"id" gorm:"primaryKey"`
	ProjectID uint           `json:"project_id" gorm:"index;not null"`
	RackName  string         `json:"rack_name" gorm:"not null"`
	Key       string         `json:"key" gorm:"not null"`
	Value     string         `json:"value" gorm:"not null"`
	CreateBy  uint           `json:"create_by" gorm:"index;not null"`
	UpdateBy  uint           `json:"update_by" gorm:"index;not null"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `json:"-" gorm:"index"`
}

// AssetHardware 记录设备上报的规范化硬件信息。
type AssetHardware struct {
	ID           uint           `json:"id" gorm:"primaryKey"`
	ProjectID    uint           `json:"project_id" gorm:"index;not null"`
	AssetID      uint           `json:"asset_id" gorm:"index;not null"`
	Category     string         `json:"category" gorm:"not null"`
	Name         string         `json:"name" gorm:"not null"`
	SerialNumber string         `json:"serial_number" gorm:"index"`
	Vendor       string         `json:"vendor"`
	Model        string         `json:"model"`
	Details      string         `json:"details" gorm:"type:jsonb"`
	CreateBy     uint           `json:"create_by" gorm:"index;not null"`
	UpdateBy     uint           `json:"update_by" gorm:"index;not null"`
	CreatedAt    time.Time      `json:"created_at"`
	UpdatedAt    time.Time      `json:"updated_at"`
	DeletedAt    gorm.DeletedAt `json:"-" gorm:"index"`
}

// FirmwareInventory 记录设备固件版本，不负责执行固件更新。
type FirmwareInventory struct {
	ID              uint           `json:"id" gorm:"primaryKey"`
	ProjectID       uint           `json:"project_id" gorm:"index;not null"`
	AssetID         uint           `json:"asset_id" gorm:"index;not null"`
	Component       string         `json:"component" gorm:"not null"`
	Version         string         `json:"version" gorm:"not null"`
	Vendor          string         `json:"vendor"`
	ReleasedAt      *time.Time     `json:"released_at,omitempty"`
	Details         string         `json:"details" gorm:"type:jsonb"`
	ComponentType   string         `json:"component_type" gorm:"size:50;comment:组件类型"`
	UpgradeAvailable bool          `json:"upgrade_available" gorm:"default:false;comment:是否可升级"`
	TargetVersion   string         `json:"target_version" gorm:"size:100;comment:目标升级版本"`
	CreateBy        uint           `json:"create_by" gorm:"index;not null"`
	UpdateBy        uint           `json:"update_by" gorm:"index;not null"`
	CreatedAt       time.Time      `json:"created_at"`
	UpdatedAt       time.Time      `json:"updated_at"`
	DeletedAt       gorm.DeletedAt `json:"-" gorm:"index"`
}

// IPPool 描述项目网络中可分配的地址范围。
type IPPool struct {
	ID        uint           `json:"id" gorm:"primaryKey"`
	ProjectID uint           `json:"project_id" gorm:"index;not null"`
	NetworkID uint           `json:"network_id" gorm:"index;not null"`
	Name      string         `json:"name" gorm:"not null"`
	CIDR      string         `json:"cidr" gorm:"column:cidr;not null"`
	Gateway   string         `json:"gateway"`
	CreateBy  uint           `json:"create_by" gorm:"index;not null"`
	UpdateBy  uint           `json:"update_by" gorm:"index;not null"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `json:"-" gorm:"index"`
}

// ImageTemplate 记录可供安装计划使用的镜像模板元数据。
type ImageTemplate struct {
	ID          uint           `json:"id" gorm:"primaryKey"`
	ProjectID   uint           `json:"project_id" gorm:"index;not null"`
	Name        string         `json:"name" gorm:"not null"`
	OS          string         `json:"os" gorm:"not null"`
	Version     string         `json:"version"`
	ImageID     uint           `json:"image_id" gorm:"index"`
	Description string         `json:"description"`
	Metadata    string         `json:"metadata" gorm:"type:jsonb"`
	CreateBy    uint           `json:"create_by" gorm:"index;not null"`
	UpdateBy    uint           `json:"update_by" gorm:"index;not null"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
	DeletedAt   gorm.DeletedAt `json:"-" gorm:"index"`
}

// InstallPlan 表示声明式安装计划，不负责执行安装。
type InstallPlan struct {
	ID          uint           `json:"id" gorm:"primaryKey"`
	ProjectID   uint           `json:"project_id" gorm:"index;not null"`
	Name        string         `json:"name" gorm:"not null"`
	TemplateID  uint           `json:"template_id" gorm:"index;not null"`
	Description string         `json:"description"`
	Config      string         `json:"config" gorm:"type:jsonb"`
	CreateBy    uint           `json:"create_by" gorm:"index;not null"`
	UpdateBy    uint           `json:"update_by" gorm:"index;not null"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
	DeletedAt   gorm.DeletedAt `json:"-" gorm:"index"`
}

// ============================================================
// 以下为 002_expansion.sql 新增模型
// ============================================================

// Datacenter 机房信息表
type Datacenter struct {
	ID          uint           `json:"id" gorm:"primaryKey"`
	Name        string         `json:"name" gorm:"size:200;not null;comment:机房名称"`
	Code        string         `json:"code" gorm:"size:50;comment:机房编码"`
	Location    string         `json:"location" gorm:"size:500;comment:位置"`
	Description string         `json:"description" gorm:"type:text;comment:描述"`
	ProjectID   uint           `json:"project_id" gorm:"index;not null;comment:所属项目"`
	Contact     string         `json:"contact" gorm:"size:200;comment:联系人"`
	Capacity    int            `json:"capacity" gorm:"default:0;comment:容量(机柜数)"`
	CreateBy    *uint          `json:"create_by,omitempty" gorm:"comment:创建人"`
	UpdateBy    *uint          `json:"update_by,omitempty" gorm:"comment:更新人"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
	DeletedAt   gorm.DeletedAt `json:"-" gorm:"index;comment:软删除"`
}

// Rack 机柜信息表
type Rack struct {
	ID           uint           `json:"id" gorm:"primaryKey"`
	Name         string         `json:"name" gorm:"size:200;not null;comment:机柜名称"`
	DatacenterID uint           `json:"datacenter_id" gorm:"index;not null;comment:所属机房"`
	ProjectID    uint           `json:"project_id" gorm:"index;not null;comment:所属项目"`
	HeightU      int            `json:"height_u" gorm:"default:42;comment:U位高度"`
	Location     string         `json:"location" gorm:"size:500;comment:机柜位置"`
	PowerCapacity string        `json:"power_capacity" gorm:"size:100;comment:供电容量"`
	CreateBy     *uint          `json:"create_by,omitempty" gorm:"comment:创建人"`
	UpdateBy     *uint          `json:"update_by,omitempty" gorm:"comment:更新人"`
	CreatedAt    time.Time      `json:"created_at"`
	UpdatedAt    time.Time      `json:"updated_at"`
	DeletedAt    gorm.DeletedAt `json:"-" gorm:"index;comment:软删除"`
}

// Tag 标签定义表
type Tag struct {
	ID        uint      `json:"id" gorm:"primaryKey"`
	Name      string    `json:"name" gorm:"size:100;not null;uniqueIndex:idx_tag_project_name;comment:标签名称"`
	Color     string    `json:"color" gorm:"size:20;comment:标签颜色"`
	ProjectID uint      `json:"project_id" gorm:"uniqueIndex:idx_tag_project_name;not null;comment:所属项目"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// AssetTag 资产与标签关联表
type AssetTag struct {
	AssetID uint `json:"asset_id" gorm:"primaryKey;comment:资产主键"`
	TagID   uint `json:"tag_id" gorm:"primaryKey;comment:标签主键"`
}

// SensorRecord 传感器历史数据表
type SensorRecord struct {
	ID          uint      `json:"id" gorm:"primaryKey"`
	AssetID     uint      `json:"asset_id" gorm:"index;not null;comment:所属资产"`
	SensorType  string    `json:"sensor_type" gorm:"size:50;not null;comment:传感器类型"`
	Value       float64   `json:"value" gorm:"comment:传感器数值"`
	Unit        string    `json:"unit" gorm:"size:20;comment:计量单位"`
	CollectedAt time.Time `json:"collected_at" gorm:"index;comment:采集时间"`
}

// AlertRule 告警规则表
type AlertRule struct {
	ID         uint           `json:"id" gorm:"primaryKey"`
	Name       string         `json:"name" gorm:"size:200;not null;comment:规则名称"`
	ProjectID  uint           `json:"project_id" gorm:"index;not null;comment:所属项目"`
	Metric     string         `json:"metric" gorm:"size:100;comment:监控指标"`
	Operator   string         `json:"operator" gorm:"size:10;comment:比较运算符"`
	Threshold  float64        `json:"threshold" gorm:"comment:告警阈值"`
	Severity   string         `json:"severity" gorm:"size:20;comment:告警等级"`
	Enabled    bool           `json:"enabled" gorm:"default:true;comment:是否启用"`
	TimeWindow int            `json:"time_window" gorm:"default:300;comment:生效时间窗口(秒)"`
	CreateBy   *uint          `json:"create_by,omitempty" gorm:"comment:创建人"`
	CreatedAt  time.Time      `json:"created_at"`
	UpdatedAt  time.Time      `json:"updated_at"`
	DeletedAt  gorm.DeletedAt `json:"-" gorm:"index;comment:软删除"`
}

// AlertChannel 告警推送渠道表
type AlertChannel struct {
	ID        uint      `json:"id" gorm:"primaryKey"`
	Name      string    `json:"name" gorm:"size:200;not null;comment:渠道名称"`
	Type      string    `json:"type" gorm:"size:20;comment:渠道类型(wechat/dingtalk/webhook/email)"`
	Config    string    `json:"config" gorm:"type:jsonb;comment:渠道配置(JSON)"`
	ProjectID uint      `json:"project_id" gorm:"index;not null;comment:所属项目"`
	Enabled   bool      `json:"enabled" gorm:"default:true;comment:是否启用"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// APIKey 开放API密钥表
type APIKey struct {
	ID          uint           `json:"id" gorm:"primaryKey"`
	Name        string         `json:"name" gorm:"size:200;not null;comment:密钥名称"`
	KeyHash     string         `json:"key_hash" gorm:"size:255;uniqueIndex;comment:密钥哈希值"`
	UserID      uint           `json:"user_id" gorm:"index;not null;comment:所属用户"`
	ProjectID   *uint          `json:"project_id,omitempty" gorm:"index;comment:所属项目(可为空)"`
	Permissions string         `json:"permissions" gorm:"type:text;comment:权限列表(逗号分隔)"`
	ExpiresAt   *time.Time     `json:"expires_at,omitempty" gorm:"comment:过期时间"`
	LastUsedAt  *time.Time     `json:"last_used_at,omitempty" gorm:"comment:最后使用时间"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
	DeletedAt   gorm.DeletedAt `json:"-" gorm:"index;comment:软删除"`
}

// SystemSetting 系统设置表
type SystemSetting struct {
	ID          uint      `json:"id" gorm:"primaryKey"`
	Key         string    `json:"key" gorm:"size:200;uniqueIndex;comment:设置键名"`
	Value       string    `json:"value" gorm:"type:text;comment:设置值"`
	Description string    `json:"description" gorm:"type:text;comment:设置描述"`
	Category    string    `json:"category" gorm:"size:100;comment:设置分类"`
	UpdatedBy   *uint     `json:"updated_by,omitempty" gorm:"comment:更新用户"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// KickstartTemplate 装机应答模板表
type KickstartTemplate struct {
	ID              uint           `json:"id" gorm:"primaryKey"`
	Name            string         `json:"name" gorm:"size:200;not null;comment:模板名称"`
	TemplateContent string         `json:"template_content" gorm:"type:text;comment:模板内容"`
	Variables       string         `json:"variables" gorm:"type:jsonb;comment:变量定义(JSON)"`
	OSType          string         `json:"os_type" gorm:"size:50;comment:操作系统类型"`
	PartitionScheme string         `json:"partition_scheme" gorm:"type:text;comment:分区方案"`
	PostScript      string         `json:"post_script" gorm:"type:text;comment:装机后执行脚本"`
	ProjectID       uint           `json:"project_id" gorm:"index;not null;comment:所属项目"`
	CreateBy        *uint          `json:"create_by,omitempty" gorm:"comment:创建人"`
	CreatedAt       time.Time      `json:"created_at"`
	UpdatedAt       time.Time      `json:"updated_at"`
	DeletedAt       gorm.DeletedAt `json:"-" gorm:"index;comment:软删除"`
}
