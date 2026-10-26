package main

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"
)

const systemStartSector = uint64(524288)

type gptPartition struct {
	Number           int
	Start, End       uint64
	Type, UUID, Name string
	Attributes       uint64
}

func gptGUID(data []byte) string {
	return fmt.Sprintf("%08x-%04x-%04x-%02x%02x-%x",
		binary.LittleEndian.Uint32(data[:4]), binary.LittleEndian.Uint16(data[4:6]),
		binary.LittleEndian.Uint16(data[6:8]), data[8], data[9], data[10:16])
}

func readGPT(path string) ([]gptPartition, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return readGPTReader(f)
}

func readGPTReader(f io.ReaderAt) ([]gptPartition, error) {
	header := make([]byte, 512)
	if _, err := f.ReadAt(header, 512); err != nil {
		return nil, err
	}
	if string(header[:8]) != "EFI PART" {
		return nil, fmt.Errorf("镜像没有 GPT 分区表")
	}
	size := binary.LittleEndian.Uint32(header[12:16])
	if size < 92 || size > 512 {
		return nil, fmt.Errorf("GPT 表头长度无效")
	}
	check := binary.LittleEndian.Uint32(header[16:20])
	binary.LittleEndian.PutUint32(header[16:20], 0)
	if crc32.ChecksumIEEE(header[:size]) != check {
		return nil, fmt.Errorf("GPT 表头校验失败")
	}
	count, entrySize := binary.LittleEndian.Uint32(header[80:84]), binary.LittleEndian.Uint32(header[84:88])
	lba := binary.LittleEndian.Uint64(header[72:80])
	if count == 0 || count > 128 || entrySize != 128 || lba != 2 {
		return nil, fmt.Errorf("不支持的 GPT 分区表结构")
	}
	entries := make([]byte, int(count*entrySize))
	if _, err := f.ReadAt(entries, int64(lba*512)); err != nil {
		return nil, err
	}
	if crc32.ChecksumIEEE(entries) != binary.LittleEndian.Uint32(header[88:92]) {
		return nil, fmt.Errorf("GPT 分区表校验失败")
	}
	var result []gptPartition
	for i := 0; i < int(count); i++ {
		entry := entries[i*128 : (i+1)*128]
		if bytes.Equal(entry[:16], make([]byte, 16)) {
			continue
		}
		start, end := binary.LittleEndian.Uint64(entry[32:40]), binary.LittleEndian.Uint64(entry[40:48])
		if start < binary.LittleEndian.Uint64(header[40:48]) || end < start ||
			end > binary.LittleEndian.Uint64(header[48:56]) {
			return nil, fmt.Errorf("GPT 分区 %d 范围无效", i+1)
		}
		var name []uint16
		for j := 56; j < 128; j += 2 {
			value := binary.LittleEndian.Uint16(entry[j : j+2])
			if value == 0 {
				break
			}
			name = append(name, value)
		}
		result = append(result, gptPartition{i + 1, start, end, gptGUID(entry[:16]),
			gptGUID(entry[16:32]), string(utf16.Decode(name)), binary.LittleEndian.Uint64(entry[48:56])})
	}
	sorted := append([]gptPartition(nil), result...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Start < sorted[j].Start })
	for i := 1; i < len(sorted); i++ {
		if sorted[i].Start <= sorted[i-1].End {
			return nil, fmt.Errorf("GPT 分区重叠")
		}
	}
	return result, nil
}

func recoveryPartition(parts []gptPartition) (gptPartition, error) {
	for _, part := range parts {
		if part.Number == 1 && part.Start == 65536 && part.End == 524253 && part.Name == "LUNZN_RECOVERY" {
			for _, other := range parts {
				if other.Number != 1 && other.Start < systemStartSector {
					return gptPartition{}, fmt.Errorf("eMMC 系统区域布局不匹配")
				}
			}
			return part, nil
		}
	}
	return gptPartition{}, fmt.Errorf("未找到配套恢复系统，拒绝写入 eMMC")
}

// Store the complete disk image at the start of B, including its inert loader
// and GPT. Project its partitions into the outer GPT for normal U-Boot/Linux
// access, preserving PARTUUIDs and all offsets relative to the image.
func planSystemArea(path string, diskSectors uint64, current []gptPartition) ([]gptPartition, []FlashWriteItem, error) {
	recovery, err := recoveryPartition(current)
	if err != nil {
		return nil, nil, err
	}
	if diskSectors <= systemStartSector+gptBackupSectors {
		return nil, nil, fmt.Errorf("eMMC 容量不足")
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, nil, err
	}
	src, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	head := make([]byte, 520)
	n, _ := src.Read(head)
	src.Close()
	if n == 520 && string(head[512:520]) == "EFI PART" {
		if uint64(info.Size()) > (diskSectors-gptBackupSectors-systemStartSector)*512 {
			return nil, nil, fmt.Errorf("系统镜像超过系统空间容量")
		}
		sourceParts, err := readGPT(path)
		if err != nil {
			return nil, nil, err
		}
		if len(sourceParts) == 0 || len(sourceParts) > 127 {
			return nil, nil, fmt.Errorf("系统镜像分区数量无效")
		}
		parts, err := projectSystemPartitions(sourceParts, info.Size(), diskSectors, recovery)
		if err != nil {
			return nil, nil, err
		}
		return parts, []FlashWriteItem{{Name: "system image", Offset: int64(systemStartSector * 512),
			Size: info.Size(), Path: path}}, nil
	}
	if err := validateSystemImage(path); err != nil {
		return nil, nil, err
	}
	if uint64(info.Size()) > (diskSectors-gptBackupSectors-systemStartSector)*512 {
		return nil, nil, fmt.Errorf("镜像超过系统空间容量")
	}
	id := make([]byte, 16)
	if _, err := rand.Read(id); err != nil {
		return nil, nil, err
	}
	id[7] = (id[7] & 0x0f) | 0x40
	id[8] = (id[8] & 0x3f) | 0x80
	part := gptPartition{Number: 2, Start: systemStartSector, End: diskSectors - gptBackupSectors - 1,
		Type: "0fc63daf-8483-4772-8e79-3d69d8477de4", UUID: gptGUID(id), Name: "LUNZN_SYSTEM"}
	return []gptPartition{part}, []FlashWriteItem{{Name: "system", Offset: int64(systemStartSector * 512),
		Size: info.Size(), Path: path}}, nil
}

func projectSystemPartitions(source []gptPartition, size int64, sectors uint64, recovery gptPartition) ([]gptPartition, error) {
	if size <= 0 || sectors <= systemStartSector+gptBackupSectors ||
		uint64(size) > (sectors-systemStartSector-gptBackupSectors)*512 ||
		len(source) == 0 || len(source) > 127 {
		return nil, fmt.Errorf("系统镜像大小或分区数量无效")
	}
	var parts []gptPartition
	seen := map[string]bool{recovery.UUID: true}
	for i, part := range source {
		if seen[part.UUID] {
			return nil, fmt.Errorf("镜像分区 UUID 重复")
		}
		seen[part.UUID] = true
		if part.End >= uint64(size/512) {
			return nil, fmt.Errorf("镜像分区 %s 数据不完整", part.Name)
		}
		part.Number = i + 2
		part.Start += systemStartSector
		part.End += systemStartSector
		if part.End >= sectors-gptBackupSectors {
			return nil, fmt.Errorf("系统镜像超过系统空间容量")
		}
		parts = append(parts, part)
	}
	return parts, nil
}

func buildSystemAreaTask(files []string, target string) (*FlashTask, string, error) {
	if len(files) != 1 {
		return nil, "", fmt.Errorf("请选择一个包含 GPT 分区表的系统镜像，或单一文件系统镜像")
	}
	if err := checkSystemAreaIdle(target); err != nil {
		return nil, "", err
	}
	sectors, err := blockDeviceSectors(target)
	if err != nil {
		return nil, "", err
	}
	current, err := readGPT(target)
	if err != nil {
		return nil, "", err
	}
	parts, items, err := planSystemArea(files[0], sectors, current)
	if err != nil {
		return nil, "", err
	}
	var total int64
	for _, item := range items {
		total += item.Size
	}
	task := newTask(target, total)
	task.Mode, task.Items, task.SystemPartitions = "system", items, parts
	return task, "将替换 eMMC 系统空间中的系统分区；保留引导和恢复系统", nil
}

func checkSystemAreaIdle(target string) error {
	if target != "/dev/mmcblk0" {
		return fmt.Errorf("系统空间目标无效")
	}
	current, err := readGPT(target)
	if err != nil {
		return err
	}
	if _, err := recoveryPartition(current); err != nil {
		return err
	}
	mounts, err := os.ReadFile("/proc/mounts")
	if err != nil {
		return err
	}
	for _, line := range strings.Split(string(mounts), "\n") {
		fields := strings.Fields(line)
		if len(fields) > 0 && (fields[0] == target || strings.HasPrefix(fields[0], target+"p")) {
			return fmt.Errorf("请先卸载 eMMC 上的所有分区")
		}
	}
	swaps, err := os.ReadFile("/proc/swaps")
	if err != nil {
		return err
	}
	if strings.Contains(string(swaps), target) {
		return fmt.Errorf("eMMC 正在用作交换分区")
	}
	for _, part := range current {
		holders, _ := os.ReadDir(fmt.Sprintf("/sys/class/block/mmcblk0p%d/holders", part.Number))
		if len(holders) != 0 {
			return fmt.Errorf("eMMC 分区正在被其他块设备使用")
		}
	}
	return nil
}

func verifySystemArea(task *FlashTask) error {
	dest, err := os.Open(task.TargetDev)
	if err != nil {
		return err
	}
	defer dest.Close()
	for _, item := range task.Items {
		src, err := os.Open(item.Path)
		if err != nil {
			return err
		}
		a, b := sha256.New(), sha256.New()
		_, err = io.Copy(a, io.NewSectionReader(src, item.SourceOffset, item.Size))
		src.Close()
		if err != nil {
			return err
		}
		if n, err := io.Copy(b, io.NewSectionReader(dest, item.Offset, item.Size)); err != nil || n != item.Size {
			return fmt.Errorf("系统分区 %s 读回失败", item.Name)
		}
		if !bytes.Equal(a.Sum(nil), b.Sum(nil)) {
			return fmt.Errorf("系统分区 %s 读回校验不一致", item.Name)
		}
	}
	return nil
}

func systemPartitionArgs(current, next []gptPartition) ([]string, error) {
	if _, err := recoveryPartition(current); err != nil {
		return nil, err
	}
	args := []string{"-e", "-a", "1"}
	for _, part := range current {
		if part.Number != 1 {
			args = append(args, "-d", strconv.Itoa(part.Number))
		}
	}
	for _, part := range next {
		if part.Number < 2 || part.Start < systemStartSector {
			return nil, fmt.Errorf("系统分区越过系统空间边界")
		}
		n := strconv.Itoa(part.Number)
		args = append(args, "-n", fmt.Sprintf("%s:%d:%d", n, part.Start, part.End),
			"-t", n+":"+part.Type, "-u", n+":"+part.UUID, "-c", n+":"+part.Name)
		for bit := uint(0); bit < 64; bit++ {
			if part.Attributes&(uint64(1)<<bit) != 0 {
				args = append(args, "-A", fmt.Sprintf("%s:set:%d", n, bit))
			}
		}
	}
	return args, nil
}

func applySystemAreaPartitions(task *FlashTask) error {
	current, err := readGPT(task.TargetDev)
	if err != nil {
		return err
	}
	before, err := recoveryPartition(current)
	if err != nil {
		return err
	}
	args, err := systemPartitionArgs(current, task.SystemPartitions)
	if err != nil {
		return err
	}
	args = append(args, task.TargetDev)
	if out, err := exec.Command("sgdisk", args...).CombinedOutput(); err != nil {
		return fmt.Errorf("更新系统空间分区表失败: %v: %s", err, out)
	}
	after, err := readGPT(task.TargetDev)
	if err != nil {
		return err
	}
	got, err := recoveryPartition(after)
	if err != nil || got != before || len(after) != len(task.SystemPartitions)+1 {
		return fmt.Errorf("更新分区表后恢复系统验证失败")
	}
	for i, part := range task.SystemPartitions {
		if after[i+1] != part {
			return fmt.Errorf("系统分区表读回不一致")
		}
	}
	if out, err := exec.Command("partprobe", task.TargetDev).CombinedOutput(); err != nil {
		return fmt.Errorf("分区表已写入，请重启后使用: %v: %s", err, out)
	}
	return nil
}
