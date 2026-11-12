package service

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"baremetal-manager/backend/internal/model"

	"gorm.io/gorm"
)

func override(values map[string]string, key string) string {
	if values == nil {
		return ""
	}
	return strings.TrimSpace(values[key])
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func (p *ProvisioningService) loadAsset(ctx context.Context, sc InstallScope, assetID int64) (provisioningAsset, error) {
	if assetID <= 0 {
		return provisioningAsset{}, errors.New("provisioning: asset_id 无效")
	}
	var asset provisioningAsset
	q := p.DB.WithContext(ctx).Table("assets").Select("id, tenant_id, project_id, name, mac_address::text AS mac, management_ip::text AS ip, bmc_address::text AS bmc_address, bmc_user, bmc_protocol, bmc_secret_key_id, bmc_port, bmc_tls_verify, bmc_secret_ciphertext, NULL::bigint AS network_id").Where("id = ? AND deleted_at IS NULL", assetID)
	if sc.TenantID != 0 {
		q = q.Where("tenant_id = ?", sc.TenantID)
	}
	if sc.ProjectID != 0 {
		q = q.Where("project_id = ?", sc.ProjectID)
	}
	if err := q.First(&asset).Error; err != nil {
		return asset, fmt.Errorf("provisioning: 加载资产失败: %w", err)
	}
	return asset, nil
}

func (p *ProvisioningService) resolvePlan(ctx context.Context, sc InstallScope, templateID, imageID, assetNetworkID int64, overrides map[string]string) (provisioningPlan, error) {
	planID, _ := strconv.ParseInt(override(overrides, "install_plan_id"), 10, 64)
	var plan provisioningPlan
	q := p.DB.WithContext(ctx).Table("install_plans ip").Select("ip.id, ip.name, ip.kickstart_template_id, it.image_id, ip.network_id, ip.config, it.os AS image_os").Joins("JOIN image_templates it ON it.id = ip.image_template_id AND it.tenant_id = ip.tenant_id AND it.project_id = ip.project_id").Where("ip.deleted_at IS NULL")
	if planID > 0 {
		q = q.Where("ip.id = ?", planID)
	} else if templateID > 0 || imageID > 0 {
		q = q.Order("ip.id DESC")
	} else {
		q = q.Order("ip.id DESC")
	}
	if sc.TenantID != 0 {
		q = q.Where("ip.tenant_id = ?", sc.TenantID)
	}
	if sc.ProjectID != 0 {
		q = q.Where("ip.project_id = ?", sc.ProjectID)
	}
	var row struct {
		ID, KickstartTemplateID, ImageID, NetworkID int64
		Name                                        string
		Config                                      []byte
		ImageOS                                     string
	}
	if err := q.Scan(&row).Error; err != nil {
		return plan, fmt.Errorf("provisioning: 加载装机计划失败: %w", err)
	}
	if row.ID == 0 {
		return plan, errors.New("provisioning: 未找到可用装机计划")
	}
	plan = provisioningPlan{ID: row.ID, Name: row.Name, KickstartTemplateID: row.KickstartTemplateID, ImageID: row.ImageID, NetworkID: row.NetworkID}
	if len(row.Config) > 0 && string(row.Config) != "null" {
		if err := json.Unmarshal(row.Config, &plan.Config); err != nil {
			return plan, fmt.Errorf("provisioning: 装机计划配置非法: %w", err)
		}
	}
	if templateID > 0 {
		plan.KickstartTemplateID = templateID
	}
	if imageID > 0 {
		plan.ImageID = imageID
	}
	if plan.NetworkID == 0 {
		plan.NetworkID = assetNetworkID
	}
	if v := override(overrides, "network_id"); v != "" {
		plan.NetworkID, _ = strconv.ParseInt(v, 10, 64)
	}
	if plan.ImageID == 0 {
		return plan, errors.New("provisioning: 装机计划缺少镜像")
	}
	return plan, nil
}

func (p *ProvisioningService) loadTemplate(ctx context.Context, sc InstallScope, id int64) (model.KickstartTemplate, error) {
	if id <= 0 {
		return model.KickstartTemplate{}, errors.New("provisioning: 装机计划缺少应答模板")
	}
	var tpl model.KickstartTemplate
	q := p.DB.WithContext(ctx).Table("kickstart_templates").Where("id = ? AND deleted_at IS NULL", id)
	if sc.TenantID != 0 {
		q = q.Where("tenant_id = ?", sc.TenantID)
	}
	if sc.ProjectID != 0 {
		q = q.Where("project_id = ?", sc.ProjectID)
	}
	if err := q.First(&tpl).Error; err != nil {
		return tpl, fmt.Errorf("provisioning: 加载应答模板失败: %w", err)
	}
	return tpl, nil
}

func (p *ProvisioningService) pickISOName(ctx context.Context, sc InstallScope, imageID int64, plan provisioningPlan, overrides map[string]string) (string, string, error) {
	name := firstNonEmpty(override(overrides, "iso_file_name"), plan.Config.ISOFileName)
	osType := ""
	if imageID > 0 {
		var image struct{ Name, OSType, Status string }
		q := p.DB.WithContext(ctx).Table("images").Select("name, os_type, status").Where("id = ? AND deleted_at IS NULL", imageID)
		if sc.TenantID != 0 {
			q = q.Where("tenant_id = ?", sc.TenantID)
		}
		if sc.ProjectID != 0 {
			q = q.Where("project_id = ?", sc.ProjectID)
		}
		if err := q.Scan(&image).Error; err != nil {
			return "", "", fmt.Errorf("provisioning: 加载镜像失败: %w", err)
		}
		if image.Name == "" {
			return "", "", errors.New("provisioning: 镜像不存在")
		}
		if image.Status != "" && image.Status != "ready" {
			return "", "", fmt.Errorf("provisioning: 镜像未就绪: %s", image.Status)
		}
		osType = image.OSType
		if name == "" {
			name = image.Name
		}
	}
	iso, err := SanitizeISOName(name)
	if err != nil {
		return "", "", err
	}
	return iso, osType, nil
}

func (p *ProvisioningService) resolveNetwork(ctx context.Context, sc InstallScope, asset provisioningAsset, plan provisioningPlan, overrides map[string]string) (provisioningNetwork, string, string, string, error) {
	var n provisioningNetwork
	networkID := plan.NetworkID
	if v := override(overrides, "network_id"); v != "" {
		networkID, _ = strconv.ParseInt(v, 10, 64)
	}
	if networkID > 0 {
		var row struct {
			ID                         int64
			Name, Subnet, Gateway, DNS string
		}
		q := p.DB.WithContext(ctx).Table("networks").Select("id, name, subnet::text AS subnet, COALESCE(gateway::text, '') AS gateway, COALESCE(array_to_string(dns_servers, ' '), '') AS dns").Where("id = ? AND deleted_at IS NULL", networkID)
		if sc.TenantID != 0 {
			q = q.Where("tenant_id = ?", sc.TenantID)
		}
		if sc.ProjectID != 0 {
			q = q.Where("project_id = ?", sc.ProjectID)
		}
		if err := q.Scan(&row).Error; err != nil {
			return n, "", "", "", fmt.Errorf("provisioning: 加载网络失败: %w", err)
		}
		if row.ID == 0 {
			return n, "", "", "", errors.New("provisioning: 网络不存在")
		}
		n = provisioningNetwork{ID: row.ID, Name: row.Name, Subnet: row.Subnet, Gateway: row.Gateway, DNS: row.DNS}
	}
	ip := firstNonEmpty(override(overrides, "ip"), asset.IP)
	gateway := firstNonEmpty(override(overrides, "gateway"), plan.Config.Gateway, n.Gateway)
	netmask := ""
	if _, cidr, err := net.ParseCIDR(n.Subnet); err == nil {
		netmask = net.IP(cidr.Mask).String()
	}
	if ip == "" || netmask == "" || gateway == "" {
		return n, "", "", "", nil
	}
	return n, ip, netmask, gateway, nil
}

func (p *ProvisioningService) resolveRootPassword(ctx context.Context, sc InstallScope, overrides map[string]string) (string, bool, string, error) {
	plain := override(overrides, "root_password")
	if plain == "" && override(overrides, "root_password_encrypted") != "" {
		var err error
		plain, err = DecryptCredential(override(overrides, "root_password_encrypted"))
		if err != nil {
			return "", false, "", fmt.Errorf("provisioning: root 口令解密失败: %w", err)
		}
	}
	generated := plain == ""
	if generated {
		raw := make([]byte, 18)
		if _, err := rand.Read(raw); err != nil {
			return "", false, "", err
		}
		plain = base64URL(raw)
	}
	enc, err := EncryptCredential(plain)
	if err != nil {
		return "", false, "", fmt.Errorf("provisioning: root 口令加密失败: %w", err)
	}
	return plain, generated, enc, nil
}

func base64URL(b []byte) string {
	const alphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_"
	out := make([]byte, len(b))
	for i, v := range b {
		out[i] = alphabet[int(v)&63]
	}
	return string(out)
}

func (p *ProvisioningService) resolveInstallTimeout(plan provisioningPlan, overrides map[string]string) (time.Duration, error) {
	n := plan.Config.InstallTimeoutMin
	if v := override(overrides, "install_timeout_minutes"); v != "" {
		var err error
		n, err = strconv.Atoi(v)
		if err != nil {
			return 0, errors.New("provisioning: 安装超时参数非法")
		}
	}
	if n <= 0 {
		return defaultInstallTimeout, nil
	}
	if n > 24*60 {
		return 0, errors.New("provisioning: 安装超时超过 24 小时")
	}
	return time.Duration(n) * time.Minute, nil
}

func (p *ProvisioningService) writeSeedFile(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0750); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0600)
}

func (p *ProvisioningService) ensureOperation(ctx context.Context, sc InstallScope, assetID int64, overrides map[string]string) (int64, error) {
	if sc.UserID <= 0 {
		return 0, errors.New("provisioning: 装机操作缺少数据擦除确认人")
	}
	key := override(overrides, "idempotency_key")
	if key == "" {
		token, err := NewCallbackToken()
		if err != nil {
			return 0, err
		}
		key = "install/" + token
	}
	payload := map[string]string{}
	for _, name := range []string{"install_plan_id", "network_id", "hostname", "ip", "gateway", "dns", "iso_file_name", "install_timeout_minutes", "simulate_bmc"} {
		if value := override(overrides, name); value != "" {
			payload[name] = value
		}
	}
	if password := override(overrides, "root_password"); password != "" {
		encrypted, err := EncryptCredential(password)
		if err != nil {
			return 0, fmt.Errorf("provisioning: root 口令加密失败: %w", err)
		}
		payload["root_password_encrypted"] = encrypted
	} else if encrypted := override(overrides, "root_password_encrypted"); encrypted != "" {
		payload["root_password_encrypted"] = encrypted
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return 0, err
	}
	var row struct{ ID int64 }
	err = p.DB.WithContext(ctx).Raw(`
		INSERT INTO operations (tenant_id, project_id, asset_id, user_id, kind, idempotency_key, payload, status, started_at, create_by)
		VALUES (?, ?, ?, ?, 'install', ?, ?::jsonb, 'running', now(), ?)
		RETURNING id`, sc.TenantID, sc.ProjectID, assetID, sc.UserID, key, string(encoded), sc.UserID).Scan(&row).Error
	if err != nil {
		return 0, fmt.Errorf("provisioning: 创建装机操作失败: %w", err)
	}
	return row.ID, nil
}

func (p *ProvisioningService) createInstallation(ctx context.Context, sc InstallScope, asset provisioningAsset, plan provisioningPlan, tpl model.KickstartTemplate, vars InstallVars, seedDir, scriptPath, isoName, token string, timeout time.Duration, encPassword string, generatedRoot bool) (int64, error) {
	var operation struct {
		ID     int64
		UserID int64
	}
	if err := p.DB.WithContext(ctx).Table("operations").Select("id, user_id").
		Where("id = ? AND tenant_id = ? AND project_id = ? AND asset_id = ? AND kind = 'install' AND deleted_at IS NULL", sc.OperationID, sc.TenantID, sc.ProjectID, asset.ID).
		Take(&operation).Error; err != nil {
		return 0, fmt.Errorf("provisioning: 加载装机操作失败: %w", err)
	}
	confirmedBy := sc.UserID
	if confirmedBy == 0 {
		confirmedBy = operation.UserID
	}
	if confirmedBy <= 0 {
		return 0, errors.New("provisioning: 装机操作缺少数据擦除确认人")
	}
	snapshot, err := json.Marshal(map[string]any{
		"install_plan_id": plan.ID, "install_plan_name": plan.Name,
		"template_id": tpl.ID, "template_revision": tpl.Revision, "format": tpl.Format,
		"image_id": plan.ImageID, "network_id": plan.NetworkID,
		"hostname": vars.Hostname, "mac": vars.MAC, "ip": vars.IP,
		"netmask": vars.Netmask, "gateway": vars.Gateway, "dns": vars.DNS,
		"seed_dir": seedDir, "script_path": scriptPath, "iso_file_name": isoName,
		"timeout_seconds":         int(timeout / time.Second),
		"root_password_encrypted": encPassword, "generated_root_password": generatedRoot,
	})
	if err != nil {
		return 0, fmt.Errorf("provisioning: 生成装机快照失败: %w", err)
	}
	var row struct{ ID int64 }
	err = p.DB.WithContext(ctx).Raw(`
		INSERT INTO installations (tenant_id, project_id, asset_id, operation_id, install_plan_id, status,
			config_snapshot, callback_token_hash, callback_expires_at, confirmed_by, confirmed_at, create_by)
		VALUES (?, ?, ?, ?, ?, 'preparing', ?::jsonb, ?, ?, ?, now(), ?)
		RETURNING id`, sc.TenantID, sc.ProjectID, asset.ID, sc.OperationID, plan.ID,
		string(snapshot), CallbackTokenHash(token), time.Now().Add(defaultCallbackTTL), confirmedBy, confirmedBy).Scan(&row).Error
	if err != nil {
		return 0, fmt.Errorf("provisioning: 创建装机实例失败: %w", err)
	}
	return row.ID, nil
}

func (p *ProvisioningService) mergeOperationPayload(ctx context.Context, operationID int64, values map[string]any) {
	encoded, err := json.Marshal(values)
	if err == nil {
		err = p.DB.WithContext(ctx).Table("operations").Where("id = ? AND kind = 'install' AND deleted_at IS NULL", operationID).
			Update("payload", gorm.Expr("payload || ?::jsonb", string(encoded))).Error
	}
	if err != nil {
		log.Printf("provisioning: 更新装机操作参数失败 operation_id=%d: %v", operationID, err)
	}
}

func (p *ProvisioningService) recordInstallationEvent(ctx context.Context, sc InstallScope, installationID int64, stage, message, level string) error {
	return p.DB.WithContext(ctx).Exec(`
		INSERT INTO installation_events (tenant_id, project_id, installation_id, event_key, stage, message)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT (tenant_id, project_id, installation_id, event_key) WHERE deleted_at IS NULL DO NOTHING`,
		sc.TenantID, sc.ProjectID, installationID, "orchestrator/"+stage+"/"+level, stage, message).Error
}

func (p *ProvisioningService) markInstallation(ctx context.Context, installationID int64, status, message string, irreversible, completed bool) error {
	updates := map[string]any{"status": status, "updated_at": gorm.Expr("now()")}
	if irreversible {
		updates["irreversible_at"] = gorm.Expr("COALESCE(irreversible_at, now())")
		updates["started_at"] = gorm.Expr("COALESCE(started_at, now())")
	}
	if completed {
		updates["started_at"] = gorm.Expr("COALESCE(started_at, now())")
		updates["completed_at"] = gorm.Expr("COALESCE(completed_at, now())")
		updates["callback_revoked_at"] = gorm.Expr("COALESCE(callback_revoked_at, now())")
	}
	if status == "failed" || status == "unknown" {
		updates["error"] = message
	}
	return p.DB.WithContext(ctx).Table("installations").
		Where("id = ? AND deleted_at IS NULL AND status NOT IN ('succeeded', 'failed', 'cancelled')", installationID).
		Updates(updates).Error
}

func (p *ProvisioningService) failInstallation(ctx context.Context, installationID int64, message string) error {
	return p.markInstallation(ctx, installationID, "failed", message, false, true)
}

func mustMACWithColons(mac string) string { value, _ := MACWithColons(mac); return value }
func withPort(address string, port int) string {
	host := strings.TrimSpace(address)
	if strings.Contains(host, ":") && !strings.HasPrefix(host, "[") {
		return net.JoinHostPort(host, strconv.Itoa(port))
	}
	return net.JoinHostPort(host, strconv.Itoa(port))
}
