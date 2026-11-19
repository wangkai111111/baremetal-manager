package pxeservice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"time"
)

const Socket = "/run/bmm-pxe/control.sock"
const StateFile = "/var/lib/bmm-pxe/config.json"

type Config struct {
	TenantID     int64             `json:"tenant_id"`
	ProjectID    int64             `json:"project_id"`
	NetworkID    int64             `json:"network_id"`
	PoolID       int64             `json:"pool_id"`
	Interface    string            `json:"interface"`
	Subnet       string            `json:"subnet"`
	Start        string            `json:"start"`
	End          string            `json:"end"`
	Server       string            `json:"server"`
	DHCPMode     string            `json:"dhcp_mode"`
	Gateway      string            `json:"gateway"`
	DNS          string            `json:"dns_servers"` // Canonical comma-separated IPv4 addresses.
	Reservations []DHCPReservation `json:"reservations,omitempty"`
	TaggedOnly   bool              `json:"tagged_only,omitempty"`
	RelayProxy   bool              `json:"relay_proxy,omitempty"`
	Root         string            `json:"root"`
	BIOS         string            `json:"bios"`
	EFI          string            `json:"efi"`
}

// Equal treats an omitted rule list and an explicitly cleared list equally.
func (c Config) Equal(other Config) bool {
	if len(c.Reservations) == 0 {
		c.Reservations = nil
	}
	if len(other.Reservations) == 0 {
		other.Reservations = nil
	}
	return reflect.DeepEqual(c, other)
}

type Status struct {
	Config Config `json:"config"`
	DHCP   bool   `json:"dhcp_running"`
	TFTP   bool   `json:"tftp_running"`
	Error  string `json:"error,omitempty"`
}

var safeName = regexp.MustCompile(`^[a-zA-Z0-9_-][a-zA-Z0-9_.-]{0,31}$`)

func (c Config) Validate(service string, host bool) error {
	if !safeName.MatchString(c.Interface) {
		return errors.New("必须指定有效装机网卡")
	}
	ip, err := netip.ParseAddr(c.Server)
	if err != nil || !ip.Is4() || !ip.IsGlobalUnicast() {
		return errors.New("必须指定装机网卡 IPv4 地址")
	}
	root := filepath.Clean(c.Root)
	if root != c.Root || (root != "/var/lib/tftpboot" && !strings.HasPrefix(root, "/var/lib/tftpboot/")) || strings.ContainsAny(root, "\r\n, \t") {
		return errors.New("TFTP 根目录必须位于 /var/lib/tftpboot 内")
	}
	for _, file := range []string{c.BIOS, c.EFI} {
		if file == "" || filepath.IsAbs(file) || filepath.Clean(file) != file || strings.Contains(file, "..") || strings.ContainsAny(file, "\r\n, \t\\") {
			return errors.New("必须指定安全的 BIOS/UEFI 相对引导文件")
		}
	}
	if service == "dhcp" {
		if c.DHCPMode != "" && c.DHCPMode != "direct" && c.DHCPMode != "relay" {
			return errors.New("DHCP 模式必须为同网段直连或三层 DHCP Relay")
		}
		p, e := netip.ParsePrefix(c.Subnet)
		start, e1 := netip.ParseAddr(c.Start)
		end, e2 := netip.ParseAddr(c.End)
		if e != nil || !p.Addr().Is4() || p.Bits() == 0 || p.Bits() > 30 || p != p.Masked() || e1 != nil || e2 != nil || !p.Contains(start) || !p.Contains(end) || start.Compare(end) > 0 || (ip.Compare(start) >= 0 && ip.Compare(end) <= 0) {
			return errors.New("地址池必须位于装机子网内，且不能包含 PXE 服务器地址")
		}
		if c.DHCPMode != "relay" && !p.Contains(ip) {
			return errors.New("同网段直连模式要求服务器属于装机子网；跨网段请使用三层 DHCP Relay 模式")
		}
		broadcast := p.Masked().Addr().As4()
		bits := p.Bits()
		n := uint32(broadcast[0])<<24 | uint32(broadcast[1])<<16 | uint32(broadcast[2])<<8 | uint32(broadcast[3])
		n |= ^uint32(0) >> bits
		last := netip.AddrFrom4([4]byte{byte(n >> 24), byte(n >> 16), byte(n >> 8), byte(n)})
		if start == p.Masked().Addr() || end == last {
			return errors.New("地址池不能包含网络地址或广播地址")
		}
		if c.DHCPMode == "relay" && c.Gateway == "" {
			return errors.New("三层 DHCP Relay 模式必须在装机子网中配置客户端网关")
		}
		if c.Gateway != "" {
			gateway, err := netip.ParseAddr(c.Gateway)
			if err != nil || !gateway.Is4() || !gateway.IsGlobalUnicast() || !p.Contains(gateway) || gateway == p.Masked().Addr() || gateway == last || (gateway.Compare(start) >= 0 && gateway.Compare(end) <= 0) {
				return errors.New("客户端网关必须为装机子网内的可用 IPv4，且位于地址池之外")
			}
		}
		if c.DNS != "" {
			servers := strings.Split(c.DNS, ",")
			if len(servers) > 8 {
				return errors.New("DNS 最多支持 8 个 IPv4 地址")
			}
			for _, server := range servers {
				addr, err := netip.ParseAddr(server)
				if err != nil || !addr.Is4() || !addr.IsGlobalUnicast() {
					return errors.New("DHCPv4 DNS 必须为有效 IPv4 地址")
				}
			}
		}
		if err := c.validateReservations(p, last); err != nil {
			return err
		}
	}
	if host {
		iface, e := net.InterfaceByName(c.Interface)
		if e != nil || iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			return errors.New("装机网卡不存在或未启用")
		}
		addrs, e := iface.Addrs()
		if e != nil {
			return e
		}
		found := false
		for _, addr := range addrs {
			p, e := netip.ParsePrefix(addr.String())
			if e == nil && p.Addr() == ip {
				found = true
				if service == "dhcp" && c.DHCPMode != "relay" && p.Masked().String() != c.Subnet {
					return errors.New("装机网卡实际网段与配置不一致")
				}
			}
		}
		if !found {
			return errors.New("服务器地址不属于指定装机网卡")
		}
		resolved, e := filepath.EvalSymlinks(c.Root)
		if e != nil || resolved != c.Root {
			return errors.New("TFTP 根目录不存在或包含符号链接")
		}
	}
	return nil
}
func (c Config) BootFiles() error {
	for _, file := range []string{c.BIOS, c.EFI, "default.ipxe"} {
		path, e := filepath.EvalSymlinks(filepath.Join(c.Root, file))
		if e != nil || !strings.HasPrefix(path, c.Root+"/") {
			return fmt.Errorf("引导文件不存在或越界: %s", file)
		}
		f, e := os.Open(path)
		if e != nil {
			return e
		}
		info, e := f.Stat()
		f.Close()
		if e != nil || !info.Mode().IsRegular() || info.Size() == 0 {
			return fmt.Errorf("引导文件为空或不是普通文件: %s", file)
		}
	}
	return nil
}
func (c Config) Render(service string) string {
	base := fmt.Sprintf("port=0\nkeep-in-foreground\npid-file=\nuser=root\nbind-interfaces\ninterface=%s\nlisten-address=%s\nexcept-interface=lo\nlog-facility=-\n", c.Interface, c.Server)
	if service == "tftp" {
		return base + "enable-tftp\ntftp-root=" + c.Root + "\n"
	}
	p, _ := netip.ParsePrefix(c.Subnet)
	mask := net.CIDRMask(p.Bits(), 32)
	if c.Interface == TestVMBridge && c.Subnet == "192.168.77.0/24" {
		base += "dhcp-host=" + TestVMMAC + ",192.168.77.100\n"
	}
	options := ""
	for _, option := range []struct{ code, value string }{{"3", c.Gateway}, {"6", c.DNS}} {
		options += "dhcp-option=" + option.code
		if option.value != "" {
			options += "," + option.value
		}
		options += "\n"
	}
	return base + c.renderReservations() + fmt.Sprintf("dhcp-range=%s,%s,%s,12h\ndhcp-leasefile=/var/lib/bmm-pxe/dnsmasq.leases\n%sdhcp-match=set:efi,option:client-arch,7\ndhcp-match=set:efi,option:client-arch,9\ndhcp-match=set:ipxe,175\ndhcp-userclass=set:ipxe,iPXE\ndhcp-boot=tag:!ipxe,tag:!efi,%s,,%s\ndhcp-boot=tag:!ipxe,tag:efi,%s,,%s\ndhcp-boot=tag:ipxe,default.ipxe,,%s\n", c.Start, c.End, net.IP(mask).String(), options, c.BIOS, c.Server, c.EFI, c.Server, c.Server)
}

type controller struct {
	mu       sync.Mutex
	config   Config
	commands map[string]*exec.Cmd
	last     string
	active   func(context.Context) (bool, error)
	vm       TestVMConfig
	vmDone   chan struct{}
	vmError  string
}

func Run(ctx context.Context, active func(context.Context) (bool, error)) error {
	s := &controller{commands: map[string]*exec.Cmd{}, active: active, config: Config{Root: "/var/lib/tftpboot", BIOS: "undionly.kpxe", EFI: "ipxe.efi"}}
	if data, e := os.ReadFile(StateFile); e == nil {
		if e = json.Unmarshal(data, &s.config); e != nil {
			return e
		}
	}
	if e := ensureBuiltinBootFiles(defaultTFTPRoot, "/usr/lib/ipxe", os.Getenv("PXE_HTTP_BASE")); e != nil {
		return e
	}
	if data, e := os.ReadFile(testVMState); e == nil {
		if e = json.Unmarshal(data, &s.vm); e != nil {
			return e
		}
		if s.vm.TenantID <= 0 || s.vm.ProjectID <= 0 || (s.vm.Firmware != "bios" && s.vm.Firmware != "uefi") {
			return errors.New("invalid persisted test VM owner or firmware")
		}
	} else if !os.IsNotExist(e) {
		return e
	}
	// 仅开放动态引导目录给非 root worker，静态介质保持只读。
	for _, name := range []string{"nocloud", "ipxe"} {
		dir := filepath.Join(s.config.Root, name)
		if e := os.MkdirAll(dir, 0750); e != nil {
			return e
		}
		if e := os.Chown(dir, 998, 2000); e != nil {
			return e
		}
	}
	if e := os.MkdirAll("/var/lib/bmm-pxe", 0700); e != nil {
		return e
	}
	if e := os.MkdirAll(filepath.Dir(Socket), 0755); e != nil {
		return e
	}
	if e := os.Remove(Socket); e != nil && !os.IsNotExist(e) {
		return e
	}
	l, e := net.Listen("unix", Socket)
	if e != nil {
		return e
	}
	defer l.Close()
	if e = os.Chown(Socket, -1, 2000); e != nil {
		return e
	}
	if e = os.Chmod(Socket, 0660); e != nil {
		return e
	}
	server := &http.Server{Handler: s, ReadHeaderTimeout: 3 * time.Second}
	go func() {
		<-ctx.Done()
		server.Close()
		s.mu.Lock()
		defer s.mu.Unlock()
		for _, cmd := range s.commands {
			_ = cmd.Process.Kill()
		}
	}()
	e = server.Serve(l)
	if errors.Is(e, http.ErrServerClosed) {
		return nil
	}
	return e
}
func (s *controller) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	failure := func(code int, e error) { http.Error(w, e.Error(), code) }
	if r.Method == "GET" && r.URL.Path == "/options" {
		options, err := hostOptions(s.config)
		if err != nil {
			failure(503, err)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		json.NewEncoder(w).Encode(options)
		return
	}
	if r.Method == "GET" && r.URL.Path == "/status" {
		json.NewEncoder(w).Encode(Status{s.config, s.commands["dhcp"] != nil, s.commands["tftp"] != nil, s.last})
		return
	}
	if r.URL.Path == "/test-vm" || strings.HasPrefix(r.URL.Path, "/test-vm/") {
		s.testVMHTTP(w, r)
		return
	}
	busy, e := s.active(r.Context())
	if e != nil {
		failure(503, errors.New("无法确认活动装机任务，拒绝变更"))
		return
	}
	if busy {
		failure(409, errors.New("存在活动装机任务，拒绝服务变更"))
		return
	}
	if r.Method == "PUT" && r.URL.Path == "/config" {
		if s.commands["dhcp"] != nil || s.commands["tftp"] != nil {
			failure(409, errors.New("请先停止两项服务再修改配置"))
			return
		}
		var c Config
		d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 262144))
		d.DisallowUnknownFields()
		if e = d.Decode(&c); e != nil {
			failure(400, e)
			return
		}
		validationService := "dhcp"
		if c.TenantID == 0 && c.ProjectID == 0 && c.NetworkID == 0 && c.PoolID == 0 {
			validationService = "tftp"
		}
		if e = c.Validate(validationService, true); e != nil {
			failure(400, e)
			return
		}
		if e = c.BootFiles(); e != nil {
			failure(400, e)
			return
		}
		data, _ := json.Marshal(c)
		if e = os.WriteFile(StateFile+".tmp", data, 0600); e == nil {
			e = os.Rename(StateFile+".tmp", StateFile)
		}
		if e != nil {
			failure(500, e)
			return
		}
		s.config = c
		s.last = ""
		json.NewEncoder(w).Encode(c)
		return
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if r.Method != "POST" || len(parts) != 2 || (parts[0] != "dhcp" && parts[0] != "tftp") || (parts[1] != "start" && parts[1] != "stop") {
		failure(404, errors.New("unsupported operation"))
		return
	}
	name := parts[0]
	if parts[1] == "stop" {
		if cmd := s.commands[name]; cmd != nil {
			if e = cmd.Process.Kill(); e != nil {
				failure(500, e)
				return
			}
			delete(s.commands, name)
		}
		json.NewEncoder(w).Encode(map[string]bool{"stopped": true})
		return
	}
	if s.commands[name] != nil {
		json.NewEncoder(w).Encode(map[string]bool{"running": true})
		return
	}
	if e = s.config.Validate(name, true); e != nil {
		failure(400, e)
		return
	}
	if e = s.config.BootFiles(); e != nil {
		failure(400, e)
		return
	}
	path := "/var/lib/bmm-pxe/" + name + ".conf"
	if e = os.WriteFile(path, []byte(s.config.Render(name)), 0600); e != nil {
		failure(500, e)
		return
	}
	test := exec.CommandContext(r.Context(), "/usr/sbin/dnsmasq", "--test", "--conf-file="+path)
	if e = test.Run(); e != nil {
		failure(400, errors.New("dnsmasq 配置校验失败"))
		return
	}
	cmd := exec.Command("/usr/sbin/dnsmasq", "--conf-file="+path)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if e = cmd.Start(); e != nil {
		failure(500, e)
		return
	}
	s.commands[name] = cmd
	s.last = ""
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case e = <-done:
		delete(s.commands, name)
		s.last = fmt.Sprintf("%s exited: %v", name, e)
		failure(503, errors.New(s.last))
		return
	case <-time.After(300 * time.Millisecond):
	}
	go func() {
		e := <-done
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.commands[name] == cmd {
			delete(s.commands, name)
			s.last = fmt.Sprintf("%s exited: %v", name, e)
		}
	}()
	json.NewEncoder(w).Encode(map[string]bool{"running": true})
}
func Call(ctx context.Context, method, path string, body []byte) ([]byte, int, error) {
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", Socket)
	}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 8 * time.Second}
	req, e := http.NewRequestWithContext(ctx, method, "http://pxe"+path, strings.NewReader(string(body)))
	if e != nil {
		return nil, 0, e
	}
	resp, e := client.Do(req)
	if e != nil {
		return nil, 0, errors.New("PXE 控制服务不可达，实际状态未知")
	}
	defer resp.Body.Close()
	b, e := io.ReadAll(io.LimitReader(resp.Body, 262145))
	if len(b) > 262144 {
		return nil, 0, errors.New("PXE 控制服务响应超过大小限制")
	}
	return b, resp.StatusCode, e
}
