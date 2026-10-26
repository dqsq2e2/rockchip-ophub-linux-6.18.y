package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type EmmcLife struct {
	Raw   *uint8 `json:"raw"`
	Label string `json:"label"`
}

type EmmcHealth struct {
	Device       string   `json:"device"`
	Name         string   `json:"name"`
	Manufacturer string   `json:"manufacturer"`
	Serial       string   `json:"serial"`
	Firmware     string   `json:"firmware"`
	Date         string   `json:"date"`
	CID          string   `json:"cid"`
	Revision     string   `json:"revision"`
	LifeA        EmmcLife `json:"lifeA"`
	LifeB        EmmcLife `json:"lifeB"`
	PreEOL       *uint8   `json:"preEol"`
	PreEOLLabel  string   `json:"preEolLabel"`
	Status       string   `json:"status"`
	Summary      string   `json:"summary"`
	Source       string   `json:"source"`
	CheckedAt    string   `json:"checkedAt"`
}

func mmcByte(text string) *uint8 {
	text = strings.TrimSpace(text)
	// The kernel exports these EXT_CSD fields as hexadecimal bytes.
	n, err := strconv.ParseUint(strings.TrimPrefix(strings.TrimPrefix(text, "0x"), "0X"), 16, 8)
	if err != nil {
		return nil
	}
	v := uint8(n)
	return &v
}

func emmcLife(raw *uint8) EmmcLife {
	value := EmmcLife{Raw: raw, Label: "未知（设备未上报）"}
	if raw == nil || *raw == 0 {
		return value
	}
	switch {
	case *raw <= 10:
		value.Label = fmt.Sprintf("已用 %d%%–%d%%", int(*raw-1)*10, int(*raw)*10)
	case *raw == 11:
		value.Label = "已超过设计寿命（>100%）"
	default:
		value.Label = "未知（保留值）"
	}
	return value
}

func emmcStatus(a, b, eol *uint8) (string, string, string) {
	status, summary := "unknown", "健康信息不完整"
	eolLabel := "未知（设备未上报）"
	knownLife := func(v *uint8) bool { return v != nil && *v >= 1 && *v <= 11 }
	if knownLife(a) && knownLife(b) && eol != nil && *eol >= 1 && *eol <= 3 {
		status, summary = "normal", "正常"
	}
	if eol != nil {
		switch *eol {
		case 1:
			eolLabel = "正常"
		case 2:
			eolLabel = "预警（备用块消耗达到 80%）"
			status, summary = "warning", "建议备份并关注寿命"
		case 3:
			eolLabel = "紧急（备用块消耗达到 90%）"
			status, summary = "critical", "建议尽快备份并更换"
		case 0:
		default:
			eolLabel = "未知（保留值）"
		}
	}
	for _, life := range []*uint8{a, b} {
		if life == nil {
			continue
		}
		if *life == 10 && status != "critical" {
			status, summary = "warning", "寿命接近设计上限，建议备份"
		}
		if *life == 11 {
			status, summary = "critical", "已超过设计寿命，建议备份并更换"
		}
	}
	return status, summary, eolLabel
}

func readEmmcHealth(sysRoot, name string) *EmmcHealth {
	if !wholeDiskName.MatchString(name) || !strings.HasPrefix(name, "mmcblk") {
		return nil
	}
	base := filepath.Join(sysRoot, name, "device")
	read := func(key string) string {
		data, _ := os.ReadFile(filepath.Join(base, key))
		return strings.TrimSpace(string(data))
	}
	if read("type") != "MMC" {
		return nil
	}
	life := strings.Fields(read("life_time"))
	var a, b *uint8
	if len(life) > 0 {
		a = mmcByte(life[0])
	}
	if len(life) > 1 {
		b = mmcByte(life[1])
	}
	eol := mmcByte(read("pre_eol_info"))
	status, summary, eolLabel := emmcStatus(a, b, eol)
	return &EmmcHealth{
		Device: "/dev/" + name, Name: read("name"), Manufacturer: read("manfid"),
		Serial: read("serial"), Firmware: read("fwrev"), Date: read("date"),
		CID: read("cid"), Revision: read("rev"),
		LifeA: emmcLife(a), LifeB: emmcLife(b), PreEOL: eol, PreEOLLabel: eolLabel,
		Status: status, Summary: summary,
		Source: "内核 sysfs / eMMC EXT_CSD", CheckedAt: time.Now().UTC().Format(time.RFC3339),
	}
}

func emmcHealthDevices(sysRoot string) ([]EmmcHealth, error) {
	entries, err := os.ReadDir(sysRoot)
	if err != nil {
		return nil, err
	}
	devices := make([]EmmcHealth, 0)
	for _, entry := range entries {
		if info := readEmmcHealth(sysRoot, entry.Name()); info != nil {
			devices = append(devices, *info)
		}
	}
	return devices, nil
}

func handleEmmc(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonErr(w, http.StatusMethodNotAllowed, "仅支持 GET")
		return
	}
	devices, err := emmcHealthDevices("/sys/class/block")
	if err != nil {
		jsonErr(w, http.StatusInternalServerError, "读取 eMMC 信息失败")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(map[string]interface{}{"devices": devices})
}
