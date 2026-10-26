package main

import (
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Hardware detection excludes software bridges, tunnels and other virtual links.
func hardwareInterfaceType(sysRoot, name string) (string, bool) {
	base := filepath.Join(sysRoot, name)
	kind, err := os.ReadFile(filepath.Join(base, "type"))
	if err != nil || strings.TrimSpace(string(kind)) != "1" {
		return "", false
	}
	device, err := filepath.EvalSymlinks(filepath.Join(base, "device"))
	if err != nil {
		return "", false
	}
	if _, err := os.Stat(filepath.Join(base, "phy80211")); err == nil {
		return "无线", true
	}
	if _, err := os.Stat(filepath.Join(base, "wireless")); err == nil {
		return "无线", true
	}
	subsystem, _ := filepath.EvalSymlinks(filepath.Join(device, "subsystem"))
	if filepath.Base(subsystem) == "usb" {
		return "USB", true
	}
	return "以太网", true
}

func networkCommand(name string, args ...string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 750*time.Millisecond)
	defer cancel()
	out, err := exec.CommandContext(ctx, name, args...).Output()
	if err != nil {
		return ""
	}
	return string(out)
}

func wirelessMode(info string) string {
	for _, line := range strings.Split(info, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[0] == "type" {
			return fields[1]
		}
	}
	return ""
}

func wirelessInterfaceStatus(operstate, info, link, apStatus string) string {
	if operstate == "down" || operstate == "notpresent" {
		return "未连接"
	}
	switch wirelessMode(info) {
	case "AP":
		for _, line := range strings.Split(apStatus, "\n") {
			state, ok := strings.CutPrefix(strings.TrimSpace(line), "state=")
			if !ok {
				continue
			}
			switch state {
			case "ENABLED":
				return "热点已启用"
			case "DISABLED":
				return "热点未启用"
			default:
				return "热点启动中"
			}
		}
		return "热点模式"
	case "managed":
		for _, line := range strings.Split(link, "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "Connected to ") {
				return "已连接"
			}
		}
		return "未连接"
	default:
		return "已启用"
	}
}

// Show IPv4 addresses when available, otherwise the interface's IPv6 addresses.
func interfaceAddress(addresses []net.Addr) string {
	var ipv4, ipv6 []string
	seen := make(map[string]bool)
	for _, address := range addresses {
		value := strings.SplitN(address.String(), "/", 2)[0]
		ip := net.ParseIP(value)
		if ip == nil || seen[value] {
			continue
		}
		seen[value] = true
		if ip.To4() != nil {
			ipv4 = append(ipv4, value)
		} else {
			ipv6 = append(ipv6, value)
		}
	}
	if len(ipv4) != 0 {
		return strings.Join(ipv4, ", ")
	}
	return strings.Join(ipv6, ", ")
}
