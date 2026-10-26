package main

import (
	"html/template"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLunznDiskNames(t *testing.T) {
	for name, want := range map[string]bool{
		"mmcblk0": true, "mmcblk12": true, "nvme0n1": true, "sda": true,
		"mmcblk0boot0": false, "mmcblk0boot1": false, "mmcblk0rpmb": false,
		"mmcblk0p1": false, "nvme0n1p1": false, "sda1": false, "mtdblock0": false,
	} {
		if got := wholeDiskName.MatchString(name); got != want {
			t.Errorf("disk %q = %v, want %v", name, got, want)
		}
	}
}

func TestLunznDisablesLegacyUpdateAPIs(t *testing.T) {
	for _, path := range []string{"/api/update_check", "/api/update_download", "/api/update_cancel", "/api/update_upload"} {
		for _, method := range []string{"GET", "POST"} {
			called := false
			handler := logRequests(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called = true
			}))
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(method, path, nil))
			if called || response.Code != http.StatusNotImplemented {
				t.Errorf("%s %s reached legacy update handler or was not rejected", method, path)
			}
		}
	}
}

func TestLunznTemplate(t *testing.T) {
	tmpl, err := template.New("index").Parse(indexHTML)
	if err != nil {
		t.Fatal(err)
	}
	var output strings.Builder
	if err := tmpl.Execute(&output, map[string]string{"BuildTime": "test"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "<title>讯飞rk3588 AIUI固件更新助手 - RKdev</title>") {
		t.Fatal("personalized title missing")
	}
	if !strings.Contains(output.String(), `id="nav-brand">RKdev`) ||
		!strings.Contains(output.String(), "<h1>讯飞rk3588 AIUI固件更新助手 ") ||
		!strings.Contains(output.String(), `onclick="switchTab(this)">🔀 引导顺序`) {
		t.Fatal("brand, personalized heading or menu icons missing")
	}
	if !strings.Contains(output.String(), `data-panel="boot-panel"`) ||
		!strings.Contains(output.String(), `data-panel="update-panel"`) ||
		strings.Contains(output.String(), `data-panel="recovery-panel"`) {
		t.Fatal("manual update navigation missing")
	}
	for _, legacy := range []string{"recovery-sha", "update-proxy-enable", "/api/update_check", "/api/update_download", "/api/update_upload"} {
		if strings.Contains(output.String(), legacy) {
			t.Fatalf("legacy OTA control remains: %s", legacy)
		}
	}
}
