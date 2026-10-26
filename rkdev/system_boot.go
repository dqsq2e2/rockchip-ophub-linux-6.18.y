package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var bootClockFloor = time.Date(2026, time.October, 24, 0, 0, 0, 0, time.UTC)

func validateBootClock(now time.Time) error {
	if now.Before(bootClockFloor) {
		return fmt.Errorf("恢复系统时间无效，请校准时间后重新刷写，避免损坏启动文件系统")
	}
	return nil
}

var rootDevicePattern = regexp.MustCompile(`\b(root|rootdev)=(/dev/(?:mmcblk[0-9]+p|nvme[0-9]+n[0-9]+p|sd[a-z]+)([0-9]+))\b`)
var rootPartAssign = regexp.MustCompile(`\bdistro_rootpart=(["']?)([0-9]+)(["']?)`)
var rootPartSetenv = regexp.MustCompile(`\bsetenv[ \t]+distro_rootpart[ \t]+(["']?)([0-9]+)(["']?)`)

type bootPartitionRef struct {
	Number int
	UUID   string
}

func adaptBootText(data []byte, mapping map[int]bootPartitionRef) []byte {
	text := rootDevicePattern.ReplaceAllStringFunc(string(data), func(value string) string {
		match := rootDevicePattern.FindStringSubmatch(value)
		number, _ := strconv.Atoi(match[3])
		if ref, ok := mapping[number]; ok {
			return match[1] + "=PARTUUID=" + ref.UUID
		}
		return value
	})
	for _, pattern := range []*regexp.Regexp{rootPartAssign, rootPartSetenv} {
		text = pattern.ReplaceAllStringFunc(text, func(value string) string {
			match := pattern.FindStringSubmatch(value)
			number, _ := strconv.Atoi(match[2])
			ref, ok := mapping[number]
			if !ok {
				return value
			}
			location := pattern.FindStringSubmatchIndex(value)
			return value[:location[4]] + strconv.Itoa(ref.Number) + value[location[5]:]
		})
	}
	return []byte(text)
}

// Legacy 2017 script images used an all-ones size terminator. Mainline expects
// zero. Preserve the script header metadata and recompute both CRCs.
func adaptBootScript(data []byte, mapping map[int]bootPartitionRef) ([]byte, error) {
	if len(data) < 72 || binary.BigEndian.Uint32(data[:4]) != 0x27051956 ||
		data[30] != 6 || data[31] != 0 {
		return nil, fmt.Errorf("不支持的 boot.scr 格式")
	}
	header := append([]byte(nil), data[:64]...)
	size := binary.BigEndian.Uint32(header[12:16])
	if uint64(size)+64 > uint64(len(data)) || size < 8 {
		return nil, fmt.Errorf("boot.scr 长度无效")
	}
	headerCRC := binary.BigEndian.Uint32(header[4:8])
	binary.BigEndian.PutUint32(header[4:8], 0)
	if crc32.ChecksumIEEE(header) != headerCRC {
		return nil, fmt.Errorf("boot.scr 表头校验失败")
	}
	payload := data[64 : 64+int(size)]
	if crc32.ChecksumIEEE(payload) != binary.BigEndian.Uint32(header[24:28]) {
		return nil, fmt.Errorf("boot.scr 数据校验失败")
	}
	length, terminator := binary.BigEndian.Uint32(payload[:4]), binary.BigEndian.Uint32(payload[4:8])
	if length != size-8 || (terminator != 0 && terminator != 0xffffffff) {
		return nil, fmt.Errorf("boot.scr 不是单段启动脚本")
	}
	script := adaptBootText(payload[8:], mapping)
	next := make([]byte, 8+len(script))
	binary.BigEndian.PutUint32(next[:4], uint32(len(script)))
	copy(next[8:], script)
	binary.BigEndian.PutUint32(header[12:16], uint32(len(next)))
	binary.BigEndian.PutUint32(header[24:28], crc32.ChecksumIEEE(next))
	binary.BigEndian.PutUint32(header[4:8], crc32.ChecksumIEEE(header))
	return append(header, next...), nil
}

func replaceBootFile(path string, data []byte) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("启动配置不是普通文件: %s", path)
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".lunzn-boot-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err = file.Write(data); err == nil {
		err = file.Chmod(info.Mode().Perm())
	}
	if err == nil {
		err = file.Sync()
	}
	file.Close()
	if err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}

func adaptSystemBoot(task *FlashTask) error {
	if err := validateBootClock(time.Now()); err != nil {
		return err
	}
	source, err := readGPT(task.Items[0].Path)
	if err != nil {
		// A standalone filesystem image has no source disk numbering to remap.
		return nil
	}
	return adaptSystemBootPartitions(task, source)
}

func adaptSystemBootPartitions(task *FlashTask, source []gptPartition) error {
	if err := validateBootClock(time.Now()); err != nil {
		return err
	}
	mapping := make(map[int]bootPartitionRef)
	for i, part := range source {
		if i >= len(task.SystemPartitions) {
			return fmt.Errorf("系统分区映射不完整")
		}
		next := task.SystemPartitions[i]
		mapping[part.Number] = bootPartitionRef{next.Number, next.UUID}
	}
	for _, part := range task.SystemPartitions {
		device := fmt.Sprintf("%sp%d", task.TargetDev, part.Number)
		out, err := exec.Command("blkid", "-p", "-s", "TYPE", "-o", "value", device).Output()
		if err != nil {
			continue
		}
		fsType := strings.TrimSpace(string(out))
		switch fsType {
		case "ext4", "btrfs", "vfat":
		default:
			continue
		}
		mountpoint, err := os.MkdirTemp("/tmp", "lunzn-system-boot-*")
		if err != nil {
			return err
		}
		if out, err := exec.Command("mount", device, mountpoint).CombinedOutput(); err != nil {
			os.Remove(mountpoint)
			return fmt.Errorf("挂载系统分区以适配引导失败: %v: %s", err, out)
		}
		adaptErr := adaptMountedBoot(mountpoint, mapping)
		unmountOut, unmountErr := exec.Command("umount", mountpoint).CombinedOutput()
		if unmountErr != nil {
			return fmt.Errorf("卸载系统分区失败: %v: %s", unmountErr, unmountOut)
		}
		os.Remove(mountpoint)
		if adaptErr != nil {
			return adaptErr
		}
		if fsType == "ext4" {
			if out, err := exec.Command("e2fsck", "-fn", device).CombinedOutput(); err != nil {
				return fmt.Errorf("刷后启动文件系统检查失败，不能确认系统可启动: %v: %s", err, out)
			}
		}
	}
	return nil
}

func adaptMountedBoot(root string, mapping map[int]bootPartitionRef) error {
	for _, prefix := range []string{"", "boot"} {
		for _, name := range []string{"boot.cmd", "boot.scr", "fnEnv.txt", "armbianEnv.txt", "extlinux/extlinux.conf", "extlinux.conf"} {
			path := filepath.Join(root, prefix, name)
			resolved, resolveErr := filepath.EvalSymlinks(path)
			if os.IsNotExist(resolveErr) {
				continue
			}
			if resolveErr != nil {
				return resolveErr
			}
			relative, relErr := filepath.Rel(root, resolved)
			if relErr != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
				return fmt.Errorf("启动配置链接越过系统分区: %s", name)
			}
			info, err := os.Lstat(path)
			if os.IsNotExist(err) {
				continue
			}
			if err != nil {
				return err
			}
			if !info.Mode().IsRegular() {
				continue
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			next := adaptBootText(data, mapping)
			if name == "boot.scr" {
				next, err = adaptBootScript(data, mapping)
				if err != nil {
					return fmt.Errorf("%s: %w", name, err)
				}
			}
			if !bytes.Equal(data, next) {
				if err := replaceBootFile(path, next); err != nil {
					return err
				}
				advLog("info", "已适配系统启动配置 %s", filepath.Join(prefix, name))
			}
		}
	}
	return nil
}
