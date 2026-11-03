package task

// 任务类型常量
const (
	TypePowerOn         = "power.on"          // 开机
	TypePowerOff        = "power.off"         // 关机
	TypePowerRestart    = "power.restart"     // 重启
	TypeInspect         = "inspect"           // 硬件检查
	TypeReinstall       = "reinstall"         // 系统重装
	TypeFirmwareUpgrade = "firmware.upgrade"  // 固件升级
	TypeScanNetwork     = "scan.network"      // 网段扫描
	TypeBatchAction     = "batch.action"      // 批量操作
)

// PowerAction 电源操作参数
type PowerAction struct {
	AssetID uint   `json:"asset_id"` // 资产 ID
	Action  string `json:"action"`   // on/off/restart
}

// InspectAction 硬件检查参数
type InspectAction struct {
	AssetID uint `json:"asset_id"` // 资产 ID
}

// ReinstallAction 装机参数
type ReinstallAction struct {
	AssetID    uint `json:"asset_id"`    // 资产 ID
	TemplateID uint `json:"template_id"` // Kickstart 模板 ID
	ImageID    uint `json:"image_id"`    // 镜像 ID
}

// FirmwareUpgradeAction 固件升级参数
type FirmwareUpgradeAction struct {
	AssetIDs []uint `json:"asset_ids"` // 目标资产 ID 列表
	FileKey  string `json:"file_key"`  // MinIO 文件 key
}

// ScanNetworkAction 网段扫描参数
type ScanNetworkAction struct {
	ProjectID uint   `json:"project_id"` // 项目 ID
	CIDR      string `json:"cidr"`       // 扫描网段
}

// BatchAction 批量操作参数
type BatchAction struct {
	AssetIDs []uint `json:"asset_ids"` // 目标资产 ID 列表
	Action   string `json:"action"`    // 操作类型
}
