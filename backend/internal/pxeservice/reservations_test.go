package pxeservice

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

func reservationConfig() Config {
	c := validConfig()
	c.DHCPMode, c.Gateway = "relay", "192.0.2.1"
	c.Reservations = []DHCPReservation{{Name: "机柜 A / 端口 1", Enabled: true, CircuitID: "00:01", CircuitFormat: "text", RemoteID: "00:04:00:64", RemoteFormat: "hex", IP: "192.0.2.110"}}
	return c
}
func TestDHCPReservationsValidateAndSyntax(t *testing.T) {
	c := reservationConfig()
	c.TaggedOnly, c.RelayProxy = true, true
	if err := c.Validate("dhcp", false); err != nil {
		t.Fatal(err)
	}
	rendered := c.Render("dhcp")
	for _, want := range []string{"dhcp-circuitid=set:bmm-r1-circuit,30:30:3a:30:31\n", "dhcp-remoteid=set:bmm-r1-remote,00:04:00:64\n", "dhcp-host=tag:bmm-r1-circuit,tag:bmm-r1-remote,*:*:*:*:*:*,id:*,set:bmm-reserved,192.0.2.110\n", "dhcp-ignore=tag:!bmm-reserved\n", "dhcp-proxy\n"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("missing %q", want)
		}
	}
	if binary, err := exec.LookPath("dnsmasq"); err == nil {
		file := t.TempDir() + "/dnsmasq.conf"
		if err := os.WriteFile(file, []byte(rendered), 0600); err != nil {
			t.Fatal(err)
		}
		if out, err := exec.Command(binary, "--test", "--conf-file="+file).CombinedOutput(); err != nil {
			t.Fatalf("dnsmasq syntax: %v %s", err, out)
		}
	}
	for name, change := range map[string]func(*Config){
		"no match":             func(c *Config) { c.Reservations[0].CircuitID = ""; c.Reservations[0].RemoteID = "" },
		"name control":         func(c *Config) { c.Reservations[0].Name = "port\nport=53" },
		"hex injection":        func(c *Config) { c.Reservations[0].RemoteID = "00:04\nport=53" },
		"text control":         func(c *Config) { c.Reservations[0].CircuitID = "port\nport=53" },
		"invalid format":       func(c *Config) { c.Reservations[0].CircuitFormat = "auto" },
		"oversize ID":          func(c *Config) { c.Reservations[0].CircuitID = strings.Repeat("x", 129) },
		"gateway IP":           func(c *Config) { c.Reservations[0].IP = c.Gateway },
		"server IP":            func(c *Config) { c.Reservations[0].IP = c.Server },
		"network IP":           func(c *Config) { c.Reservations[0].IP = "192.0.2.0" },
		"broadcast IP":         func(c *Config) { c.Reservations[0].IP = "192.0.2.255" },
		"remote subnet IP":     func(c *Config) { c.Reservations[0].IP = "198.51.100.10" },
		"MAC wildcard":         func(c *Config) { c.Reservations[0].MAC = "02:00:00:00:00:*" },
		"multicast MAC":        func(c *Config) { c.Reservations[0].MAC = "01:00:00:00:00:01" },
		"zero MAC":             func(c *Config) { c.Reservations[0].MAC = "00:00:00:00:00:00" },
		"Option82 direct mode": func(c *Config) { c.DHCPMode = "direct" },
		"strict disabled":      func(c *Config) { c.Reservations[0].Enabled = false; c.TaggedOnly = true },
		"duplicate IP": func(c *Config) {
			other := c.Reservations[0]
			other.CircuitID = "another-port"
			c.Reservations = append(c.Reservations, other)
		},
		"overlapping conditions": func(c *Config) {
			other := c.Reservations[0]
			other.IP = "192.0.2.111"
			other.RemoteID = ""
			c.Reservations = append(c.Reservations, other)
		},
		"equivalent text and hex": func(c *Config) {
			other := c.Reservations[0]
			other.IP = "192.0.2.111"
			other.CircuitFormat = "hex"
			other.CircuitID = "30:30:3a:30:31"
			c.Reservations = append(c.Reservations, other)
		},
	} {
		t.Run(name, func(t *testing.T) {
			invalid := reservationConfig()
			change(&invalid)
			if invalid.Validate("dhcp", false) == nil {
				t.Fatal("invalid rule accepted")
			}
		})
	}
}
func TestDHCPReservationDistinctRulesAndEquality(t *testing.T) {
	c := reservationConfig()
	other := c.Reservations[0]
	other.RemoteID = "00:04:00:65"
	other.IP = "192.0.2.20"
	c.Reservations = append(c.Reservations, other)
	if err := c.Validate("dhcp", false); err != nil {
		t.Fatal(err)
	}
	c.Reservations[0].Enabled = false
	if strings.Contains(c.Render("dhcp"), "bmm-r1-") {
		t.Fatal("disabled rule rendered")
	}
	left, right := validConfig(), validConfig()
	right.Reservations = []DHCPReservation{}
	if !left.Equal(right) {
		t.Fatal("empty rule lists changed config identity")
	}
	right.Reservations = c.Reservations
	if left.Equal(right) {
		t.Fatal("rule changes ignored")
	}
	left = right
	left.Reservations = append([]DHCPReservation(nil), right.Reservations...)
	left.Reservations[0].IP = "192.0.2.120"
	if left.Equal(right) {
		t.Fatal("fixed IP changes ignored")
	}
	left = validConfig()
	left.Reservations = []DHCPReservation{{Name: "MAC only", Enabled: true, MAC: "02:00:00:00:00:01", IP: "192.0.2.110"}}
	if err := left.Validate("dhcp", false); err != nil {
		t.Fatal(err)
	}
}
