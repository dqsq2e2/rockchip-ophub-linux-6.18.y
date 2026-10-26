package main

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

var releaseWriteMu sync.Mutex

func partitionSize(path string) (int64, error) {
	data, err := os.ReadFile("/sys/class/block/" + filepath.Base(path) + "/size")
	if err != nil {
		return 0, err
	}
	sectors, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
	return sectors * 512, err
}

func validateSystemImage(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	head := make([]byte, 65536+8)
	n, _ := io.ReadFull(f, head)
	if n >= 520 && string(head[512:520]) == "EFI PART" {
		return fmt.Errorf("不能把整盘 GPT 镜像直接写入单个分区，请从固件刷写页面写入系统空间")
	}
	if n >= 1082 && head[1080] == 0x53 && head[1081] == 0xef {
		return nil
	}
	if n == len(head) && string(head[65536:65544]) == "_BHRfS_M" {
		return nil
	}
	return fmt.Errorf("需要包含启动配置的 ext4 或 Btrfs 系统分区镜像")
}

const (
	releaseImageBytes = 512 * 1024 * 1024
	bootAreaOffset    = int64(0x8000)
	bootAreaEnd       = int64(32 * 1024 * 1024)
	recoveryOffset    = int64(32 * 1024 * 1024)
	recoveryBytes     = int64(234863616)
)

// Preserve both saved environments, including boot order and MAC addresses.
var pairedUpgradeRanges = [][2]int64{
	{bootAreaOffset, 0x1000000 - bootAreaOffset},
	{0x1040000, bootAreaEnd - 0x1040000},
	{recoveryOffset, recoveryBytes},
}

func hashPairedRanges(src *os.File, dest io.Writer) error {
	for _, region := range pairedUpgradeRanges {
		if _, err := io.CopyN(dest, io.NewSectionReader(src, region[0], region[1]), region[1]); err != nil {
			return err
		}
	}
	return nil
}

func checkReleaseChecksum(actual, supplied string) error {
	// Older clients may still send a checksum; manual uploads no longer require one.
	if supplied = strings.TrimSpace(supplied); supplied != "" && !strings.EqualFold(actual, supplied) {
		return fmt.Errorf("SHA256 不匹配")
	}
	return nil
}

// This endpoint updates the paired bootloader and partition A together. It
// never writes the GPT or partition B.
func handleRecoveryUpgrade(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonErr(w, http.StatusMethodNotAllowed, "仅支持 POST")
		return
	}
	if !releaseWriteMu.TryLock() {
		jsonErr(w, http.StatusConflict, "已有写入任务")
		return
	}
	defer releaseWriteMu.Unlock()
	r.Body = http.MaxBytesReader(w, r.Body, releaseImageBytes+1024*1024)
	if err := r.ParseMultipartForm(1024 * 1024); err != nil {
		jsonErr(w, http.StatusBadRequest, err.Error())
		return
	}
	defer r.MultipartForm.RemoveAll()
	src, _, err := r.FormFile("package")
	if err != nil {
		jsonErr(w, http.StatusBadRequest, "请选择配套 boot-recovery .img")
		return
	}
	defer src.Close()
	f, err := os.CreateTemp("/tmp", "lunzn-boot-recovery-*.img")
	if err != nil {
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer os.Remove(f.Name())
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(src, releaseImageBytes+1))
	f.Close()
	if err != nil || n != releaseImageBytes {
		jsonErr(w, http.StatusBadRequest, "配套镜像容量不正确")
		return
	}
	expected := fmt.Sprintf("%x", h.Sum(nil))
	if err := checkReleaseChecksum(expected, r.FormValue("sha256")); err != nil {
		jsonErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := validateReleaseImage(f.Name()); err != nil {
		jsonErr(w, http.StatusBadRequest, err.Error())
		return
	}
	start, startErr := os.ReadFile("/sys/class/block/mmcblk0p1/start")
	size, sizeErr := partitionSize("/dev/mmcblk0p1")
	if startErr != nil || sizeErr != nil || strings.TrimSpace(string(start)) != "65536" || size != recoveryBytes {
		jsonErr(w, http.StatusBadRequest, "本机分区布局不匹配；请先安装配套启动恢复包")
		return
	}
	mounts, _ := os.ReadFile("/proc/mounts")
	if strings.Contains(string(mounts), "/dev/mmcblk0p1 ") {
		jsonErr(w, http.StatusConflict, "请先卸载恢复分区")
		return
	}
	input, err := os.Open(f.Name())
	if err != nil {
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer input.Close()
	dst, err := os.OpenFile("/dev/mmcblk0", os.O_WRONLY|os.O_SYNC, 0)
	if err == nil {
		for _, region := range pairedUpgradeRanges {
			if _, err = input.Seek(region[0], io.SeekStart); err != nil {
				break
			}
			if _, err = copyRange(dst, input, region[0], region[1]); err != nil {
				break
			}
		}
		if err == nil {
			err = dst.Sync()
		}
		dst.Close()
	}
	if err != nil {
		jsonErr(w, http.StatusInternalServerError, "写入失败: "+err.Error())
		return
	}
	check, err := os.Open("/dev/mmcblk0")
	if err != nil {
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	h.Reset()
	err = hashPairedRanges(check, h)
	check.Close()
	if err != nil {
		jsonErr(w, http.StatusInternalServerError, "读回校验失败，请勿重启")
		return
	}
	input, err = os.Open(f.Name())
	if err != nil {
		jsonErr(w, http.StatusInternalServerError, "无法重新读取升级包")
		return
	}
	defer input.Close()
	expectedRanges := sha256.New()
	err = hashPairedRanges(input, expectedRanges)
	if err != nil || fmt.Sprintf("%x", h.Sum(nil)) != fmt.Sprintf("%x", expectedRanges.Sum(nil)) {
		jsonErr(w, http.StatusInternalServerError, "读回校验失败，请勿重启")
		return
	}
	jsonOK(w, map[string]string{"message": "引导与恢复系统升级完成；系统空间、分区表和引导顺序已保留，重启后生效", "sha256": expected})
}

func copyRange(dst, src *os.File, offset, size int64) (int64, error) {
	if _, err := dst.Seek(offset, io.SeekStart); err != nil {
		return 0, err
	}
	return io.CopyN(dst, src, size)
}

func validateReleaseImage(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	header := make([]byte, 512)
	if _, err := f.ReadAt(header, 512); err != nil || string(header[:8]) != "EFI PART" {
		return fmt.Errorf("配套镜像缺少 GPT")
	}
	entryLBA := binary.LittleEndian.Uint64(header[72:80])
	entryCount := binary.LittleEndian.Uint32(header[80:84])
	entrySize := binary.LittleEndian.Uint32(header[84:88])
	if entryCount != 128 || entrySize != 128 || entryLBA != 2 {
		return fmt.Errorf("配套镜像 GPT 分区表无效")
	}
	entries := make([]byte, int(entryCount*entrySize))
	if _, err := f.ReadAt(entries, int64(entryLBA*512)); err != nil {
		return fmt.Errorf("读取 GPT 分区表失败")
	}
	foundRecovery, foundSystem := false, false
	for i := uint32(0); i < entryCount; i++ {
		e := entries[i*entrySize : (i+1)*entrySize]
		if strings.Trim(string(e[:16]), "\x00") == "" {
			continue
		}
		first := binary.LittleEndian.Uint64(e[32:40])
		last := binary.LittleEndian.Uint64(e[40:48])
		name := strings.TrimRight(string(utf16Bytes(e[56:128])), "\x00")
		switch name {
		case "LUNZN_RECOVERY":
			foundRecovery = first == 65536 && last == 524253
		case "LUNZN_SYSTEM":
			foundSystem = first == 524288 && last > first
		}
	}
	if !foundRecovery || !foundSystem {
		return fmt.Errorf("配套镜像必须包含恢复系统和系统空间")
	}
	magic := make([]byte, 4)
	if _, err := f.ReadAt(magic, bootAreaOffset); err != nil || string(magic) != "RKNS" {
		return fmt.Errorf("配套镜像缺少 RK3588 引导头")
	}
	if _, err := f.ReadAt(magic, 0x800000); err != nil || binary.BigEndian.Uint32(magic) != 0xd00dfeed {
		return fmt.Errorf("配套镜像缺少 U-Boot FIT")
	}
	part, err := os.CreateTemp("/tmp", "lunzn-verify-*.ext4")
	if err != nil {
		return err
	}
	defer os.Remove(part.Name())
	if _, err := f.Seek(recoveryOffset, io.SeekStart); err != nil {
		part.Close()
		return err
	}
	_, err = io.CopyN(part, f, recoveryBytes)
	part.Close()
	if err != nil {
		return err
	}
	label, err := exec.Command("blkid", "-p", "-s", "LABEL", "-o", "value", part.Name()).Output()
	if err != nil || strings.TrimSpace(string(label)) != "LUNZN_RECOVERY" {
		return fmt.Errorf("恢复分区标签不正确")
	}
	if err := exec.Command("e2fsck", "-fn", part.Name()).Run(); err != nil {
		return fmt.Errorf("恢复分区文件系统校验失败")
	}
	for _, name := range []string{"/Image", "/initramfs.cpio.gz", "/extlinux/extlinux.conf", "/dtb/rockchip/rk3588-AIUI.dtb", "/release.json"} {
		out, err := exec.Command("debugfs", "-R", "stat "+name, part.Name()).CombinedOutput()
		if err != nil || !strings.Contains(string(out), "Inode:") {
			return fmt.Errorf("恢复分区缺少 %s", name)
		}
	}
	return nil
}

func utf16Bytes(src []byte) []byte {
	out := make([]byte, 0, len(src)/2)
	for i := 0; i+1 < len(src); i += 2 {
		v := binary.LittleEndian.Uint16(src[i : i+2])
		if v > 255 {
			out = append(out, '?')
		} else {
			out = append(out, byte(v))
		}
	}
	return out
}
