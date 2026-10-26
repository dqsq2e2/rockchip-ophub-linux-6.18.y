package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEmmcHealthRangesAndStatus(t *testing.T) {
	for _, test := range []struct{ a, b, eol, status, life string }{
		{"01", "0x01", "01", "normal", "已用 0%–10%"},
		{"09", "02", "01", "normal", "已用 80%–90%"},
		{"0a", "01", "01", "warning", "已用 90%–100%"},
		{"0b", "01", "01", "critical", "已超过设计寿命（>100%）"},
		{"01", "01", "02", "warning", "已用 0%–10%"},
		{"01", "01", "03", "critical", "已用 0%–10%"},
		{"00", "00", "00", "unknown", "未知（设备未上报）"},
		{"ff", "01", "01", "unknown", "未知（保留值）"},
		{"xyz", "01", "01", "unknown", "未知（设备未上报）"},
		{"", "", "", "unknown", "未知（设备未上报）"},
		{"01", "01", "04", "unknown", "已用 0%–10%"},
		{"0b", "", "", "critical", "已超过设计寿命（>100%）"},
	} {
		t.Run(test.a+"-"+test.b+"-"+test.eol, func(t *testing.T) {
			a, b, eol := mmcByte(test.a), mmcByte(test.b), mmcByte(test.eol)
			status, _, _ := emmcStatus(a, b, eol)
			if status != test.status || emmcLife(a).Label != test.life {
				t.Fatalf("status=%s life=%s", status, emmcLife(a).Label)
			}
		})
	}
}

func TestEmmcSysfsDetection(t *testing.T) {
	root := t.TempDir()
	for _, card := range []struct{ name, kind string }{
		{"mmcblk0", "MMC"}, {"mmcblk1", "SD"}, {"mmcblk0boot0", "MMC"},
		{"mmcblk0p1", "MMC"}, {"mmcblk0rpmb", "MMC"}, {"nvme0n1", "MMC"},
	} {
		base := filepath.Join(root, card.name, "device")
		if err := os.MkdirAll(base, 0755); err != nil {
			t.Fatal(err)
		}
		for key, value := range map[string]string{
			"type": card.kind, "name": "CUTB42\n", "life_time": "0x01 0x02\n",
			"pre_eol_info": "0x01\n", "cid": "15010043555442343202a44747f56a00\n",
		} {
			if err := os.WriteFile(filepath.Join(base, key), []byte(value), 0644); err != nil {
				t.Fatal(err)
			}
		}
	}
	devices, err := emmcHealthDevices(root)
	if err != nil || len(devices) != 1 {
		t.Fatalf("devices=%v err=%v", devices, err)
	}
	info := devices[0]
	if info.Name != "CUTB42" || info.Status != "normal" || info.Device != "/dev/mmcblk0" ||
		info.LifeB.Label != "已用 10%–20%" || info.CheckedAt == "" {
		t.Fatalf("incorrect health: %+v", info)
	}
	if readEmmcHealth(root, "../mmcblk0") != nil {
		t.Fatal("invalid device path accepted")
	}
	// Missing health attributes must remain unknown, never synthesized as healthy.
	os.Remove(filepath.Join(root, "mmcblk0/device/life_time"))
	info2 := readEmmcHealth(root, "mmcblk0")
	if info2.Status != "unknown" || info2.LifeA.Raw != nil {
		t.Fatalf("missing values were treated as healthy: %+v", info2)
	}
}

func TestEmmcReadOnlyAPI(t *testing.T) {
	response := httptest.NewRecorder()
	handleEmmc(response, httptest.NewRequest(http.MethodPost, "/api/emmc", nil))
	if response.Code != http.StatusMethodNotAllowed {
		t.Fatal("write method accepted")
	}
}

func TestRecoveryUserTerminology(t *testing.T) {
	for _, term := range []string{"A 恢复", "B 系统", "A/B", " A 分区", " B 分区"} {
		if strings.Contains(indexHTML, term) {
			t.Fatalf("internal partition terminology exposed: %s", term)
		}
	}
	if !strings.Contains(indexHTML, ".boot-type-sd .boot-type-badge") {
		t.Fatal("SD badge style missing")
	}
}
