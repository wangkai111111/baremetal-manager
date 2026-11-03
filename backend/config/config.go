package config

import (
	"os"
	"strconv"
)

// Config 应用配置
type Config struct {
	Server   ServerConfig   `yaml:"server"`
	Database DatabaseConfig `yaml:"database"`
	Redis    RedisConfig    `yaml:"redis"`
	MinIO    MinIOConfig    `yaml:"minio"`
	JWT      JWTConfig      `yaml:"jwt"`
	BMC      BMCConfig      `yaml:"bmc"`
	SOL      SOLConfig      `yaml:"sol"`
	Alert    AlertConfig    `yaml:"alert"`
	PXE      PXEConfig      `yaml:"pxe"`
}

// ServerConfig 服务器配置
type ServerConfig struct {
	Port int    `yaml:"port"`
	Mode string `yaml:"mode"` // debug / release
}

// DatabaseConfig 数据库配置
type DatabaseConfig struct {
	URL          string `yaml:"url"`
	MaxOpenConns int    `yaml:"max_open_conns"`
	MaxIdleConns int    `yaml:"max_idle_conns"`
}

// RedisConfig Redis 配置
type RedisConfig struct {
	URL string `yaml:"url"`
}

// MinIOConfig MinIO 对象存储配置
type MinIOConfig struct {
	Endpoint  string `yaml:"endpoint"`
	AccessKey string `yaml:"access_key"`
	SecretKey string `yaml:"secret_key"`
	Bucket    string `yaml:"bucket"`
	UseSSL    bool   `yaml:"use_ssl"`
}

// JWTConfig JWT 认证配置
type JWTConfig struct {
	Secret     string `yaml:"secret"`
	ExpiryHour int    `yaml:"expiry_hour"` // Token 有效期（小时）
}

// BMCConfig BMC 管理配置
type BMCConfig struct {
	Timeout       int  `yaml:"timeout"`         // Redfish 请求超时（秒）
	MaxConcurrent int  `yaml:"max_concurrent"`  // BMC 并发限流
	TLSSkipVerify bool `yaml:"tls_skip_verify"` // 是否跳过 TLS 证书验证
}

// SOLConfig SOL 串口配置
type SOLConfig struct {
	AllowedOrigin  string `yaml:"allowed_origin"`   // 允许的跨域来源
	SessionTimeout int    `yaml:"session_timeout"`  // 会话超时（分钟）
	LogEnabled     bool   `yaml:"log_enabled"`      // 是否启用日志
}

// AlertConfig 告警配置
type AlertConfig struct {
	DeduplicateWindow int `yaml:"deduplicate_window"` // 去重窗口（秒）
	DefaultThreshold  struct {
		CPUTemp    float64 `yaml:"cpu_temp"`    // CPU 温度阈值
		FanSpeed   float64 `yaml:"fan_speed"`   // 风扇转速阈值
		DiskHealth float64 `yaml:"disk_health"` // 磁盘健康阈值
	} `yaml:"default_threshold"`
}

// PXEConfig PXE 网络引导配置
type PXEConfig struct {
	DHCPRange string `yaml:"dhcp_range"` // DHCP 地址范围
	TFTPRoot  string `yaml:"tftp_root"`  // TFTP 根目录
	HTTPBase  string `yaml:"http_base"`  // HTTP 基础地址
}

// Load 从环境变量加载配置（保持与现有 .env 兼容）
func Load() *Config {
	cfg := &Config{}

	// 服务器配置
	cfg.Server.Port = envInt("SERVER_PORT", 8000)
	cfg.Server.Mode = envStr("GIN_MODE", "release")

	// 数据库配置
	cfg.Database.URL = envStr("DATABASE_URL", "")
	cfg.Database.MaxOpenConns = envInt("DB_MAX_OPEN_CONNS", 25)
	cfg.Database.MaxIdleConns = envInt("DB_MAX_IDLE_CONNS", 5)

	// Redis 配置
	cfg.Redis.URL = envStr("REDIS_URL", "redis://redis:6379/0")

	// MinIO 配置
	cfg.MinIO.Endpoint = envStr("MINIO_ENDPOINT", "minio:9000")
	cfg.MinIO.AccessKey = envStr("MINIO_ACCESS_KEY", "")
	cfg.MinIO.SecretKey = envStr("MINIO_SECRET_KEY", "")
	cfg.MinIO.Bucket = envStr("MINIO_BUCKET", "baremetal-images")

	// JWT 配置
	cfg.JWT.Secret = envStr("JWT_SECRET", "")
	cfg.JWT.ExpiryHour = envInt("JWT_EXPIRY_HOUR", 12)

	// BMC 配置
	cfg.BMC.Timeout = envInt("BMC_TIMEOUT", 12)
	cfg.BMC.MaxConcurrent = envInt("BMC_MAX_CONCURRENT", 10)
	cfg.BMC.TLSSkipVerify = envBool("BMC_TLS_SKIP_VERIFY", true)

	// SOL 配置
	cfg.SOL.AllowedOrigin = envStr("SOL_ALLOWED_ORIGIN", "http://localhost:8080")
	cfg.SOL.SessionTimeout = envInt("SOL_SESSION_TIMEOUT", 15)
	cfg.SOL.LogEnabled = envBool("SOL_LOG_ENABLED", true)

	// 告警配置
	cfg.Alert.DeduplicateWindow = envInt("ALERT_DEDUPLICATE_WINDOW", 300)
	cfg.Alert.DefaultThreshold.CPUTemp = envFloat("ALERT_DEFAULT_CPU_TEMP", 85.0)
	cfg.Alert.DefaultThreshold.FanSpeed = envFloat("ALERT_DEFAULT_FAN_SPEED", 1000.0)
	cfg.Alert.DefaultThreshold.DiskHealth = envFloat("ALERT_DEFAULT_DISK_HEALTH", 90.0)

	// PXE 配置
	cfg.PXE.DHCPRange = envStr("PXE_DHCP_RANGE", "10.0.0.100-10.0.0.200")
	cfg.PXE.TFTPRoot = envStr("PXE_TFTP_ROOT", "/var/lib/tftpboot")
	cfg.PXE.HTTPBase = envStr("PXE_HTTP_BASE", "http://localhost:8080")

	return cfg
}

// envStr 读取环境变量，不存在时返回默认值
func envStr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// envInt 读取环境变量并解析为整数，解析失败时返回默认值
func envInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}

// envBool 读取环境变量并解析为布尔值，解析失败时返回默认值
func envBool(key string, fallback bool) bool {
	if v := os.Getenv(key); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	return fallback
}

// envFloat 读取环境变量并解析为浮点数，解析失败时返回默认值
func envFloat(key string, fallback float64) float64 {
	if v := os.Getenv(key); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return fallback
}
