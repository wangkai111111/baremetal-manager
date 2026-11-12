package model

import (
	"time"

	"gorm.io/gorm"
)

// User 表示可登录系统的用户及其角色。
type User struct {
	ID           uint       `json:"id" gorm:"primaryKey"`
	Username     string     `json:"username" gorm:"uniqueIndex;not null"`
	PasswordHash string     `json:"-" gorm:"not null"`
	Role         string     `json:"role" gorm:"not null"`
	Name         string     `json:"name" gorm:"size:200;comment:用户姓名"`
	Email        string     `json:"email" gorm:"size:255;comment:用户邮箱"`
	Status       string     `json:"status" gorm:"size:20;default:active;comment:用户状态"`
	LastLoginAt  *time.Time `json:"last_login_at,omitempty" gorm:"comment:最后登录时间"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
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
	ID                 uint           `json:"id" gorm:"primaryKey"`
	ProjectID          uint           `json:"project_id" gorm:"index;not null"`
	Name               string         `json:"name" gorm:"not null"`
	BMCAddress         string         `json:"bmc_address" gorm:"not null"`
	BMCUser            string         `json:"bmc_user"`
	BMCSecret          string         `json:"-"`
	IPAddress          string         `json:"ip_address"`
	MACAddress         string         `json:"mac_address"`
	Status             string         `json:"status"`
	SerialNumber       string         `json:"serial_number" gorm:"size:200;comment:序列号"`
	Manufacturer       string         `json:"manufacturer" gorm:"size:200;comment:制造商"`
	Model              string         `json:"model" gorm:"size:200;comment:型号"`
	DatacenterID       *uint          `json:"datacenter_id,omitempty" gorm:"index;comment:所属机房"`
	PurchaseDate       *time.Time     `json:"purchase_date,omitempty" gorm:"comment:采购日期"`
	WarrantyExpiry     *time.Time     `json:"warranty_expiry,omitempty" gorm:"comment:维保到期日期"`
	Vendor             string         `json:"vendor" gorm:"size:200;comment:供应商"`
	DepartmentID       *int64         `json:"department_id,omitempty" gorm:"index;comment:所属部门"`
	RedfishSnapshot    string         `json:"-" gorm:"type:jsonb;default:null;comment:Redfish硬件信息快照"`
	RedfishCollectedAt *time.Time     `json:"redfish_collected_at,omitempty" gorm:"comment:Redfish最近采集时间"`
	RedfishError       string         `json:"redfish_error,omitempty" gorm:"type:text;comment:Redfish最近采集错误"`
	CreatedAt          time.Time      `json:"created_at"`
	UpdatedAt          time.Time      `json:"updated_at"`
	DeletedAt          gorm.DeletedAt `json:"-" gorm:"index"`
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
	ID          uint      `json:"id" gorm:"primaryKey"`
	ProjectID   uint      `json:"project_id" gorm:"index"`
	Name        string    `json:"name"`
	CIDR        string    `json:"cidr" gorm:"column:cidr"`
	Gateway     string    `json:"gateway"`
	VlanID      *int      `json:"vlan_id,omitempty" gorm:"comment:VLAN ID"`
	DNSServers  string    `json:"dns_servers" gorm:"size:500;comment:DNS服务器地址"`
	DHCPEnabled bool      `json:"dhcp_enabled" gorm:"default:false;comment:DHCP开关"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
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
	ID               uint           `json:"id" gorm:"primaryKey"`
	ProjectID        uint           `json:"project_id" gorm:"index;not null"`
	AssetID          uint           `json:"asset_id" gorm:"index;not null"`
	Component        string         `json:"component" gorm:"not null"`
	Version          string         `json:"version" gorm:"not null"`
	Vendor           string         `json:"vendor"`
	ReleasedAt       *time.Time     `json:"released_at,omitempty"`
	Details          string         `json:"details" gorm:"type:jsonb"`
	ComponentType    string         `json:"component_type" gorm:"size:50;comment:组件类型"`
	UpgradeAvailable bool           `json:"upgrade_available" gorm:"default:false;comment:是否可升级"`
	TargetVersion    string         `json:"target_version" gorm:"size:100;comment:目标升级版本"`
	CreateBy         uint           `json:"create_by" gorm:"index;not null"`
	UpdateBy         uint           `json:"update_by" gorm:"index;not null"`
	CreatedAt        time.Time      `json:"created_at"`
	UpdatedAt        time.Time      `json:"updated_at"`
	DeletedAt        gorm.DeletedAt `json:"-" gorm:"index"`
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
	CreateBy    *uint          `json:"create_by,omitempty" gorm:"comment:创建人"`
	UpdateBy    *uint          `json:"update_by,omitempty" gorm:"comment:更新人"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
	DeletedAt   gorm.DeletedAt `json:"-" gorm:"index;comment:软删除"`
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

// ============================================================
// 以下为 004_mode_system.sql 新增模型
// ============================================================

// SystemMode 系统部署模式
type SystemMode struct {
	ID        int64     `json:"id" gorm:"primaryKey;column:id"`
	Mode      string    `json:"mode" gorm:"column:mode;not null"`
	Locked    bool      `json:"locked" gorm:"column:locked;not null;default:false"`
	CreatedAt time.Time `json:"created_at" gorm:"column:created_at"`
	UpdatedAt time.Time `json:"updated_at" gorm:"column:updated_at"`
}

func (SystemMode) TableName() string { return "system_modes" }

// Department 部门信息表（自用模式）
type Department struct {
	ID          uint           `json:"id" gorm:"primaryKey"`
	TenantID    int64          `json:"tenant_id" gorm:"column:tenant_id;not null;index"`
	Name        string         `json:"name" gorm:"column:name;size:128;not null"`
	ParentID    *int64         `json:"parent_id,omitempty" gorm:"column:parent_id;index"`
	Description string         `json:"description" gorm:"column:description;type:text"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
	DeletedAt   gorm.DeletedAt `json:"-" gorm:"index"`
}

// AssetDepreciation 资产折旧记录
type AssetDepreciation struct {
	ID                      uint           `json:"id" gorm:"primaryKey"`
	TenantID                int64          `json:"tenant_id" gorm:"column:tenant_id;not null;index"`
	AssetID                 uint           `json:"asset_id" gorm:"column:asset_id;not null;index"`
	DepreciationMethod      string         `json:"depreciation_method" gorm:"column:depreciation_method;size:32"`
	PurchaseCost            float64        `json:"purchase_cost" gorm:"column:purchase_cost"`
	SalvageValue            float64        `json:"salvage_value" gorm:"column:salvage_value"`
	UsefulLifeMonths        int            `json:"useful_life_months" gorm:"column:useful_life_months"`
	MonthlyDepreciation     float64        `json:"monthly_depreciation" gorm:"column:monthly_depreciation"`
	AccumulatedDepreciation float64        `json:"accumulated_depreciation" gorm:"column:accumulated_depreciation"`
	NetValue                float64        `json:"net_value" gorm:"column:net_value"`
	StartDate               *time.Time     `json:"start_date,omitempty" gorm:"column:start_date"`
	Status                  string         `json:"status" gorm:"column:status;size:16"`
	CreatedAt               time.Time      `json:"created_at"`
	UpdatedAt               time.Time      `json:"updated_at"`
	DeletedAt               gorm.DeletedAt `json:"-" gorm:"index"`
}

// TenantProfile 租户扩展档案（运营模式）
type TenantProfile struct {
	ID             uint           `json:"id" gorm:"primaryKey"`
	TenantID       int64          `json:"tenant_id" gorm:"column:tenant_id;not null;uniqueIndex"`
	ContactName    string         `json:"contact_name" gorm:"column:contact_name;size:128"`
	ContactPhone   string         `json:"contact_phone" gorm:"column:contact_phone;size:32"`
	ContactEmail   string         `json:"contact_email" gorm:"column:contact_email;size:255"`
	CompanyName    string         `json:"company_name" gorm:"column:company_name;size:256"`
	CompanyAddress string         `json:"company_address" gorm:"column:company_address;type:text"`
	IDNumber       string         `json:"id_number" gorm:"column:id_number;size:64"`
	Verified       bool           `json:"verified" gorm:"column:verified;not null;default:false"`
	VerifiedAt     *time.Time     `json:"verified_at,omitempty" gorm:"column:verified_at"`
	CreatedAt      time.Time      `json:"created_at"`
	UpdatedAt      time.Time      `json:"updated_at"`
	DeletedAt      gorm.DeletedAt `json:"-" gorm:"index"`
}

// Product 产品/型号上架表（运营模式）
type Product struct {
	ID           uint           `json:"id" gorm:"primaryKey"`
	TenantID     int64          `json:"tenant_id" gorm:"column:tenant_id;not null;index"`
	Name         string         `json:"name" gorm:"column:name;size:128;not null"`
	Description  string         `json:"description" gorm:"column:description;type:text"`
	SpecJSON     string         `json:"spec_json" gorm:"column:spec_json;type:jsonb"`
	PricingJSON  string         `json:"pricing_json" gorm:"column:pricing_json;type:jsonb"`
	BillingCycle string         `json:"billing_cycle" gorm:"column:billing_cycle;size:16"`
	Status       string         `json:"status" gorm:"column:status;size:16;not null;default:active"`
	CreatedAt    time.Time      `json:"created_at"`
	UpdatedAt    time.Time      `json:"updated_at"`
	DeletedAt    gorm.DeletedAt `json:"-" gorm:"index"`
}

// Order 订单记录（运营模式）
type Order struct {
	ID             uint           `json:"id" gorm:"primaryKey"`
	TenantID       int64          `json:"tenant_id" gorm:"column:tenant_id;not null;index"`
	UserID         int64          `json:"user_id" gorm:"column:user_id;not null;index"`
	Type           string         `json:"type" gorm:"column:type;size:16;not null"`
	ProductID      *int64         `json:"product_id,omitempty" gorm:"column:product_id;index"`
	AssetID        *int64         `json:"asset_id,omitempty" gorm:"column:asset_id;index"`
	Amount         float64        `json:"amount" gorm:"column:amount;not null;default:0"`
	DiscountAmount float64        `json:"discount_amount" gorm:"column:discount_amount;not null;default:0"`
	FinalAmount    float64        `json:"final_amount" gorm:"column:final_amount;not null;default:0"`
	Status         string         `json:"status" gorm:"column:status;size:16;not null;default:pending"`
	PaidAt         *time.Time     `json:"paid_at,omitempty" gorm:"column:paid_at"`
	CompletedAt    *time.Time     `json:"completed_at,omitempty" gorm:"column:completed_at"`
	CreatedAt      time.Time      `json:"created_at"`
	UpdatedAt      time.Time      `json:"updated_at"`
	DeletedAt      gorm.DeletedAt `json:"-" gorm:"index"`
}

// Bill 账单记录（运营模式）
type Bill struct {
	ID          uint           `json:"id" gorm:"primaryKey"`
	TenantID    int64          `json:"tenant_id" gorm:"column:tenant_id;not null;index"`
	PeriodStart *time.Time     `json:"period_start,omitempty" gorm:"column:period_start"`
	PeriodEnd   *time.Time     `json:"period_end,omitempty" gorm:"column:period_end"`
	TotalAmount float64        `json:"total_amount" gorm:"column:total_amount;not null;default:0"`
	Status      string         `json:"status" gorm:"column:status;size:16;not null;default:unpaid"`
	DetailJSON  string         `json:"detail_json" gorm:"column:detail_json;type:jsonb"`
	DueDate     *time.Time     `json:"due_date,omitempty" gorm:"column:due_date"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
	DeletedAt   gorm.DeletedAt `json:"-" gorm:"index"`
}

// Account 租户账户余额（运营模式）
type Account struct {
	ID           uint           `json:"id" gorm:"primaryKey"`
	TenantID     int64          `json:"tenant_id" gorm:"column:tenant_id;not null;uniqueIndex"`
	Balance      float64        `json:"balance" gorm:"column:balance;not null;default:0"`
	FrozenAmount float64        `json:"frozen_amount" gorm:"column:frozen_amount;not null;default:0"`
	CreditLimit  float64        `json:"credit_limit" gorm:"column:credit_limit;not null;default:0"`
	CreatedAt    time.Time      `json:"created_at"`
	UpdatedAt    time.Time      `json:"updated_at"`
	DeletedAt    gorm.DeletedAt `json:"-" gorm:"index"`
}

// Payment 支付记录（运营模式）
type Payment struct {
	ID            uint           `json:"id" gorm:"primaryKey"`
	AccountID     int64          `json:"account_id" gorm:"column:account_id;not null;index"`
	Amount        float64        `json:"amount" gorm:"column:amount;not null"`
	Method        string         `json:"method" gorm:"column:method;size:16"`
	Status        string         `json:"status" gorm:"column:status;size:16;not null;default:pending"`
	TransactionID string         `json:"transaction_id" gorm:"column:transaction_id;size:128"`
	PaidAt        *time.Time     `json:"paid_at,omitempty" gorm:"column:paid_at"`
	CreatedAt     time.Time      `json:"created_at"`
	UpdatedAt     time.Time      `json:"updated_at"`
	DeletedAt     gorm.DeletedAt `json:"-" gorm:"index"`
}

// Invoice 发票记录（运营模式）
type Invoice struct {
	ID        uint           `json:"id" gorm:"primaryKey"`
	TenantID  int64          `json:"tenant_id" gorm:"column:tenant_id;not null;index"`
	BillID    *int64         `json:"bill_id,omitempty" gorm:"column:bill_id;index"`
	Type      string         `json:"type" gorm:"column:type;size:16"`
	Title     string         `json:"title" gorm:"column:title;size:256"`
	TaxNumber string         `json:"tax_number" gorm:"column:tax_number;size:64"`
	Amount    float64        `json:"amount" gorm:"column:amount"`
	Status    string         `json:"status" gorm:"column:status;size:16;not null;default:requested"`
	IssuedAt  *time.Time     `json:"issued_at,omitempty" gorm:"column:issued_at"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `json:"-" gorm:"index"`
}

// Coupon 优惠券（运营模式）
type Coupon struct {
	ID          uint           `json:"id" gorm:"primaryKey"`
	TenantID    *int64         `json:"tenant_id,omitempty" gorm:"column:tenant_id;index"`
	Code        string         `json:"code" gorm:"column:code;size:64;uniqueIndex"`
	Name        string         `json:"name" gorm:"column:name;size:128"`
	Type        string         `json:"type" gorm:"column:type;size:16"`
	Value       float64        `json:"value" gorm:"column:value"`
	MinAmount   float64        `json:"min_amount" gorm:"column:min_amount;not null;default:0"`
	MaxDiscount *float64       `json:"max_discount,omitempty" gorm:"column:max_discount"`
	ValidFrom   *time.Time     `json:"valid_from,omitempty" gorm:"column:valid_from"`
	ValidUntil  *time.Time     `json:"valid_until,omitempty" gorm:"column:valid_until"`
	UsageLimit  *int           `json:"usage_limit,omitempty" gorm:"column:usage_limit"`
	UsedCount   int            `json:"used_count" gorm:"column:used_count;not null;default:0"`
	Status      string         `json:"status" gorm:"column:status;size:16;not null;default:active"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
	DeletedAt   gorm.DeletedAt `json:"-" gorm:"index"`
}

// WorkOrder 工单（两种模式通用）
type WorkOrder struct {
	ID          uint           `json:"id" gorm:"primaryKey"`
	TenantID    int64          `json:"tenant_id" gorm:"column:tenant_id;not null;index"`
	UserID      int64          `json:"user_id" gorm:"column:user_id;not null;index"`
	Type        string         `json:"type" gorm:"column:type;size:16;not null"`
	Title       string         `json:"title" gorm:"column:title;size:256;not null"`
	Description string         `json:"description" gorm:"column:description;type:text"`
	Status      string         `json:"status" gorm:"column:status;size:16;not null;default:open"`
	Priority    string         `json:"priority" gorm:"column:priority;size:16;not null;default:medium"`
	AssignedTo  *int64         `json:"assigned_to,omitempty" gorm:"column:assigned_to;index"`
	ResolvedAt  *time.Time     `json:"resolved_at,omitempty" gorm:"column:resolved_at"`
	ClosedAt    *time.Time     `json:"closed_at,omitempty" gorm:"column:closed_at"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
	DeletedAt   gorm.DeletedAt `json:"-" gorm:"index"`
}

// OperationAuditLog 增强审计日志
type OperationAuditLog struct {
	ID         int64     `json:"id" gorm:"primaryKey;column:id"`
	TenantID   *int64    `json:"tenant_id,omitempty" gorm:"column:tenant_id;index"`
	ProjectID  *int64    `json:"project_id,omitempty" gorm:"column:project_id;index"`
	UserID     *int64    `json:"user_id,omitempty" gorm:"column:user_id;index"`
	Action     string    `json:"action" gorm:"column:action;size:128;not null"`
	TargetType string    `json:"target_type" gorm:"column:target_type;size:64"`
	TargetID   *int64    `json:"target_id,omitempty" gorm:"column:target_id"`
	Detail     string    `json:"detail" gorm:"column:detail;type:jsonb"`
	ClientIP   string    `json:"client_ip" gorm:"column:client_ip;size:64"`
	UserAgent  string    `json:"user_agent" gorm:"column:user_agent;type:text"`
	Result     string    `json:"result" gorm:"column:result;size:16"`
	CreatedAt  time.Time `json:"created_at" gorm:"column:created_at"`
}

func (OperationAuditLog) TableName() string { return "operation_audit_logs" }

// KickstartTemplate 装机应答模板表。
// 说明：目标库 kickstart_templates 表（见 003_target_schema.sql）仅包含
// name / format / content / revision 等列，并不存在 template_content、
// variables、os_type、partition_scheme、post_script 列。为兼容既有代码与前端 JSON，
// 这里将 TemplateContent 映射到数据库 content 列，Format/Revision 对齐数据库；
// 其余富字段以 gorm:"-" 标注（不落到数据库列，避免查询不存在的列导致 SQL 报错），
// 供前端展示与渲染引擎在内存中使用。
type KickstartTemplate struct {
	ID              uint           `json:"id" gorm:"primaryKey"`
	Name            string         `json:"name" gorm:"size:200;not null;comment:模板名称"`
	Format          string         `json:"format" gorm:"column:format;size:20;comment:应答文件格式(kickstart/autoinstall/preseed/unattend)"`
	TemplateContent string         `json:"template_content" gorm:"column:content;type:text;comment:模板正文"`
	Revision        int            `json:"revision" gorm:"column:revision;default:1;comment:模板修订号"`
	Variables       string         `json:"variables" gorm:"-;comment:变量定义(JSON)"`
	OSType          string         `json:"os_type" gorm:"-;comment:操作系统类型"`
	PartitionScheme string         `json:"partition_scheme" gorm:"-;comment:分区方案"`
	PostScript      string         `json:"post_script" gorm:"-;comment:装机后执行脚本"`
	ProjectID       uint           `json:"project_id" gorm:"index;not null;comment:所属项目"`
	CreateBy        *uint          `json:"create_by,omitempty" gorm:"comment:创建人"`
	CreatedAt       time.Time      `json:"created_at"`
	UpdatedAt       time.Time      `json:"updated_at"`
	DeletedAt       gorm.DeletedAt `json:"-" gorm:"index;comment:软删除"`
}
