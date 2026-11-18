package pxeservice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const TestVMBridge = "bmm-pxe0"
const TestVMMAC = "52:54:00:77:00:10"
const testVMState = "/var/lib/bmm-pxe/test-vm.json"
const testVMDisk = "/var/lib/bmm-pxe/test-vm.qcow2"

type TestVMConfig struct {
	TenantID  int64  `json:"tenant_id"`
	ProjectID int64  `json:"project_id"`
	Firmware  string `json:"firmware"`
}
type TestVMStatus struct {
	TestVMConfig
	Running   bool   `json:"running"`
	Bridge    string `json:"bridge"`
	Server    string `json:"server"`
	MAC       string `json:"mac"`
	Error     string `json:"error,omitempty"`
	SerialLog string `json:"serial_log,omitempty"`
}

var terminalEscape = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]`)
var callbackSecret = regexp.MustCompile(`((?:install-callback|nocloud-token|media-token)/)[A-Za-z0-9_-]+`)

func testVMSerialLog(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return ""
	}
	if info.Size() > 16000 {
		_, _ = f.Seek(-16000, io.SeekEnd)
	}
	data, _ := io.ReadAll(io.LimitReader(f, 16000))
	clean := terminalEscape.ReplaceAllString(string(data), "")
	clean = strings.Map(func(r rune) rune {
		if r < 32 && r != '\n' && r != '\t' {
			return -1
		}
		return r
	}, clean)
	return callbackSecret.ReplaceAllString(clean, "${1}[redacted]")
}

func (s *controller) vmStatus() TestVMStatus {
	return TestVMStatus{TestVMConfig: s.vm, Running: s.commands["test-vm"] != nil, Bridge: TestVMBridge, Server: "192.168.77.1", MAC: TestVMMAC, Error: s.vmError, SerialLog: testVMSerialLog("/var/lib/bmm-pxe/test-vm-serial.log")}
}

func (s *controller) testVMHTTP(w http.ResponseWriter, r *http.Request) {
	fail := func(code int, err error) { http.Error(w, err.Error(), code) }
	if r.Method == http.MethodGet && r.URL.Path == "/test-vm" {
		json.NewEncoder(w).Encode(s.vmStatus())
		return
	}
	var cfg TestVMConfig
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
	d.DisallowUnknownFields()
	if err := d.Decode(&cfg); err != nil || cfg.TenantID <= 0 || cfg.ProjectID <= 0 || (cfg.Firmware != "bios" && cfg.Firmware != "uefi") {
		fail(400, errors.New("invalid test VM owner or firmware"))
		return
	}
	if s.vm.TenantID != 0 && (s.vm.TenantID != cfg.TenantID || s.vm.ProjectID != cfg.ProjectID) {
		fail(409, errors.New("测试虚机已归属其他租户/项目，禁止抢占"))
		return
	}
	action := strings.TrimPrefix(r.URL.Path, "/test-vm/")
	if !(r.Method == "PUT" && r.URL.Path == "/test-vm") && !(r.Method == "POST" && (action == "start" || action == "stop" || action == "restart" || action == "reboot" || action == "abort" || action == "boot-disk")) {
		fail(404, errors.New("unsupported test VM operation"))
		return
	}
	// Worker reboots are required while its installation is active; all other mutations are guarded.
	if action != "reboot" && action != "abort" && action != "boot-disk" {
		busy, err := s.active(r.Context())
		if err != nil || busy {
			fail(409, errors.New("无法确认空闲状态或存在活动装机任务"))
			return
		}
	}
	if action == "boot-disk" && s.commands["test-vm"] != nil {
		fail(409, errors.New("等待安装器正常重启退出后才能从磁盘启动"))
		return
	}
	if s.vm.TenantID == 0 && r.Method != "PUT" {
		fail(409, errors.New("请先创建测试虚机"))
		return
	}
	if s.vm.Firmware != "" && s.vm.Firmware != cfg.Firmware && s.commands["test-vm"] != nil {
		fail(409, errors.New("请先停止虚机再修改固件"))
		return
	}
	if err := os.MkdirAll("/var/lib/bmm-pxe", 0700); err != nil {
		fail(500, err)
		return
	}
	data, _ := json.Marshal(cfg)
	if err := os.WriteFile(testVMState+".tmp", data, 0600); err != nil {
		fail(500, err)
		return
	}
	if err := os.Rename(testVMState+".tmp", testVMState); err != nil {
		fail(500, err)
		return
	}
	s.vm = cfg
	var err error
	if r.Method == "PUT" {
		err = prepareTestVM(r.Context())
	} else {
		if action != "start" {
			err = s.stopTestVM()
		}
		if err == nil && action != "stop" && action != "abort" {
			err = s.startTestVM(r.Context(), action == "reboot")
		}
	}
	if err != nil {
		s.vmError = err.Error()
		fail(500, err)
		return
	}
	s.vmError = ""
	json.NewEncoder(w).Encode(s.vmStatus())
}

func ipCommand(ctx context.Context, args ...string) error {
	out, err := exec.CommandContext(ctx, "ip", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("隔离网络配置失败: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
func prepareTestVM(ctx context.Context) error {
	if _, err := os.Stat("/sys/class/net/" + TestVMBridge); os.IsNotExist(err) {
		if err = ipCommand(ctx, "link", "add", TestVMBridge, "type", "bridge"); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	if _, err := os.Stat("/sys/class/net/" + TestVMBridge + "/bridge"); err != nil {
		return errors.New("固定网络名称已被非 bridge 接口占用")
	}
	ports, err := os.ReadDir("/sys/class/net/" + TestVMBridge + "/brif")
	if err != nil {
		return err
	}
	for _, port := range ports {
		if port.Name() != "bmm-pxetap" {
			return errors.New("测试 bridge 存在非测试端口，拒绝连接物理网络")
		}
	}
	if _, err = os.Stat("/sys/class/net/bmm-pxetap"); os.IsNotExist(err) {
		if err = ipCommand(ctx, "tuntap", "add", "dev", "bmm-pxetap", "mode", "tap"); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	flags, err := os.ReadFile("/sys/class/net/bmm-pxetap/tun_flags")
	value, parseErr := strconv.ParseUint(strings.TrimSpace(string(flags)), 0, 32)
	// TAP、NO_PI 和 PERSIST 必须存在；内核可附加其他合法标志。
	if err != nil || parseErr != nil || value&0x1803 != 0x1802 {
		return errors.New("固定 tap 名称不是安全的持久化 TAP 接口")
	}
	if master, e := filepath.EvalSymlinks("/sys/class/net/bmm-pxetap/master"); e == nil && filepath.Base(master) != TestVMBridge {
		return errors.New("测试 TAP 已连接其他网络")
	}
	for _, args := range [][]string{{"addr", "replace", "192.168.77.1/24", "dev", TestVMBridge}, {"link", "set", TestVMBridge, "up"}, {"link", "set", "bmm-pxetap", "master", TestVMBridge}, {"link", "set", "bmm-pxetap", "up"}} {
		if err = ipCommand(ctx, args...); err != nil {
			return err
		}
	}
	if _, err = os.Lstat(testVMDisk); os.IsNotExist(err) {
		out, e := exec.CommandContext(ctx, "qemu-img", "create", "-f", "qcow2", testVMDisk, "40G").CombinedOutput()
		if e != nil {
			return fmt.Errorf("创建测试磁盘失败: %w: %s", e, out)
		}
	} else if err != nil {
		return err
	}
	info, err := os.Lstat(testVMDisk)
	if err != nil || !info.Mode().IsRegular() {
		return errors.New("测试磁盘必须是固定路径的普通文件")
	}
	return nil
}
func (s *controller) stopTestVM() error {
	cmd := s.commands["test-vm"]
	if cmd == nil {
		return nil
	}
	if err := cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}
	select {
	case <-s.vmDone:
	case <-time.After(3 * time.Second):
		return errors.New("等待虚机停止超时")
	}
	delete(s.commands, "test-vm")
	return nil
}
func (s *controller) startTestVM(ctx context.Context, pxe bool) error {
	if s.commands["test-vm"] != nil {
		return nil
	}
	if err := prepareTestVM(ctx); err != nil {
		return err
	}
	args := []string{"-name", "bmm-pxe-test", "-machine", "q35", "-accel", "kvm", "-accel", "tcg", "-m", "8192", "-smp", "2", "-display", "none", "-monitor", "none", "-serial", "file:/var/lib/bmm-pxe/test-vm-serial.log", "-drive", "file=" + testVMDisk + ",format=qcow2,if=none,id=osdisk", "-device", testVMDiskDevice(s.vm.Firmware, pxe), "-netdev", "tap,id=pxe,ifname=bmm-pxetap,script=no,downscript=no", "-device", testVMNetworkDevice(s.vm.Firmware, pxe)}
	if pxe {
		args = append(args, "-no-reboot")
	}
	if s.vm.Firmware != "uefi" {
		args = append(args, "-boot", testVMBootOrder(pxe))
	}
	if s.vm.Firmware == "uefi" {
		code, vars := "/usr/share/OVMF/OVMF_CODE.fd", "/usr/share/OVMF/OVMF_VARS.fd"
		if _, err := os.Stat(code); err != nil {
			code, vars = "/usr/share/OVMF/OVMF_CODE_4M.fd", "/usr/share/OVMF/OVMF_VARS_4M.fd"
		}
		dst := "/var/lib/bmm-pxe/test-vm-vars.fd"
		if pxe {
			if info, err := os.Lstat(dst); err == nil && !info.Mode().IsRegular() {
				return errors.New("UEFI 变量文件无效")
			}
			if err := os.Remove(dst); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
		if _, err := os.Lstat(dst); os.IsNotExist(err) {
			src, err := os.Open(vars)
			if err != nil {
				return err
			}
			defer src.Close()
			f, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
			if err != nil {
				return err
			}
			_, err = io.Copy(f, src)
			closeErr := f.Close()
			if err != nil {
				return err
			}
			if closeErr != nil {
				return closeErr
			}
		}
		info, err := os.Lstat(dst)
		if err != nil || !info.Mode().IsRegular() {
			return errors.New("UEFI 变量文件无效")
		}
		args = append(args, "-drive", "if=pflash,format=raw,readonly=on,file="+code, "-drive", "if=pflash,format=raw,file="+dst)
	}
	cmd := exec.Command("qemu-system-x86_64", args...)
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	s.commands["test-vm"] = cmd
	done := make(chan struct{})
	s.vmDone = done
	go func() {
		err := cmd.Wait()
		close(done)
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.commands["test-vm"] == cmd {
			delete(s.commands, "test-vm")
			if err != nil {
				s.vmError = err.Error()
			}
		}
	}()
	select {
	case <-done:
		delete(s.commands, "test-vm")
		return errors.New("QEMU 启动后立即退出，请检查控制器日志")
	case <-time.After(300 * time.Millisecond):
		return nil
	}
}

// Explicit PXE reinstallation clears stale UEFI BootOrder and uses the network
// only for the first boot. Installer reboot and ordinary power actions use disk.
func testVMBootOrder(pxe bool) string {
	if pxe {
		return "order=c,once=n,menu=off,strict=on"
	}
	return "order=c,menu=off,strict=on"
}

// OVMF requires explicit device priority; legacy -boot once is insufficient on
// a disk with an installed OS. After late-commands, the per-MAC script exits,
// allowing firmware to fall through to disk without reinstalling again.
func testVMDiskDevice(firmware string, pxe bool) string {
	device := "virtio-blk-pci,drive=osdisk"
	if firmware == "uefi" {
		if pxe {
			return device + ",bootindex=2"
		}
		return device + ",bootindex=1"
	}
	return device
}
func testVMNetworkDevice(firmware string, pxe bool) string {
	device := "e1000,netdev=pxe,mac=" + TestVMMAC
	if firmware == "uefi" {
		if pxe {
			return device + ",bootindex=1"
		}
		return device + ",bootindex=2"
	}
	return device
}
