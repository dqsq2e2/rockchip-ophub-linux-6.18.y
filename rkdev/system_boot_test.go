package main

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBootClockRejectsUnsetRTC(t *testing.T) {
	if err := validateBootClock(time.Unix(3600, 0)); err == nil {
		t.Fatal("unset RTC allowed to modify installed boot filesystem")
	}
	if err := validateBootClock(bootClockFloor); err != nil {
		t.Fatal(err)
	}
	if err := validateBootClock(bootClockFloor.Add(365 * 24 * time.Hour)); err != nil {
		t.Fatal(err)
	}
}

func TestSystemBootRemapsRoot(t *testing.T) {
	mapping := map[int]bootPartitionRef{1: {2, "boot-guid"}, 2: {3, "root-guid"}}
	source := []byte("test -n \"${distro_rootpart}\" || distro_rootpart=2\nsetenv distro_rootpart \"2\"\nAPPEND root=/dev/mmcblk0p2 rw\nrootdev=/dev/nvme0n1p2\nroot=PARTUUID=existing-guid\n")
	got := string(adaptBootText(source, mapping))
	for _, want := range []string{"distro_rootpart=3", "setenv distro_rootpart \"3\"", "root=PARTUUID=root-guid", "rootdev=PARTUUID=root-guid", "root=PARTUUID=existing-guid"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in %s", want, got)
		}
	}
}

func TestLegacyBootScriptConversion(t *testing.T) {
	script := []byte("distro_rootpart=2\n")
	payload := make([]byte, 8+len(script))
	binary.BigEndian.PutUint32(payload[:4], uint32(len(script)))
	binary.BigEndian.PutUint32(payload[4:8], 0xffffffff)
	copy(payload[8:], script)
	header := make([]byte, 64)
	binary.BigEndian.PutUint32(header[:4], 0x27051956)
	binary.BigEndian.PutUint32(header[12:16], uint32(len(payload)))
	binary.BigEndian.PutUint32(header[24:28], crc32.ChecksumIEEE(payload))
	header[30] = 6
	binary.BigEndian.PutUint32(header[4:8], crc32.ChecksumIEEE(header))
	result, err := adaptBootScript(append(header, payload...), map[int]bootPartitionRef{2: {3, "root-guid"}})
	if err != nil {
		t.Fatal(err)
	}
	if binary.BigEndian.Uint32(result[68:72]) != 0 || !bytes.Contains(result[72:], []byte("distro_rootpart=3")) {
		t.Fatal("legacy script not normalized/remapped")
	}
	if _, err := adaptBootScript(result, nil); err != nil {
		t.Fatalf("rewritten CRC invalid: %v", err)
	}
	result[4] ^= 1
	if _, err := adaptBootScript(result, nil); err == nil {
		t.Fatal("bad script CRC accepted")
	}
}

func TestMountedBootConfig(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "extlinux"), 0755)
	path := filepath.Join(dir, "extlinux/extlinux.conf")
	os.WriteFile(path, []byte("APPEND root=/dev/mmcblk0p2 rw\n"), 0644)
	if err := adaptMountedBoot(dir, map[int]bootPartitionRef{2: {3, "root-guid"}}); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "APPEND root=PARTUUID=root-guid rw\n" {
		t.Fatalf("%s", got)
	}
}
