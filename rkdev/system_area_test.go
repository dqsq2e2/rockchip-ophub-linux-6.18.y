package main

import (
	"bytes"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func sparseDisk(t *testing.T, path string, size int64, args ...string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(size); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if out, err := exec.Command("sgdisk", append(args, path)...).CombinedOutput(); err != nil {
		t.Fatalf("sgdisk: %v: %s", err, out)
	}
}

func TestSystemAreaMultiPartitionImage(t *testing.T) {
	if _, err := exec.LookPath("sgdisk"); err != nil {
		t.Skip("requires sgdisk")
	}
	dir := t.TempDir()
	target, source := filepath.Join(dir, "emmc.img"), filepath.Join(dir, "system.img")
	sparseDisk(t, target, 1024*1024*1024, "-o", "-n", "1:65536:524253", "-c", "1:LUNZN_RECOVERY",
		"-n", "2:524288:0", "-c", "2:LUNZN_SYSTEM")
	sparseDisk(t, source, 16*1024*1024, "-o", "-n", "1:2048:4095", "-c", "1:BOOT",
		"-A", "1:set:0", "-A", "1:set:60", "-n", "2:4096:8191", "-c", "2:rootfs")
	old, err := readGPT(target)
	if err != nil {
		t.Fatal(err)
	}
	original, err := readGPT(source)
	if err != nil {
		t.Fatal(err)
	}
	parts, items, err := planSystemArea(source, 2097152, old)
	if err != nil {
		t.Fatal(err)
	}
	if len(parts) != 2 || parts[0].Start != systemStartSector+2048 || parts[1].Start != systemStartSector+4096 {
		t.Fatalf("incorrect relocation: %+v", parts)
	}
	for i, part := range parts {
		if part.UUID != original[i].UUID || part.Type != original[i].Type ||
			part.Number != i+2 || part.Attributes != original[i].Attributes {
			t.Fatalf("partition identity/offset changed: %+v", part)
		}
	}
	if len(items) != 1 || items[0].Offset != int64(systemStartSector*512) || items[0].SourceOffset != 0 || items[0].Size != 16*1024*1024 {
		t.Fatalf("complete image not written at B origin: %+v", items)
	}
	if _, _, err := planSystemArea(source, systemStartSector+4096, old); err == nil {
		t.Fatal("oversized image accepted")
	}
	args, err := systemPartitionArgs(old, parts)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(args, " "), ":=:") {
		t.Fatalf("unsupported attribute mask syntax generated: %v", args)
	}
	attrArgs := strings.Join(args, " ")
	if !strings.Contains(attrArgs, "-A 2:set:0") || !strings.Contains(attrArgs, "-A 2:set:60") {
		t.Fatalf("source GPT attribute bits were not preserved: %s", attrArgs)
	}
	if strings.Contains(attrArgs, "-A 3:") {
		t.Fatalf("zero source attributes should not generate -A: %s", attrArgs)
	}
	if out, err := exec.Command("sgdisk", append(args, target)...).CombinedOutput(); err != nil {
		t.Fatalf("update GPT: %v: %s", err, out)
	}
	got, err := readGPT(target)
	if err != nil || len(got) != 3 || got[0] != old[0] || got[1] != parts[0] || got[2] != parts[1] {
		t.Fatalf("recovery not preserved or import incorrect: %+v %v", got, err)
	}
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	data[512+24] ^= 1
	if err := os.WriteFile(source, data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := planSystemArea(source, 2097152, old); err == nil {
		t.Fatal("bad GPT CRC accepted")
	}
}

func TestSystemAreaDoesNotWriteSourceLoader(t *testing.T) {
	dir := t.TempDir()
	src, dst := filepath.Join(dir, "source"), filepath.Join(dir, "destination")
	content := append(bytes.Repeat([]byte{0x11}, 512), bytes.Repeat([]byte{0x22}, 512)...)
	os.WriteFile(src, content, 0600)
	os.WriteFile(dst, append(bytes.Repeat([]byte{0xaa}, 512), content[512:]...), 0600)
	task := &FlashTask{TargetDev: dst, Items: []FlashWriteItem{{Name: "boot", Path: src, SourceOffset: 512, Offset: 512, Size: 512}}}
	if err := verifySystemArea(task); err != nil {
		t.Fatal(err)
	}
	changed, _ := os.ReadFile(dst)
	if !bytes.Equal(changed[:512], bytes.Repeat([]byte{0xaa}, 512)) {
		t.Fatal("loader prefix overwritten")
	}
	changed[700] ^= 1
	os.WriteFile(dst, changed, 0600)
	if err := verifySystemArea(task); err == nil {
		t.Fatal("bad readback accepted")
	}
}

func TestSystemAreaReleaseImage(t *testing.T) {
	source := os.Getenv("LUNZN_TEST_SYSTEM_IMAGE")
	if source == "" {
		t.Skip("set LUNZN_TEST_SYSTEM_IMAGE to verify a real system image")
	}
	target := filepath.Join(t.TempDir(), "emmc.img")
	const sectors = uint64(122142720)
	sparseDisk(t, target, int64(sectors*512), "-o",
		"-n", "1:65536:524253", "-c", "1:LUNZN_RECOVERY",
		"-n", "2:524288:0", "-c", "2:LUNZN_SYSTEM")
	current, err := readGPT(target)
	if err != nil {
		t.Fatal(err)
	}
	parts, _, err := planSystemArea(source, sectors, current)
	if err != nil {
		t.Fatal(err)
	}
	args, err := systemPartitionArgs(current, parts)
	if err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("sgdisk", append(args, target)...).CombinedOutput(); err != nil {
		t.Fatalf("real image partition mapping: %v: %s", err, out)
	}
	got, err := readGPT(target)
	if err != nil || len(got) != len(parts)+1 || got[0] != current[0] {
		t.Fatalf("recovery partition changed: %+v, %v", got, err)
	}
	for i, part := range parts {
		if got[i+1] != part {
			t.Fatalf("source partition was not preserved: got %+v, want %+v", got[i+1], part)
		}
		t.Logf("mapped p%d %s: %d-%d attributes=%016x", part.Number, part.Name,
			part.Start, part.End, part.Attributes)
	}
}

func TestNetworkAddressGrouping(t *testing.T) {
	var addrs []net.Addr
	for _, value := range []string{"192.168.1.226/24", "240e::123/64", "fe80::123/64", "192.168.1.226/24"} {
		ip, network, err := net.ParseCIDR(value)
		if err != nil {
			t.Fatal(err)
		}
		network.IP = ip
		addrs = append(addrs, network)
	}
	if got := interfaceAddress(addrs); got != "192.168.1.226" {
		t.Fatalf("got %s", got)
	}
	if got := interfaceAddress(addrs[1:3]); !strings.Contains(got, "240e::123") {
		t.Fatalf("IPv6 missing: %s", got)
	}
}

func TestBootCommandsMatchBuiltEnvironment(t *testing.T) {
	data, err := os.ReadFile("../../uboot-mainline/initial-env.txt")
	if err != nil {
		t.Skip("U-Boot not built")
	}
	values := make(map[string]string)
	for _, line := range strings.Split(string(data), "\n") {
		if key, value, ok := strings.Cut(line, "="); ok {
			values[key] = strings.TrimSpace(value)
		}
	}
	normalize := func(value string) string { return strings.Join(strings.Fields(value), " ") }
	for _, key := range []string{"boot_system_usb", "boot_system_nvme", "boot_system_sd", "boot_system_emmc", "lunzn_scan", "lunzn_boot_part"} {
		if normalize(values[key]) != normalize(ubootDefaultVars[key]) {
			t.Errorf("%s differs from built U-Boot", key)
		}
	}
}
