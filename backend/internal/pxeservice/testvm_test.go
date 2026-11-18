package pxeservice

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
