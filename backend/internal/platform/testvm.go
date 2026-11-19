package platform

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"baremetal-manager/backend/config"
	"baremetal-manager/backend/internal/pxeservice"
	"baremetal-manager/backend/internal/service"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

var errTestVMLabRequired = errors.New("测试 VM 请通过 PXE 实验室操作，不能使用物理机 BMC 接口")

func isTestVMIdentity(manufacturer, model, mac string) bool {
	return manufacturer == "BMM Test VM" && model == "QEMU PXE Lab" && strings.EqualFold(mac, pxeservice.TestVMMAC)
}

func testVMOwner(state pxeservice.TestVMStatus, tenantID, projectID int64, required bool) error {
	if tenantID <= 0 || projectID <= 0 {
		return errors.New("必须选择有效租户和项目")
	}
	if state.TenantID == 0 && state.ProjectID == 0 && !required {
		return nil
	}
	if state.TenantID != tenantID || state.ProjectID != projectID {
		return errors.New("测试虚机未创建或已归属其他租户/项目，禁止访问")
	}
	return nil
}

func testVMCall(ctx context.Context, method, path string, cfg *pxeservice.TestVMConfig) (pxeservice.TestVMStatus, int, error) {
	var state pxeservice.TestVMStatus
	var body []byte
	if cfg != nil {
		body, _ = json.Marshal(cfg)
	}
	data, status, err := pxeservice.Call(ctx, method, path, body)
	if err != nil {
		return state, http.StatusServiceUnavailable, err
	}
	if status != http.StatusOK {
		return state, status, fmt.Errorf("测试虚机控制失败: %s", strings.TrimSpace(string(data)))
	}
	if err = json.Unmarshal(data, &state); err != nil {
		return state, http.StatusBadGateway, errors.New("invalid controller response")
	}
	return state, status, nil
}

func testVMReboot(ctx context.Context, tenantID, projectID int64) error {
	if tenantID <= 0 || projectID <= 0 {
		return errors.New("必须选择有效租户和项目")
	}
	state, _, err := testVMCall(ctx, http.MethodGet, "/test-vm", nil)
	if err != nil {
		return err
	}
	if err = testVMOwner(state, tenantID, projectID, true); err != nil {
		return err
	}
	_, _, err = testVMCall(ctx, http.MethodPost, "/test-vm/reboot", &state.TestVMConfig)
	return err
}

func (a *App) testVM(c *gin.Context) {
	if !current(c).IsPlatformAdmin {
		fail(c, 403, "platform administrator required")
		return
	}
	sc := contextScope(c)
	if sc.Tenant <= 0 || sc.Project <= 0 {
		fail(c, 400, "必须选择有效租户和项目")
		return
	}
	action := c.Param("action")
	if c.Request.Method != http.MethodGet && c.Request.Method != http.MethodPost && c.Request.Method != http.MethodDelete {
		fail(c, 405, "unsupported method")
		return
	}
	if action != "" && action != "start" && action != "stop" && action != "restart" && action != "reboot" && action != "prepare-network" && action != "cancel-install" {
		fail(c, 400, "unsupported test VM action")
		return
	}
	ctx := c.Request.Context()
	if c.Request.Method == http.MethodDelete {
		var in struct {
			Confirmed bool   `json:"confirmed"`
			AssetID   *int64 `json:"asset_id"`
		}
		if c.ShouldBindJSON(&in) != nil || !in.Confirmed || in.AssetID == nil || *in.AssetID < 0 {
			fail(c, 400, "请确认永久删除测试 VM 及其系统磁盘")
			return
		}
		if err := a.deleteTestVM(ctx, sc, *in.AssetID, testVMCall); err != nil {
			fail(c, 409, err.Error())
			return
		}
		ok(c, gin.H{"deleted": true})
		return
	}
	state, status, err := testVMCall(ctx, http.MethodGet, "/test-vm", nil)
	if err != nil {
		fail(c, status, err.Error())
		return
	}
	if err = testVMOwner(state, sc.Tenant, sc.Project, action != ""); err != nil {
		fail(c, 409, err.Error())
		return
	}
	if c.Request.Method == http.MethodGet {
		a.testVMLabStatus(c, state, sc)
		return
	}
	if action == "prepare-network" {
		if err := a.prepareTestVMNetwork(ctx, sc); err != nil {
			fail(c, 409, err.Error())
			return
		}
		ok(c, gin.H{"ready": true})
		return
	}
	if action == "cancel-install" {
		if err := a.cancelTestVMInstallation(ctx, sc, state); err != nil {
			fail(c, 409, err.Error())
			return
		}
		ok(c, gin.H{"cancelled": true})
		return
	}
	// Only the installation worker may bypass the active-installation guard.
	if action == "reboot" {
		action = "restart"
	}
	cfg := pxeservice.TestVMConfig{TenantID: sc.Tenant, ProjectID: sc.Project, Firmware: state.Firmware}
	if action == "" {
		var in struct {
			Firmware string `json:"firmware"`
		}
		if c.ShouldBindJSON(&in) != nil {
			fail(c, 400, "invalid configuration")
			return
		}
		cfg.Firmware = in.Firmware
		if cfg.Firmware == "" {
			cfg.Firmware = "bios"
		}
	}
	if cfg.Firmware != "bios" && cfg.Firmware != "uefi" {
		fail(c, 400, "firmware must be bios or uefi")
		return
	}
	if action == "" {
		records, state, status, err := a.ensureTestVM(ctx, sc, cfg)
		if err != nil {
			fail(c, status, err.Error())
			return
		}
		ok(c, gin.H{"vm": state, "asset_id": records.AssetID, "network_id": records.NetworkID, "pool_id": records.PoolID, "plan_id": records.PlanID})
		return
	}
	state, status, err = testVMCall(ctx, http.MethodPost, "/test-vm/"+action, &cfg)
	if err != nil {
		fail(c, status, err.Error())
		return
	}
	power := "off"
	if state.Running {
		power = "on"
	}
	if err := a.store.db.WithContext(ctx).Table("assets").Where("tenant_id=? AND project_id=? AND manufacturer='BMM Test VM' AND model='QEMU PXE Lab' AND mac_address=? AND deleted_at IS NULL", sc.Tenant, sc.Project, pxeservice.TestVMMAC).Update("power_state", power).Error; err != nil {
		fail(c, 500, "VM 操作完成，但资产电源状态同步失败，请刷新实验室核对")
		return
	}
	ok(c, state)
}

type testVMRecords struct{ AssetID, NetworkID, PoolID, PlanID int64 }

const testVMInstallName = "BMM Test VM Ubuntu 22.04.5"

const testVMAutoinstall = `#cloud-config
autoinstall:
  version: 1
  refresh-installer:
    update: false
  locale: en_US.UTF-8
  keyboard:
    layout: us
  network:
    version: 2
    ethernets:
      pxe:
        match:
          macaddress: "52:54:00:77:00:10"
        dhcp4: true
  identity:
    hostname: {{ printf "%q" .Hostname }}
    username: admin
    password: "!"
  ssh:
    install-server: true
    allow-pw: false
{{- if .HasSSHKey }}
    authorized-keys:
      - {{ printf "%q" .SSHKey }}
{{- end }}
  storage:
    layout:
      name: direct
  shutdown: reboot
`

type testVMISOMetadata struct {
	ObjectKey string
	SHA256    string
	Size      int64
}

func testVMReadISO(path string) (testVMISOMetadata, error) {
	var meta testVMISOMetadata
	file, err := os.Open(path)
	if err != nil {
		return meta, fmt.Errorf("真实 Ubuntu ISO 不可用: %w", err)
	}
	defer file.Close()
	before, err := file.Stat()
	if err != nil {
		return meta, err
	}
	if !before.Mode().IsRegular() || before.Size() <= 0 {
		return meta, errors.New("Ubuntu ISO 必须是非空普通文件")
	}
	hash := sha256.New()
	size, err := io.Copy(hash, file)
	if err != nil {
		return meta, err
	}
	after, err := file.Stat()
	if err != nil {
		return meta, err
	}
	if size != before.Size() || size != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		return meta, errors.New("Ubuntu ISO 在校验期间发生变化，请重试")
	}
	return testVMISOMetadata{ObjectKey: "file://" + path, SHA256: hex.EncodeToString(hash.Sum(nil)), Size: size}, nil
}

func ensureTestVMInstallPlan(tx *gorm.DB, sc scope, networkID int64, iso testVMISOMetadata) (int64, error) {
	var imageID, imageTemplateID, kickstartID, partitionID, planID int64
	imageConfig, err := json.Marshal(map[string]string{"storage": "local_mount", "iso_path": strings.TrimPrefix(iso.ObjectKey, "file://")})
	if err != nil {
		return 0, err
	}
	if err := tx.Raw(`INSERT INTO images(tenant_id,project_id,name,object_key,size_bytes,sha256,os_type,version,architecture,status,create_by,update_by)
 VALUES (?,?,?,?,?,?,'ubuntu','22.04.5','x86_64','ready',?,?)
 ON CONFLICT (tenant_id,project_id,object_key) WHERE deleted_at IS NULL DO UPDATE
 SET size_bytes=EXCLUDED.size_bytes,sha256=EXCLUDED.sha256,os_type=EXCLUDED.os_type,version=EXCLUDED.version,architecture=EXCLUDED.architecture,status='ready',update_by=EXCLUDED.update_by
 RETURNING id`, sc.Tenant, sc.Project, testVMInstallName, iso.ObjectKey, iso.Size, iso.SHA256, sc.User, sc.User).Scan(&imageID).Error; err != nil {
		return 0, err
	}
	if err := tx.Raw(`INSERT INTO image_templates(tenant_id,project_id,name,image_id,config,create_by,update_by)
 VALUES (?,?,?,?,?::jsonb,?,?)
 ON CONFLICT (tenant_id,project_id,name) WHERE deleted_at IS NULL DO UPDATE
 SET image_id=EXCLUDED.image_id,config=EXCLUDED.config,update_by=EXCLUDED.update_by RETURNING id`, sc.Tenant, sc.Project, testVMInstallName, imageID, string(imageConfig), sc.User, sc.User).Scan(&imageTemplateID).Error; err != nil {
		return 0, err
	}
	if err := tx.Raw(`INSERT INTO kickstart_templates(tenant_id,project_id,name,format,content,revision,create_by,update_by)
 VALUES (?,?,?,'autoinstall',?,1,?,?)
 ON CONFLICT (tenant_id,project_id,name,revision) WHERE deleted_at IS NULL DO UPDATE
 SET format=EXCLUDED.format,content=EXCLUDED.content,update_by=EXCLUDED.update_by RETURNING id`, sc.Tenant, sc.Project, testVMInstallName, testVMAutoinstall, sc.User, sc.User).Scan(&kickstartID).Error; err != nil {
		return 0, err
	}
	if err := tx.Raw(`INSERT INTO partition_templates(tenant_id,project_id,name,layout,revision,create_by,update_by)
 VALUES (?,?,?,'{"layout":{"name":"direct"}}'::jsonb,1,?,?)
 ON CONFLICT (tenant_id,project_id,name,revision) WHERE deleted_at IS NULL DO UPDATE
 SET layout=EXCLUDED.layout,update_by=EXCLUDED.update_by RETURNING id`, sc.Tenant, sc.Project, testVMInstallName, sc.User, sc.User).Scan(&partitionID).Error; err != nil {
		return 0, err
	}
	if err := tx.Raw(`INSERT INTO install_plans(tenant_id,project_id,name,image_template_id,kickstart_template_id,partition_template_id,network_id,config,create_by,update_by)
 VALUES (?,?,?,?,?,?,?,'{"hostname":"bmm-test-vm"}'::jsonb,?,?)
 ON CONFLICT (tenant_id,project_id,name) WHERE deleted_at IS NULL DO UPDATE
 SET image_template_id=EXCLUDED.image_template_id,kickstart_template_id=EXCLUDED.kickstart_template_id,partition_template_id=EXCLUDED.partition_template_id,network_id=EXCLUDED.network_id,update_by=EXCLUDED.update_by RETURNING id`, sc.Tenant, sc.Project, testVMInstallName, imageTemplateID, kickstartID, partitionID, networkID, sc.User, sc.User).Scan(&planID).Error; err != nil {
		return 0, err
	}
	return planID, nil
}

func (a *App) ensureTestVM(ctx context.Context, sc scope, cfg pxeservice.TestVMConfig) (testVMRecords, pxeservice.TestVMStatus, int, error) {
	var ids testVMRecords
	var state pxeservice.TestVMStatus
	iso, err := testVMReadISO(filepath.Join(config.Load().PXE.TFTPRoot, "iso", DefaultISOFileName))
	if err != nil {
		return ids, state, http.StatusServiceUnavailable, err
	}
	status := http.StatusConflict
	err = a.store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SELECT pg_advisory_xact_lock(684219734)").Error; err != nil {
			return err
		}
		var assets []struct {
			ID, TenantID, ProjectID int64
			Manufacturer, Model     string
		}
		if err := tx.Table("assets").Select("id,tenant_id,project_id,manufacturer,model").Where("mac_address=? AND deleted_at IS NULL", pxeservice.TestVMMAC).Find(&assets).Error; err != nil {
			return err
		}
		for _, asset := range assets {
			if asset.TenantID != sc.Tenant || asset.ProjectID != sc.Project || asset.Manufacturer != "BMM Test VM" || asset.Model != "QEMU PXE Lab" || ids.AssetID != 0 {
				return errors.New("固定测试 MAC 已被其他资产或项目占用")
			}
			ids.AssetID = asset.ID
		}
		var err error
		state, status, err = testVMCall(ctx, http.MethodGet, "/test-vm", nil)
		if err != nil {
			return err
		}
		status = http.StatusConflict
		if err = testVMOwner(state, sc.Tenant, sc.Project, false); err != nil {
			return err
		}
		if err = tx.Raw(`INSERT INTO networks(tenant_id,project_id,name,subnet,gateway,dhcp_enabled,create_by,update_by)
   VALUES (?,?,'BMM Test VM','192.168.77.0/24','192.168.77.1',true,?,?)
   ON CONFLICT (tenant_id,project_id,name) WHERE deleted_at IS NULL DO UPDATE SET name=EXCLUDED.name
   RETURNING id`, sc.Tenant, sc.Project, sc.User, sc.User).Scan(&ids.NetworkID).Error; err != nil {
			return err
		}
		var valid int64
		if err = tx.Table("networks").Where("id=? AND subnet='192.168.77.0/24'::cidr AND gateway='192.168.77.1'::inet AND dhcp_enabled", ids.NetworkID).Count(&valid).Error; err != nil {
			return err
		}
		if valid != 1 {
			return errors.New("同名测试网络配置不匹配，拒绝覆盖")
		}
		if err = tx.Raw(`INSERT INTO ip_pools(tenant_id,project_id,network_id,name,start_address,end_address,create_by,update_by)
   VALUES (?,?,?,'BMM Test VM','192.168.77.100','192.168.77.200',?,?)
   ON CONFLICT (tenant_id,project_id,network_id,name) WHERE deleted_at IS NULL DO UPDATE SET name=EXCLUDED.name
   RETURNING id`, sc.Tenant, sc.Project, ids.NetworkID, sc.User, sc.User).Scan(&ids.PoolID).Error; err != nil {
			return err
		}
		if err = tx.Table("ip_pools").Where("id=? AND start_address='192.168.77.100'::inet AND end_address='192.168.77.200'::inet", ids.PoolID).Count(&valid).Error; err != nil {
			return err
		}
		if valid != 1 {
			return errors.New("同名测试地址池配置不匹配，拒绝覆盖")
		}
		if ids.AssetID == 0 {
			cipher, err := service.EncryptCredential("test-vm-no-bmc")
			if err != nil {
				return errors.New("credential encryption failed")
			}
			if err = tx.Raw(`INSERT INTO assets(tenant_id,project_id,name,manufacturer,model,bmc_address,bmc_user,bmc_secret_ciphertext,bmc_secret_key_id,mac_address,create_by,update_by)
    VALUES (?,?,'BMM Test VM','BMM Test VM','QEMU PXE Lab','192.168.77.10','test-vm',?,'v1',?,?,?) RETURNING id`, sc.Tenant, sc.Project, []byte(cipher), pxeservice.TestVMMAC, sc.User, sc.User).Scan(&ids.AssetID).Error; err != nil {
				return err
			}
		}
		ids.PlanID, err = ensureTestVMInstallPlan(tx, sc, ids.NetworkID, iso)
		if err != nil {
			return err
		}
		state, status, err = testVMCall(ctx, http.MethodPut, "/test-vm", &cfg)
		return err
	})
	if err != nil && status == http.StatusOK {
		status = http.StatusInternalServerError
	}
	return ids, state, status, err
}
