package platform

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"baremetal-manager/backend/config"
	"baremetal-manager/backend/internal/model"
	"baremetal-manager/backend/internal/pxeservice"
	"baremetal-manager/backend/internal/service"
	"github.com/google/uuid"
	"gopkg.in/yaml.v3"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const testInstallHTTPBase = "http://192.168.77.1:18080/api/v1/provision"

type installationSnapshot struct {
	MAC            string                 `json:"mac"`
	Root           string                 `json:"root"`
	IP             string                 `json:"ip"`
	TokenEncrypted string                 `json:"token_encrypted"`
	Hostname       string                 `json:"hostname"`
	SSHKey         string                 `json:"ssh_public_key"`
	Access         *installAccessSnapshot `json:"access,omitempty"`
	Content        string                 `json:"content"`
	Layout         json.RawMessage        `json:"layout"`
	Plan           json.RawMessage        `json:"plan"`
}

// Validate and render before enqueueing and again in the worker. This keeps a
// malformed template from becoming an accepted, but unusable installation job.
func renderInstallationSeed(snap installationSnapshot) (map[string]any, error) {
	rendered, err := service.RenderInstallConfig(model.KickstartTemplate{Format: "autoinstall", TemplateContent: snap.Content}, service.InstallVars{Hostname: snap.Hostname, MAC: snap.MAC, SSHKey: snap.SSHKey, PasswordHash: "!", HTTPBase: testInstallHTTPBase})
	if err != nil {
		return nil, err
	}
	var seed map[string]any
	if err := yaml.Unmarshal([]byte(rendered), &seed); err != nil {
		return nil, errors.New("autoinstall YAML 无效")
	}
	ai, ok := seed["autoinstall"].(map[string]any)
	if !ok {
		return nil, errors.New("autoinstall 模板缺少根节点")
	}
	if err := applyInstallAccess(ai, snap.Access); err != nil {
		return nil, err
	}
	if snap.Access != nil {
		ai["identity"].(map[string]any)["hostname"] = snap.Hostname
	}
	return seed, nil
}

func (a *App) enqueueInstallation(ctx context.Context, sc scope, assetID, planID, networkID int64, root string, access *installAccessInput, meta auditMeta) (int64, int64, error) {
	var installationID, operationID int64
	accessSnapshot, err := access.snapshot()
	if err != nil {
		return 0, 0, err
	}
	token, err := service.NewCallbackToken()
	if err != nil {
		return 0, 0, err
	}
	encrypted, err := service.EncryptCredential(token)
	if err != nil {
		return 0, 0, err
	}
	err = a.store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockScope(tx, sc); err != nil {
			return err
		}
		var asset struct {
			ID                              int64
			Manufacturer, Model, MACAddress string
		}
		if err := queryScope(tx, resource{table: "assets"}, sc).Clauses(clause.Locking{Strength: "UPDATE"}).Where("id=? AND tenant_id=? AND project_id=? AND deleted_at IS NULL AND status<>'retired'", assetID, sc.Tenant, sc.Project).Take(&asset).Error; err != nil {
			return err
		}
		if asset.Manufacturer != "BMM Test VM" || asset.Model != "QEMU PXE Lab" || !strings.EqualFold(asset.MACAddress, pxeservice.TestVMMAC) {
			return errors.New("仅允许 BMM Test VM Ubuntu 22.04 安装")
		}
		var active int64
		if err := tx.Table("operations").Where("asset_id=? AND deleted_at IS NULL AND status IN ('pending','running','retry_wait','unknown')", assetID).Count(&active).Error; err != nil {
			return err
		}
		var cleanup int64
		if err := tx.Table("installations").Where("asset_id=? AND deleted_at IS NULL AND cleanup_pending", assetID).Count(&cleanup).Error; err != nil {
			return err
		}
		if active != 0 || cleanup != 0 {
			return errors.New("资产已有执行中或结果未知的操作")
		}
		var plan struct {
			ID                                             int64
			NetworkID                                      int64
			OSType, Version, Architecture, Format, Content string
			Config, Layout, Snapshot                       json.RawMessage
		}
		err := tx.Raw(`SELECT p.id,p.network_id,i.os_type,i.version,i.architecture,k.format,k.content,p.config,pt.layout,
   jsonb_build_object('plan',to_jsonb(p),'image_template',to_jsonb(it),'image',to_jsonb(i),'kickstart_template',to_jsonb(k),'partition_template',to_jsonb(pt)) AS snapshot
   FROM install_plans p JOIN image_templates it ON it.id=p.image_template_id AND it.tenant_id=p.tenant_id AND it.project_id=p.project_id AND it.deleted_at IS NULL
   JOIN images i ON i.id=it.image_id AND i.tenant_id=p.tenant_id AND i.project_id=p.project_id AND i.deleted_at IS NULL
   JOIN kickstart_templates k ON k.id=p.kickstart_template_id AND k.tenant_id=p.tenant_id AND k.project_id=p.project_id AND k.deleted_at IS NULL
   JOIN partition_templates pt ON pt.id=p.partition_template_id AND pt.tenant_id=p.tenant_id AND pt.project_id=p.project_id AND pt.deleted_at IS NULL
   WHERE p.id=? AND p.tenant_id=? AND p.project_id=? AND p.deleted_at IS NULL`, planID, sc.Tenant, sc.Project).Scan(&plan).Error
		if err != nil {
			return err
		}
		if plan.ID == 0 || plan.OSType != "ubuntu" || !strings.HasPrefix(plan.Version, "22.04") || (plan.Architecture != "amd64" && plan.Architecture != "x86_64") || plan.Format != "autoinstall" || plan.NetworkID != networkID {
			return errors.New("计划必须使用当前 PXE 网络、Ubuntu 22.04 amd64 镜像及 autoinstall 模板")
		}
		var scripts int64
		if err := tx.Table("install_plan_scripts").Where("install_plan_id=? AND tenant_id=? AND project_id=? AND deleted_at IS NULL", planID, sc.Tenant, sc.Project).Count(&scripts).Error; err != nil {
			return err
		}
		if scripts != 0 {
			return errors.New("测试虚机安装暂不支持附加装机脚本")
		}
		var cfg struct {
			Hostname string `json:"hostname"`
			SSHKey   string `json:"ssh_public_key"`
		}
		if err := json.Unmarshal(plan.Config, &cfg); err != nil {
			return err
		}
		if cfg.Hostname == "" {
			cfg.Hostname = "bmm-test-vm"
		}
		if access != nil && access.Hostname != "" {
			cfg.Hostname = access.Hostname
		}
		if access != nil {
			// Explicit per-install settings replace a plan's legacy SSH key.
			cfg.SSHKey = ""
		}
		snap := installationSnapshot{MAC: asset.MACAddress, IP: "192.168.77.100", Root: root, TokenEncrypted: encrypted, Hostname: cfg.Hostname, SSHKey: cfg.SSHKey, Access: accessSnapshot, Content: plan.Content, Layout: plan.Layout, Plan: plan.Snapshot}
		if _, err := renderInstallationSeed(snap); err != nil {
			return errors.New("安装配置无效，请检查 autoinstall 模板和系统访问设置")
		}
		encoded, err := json.Marshal(snap)
		if err != nil {
			return err
		}
		key := "install/" + uuid.NewString()
		var op struct{ ID int64 }
		if err := tx.Raw(`INSERT INTO operations(tenant_id,project_id,asset_id,user_id,kind,idempotency_key,status,max_attempts,timeout_seconds,create_by,client_ip) VALUES (?,?,?,?,'install',?,'pending',1,3600,?,NULLIF(?,'')::inet) RETURNING id`, sc.Tenant, sc.Project, assetID, sc.User, key, sc.User, meta.ClientIP).Scan(&op).Error; err != nil {
			return err
		}
		operationID = op.ID
		var inst struct{ ID int64 }
		if err := tx.Raw(`INSERT INTO installations(tenant_id,project_id,asset_id,operation_id,install_plan_id,config_snapshot,callback_token_hash,callback_expires_at,confirmed_by,confirmed_at,create_by) VALUES (?,?,?,?,?,?::jsonb,?,now()+interval '60 minutes',?,now(),?) RETURNING id`, sc.Tenant, sc.Project, assetID, op.ID, planID, string(encoded), service.CallbackTokenHash(token), sc.User, sc.User).Scan(&inst).Error; err != nil {
			return err
		}
		installationID = inst.ID
		progress, _ := json.Marshal(newInstallSteps(time.Now()))
		if err := tx.Table("installations").Where("id=?", inst.ID).Updates(map[string]any{"progress": string(progress), "deadline_at": gorm.Expr("now()+interval '60 minutes'")}).Error; err != nil {
			return err
		}
		if err := tx.Table("task_outbox").Create(map[string]any{"tenant_id": sc.Tenant, "project_id": sc.Project, "operation_id": op.ID, "event_key": key, "event_type": "operation.execute"}).Error; err != nil {
			return err
		}
		return a.store.audit(tx, sc, "submit", "installations", inst.ID)
	})
	return installationID, operationID, err
}

func (a *App) runInstallation(ctx context.Context, op operation, event outbox) {
	err := a.prepareTestInstallation(ctx, op)
	if err != nil {
		fmt.Printf("installation preparation failed: operation_id=%d\n", op.ID)
	}
	finishCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	finishErr := a.store.db.WithContext(finishCtx).Transaction(func(tx *gorm.DB) error {
		var current operation
		if e := tx.Table("operations").Clauses(clause.Locking{Strength: "UPDATE"}).Where("id=?", op.ID).Take(&current).Error; e != nil {
			return e
		}
		if err != nil && current.Status == "running" {
			status := "unknown"
			message := "重启结果未确认，请查看串口日志；任务仍受阶段时限约束，超时将停止测试 VM"
			var installation struct{ Status string }
			if e := tx.Table("installations").Select("status").Where("operation_id=?", op.ID).Take(&installation).Error; e != nil {
				return e
			}
			// pending 阶段尚未调用虚拟机重启，可以明确判定为准备失败。
			if installation.Status == "pending" || installation.Status == "preparing" {
				status = "failed"
				message = "安装准备失败，尚未重启 VM；请检查安装介质、应答配置及 PXE 网络"
			}
			if e := tx.Table("operations").Where("id=?", op.ID).Updates(map[string]any{"status": status, "error": message, "completed_at": time.Now()}).Error; e != nil {
				return e
			}
			if e := tx.Table("installations").Where("operation_id=? AND status NOT IN ('succeeded','failed','cancelled')", op.ID).Updates(map[string]any{"status": status, "error": message}).Error; e != nil {
				return e
			}
			current.Status = status
			if status == "failed" {
				if e := finishInstallProgress(tx, op, "failed", message); e != nil {
					return e
				}
				if e := tx.Table("installations").Where("operation_id=?", op.ID).Updates(map[string]any{"started_at": gorm.Expr("COALESCE(started_at,now())"), "completed_at": time.Now(), "callback_revoked_at": time.Now()}).Error; e != nil {
					return e
				}
				if e := tx.Table("device_leases").Where("operation_id=? AND holder_id=?", op.ID, a.node).Update("expires_at", time.Now()).Error; e != nil {
					return e
				}
			}
		}
		if e := tx.Table("task_outbox").Where("id=?", event.ID).Updates(map[string]any{"status": "published", "published_at": time.Now(), "locked_until": nil}).Error; e != nil {
			return e
		}
		if current.Status == "running" || current.Status == "unknown" {
			return tx.Table("device_leases").Where("operation_id=? AND holder_id=?", op.ID, a.node).Updates(map[string]any{"heartbeat_at": time.Now(), "expires_at": time.Now().Add(65 * time.Minute)}).Error
		}
		return nil
	})
	if finishErr != nil {
		fmt.Printf("installation finalization failed: operation_id=%d\n", op.ID)
	}
}

func (a *App) prepareTestInstallation(ctx context.Context, op operation) error {
	var row struct {
		ID             int64
		ConfigSnapshot json.RawMessage
	}
	if err := a.store.db.WithContext(ctx).Table("installations").Select("id,config_snapshot").Where("operation_id=? AND tenant_id=? AND project_id=? AND status='pending' AND deleted_at IS NULL", op.ID, op.TenantID, op.ProjectID).Take(&row).Error; err != nil {
		return err
	}
	var snap installationSnapshot
	if err := json.Unmarshal(row.ConfigSnapshot, &snap); err != nil {
		return err
	}
	token, err := service.DecryptCredential(snap.TokenEncrypted)
	if err != nil {
		return err
	}
	for _, name := range []string{"netboot/vmlinuz", "netboot/initrd", "iso/" + DefaultISOFileName} {
		path, err := provisionResolveUnder(snap.Root, name)
		if err != nil {
			return err
		}
		st, err := os.Stat(path)
		if err != nil || !st.Mode().IsRegular() {
			return errors.New("真实 Ubuntu 安装介质不可用")
		}
	}
	seed, err := renderInstallationSeed(snap)
	if err != nil {
		return err
	}
	autoinstall := seed["autoinstall"].(map[string]any)
	var storage map[string]any
	if err := json.Unmarshal(snap.Layout, &storage); err != nil {
		return err
	}
	if len(storage) == 0 {
		storage = map[string]any{"layout": map[string]any{"name": "direct"}}
	}
	autoinstall["storage"] = storage
	// Subiquity 6066 in the bundled 22.04.5 ISO supports explicit offline fallback.
	// No external mirror candidates: install packages (including SSH) from the ISO.
	autoinstall["apt"] = testVMOfflineAPT()
	callbackURL := testInstallHTTPBase + "/install-callback/" + token
	command := func(status string) string {
		return service.CallbackCommand(callbackURL, fmt.Sprintf(`{"status":%q}`, status))
	}
	for key, status := range map[string]string{"early-commands": "installing", "late-commands": "post_install", "error-commands": "failed"} {
		commands, _ := autoinstall[key].([]any)
		autoinstall[key] = append(commands, command(status))
	}
	late, _ := autoinstall["late-commands"].([]any)
	autoinstall["late-commands"] = append(late, testVMFirstBootCommands(callbackURL)...)
	data, err := yaml.Marshal(seed)
	if err != nil {
		return err
	}
	cfg := config.Load()
	cfg.PXE.TFTPRoot = snap.Root
	cfg.PXE.HTTPBase = testInstallHTTPBase
	generator := NewIPXEScriptGenerator(cfg)
	script, err := generator.UbuntuScript(snap.MAC, "", "", "", DefaultISOFileName)
	if err != nil {
		return err
	}
	script = strings.Replace(script, " ---\n", " console=tty0 console=ttyS0,115200n8 ---\n", 1)
	mac, err := provisioningNormalizeMAC(snap.MAC)
	if err != nil {
		return err
	}
	// Credentials in NoCloud must only be downloadable with this installation's
	// expiring token; a known MAC address alone is not authorization.
	script = strings.Replace(script, "/nocloud/"+mac+"/", "/nocloud-token/"+token+"/"+mac+"/", 1)
	script = strings.ReplaceAll(script, "${http-base}/netboot/", "${http-base}/media-token/"+token+"/netboot/")
	script = strings.ReplaceAll(script, "${http-base}/iso/", "${http-base}/media-token/"+token+"/iso/")
	dir := filepath.Join(snap.Root, "nocloud", mac)
	if err := os.MkdirAll(dir, 0750); err != nil {
		return err
	}
	if err := writeEncryptedInstallSeed(filepath.Join(dir, "user-data.enc"), append([]byte("#cloud-config\n"), data...)); err != nil {
		return err
	}
	meta, _ := yaml.Marshal(map[string]any{"instance-id": fmt.Sprintf("bmm-install-%d", row.ID), "local-hostname": snap.Hostname})
	if err := writeEncryptedInstallSeed(filepath.Join(dir, "meta-data.enc"), meta); err != nil {
		return err
	}
	if _, err := generator.WriteScript(snap.MAC, script); err != nil {
		return err
	}
	if err := a.installationStep(ctx, op, "reboot", "安装介质与应答文件已就绪，正在重启测试 VM 并设置一次性 PXE 启动"); err != nil {
		return err
	}
	updated := a.store.db.WithContext(ctx).Table("installations").Where("id=? AND status='pending' AND callback_revoked_at IS NULL", row.ID).Updates(map[string]any{"status": "booting", "started_at": time.Now()})
	if updated.Error != nil {
		return updated.Error
	}
	if updated.RowsAffected != 1 {
		return errors.New("installation state changed before VM reboot")
	}
	// Persist booting before attempting the external side effect: an uncertain
	// reboot must not roll back to pending and be classified as a safe failure.
	// Use cancellation's operation -> installation lock order to fence the reboot.
	return a.store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var current operation
		if err := tx.Table("operations").Clauses(clause.Locking{Strength: "UPDATE"}).Where("id=? AND status='running' AND deleted_at IS NULL", op.ID).Take(&current).Error; err != nil {
			return err
		}
		var installation struct{ ID int64 }
		if err := tx.Table("installations").Select("id").Clauses(clause.Locking{Strength: "UPDATE"}).Where("id=? AND status='booting' AND callback_revoked_at IS NULL", row.ID).Take(&installation).Error; err != nil {
			return err
		}
		if err := testVMReboot(ctx, op.TenantID, op.ProjectID); err != nil {
			return err
		}
		return recordInstallStep(tx, op, "pxe_boot", "VM 已重启，等待获取 DHCP 地址、TFTP 引导文件及 iPXE 脚本（最多 3 分钟）")
	})
}

func testVMOfflineAPT() map[string]any {
	return map[string]any{"geoip": false, "mirror-selection": map[string]any{"primary": []any{}}, "fallback": "offline-install"}
}

// A successful installer alone is insufficient: report success from the installed OS.
func testVMFirstBootCommands(callbackURL string) []any {
	python := fmt.Sprintf(`#!/usr/bin/python3
import pathlib, urllib.request
request = urllib.request.Request(%q, data=b'{"status":"succeeded","stage":"first_boot","message":"Ubuntu installed and booted from disk"}', headers={"Content-Type":"application/json"})
with urllib.request.urlopen(request, timeout=20) as response:
    if response.status != 200:
        raise RuntimeError("installation callback failed")
pathlib.Path("/var/lib/bmm-pxe-boot-verified").touch()
print("BMM_PXE_BOOT_VERIFIED: Ubuntu installed and booted from disk", flush=True)
`, callbackURL)
	unit := `[Unit]
Description=Confirm BMM PXE installation after first disk boot
Wants=network-online.target
After=network-online.target
ConditionPathExists=!/var/lib/bmm-pxe-boot-verified
[Service]
Type=oneshot
ExecStart=/usr/bin/python3 /usr/local/sbin/bmm-pxe-verify.py
Restart=on-failure
RestartSec=15
StandardOutput=journal+console
StandardError=journal+console
[Install]
WantedBy=multi-user.target
`
	return []any{
		"cat > /target/usr/local/sbin/bmm-pxe-verify.py <<'BMM_EOF'\n" + python + "BMM_EOF\nchmod 0700 /target/usr/local/sbin/bmm-pxe-verify.py",
		"cat > /target/etc/systemd/system/bmm-pxe-verify.service <<'BMM_EOF'\n" + unit + "BMM_EOF\nln -sf ../bmm-pxe-verify.service /target/etc/systemd/system/multi-user.target.wants/bmm-pxe-verify.service",
		`printf '%s\n' 'GRUB_CMDLINE_LINUX="console=tty0 console=ttyS0,115200n8"' > /target/etc/default/grub.d/99-bmm-serial.cfg`,
		"curtin in-target --target=/target -- update-grub",
		service.CallbackCommand(callbackURL, `{"status":"post_install","stage":"first_boot","message":"安装配置已完成，等待从磁盘启动并回报"}`),
	}
}
