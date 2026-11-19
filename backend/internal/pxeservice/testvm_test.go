package pxeservice

import (
	"context"
	"errors"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestDeleteVMStopsProcessAndOnlyRemovesVMFiles(t *testing.T) {
	dir := t.TempDir()
	files := []string{"test-vm.qcow2", "test-vm-vars.fd", "test-vm-serial.log", "test-vm.json.tmp", "test-vm.json", "config.json", "ubuntu.iso"}
	for _, file := range files {
		if err := os.WriteFile(filepath.Join(dir, file), []byte("keep or delete"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()
	c := &controller{commands: map[string]*exec.Cmd{"test-vm": cmd}, vmDone: done, vm: TestVMConfig{TenantID: 1, ProjectID: 2, Firmware: "uefi"}, vmError: "previous failure"}
	if err := c.deleteTestVM(dir); err != nil {
		t.Fatal(err)
	}
	if c.commands["test-vm"] != nil || c.vm != (TestVMConfig{}) || c.vmError != "" {
		t.Fatal("VM process/ownership not released")
	}
	for _, file := range files[:5] {
		if _, err := os.Lstat(filepath.Join(dir, file)); !os.IsNotExist(err) {
			t.Fatalf("VM file remains: %s", file)
		}
	}
	for _, file := range files[5:] {
		if _, err := os.Stat(filepath.Join(dir, file)); err != nil {
			t.Fatalf("shared file removed: %s", file)
		}
	}
	if err := c.deleteTestVM(dir); err != nil {
		t.Fatalf("idempotent retry: %v", err)
	}
}

func TestDeleteVMRejectsUnexpectedFilesBeforeRemovingDisk(t *testing.T) {
	for _, kind := range []string{"directory", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			disk := filepath.Join(dir, "test-vm.qcow2")
			if err := os.WriteFile(disk, []byte("disk"), 0600); err != nil {
				t.Fatal(err)
			}
			vars := filepath.Join(dir, "test-vm-vars.fd")
			var err error
			if kind == "directory" {
				err = os.Mkdir(vars, 0700)
			} else {
				err = os.Symlink(disk, vars)
			}
			if err != nil {
				t.Fatal(err)
			}
			owner := TestVMConfig{TenantID: 1, ProjectID: 2}
			c := &controller{commands: map[string]*exec.Cmd{}, vm: owner}
			if c.deleteTestVM(dir) == nil {
				t.Fatal("unexpected file accepted")
			}
			if _, err := os.Stat(disk); err != nil || c.vm != owner {
				t.Fatal("disk or ownership lost on failure")
			}
		})
	}
}

func TestDeleteVMHTTPGuards(t *testing.T) {
	for _, mode := range []string{"other-project", "active", "unknown", "bad-owner", "unsupported-path"} {
		t.Run(mode, func(t *testing.T) {
			c := &controller{vm: TestVMConfig{TenantID: 1, ProjectID: 2, Firmware: "uefi"}, commands: map[string]*exec.Cmd{}, active: func(context.Context) (bool, error) {
				if mode == "unknown" {
					return false, errors.New("database unavailable")
				}
				return mode == "active", nil
			}}
			body := `{"tenant_id":1,"project_id":2,"firmware":"uefi"}`
			if mode == "other-project" {
				body = `{"tenant_id":1,"project_id":3,"firmware":"uefi"}`
			}
			if mode == "bad-owner" {
				body = `{}`
			}
			path := "/test-vm"
			if mode == "unsupported-path" {
				path += "/delete"
			}
			w := httptest.NewRecorder()
			c.ServeHTTP(w, httptest.NewRequest("DELETE", path, strings.NewReader(body)))
			if w.Code != 409 && w.Code != 400 && w.Code != 404 {
				t.Fatalf("delete was not rejected: %d", w.Code)
			}
			if c.vm.TenantID != 1 || c.vm.ProjectID != 2 {
				t.Fatal("guard changed VM ownership")
			}
		})
	}
}

func TestSerialLogBoundedAndRedacted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "serial.log")
	data := strings.Repeat("old\n", 8000) + "\x1b[32mUbuntu\x1b[0m\r\nhttp://server/install-callback/secret_callback_token_123\nhttp://server/nocloud-token/secret_callback_token_123/mac/\nhttp://server/media-token/secret_callback_token_123/iso/test.iso\n"
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	result := testVMSerialLog(path)
	if len(result) > 16000 || strings.Contains(result, "secret_callback_token_123") || strings.ContainsRune(result, '\x1b') || !strings.Contains(result, "Ubuntu\n") || !strings.Contains(result, "install-callback/[redacted]") {
		t.Fatalf("invalid log tail: %q", result)
	}
	if testVMSerialLog(path+".missing") != "" {
		t.Fatal("missing logs should be empty")
	}
}

func TestLabDHCPFixedAddressOnlyOnLabNetwork(t *testing.T) {
	lab := Config{Interface: TestVMBridge, Subnet: "192.168.77.0/24", Server: "192.168.77.1", Start: "192.168.77.100", End: "192.168.77.200"}
	if !strings.Contains(lab.Render("dhcp"), "dhcp-host="+TestVMMAC+",192.168.77.100") {
		t.Fatal("test VM must receive the address recorded in the installation snapshot")
	}
	lab.Interface = "eth0"
	if strings.Contains(lab.Render("dhcp"), "dhcp-host="+TestVMMAC) {
		t.Fatal("fixed test lease leaked into a physical network config")
	}
}

func TestVMUsesOneTimePXEAndDiskForOrdinaryRestart(t *testing.T) {
	if got := testVMBootOrder(true); !strings.Contains(got, "once=n") || !strings.Contains(got, "order=c") {
		t.Fatalf("PXE order %q", got)
	}
	if got := testVMBootOrder(false); strings.Contains(got, "once=n") || strings.Contains(got, "order=n") {
		t.Fatalf("ordinary restart must use disk: %q", got)
	}
}

func TestUEFIPXEDevicePriority(t *testing.T) {
	if !strings.HasSuffix(testVMNetworkDevice("uefi", true), "bootindex=1") || !strings.HasSuffix(testVMDiskDevice("uefi", true), "bootindex=2") {
		t.Fatal("UEFI PXE must explicitly prefer NIC over the existing disk")
	}
	if !strings.HasSuffix(testVMDiskDevice("uefi", false), "bootindex=1") || !strings.HasSuffix(testVMNetworkDevice("uefi", false), "bootindex=2") {
		t.Fatal("UEFI ordinary start must prefer disk")
	}
	if strings.Contains(testVMNetworkDevice("bios", true), "bootindex") || strings.Contains(testVMDiskDevice("bios", true), "bootindex") {
		t.Fatal("BIOS once boot cannot mix with device priority")
	}
}
