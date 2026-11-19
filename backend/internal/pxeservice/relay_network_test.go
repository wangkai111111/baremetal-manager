package pxeservice

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Opt-in root-only packet test. Every interface, route and DHCP socket is created
// in disposable network namespaces; no packet can reach a host LAN interface.
func TestRelayDHCPPackets(t *testing.T) {
	if os.Getenv("BMM_PXE_RELAY_NETNS_TEST") != "1" {
		t.Skip("set BMM_PXE_RELAY_NETNS_TEST=1 for isolated Linux DHCP relay packet test")
	}
	role := os.Getenv("BMM_PXE_RELAY_TEST_ROLE")
	if role == "" {
		for _, scenario := range []string{"dynamic", "fixed", "strict", "proxy"} {
			t.Run(scenario, func(t *testing.T) {
				cmd := exec.Command("unshare", "-n", os.Args[0], "-test.run=^TestRelayDHCPPackets$", "-test.v")
				cmd.Env = append(os.Environ(), "BMM_PXE_RELAY_TEST_ROLE=server", "BMM_PXE_RELAY_SCENARIO="+scenario)
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("isolated relay test: %v\n%s", err, out)
				} else {
					t.Log(string(out))
				}
			})
		}
		return
	}
	// Refuse to configure networking if unshare was accidentally removed.
	hostNS, _ := os.Readlink("/proc/1/ns/net")
	selfNS, _ := os.Readlink("/proc/self/ns/net")
	if hostNS == selfNS {
		t.Fatal("packet test must run in a private network namespace")
	}
	ip := func(args ...string) {
		t.Helper()
		if out, err := exec.Command("ip", args...).CombinedOutput(); err != nil {
			t.Fatalf("ip %v: %v: %s", args, err, out)
		}
	}
	if role == "relay" {
		fmt.Println("relay-ready")
		if _, err := bufio.NewReader(os.Stdin).ReadString('\n'); err != nil {
			t.Fatal(err)
		}
		ip("link", "set", "lo", "up")
		ip("addr", "add", "192.0.2.1/24", "dev", "pxe-relay")
		ip("link", "set", "pxe-relay", "up")
		ip("link", "add", "clientnet", "type", "dummy")
		ip("addr", "add", "198.51.100.1/24", "dev", "clientnet")
		ip("link", "set", "clientnet", "up")
		testRelayLease(t)
		return
	}
	if role != "server" {
		t.Fatal("unknown helper role")
	}
	relay := exec.Command("unshare", "-n", os.Args[0], "-test.run=^TestRelayDHCPPackets$")
	relay.Env = append(os.Environ(), "BMM_PXE_RELAY_TEST_ROLE=relay")
	stdin, err := relay.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := relay.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	relay.Stderr = &stderr
	if err := relay.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = relay.Process.Kill(); _ = relay.Wait() }()
	reader := bufio.NewReader(stdout)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("relay setup: %v: %s", err, stderr.String())
		}
		if strings.TrimSpace(line) == "relay-ready" {
			break
		}
	}
	ip("link", "set", "lo", "up")
	ip("link", "add", "pxe-server", "type", "veth", "peer", "name", "pxe-relay")
	ip("link", "set", "pxe-relay", "netns", fmt.Sprint(relay.Process.Pid))
	ip("addr", "add", "192.0.2.2/24", "dev", "pxe-server")
	ip("link", "set", "pxe-server", "up")
	ip("route", "add", "198.51.100.0/24", "via", "192.0.2.1")
	c := validConfig()
	c.Interface, c.Server, c.Subnet, c.Start, c.End = "pxe-server", "192.0.2.2", "198.51.100.0/24", "198.51.100.100", "198.51.100.200"
	c.DHCPMode, c.Gateway, c.DNS = "relay", "198.51.100.1", "223.5.5.5,1.1.1.1"
	if os.Getenv("BMM_PXE_RELAY_SCENARIO") != "dynamic" {
		c.Reservations = []DHCPReservation{
			{Name: "端口一", Enabled: true, CircuitID: "rack-a/port1", CircuitFormat: "text", RemoteID: "00:01:02:03", RemoteFormat: "hex", IP: "198.51.100.110"},
			{Name: "端口二", Enabled: true, CircuitID: "00:04:00:64", CircuitFormat: "hex", IP: "198.51.100.20"},
			{Name: "指定 MAC", Enabled: true, CircuitID: "mac-port", MAC: "02:00:00:00:00:03", IP: "198.51.100.111"},
			{Name: "已停用", Enabled: false, CircuitID: "disabled", IP: "198.51.100.112"},
			{Name: "单字节标识", Enabled: true, CircuitID: "X", IP: "198.51.100.21"},
		}
		c.TaggedOnly = os.Getenv("BMM_PXE_RELAY_SCENARIO") == "strict"
		c.RelayProxy = os.Getenv("BMM_PXE_RELAY_SCENARIO") == "proxy"
	}
	if err := c.Validate("dhcp", false); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	config := strings.ReplaceAll(c.Render("dhcp"), "/var/lib/bmm-pxe/dnsmasq.leases", filepath.Join(dir, "leases"))
	config += "no-ping\n" // Avoid probe delays in the empty, private test subnet.
	path := filepath.Join(dir, "dnsmasq.conf")
	if err := os.WriteFile(path, []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	dnsmasq := exec.Command("dnsmasq", "--conf-file="+path)
	var log bytes.Buffer
	dnsmasq.Stdout, dnsmasq.Stderr = &log, &log
	if err := dnsmasq.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = dnsmasq.Process.Kill(); _ = dnsmasq.Wait() }()
	_, _ = stdin.Write([]byte("start\n"))
	_ = stdin.Close()
	result, readErr := io.ReadAll(reader)
	if err := relay.Wait(); err != nil {
		_ = dnsmasq.Process.Kill()
		_ = dnsmasq.Wait()
		t.Fatalf("relay exchange: %v\n%s\n%s\n%s", err, result, stderr.String(), log.String())
	}
	if readErr != nil {
		t.Fatal(readErr)
	}
	t.Log("Real DHCP DISCOVER/OFFER/REQUEST/ACK across two routed namespaces passed")
}

func testRelayLease(t *testing.T) {
	t.Helper()
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("198.51.100.1"), Port: 67})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	server := &net.UDPAddr{IP: net.ParseIP("192.0.2.2"), Port: 67}
	scenario := os.Getenv("BMM_PXE_RELAY_SCENARIO")
	serverID := []byte{192, 0, 2, 2}
	if scenario == "proxy" {
		serverID = []byte{198, 51, 100, 1}
	}
	packet := func(kind byte, extra []byte) []byte {
		b := make([]byte, 240)
		b[0], b[1], b[2], b[3] = 1, 1, 6, 1
		binary.BigEndian.PutUint32(b[4:8], 0x12345678)
		copy(b[24:28], net.ParseIP("198.51.100.1").To4()) // giaddr selects the remote scope.
		copy(b[28:34], []byte{2, 0, 0, 0, 0, 1})
		copy(b[236:], []byte{99, 130, 83, 99})
		b = append(b, 53, 1, kind, 55, 5, 1, 3, 6, 54, 67)
		b = append(b, extra...)
		return append(b, 255)
	}
	exchange := func(request []byte, want byte, wantFile, wantIP string) []byte {
		t.Helper()
		for attempt := 0; attempt < 5; attempt++ {
			if _, err := conn.WriteToUDP(request, server); err != nil {
				t.Fatal(err)
			}
			_ = conn.SetReadDeadline(time.Now().Add(time.Second))
			buf := make([]byte, 1500)
			n, _, err := conn.ReadFromUDP(buf)
			if err != nil {
				if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
					continue
				}
				t.Fatal(err)
			}
			buf = buf[:n]
			if n < 240 || buf[0] != 2 || !bytes.Equal(buf[4:8], request[4:8]) {
				t.Fatal("invalid BOOTP response")
			}
			opts := map[byte][]byte{}
			for off := 240; off < n && buf[off] != 255; {
				code := buf[off]
				off++
				if code == 0 {
					continue
				}
				if off >= n || off+1+int(buf[off]) > n {
					t.Fatal("truncated DHCP option")
				}
				length := int(buf[off])
				off++
				opts[code] = buf[off : off+length]
				off += length
			}
			if want == 5 && bytes.Equal(opts[53], []byte{2}) {
				continue // A retransmitted DISCOVER can leave a duplicate OFFER queued.
			}
			for code, expected := range map[byte][]byte{53: {want}, 1: {255, 255, 255, 0}, 3: {198, 51, 100, 1}, 6: {223, 5, 5, 5, 1, 1, 1, 1}, 54: serverID} {
				if !bytes.Equal(opts[code], expected) {
					t.Fatalf("option %d: got %v, want %v", code, opts[code], expected)
				}
			}
			bootFile := strings.TrimRight(string(buf[108:236]), "\x00")
			if value := opts[67]; len(value) > 0 {
				bootFile = strings.TrimRight(string(value), "\x00")
			}
			if !bytes.Equal(buf[20:24], net.ParseIP("192.0.2.2").To4()) || bootFile != wantFile {
				t.Fatalf("wrong boot server/file: %v / %q", net.IP(buf[20:24]), bootFile)
			}
			if wantIP != "" {
				if !net.IP(buf[16:20]).Equal(net.ParseIP(wantIP)) {
					t.Fatalf("wrong reserved IP: got %v, want %s", net.IP(buf[16:20]), wantIP)
				}
			} else if !bytes.Equal(buf[16:19], []byte{198, 51, 100}) || buf[19] < 100 || buf[19] > 200 {
				t.Fatalf("lease outside remote pool: %v", net.IP(buf[16:20]))
			}
			return buf
		}
		t.Fatal("no DHCP response after five retries")
		return nil
	}
	option82 := func(circuit, remote []byte) []byte {
		var data []byte
		if len(circuit) > 0 {
			data = append(data, 1, byte(len(circuit)))
			data = append(data, circuit...)
		}
		if len(remote) > 0 {
			data = append(data, 2, byte(len(remote)))
			data = append(data, remote...)
		}
		return append([]byte{82, byte(len(data))}, data...)
	}
	var relayOption []byte
	wantIP := ""
	if scenario != "dynamic" {
		relayOption = option82([]byte("rack-a/port1"), []byte{0, 1, 2, 3})
		wantIP = "198.51.100.110"
	}
	offer := exchange(packet(1, relayOption), 2, "undionly.kpxe", wantIP)
	extra := append([]byte{50, 4}, offer[16:20]...)
	extra = append(extra, 54, 4)
	extra = append(extra, serverID...)
	extra = append(extra, relayOption...)
	ack := exchange(packet(3, extra), 5, "undionly.kpxe", wantIP)
	if !bytes.Equal(offer[16:20], ack[16:20]) {
		t.Fatal("ACK changed the offered address")
	}
	renew := packet(3, relayOption)
	copy(renew[12:16], ack[16:20])
	binary.BigEndian.PutUint32(renew[4:8], 0x12345677)
	exchange(renew, 5, "undionly.kpxe", wantIP)
	uefi := packet(1, append([]byte{93, 2, 0, 7}, relayOption...))
	binary.BigEndian.PutUint32(uefi[4:8], 0x12345679)
	exchange(uefi, 2, "ipxe.efi", wantIP)
	ipxe := packet(1, append([]byte{175, 1, 1}, relayOption...))
	binary.BigEndian.PutUint32(ipxe[4:8], 0x12345680)
	exchange(ipxe, 2, "default.ipxe", wantIP)
	if scenario == "dynamic" {
		return
	}
	// Release the current lease before moving the same device to a different port.
	release := packet(7, relayOption)
	copy(release[12:16], ack[16:20])
	_, _ = conn.WriteToUDP(release, server)
	outside := packet(1, option82([]byte{0, 4, 0, 100}, nil))
	binary.BigEndian.PutUint32(outside[4:8], 0x12345681)
	outside[33] = 2
	exchange(outside, 2, "undionly.kpxe", "198.51.100.20")
	specific := packet(1, option82([]byte("mac-port"), nil))
	binary.BigEndian.PutUint32(specific[4:8], 0x12345682)
	specific[33] = 3
	exchange(specific, 2, "undionly.kpxe", "198.51.100.111")
	oneByte := packet(1, option82([]byte("X"), nil))
	binary.BigEndian.PutUint32(oneByte[4:8], 0x12345683)
	oneByte[33] = 8
	exchange(oneByte, 2, "undionly.kpxe", "198.51.100.21")
	for index, fields := range [][]byte{nil, option82([]byte("rack-a/port1"), []byte{0, 1, 2, 9}), option82([]byte("disabled"), nil), option82([]byte("mac-port"), nil)} {
		unknown := packet(1, fields)
		if index == 0 {
			unknown = packet(1, []byte{50, 4, 198, 51, 100, 110})
		}
		binary.BigEndian.PutUint32(unknown[4:8], uint32(0x12345690+index))
		unknown[33] = byte(4 + index)
		if scenario == "strict" {
			_, _ = conn.WriteToUDP(unknown, server)
			_ = conn.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
			_, _, err := conn.ReadFromUDP(make([]byte, 1500))
			if timeout, ok := err.(net.Error); !ok || !timeout.Timeout() {
				t.Fatalf("unmatched/disabled rule received DHCP response: %v", err)
			}
		} else {
			reply := exchange(unknown, 2, "undionly.kpxe", "")
			if reply[19] == 110 || reply[19] == 111 {
				t.Fatalf("dynamic client received a reserved IP: %v", net.IP(reply[16:20]))
			}
		}
	}
}
