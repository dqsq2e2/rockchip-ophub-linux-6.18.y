package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestHardwareInterfaceType(t *testing.T) {
	root := t.TempDir()
	for _, test := range []struct {
		name, kind, expected string
		physical, wireless   bool
	}{
		{"eth0", "1", "以太网", true, false},
		{"radio0", "1", "无线", true, true},
		{"sit0", "776", "", false, false},
		{"ip6tnl0", "769", "", false, false},
		{"br0", "1", "", false, false},
		{"veth0", "1", "", false, false},
		{"lo", "772", "", false, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			base := filepath.Join(root, test.name)
			if err := os.MkdirAll(base, 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(base, "type"), []byte(test.kind+"\n"), 0644); err != nil {
				t.Fatal(err)
			}
			if test.physical {
				if err := os.Mkdir(filepath.Join(base, "device"), 0755); err != nil {
					t.Fatal(err)
				}
			}
			if test.wireless {
				if err := os.Mkdir(filepath.Join(base, "phy80211"), 0755); err != nil {
					t.Fatal(err)
				}
			}
			got, visible := hardwareInterfaceType(root, test.name)
			if got != test.expected || visible != (test.expected != "") {
				t.Fatalf("got (%q, %v), want %q", got, visible, test.expected)
			}
		})
	}
	usb := filepath.Join(root, "adapter0")
	if err := os.MkdirAll(filepath.Join(usb, "device"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(usb, "type"), []byte("1\n"), 0644); err != nil {
		t.Fatal(err)
	}
	bus := filepath.Join(root, "bus", "usb")
	if err := os.MkdirAll(bus, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(bus, filepath.Join(usb, "device", "subsystem")); err != nil {
		t.Fatal(err)
	}
	if got, visible := hardwareInterfaceType(root, "adapter0"); got != "USB" || !visible {
		t.Fatalf("USB adapter got (%q, %v)", got, visible)
	}
}

func TestWirelessInterfaceStatus(t *testing.T) {
	for _, test := range []struct {
		name, operstate, info, link, apStatus, expected string
	}{
		{"ap-enabled", "up", "\ttype AP\n", "", "state=ENABLED\n", "热点已启用"},
		{"ap-starting", "up", "\ttype AP\n", "", "state=COUNTRY_UPDATE\n", "热点启动中"},
		{"ap-disabled", "up", "\ttype AP\n", "", "state=DISABLED\n", "热点未启用"},
		{"ap-no-control", "up", "\ttype AP\n", "", "", "热点模式"},
		{"station-connected", "up", "\ttype managed\n", "Connected to aa:bb:cc:dd:ee:ff (on radio0)\n", "", "已连接"},
		{"station-unassociated", "up", "\ttype managed\n", "Not connected.\n", "", "未连接"},
		{"station-unknown-state", "unknown", "\ttype managed\n", "Connected to aa:bb:cc:dd:ee:ff\n", "", "已连接"},
		{"down", "down", "\ttype AP\n", "", "state=ENABLED\n", "未连接"},
		{"missing-iw", "up", "", "", "", "已启用"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := wirelessInterfaceStatus(test.operstate, test.info, test.link, test.apStatus); got != test.expected {
				t.Fatalf("got %q, want %q", got, test.expected)
			}
		})
	}
}
