package pxeservice

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const defaultTFTPRoot = "/var/lib/tftpboot"

type HostAddress struct {
	Address string `json:"address"`
	Prefix  string `json:"prefix"`
	Subnet  string `json:"subnet"`
}
type HostInterface struct {
	Name      string        `json:"name"`
	MAC       string        `json:"mac"`
	Up        bool          `json:"up"`
	Addresses []HostAddress `json:"addresses"`
}
type BootFile struct {
	Name  string `json:"name"`
	Label string `json:"label"`
}
type BootRoot struct {
	Path       string     `json:"path"`
	BIOS       []BootFile `json:"bios_files"`
	EFI        []BootFile `json:"efi_files"`
	ChainReady bool       `json:"chain_ready"`
}
type HostOptions struct {
	Interfaces []HostInterface `json:"interfaces"`
	Roots      []BootRoot      `json:"roots"`
}

var builtinBIOS = []BootFile{{"undionly.kpxe", "undionly.kpxe · BIOS / UNDI（推荐）"}, {"ipxe.pxe", "ipxe.pxe · BIOS / iPXE 原生驱动"}}
var builtinEFI = []BootFile{{"ipxe.efi", "ipxe.efi · x86_64 UEFI（推荐）"}, {"snponly.efi", "snponly.efi · x86_64 UEFI / SNP 驱动"}}

func interfaceOption(iface net.Interface, addrs []net.Addr) HostInterface {
	row := HostInterface{Name: iface.Name, MAC: iface.HardwareAddr.String(), Up: iface.Flags&net.FlagUp != 0, Addresses: []HostAddress{}}
	seen := map[string]bool{}
	for _, raw := range addrs {
		prefix, err := netip.ParsePrefix(raw.String())
		if err != nil || !prefix.Addr().Is4() || !prefix.Addr().IsGlobalUnicast() || seen[prefix.String()] {
			continue
		}
		seen[prefix.String()] = true
		row.Addresses = append(row.Addresses, HostAddress{prefix.Addr().String(), prefix.String(), prefix.Masked().String()})
	}
	sort.Slice(row.Addresses, func(i, j int) bool { return row.Addresses[i].Prefix < row.Addresses[j].Prefix })
	return row
}

func regularFileWithin(root, name string) bool {
	if name == "" || filepath.IsAbs(name) || filepath.Clean(name) != name || strings.Contains(name, "..") {
		return false
	}
	path, err := filepath.EvalSymlinks(filepath.Join(root, name))
	if err != nil || !strings.HasPrefix(path, root+string(os.PathSeparator)) {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular() && info.Size() > 0
}

func bootRoots(base string, cfg Config) []BootRoot {
	paths := []string{base}
	// Only inspect immediate directories and known boot names, never installation
	// seeds, callback tokens, disk images or an unrestricted recursive file tree.
	if dir, err := os.Open(base); err == nil {
		entries, _ := dir.ReadDir(128)
		dir.Close()
		for _, entry := range entries {
			if entry.IsDir() && entry.Type()&os.ModeSymlink == 0 {
				paths = append(paths, filepath.Join(base, entry.Name()))
			}
		}
	}
	if cfg.Root != base && strings.HasPrefix(cfg.Root, base+"/") {
		paths = append(paths, cfg.Root)
	}
	seen := map[string]bool{}
	result := []BootRoot{}
	for _, path := range paths {
		if seen[path] || filepath.Clean(path) != path {
			continue
		}
		seen[path] = true
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil || resolved != path {
			continue
		}
		info, err := os.Stat(path)
		if err != nil || !info.IsDir() {
			continue
		}
		root := BootRoot{Path: path, BIOS: []BootFile{}, EFI: []BootFile{}, ChainReady: regularFileWithin(path, "default.ipxe")}
		for kind, files := range [][]BootFile{builtinBIOS, builtinEFI} {
			if path == cfg.Root {
				custom := cfg.BIOS
				if kind == 1 {
					custom = cfg.EFI
				}
				files = append(append([]BootFile{}, files...), BootFile{custom, custom + " · 当前配置"})
			}
			added := map[string]bool{}
			for _, file := range files {
				if !added[file.Name] && regularFileWithin(path, file.Name) {
					added[file.Name] = true
					if kind == 0 {
						root.BIOS = append(root.BIOS, file)
					} else {
						root.EFI = append(root.EFI, file)
					}
				}
			}
		}
		if path == base || path == cfg.Root || len(root.BIOS)+len(root.EFI) > 0 {
			result = append(result, root)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Path < result[j].Path })
	return result
}

func hostOptions(cfg Config) (HostOptions, error) {
	result := HostOptions{Interfaces: []HostInterface{}, Roots: bootRoots(defaultTFTPRoot, cfg)}
	interfaces, err := net.Interfaces()
	if err != nil {
		return result, err
	}
	for _, iface := range interfaces {
		if iface.Flags&net.FlagLoopback != 0 || !safeName.MatchString(iface.Name) {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			return result, fmt.Errorf("读取网卡 %s 地址失败: %w", iface.Name, err)
		}
		result.Interfaces = append(result.Interfaces, interfaceOption(iface, addrs))
	}
	sort.Slice(result.Interfaces, func(i, j int) bool { return result.Interfaces[i].Name < result.Interfaces[j].Name })
	return result, nil
}

// Bootstrap only missing files from the runtime image. Local administrator
// files and saved boot choices are never overwritten by a platform upgrade.
func ensureBuiltinBootFiles(root, source, httpBase string) error {
	if err := os.MkdirAll(root, 0755); err != nil {
		return err
	}
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil || resolved != root {
		return errors.New("内置 TFTP 目录包含符号链接")
	}
	for _, file := range append(append([]BootFile{}, builtinBIOS...), builtinEFI...) {
		if _, err := os.Lstat(filepath.Join(root, file.Name)); err == nil {
			continue
		} else if !os.IsNotExist(err) {
			return err
		}
		src, err := os.Open(filepath.Join(source, file.Name))
		if os.IsNotExist(err) {
			continue // Package variants may not provide every optional driver.
		}
		if err != nil {
			return err
		}
		err = publishMissingFile(root, file.Name, src)
		src.Close()
		if err != nil {
			return err
		}
	}
	if _, err := os.Lstat(filepath.Join(root, "default.ipxe")); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	if httpBase == "" {
		return nil // Discovery reports the missing chain; no hard-coded host IP.
	}
	u, err := url.Parse(httpBase)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || strings.ContainsAny(httpBase, "\r\n \t") {
		return errors.New("PXE_HTTP_BASE 必须是装机客户端可访问的 HTTP(S) API 基址")
	}
	return publishMissingFile(root, "default.ipxe", strings.NewReader("#!ipxe\nchain --autofree "+strings.TrimRight(httpBase, "/")+"/ipxe/${net0/mac:hexhyp}\n"))
}

func publishMissingFile(root, name string, src io.Reader) error {
	f, err := os.CreateTemp(root, ".builtin-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, err = io.Copy(f, src)
	if err == nil {
		err = f.Chmod(0644)
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	err = os.Link(f.Name(), filepath.Join(root, name))
	if os.IsExist(err) {
		return nil
	}
	return err
}
