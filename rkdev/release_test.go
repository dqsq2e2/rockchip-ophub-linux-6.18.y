package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOptionalReleaseChecksum(t *testing.T) {
	actual := strings.Repeat("ab", 32)
	for _, supplied := range []string{"", "  ", actual, strings.ToUpper(actual)} {
		if err := checkReleaseChecksum(actual, supplied); err != nil {
			t.Fatalf("valid optional checksum %q rejected: %v", supplied, err)
		}
	}
	for _, supplied := range []string{"bad", strings.Repeat("cd", 32)} {
		if checkReleaseChecksum(actual, supplied) == nil {
			t.Fatalf("incorrect supplied checksum %q accepted", supplied)
		}
	}
}

func TestProtectedTargets(t *testing.T) {
	for _, target := range []string{"/dev/mmcblk0p1", "/dev/mmcblk0p2", "/dev/mmcblk0p3", "/dev/mmcblk0boot0", "system", "system-a", "system-b"} {
		if validateFlashTarget(target) == nil {
			t.Fatalf("normal flash accepted protected target %q", target)
		}
	}
	if validateFlashTarget("/dev/mmcblk0") != nil || !isSystemSlotTarget("/dev/mmcblk0") {
		t.Fatal("eMMC system area target rejected")
	}
}

func TestPairedCopyBoundaries(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "destination")
	source := filepath.Join(dir, "source")
	original := bytes.Repeat([]byte{0xa5}, 4096)
	if err := os.WriteFile(dest, original, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, bytes.Repeat([]byte{0x5a}, 4096), 0600); err != nil {
		t.Fatal(err)
	}
	dst, err := os.OpenFile(dest, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer dst.Close()
	src, err := os.Open(source)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	for _, region := range [][2]int64{{512, 512}, {1024, 1024}} {
		src.Seek(region[0], 0)
		if _, err := copyRange(dst, src, region[0], region[1]); err != nil {
			t.Fatal(err)
		}
	}
	result, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(result[:512], original[:512]) ||
		!bytes.Equal(result[2048:], original[2048:]) {
		t.Fatal("paired write changed protected head or system tail")
	}
	if !bytes.Equal(result[512:2048], bytes.Repeat([]byte{0x5a}, 1536)) {
		t.Fatal("paired write did not update both ranges")
	}
}

func TestReleasePackageValidation(t *testing.T) {
	path := os.Getenv("LUNZN_TEST_RELEASE_IMAGE")
	if path == "" {
		path = "../release-10.26/rk3588-AIUI-recovery-10.26.img"
	}
	if _, err := os.Stat(path); err != nil {
		t.Skip("release package not built")
	}
	if err := validateReleaseImage(path); err != nil {
		t.Fatal(err)
	}
}
