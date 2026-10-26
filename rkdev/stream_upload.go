package main

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"bytes"
	"compress/bzip2"
	"compress/gzip"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"hash"
	"io"
	"math"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/bodgit/sevenzip"
	"github.com/nwaples/rardecode/v2"
)

const directWriteThreshold int64 = 4 << 30
const streamProbeBytes = 65536 + 8

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(p)
}

type streamTarget struct {
	file    *os.File
	path    string
	sectors uint64
	offset  int64
	limit   int64
	current []gptPartition
	system  bool
	check   func() error
}

// Refuse mounted disks, swap and device-mapper/RAID holders before receiving
// firmware. Check kernel device identities, not just mount-source spellings.
func checkFlashDiskIdle(target string) error {
	if err := validateFlashTarget(target); err != nil {
		return err
	}
	info, err := os.Lstat(target)
	if err != nil || info.Mode()&os.ModeDevice == 0 || info.Mode()&os.ModeCharDevice != 0 {
		return fmt.Errorf("目标不是块设备")
	}
	base := filepath.Base(target)
	nodes, err := os.ReadDir("/sys/class/block")
	if err != nil {
		return err
	}
	identities := make(map[string]bool)
	for _, node := range nodes {
		name := node.Name()
		real, err := filepath.EvalSymlinks(filepath.Join("/sys/class/block", name))
		if err != nil {
			return err
		}
		if name != base && filepath.Base(filepath.Dir(real)) != base {
			continue
		}
		dev, err := os.ReadFile(filepath.Join(real, "dev"))
		if err != nil {
			return err
		}
		identities[strings.TrimSpace(string(dev))] = true
		holders, err := os.ReadDir(filepath.Join(real, "holders"))
		if err != nil || len(holders) != 0 {
			return fmt.Errorf("目标设备或其分区正在被其他块设备使用")
		}
	}
	if len(identities) == 0 {
		return fmt.Errorf("未找到目标内核块设备")
	}
	mounts, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return err
	}
	for _, line := range strings.Split(string(mounts), "\n") {
		fields := strings.Fields(line)
		if len(fields) > 2 && identities[fields[2]] {
			return fmt.Errorf("请先卸载目标设备上的所有分区")
		}
	}
	swaps, err := os.ReadFile("/proc/swaps")
	if err != nil {
		return err
	}
	for _, line := range strings.Split(string(swaps), "\n")[1:] {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		info, err := os.Stat(fields[0])
		if err != nil {
			return fmt.Errorf("无法确认交换分区是否占用目标设备")
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok {
			return fmt.Errorf("无法检查交换分区")
		}
		id := uint64(stat.Rdev)
		if info.Mode()&os.ModeDevice == 0 {
			id = uint64(stat.Dev)
		}
		major := (id>>8)&0xfff | (id>>32)&0xfffff000
		minor := id&0xff | (id>>12)&0xffffff00
		if identities[fmt.Sprintf("%d:%d", major, minor)] {
			return fmt.Errorf("目标设备正在用作交换空间")
		}
	}
	return nil
}

func openStreamTarget(path string) (*streamTarget, error) {
	if err := checkFlashDiskIdle(path); err != nil {
		return nil, err
	}
	sectors, err := blockDeviceSectors(path)
	if err != nil || sectors == 0 || sectors > math.MaxInt64/512 {
		return nil, fmt.Errorf("目标设备容量无效")
	}
	t := &streamTarget{path: path, sectors: sectors, limit: int64(sectors * 512), system: isSystemSlotTarget(path)}
	if t.system {
		if err := validateBootClock(time.Now()); err != nil {
			return nil, err
		}
		if err := checkSystemAreaIdle(path); err != nil {
			return nil, err
		}
		t.current, err = readGPT(path)
		if err != nil {
			return nil, err
		}
		if sectors <= systemStartSector+gptBackupSectors {
			return nil, fmt.Errorf("系统空间容量不足")
		}
		t.offset = int64(systemStartSector * 512)
		t.limit -= t.offset + gptBackupSectors*512
	}
	t.file, err = os.OpenFile(path, os.O_RDWR|os.O_SYNC, 0)
	t.check = func() error {
		if err := checkFlashDiskIdle(path); err != nil {
			return err
		}
		if t.system {
			current, err := readGPT(path)
			if err != nil || !sameGPT(current, t.current) {
				return fmt.Errorf("eMMC 分区表已变化，请刷新后重试")
			}
		}
		return nil
	}
	return t, err
}

type streamImage struct {
	target    *streamTarget
	task      *FlashTask
	hash      hash.Hash
	written   int64
	expected  int64
	minimum   int64
	sourceGPT []gptPartition
	nextGPT   []gptPartition
	start     time.Time
}

func streamGPT(prefix []byte) ([]gptPartition, int64, error) {
	parts, err := readGPTReader(bytes.NewReader(prefix))
	if err != nil {
		return nil, 0, err
	}
	last := binary.LittleEndian.Uint64(prefix[544:552])
	firstUsable := binary.LittleEndian.Uint64(prefix[552:560])
	lastUsable := binary.LittleEndian.Uint64(prefix[560:568])
	if binary.LittleEndian.Uint64(prefix[536:544]) != 1 || last < 34 ||
		last > math.MaxInt64/512-1 || firstUsable < gptReservedSectors ||
		lastUsable >= last-gptBackupSectors+1 || len(parts) == 0 {
		return nil, 0, fmt.Errorf("GPT 镜像容量或可用范围无效")
	}
	return parts, int64((last + 1) * 512), nil
}

func beginStreamImage(target *streamTarget, prefix []byte, expected int64) (*streamImage, error) {
	if target.check != nil {
		if err := target.check(); err != nil {
			return nil, err
		}
	}
	s := &streamImage{target: target, hash: sha256.New(), expected: expected, start: time.Now()}
	hasGPT := len(prefix) >= 520 && string(prefix[512:520]) == "EFI PART"
	if hasGPT {
		var size int64
		var err error
		s.sourceGPT, size, err = streamGPT(prefix)
		if err != nil {
			return nil, err
		}
		if expected >= 0 && expected < size {
			return nil, fmt.Errorf("镜像长度小于 GPT 声明")
		}
		s.minimum = size
	}
	if s.expected > target.limit || s.minimum > target.limit {
		return nil, fmt.Errorf("镜像超过目标空间容量")
	}
	if target.system {
		recovery, err := recoveryPartition(target.current)
		if err != nil {
			return nil, err
		}
		if hasGPT {
			s.nextGPT, err = projectSystemPartitions(s.sourceGPT, max(s.minimum, s.expected), target.sectors, recovery)
			if err != nil {
				return nil, err
			}
		} else {
			ext4 := len(prefix) >= 1082 && prefix[1080] == 0x53 && prefix[1081] == 0xef
			btrfs := len(prefix) >= streamProbeBytes && string(prefix[65536:65544]) == "_BHRfS_M"
			if !ext4 && !btrfs {
				return nil, fmt.Errorf("系统空间仅接受 GPT、ext4 或 Btrfs 镜像")
			}
			id := make([]byte, 16)
			if _, err := rand.Read(id); err != nil {
				return nil, err
			}
			id[7], id[8] = id[7]&0x0f|0x40, id[8]&0x3f|0x80
			s.nextGPT = []gptPartition{{Number: 2, Start: systemStartSector,
				End: target.sectors - gptBackupSectors - 1, Name: "LUNZN_SYSTEM",
				Type: "0fc63daf-8483-4772-8e79-3d69d8477de4", UUID: gptGUID(id)}}
		}
	}
	total := max(0, max(s.expected, s.minimum))
	s.task = newTask(target.path, total)
	s.task.Mode = "direct"
	s.task.SystemPartitions = s.nextGPT
	upMu.Lock()
	upState.Stage, upState.TaskID, upState.ExpandedTotal = "direct", s.task.ID, total
	upState.ExpandedEstimate = s.expected < 0
	upMu.Unlock()
	advLog("warn", "流式直写开始: %s，偏移 %d；传输失败后需重新刷写目标系统", target.path, target.offset)
	return s, nil
}

func (s *streamImage) Write(data []byte) (int, error) {
	if int64(len(data)) > s.target.limit-s.written ||
		(s.expected >= 0 && int64(len(data)) > s.expected-s.written) {
		return 0, fmt.Errorf("展开镜像超过声明长度或目标容量")
	}
	n, err := s.target.file.WriteAt(data, s.target.offset+s.written)
	if n > 0 {
		s.hash.Write(data[:n])
		s.written += int64(n)
		updateTask(s.task.ID, func(t *FlashTask) {
			t.Written = s.written
			t.Speed = float64(s.written) / time.Since(s.start).Seconds()
			if t.Total > 0 {
				t.Progress = min(99, float64(t.Written)/float64(t.Total)*100)
			}
		})
		upMu.Lock()
		upState.Written = s.written
		upMu.Unlock()
	}
	if err == nil && n != len(data) {
		err = io.ErrShortWrite
	}
	return n, err
}

func (s *streamImage) copyFrom(reader io.Reader) error {
	writer := bufio.NewWriterSize(s, blockSize)
	if _, err := io.CopyBuffer(writer, reader, make([]byte, blockSize)); err != nil {
		return err
	}
	return writer.Flush()
}

func (s *streamImage) verify(ctx context.Context) error {
	if s.written == 0 || s.written < s.minimum || (s.expected >= 0 && s.written != s.expected) || s.written%512 != 0 {
		return fmt.Errorf("镜像数据不完整或未按扇区对齐")
	}
	if err := s.target.file.Sync(); err != nil {
		return err
	}
	updateTask(s.task.ID, func(t *FlashTask) { t.Status, t.Total = "verifying", s.written })
	upMu.Lock()
	upState.ExpandedTotal, upState.ExpandedEstimate = s.written, false
	upMu.Unlock()
	upSetStage("verifying")
	check := sha256.New()
	n, err := io.CopyBuffer(check, contextReader{ctx, io.NewSectionReader(s.target.file, s.target.offset, s.written)},
		make([]byte, blockSize))
	if err != nil || n != s.written || !bytes.Equal(check.Sum(nil), s.hash.Sum(nil)) {
		return fmt.Errorf("流式写入 SHA256 读回校验失败: %v", err)
	}
	return nil
}

func (s *streamImage) commit(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := checkFlashDiskIdle(s.target.path); err != nil {
		return err
	}
	if s.target.system {
		current, err := readGPT(s.target.path)
		if err != nil || !sameGPT(current, s.target.current) {
			return fmt.Errorf("写入过程中 eMMC 分区表发生变化，未提交新分区表")
		}
		updateTask(s.task.ID, func(t *FlashTask) { t.Status = "partitioning" })
		if err := applySystemAreaPartitions(s.task); err != nil {
			return err
		}
		if len(s.sourceGPT) == 0 {
			return nil
		}
		return adaptSystemBootPartitions(s.task, s.sourceGPT)
	}
	if len(s.sourceGPT) > 0 {
		if out, err := exec.Command("sgdisk", "-e", s.target.path).CombinedOutput(); err != nil {
			return fmt.Errorf("修正备份 GPT 失败: %v: %s", err, out)
		}
	}
	if out, err := exec.Command("partprobe", s.target.path).CombinedOutput(); err != nil {
		return fmt.Errorf("刷新目标分区表失败: %v: %s", err, out)
	}
	return nil
}

func sameGPT(a, b []gptPartition) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

type firmwareReceiver struct {
	ctx       context.Context
	target    *streamTarget
	dir       string
	threshold int64
	files     []string
	staged    int64
	direct    *streamImage
}

func safeArchiveName(name string) (string, error) {
	name = strings.ReplaceAll(name, "\\", "/")
	if name == "" || strings.HasPrefix(name, "/") || strings.Contains(name, ":") {
		return "", fmt.Errorf("固件包路径无效")
	}
	for _, component := range strings.Split(name, "/") {
		if component == ".." {
			return "", fmt.Errorf("固件包路径越界")
		}
	}
	name = filepath.Clean(filepath.FromSlash(name))
	if name == "." {
		return "", fmt.Errorf("固件包文件名无效")
	}
	return name, nil
}

func (u *firmwareReceiver) receive(name string, size int64, input io.Reader) error {
	name, err := safeArchiveName(name)
	if err != nil {
		return err
	}
	if u.direct != nil {
		return fmt.Errorf("大镜像压缩包只允许包含一个镜像文件；目标系统不完整，请重新刷写")
	}
	if len(u.files) >= 256 || size > u.target.limit {
		return fmt.Errorf("文件数量过多或镜像超过目标容量")
	}
	if size == 0 {
		return fmt.Errorf("固件包包含空文件")
	}
	reader := bufio.NewReaderSize(contextReader{u.ctx, input}, streamProbeBytes)
	prefix, peekErr := reader.Peek(streamProbeBytes)
	if peekErr != nil && peekErr != io.EOF {
		return peekErr
	}
	hintedSize := int64(0)
	if len(prefix) >= 520 && string(prefix[512:520]) == "EFI PART" {
		_, hintedSize, err = streamGPT(prefix)
		if err != nil {
			return err
		}
	}
	var fs syscall.Statfs_t
	if err := syscall.Statfs(u.dir, &fs); err != nil {
		return err
	}
	stagingLimit := min(u.threshold-u.staged, max(0, int64(fs.Bavail)*fs.Bsize-(64<<20)))
	if hintedSize > u.target.limit {
		return fmt.Errorf("GPT 镜像超过目标空间容量")
	}
	if size > stagingLimit || size < 0 {
		if len(u.files) != 0 {
			return fmt.Errorf("大镜像压缩包只允许包含一个镜像文件")
		}
		if _, err := checkFirmwareName(name); err != nil {
			return err
		}
		u.direct, err = beginStreamImage(u.target, prefix, size)
		if err != nil {
			return err
		}
		return u.direct.copyFrom(reader)
	}
	path := filepath.Join(u.dir, name)
	for _, previous := range u.files {
		if path == previous {
			return fmt.Errorf("固件包包含重复文件路径")
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer file.Close()
	remaining := stagingLimit
	if remaining < 0 {
		return fmt.Errorf("多文件固件包展开大小超过 4 GiB")
	}
	n, err := io.CopyBuffer(file, io.LimitReader(reader, remaining+1), make([]byte, blockSize))
	if err != nil {
		return err
	}
	if n > remaining {
		return fmt.Errorf("固件包长度不符或多文件固件包超过暂存容量")
	}
	if size >= 0 && n != size {
		return fmt.Errorf("解压文件长度与声明不符")
	}
	if n == 0 {
		return fmt.Errorf("固件文件为空")
	}
	u.staged += n
	u.files = append(u.files, path)
	return nil
}

func (u *firmwareReceiver) receiveTar(input io.Reader) error {
	tr := tar.NewReader(input)
	for {
		head, err := tr.Next()
		if err == io.EOF {
			// Consume the compression trailer as well; tar EOF alone does not
			// prove a valid gzip checksum or a complete HTTP request.
			_, err = io.Copy(io.Discard, input)
			return err
		}
		if err != nil {
			return err
		}
		if head.Typeflag == tar.TypeDir {
			if _, err := safeArchiveName(head.Name); err != nil {
				return err
			}
			continue
		}
		if head.Typeflag != tar.TypeReg && head.Typeflag != tar.TypeRegA {
			return fmt.Errorf("固件包不允许链接或特殊文件")
		}
		if err := u.receive(head.Name, head.Size, tr); err != nil {
			return err
		}
	}
}

func (u *firmwareReceiver) receiveArchive(name string, declared int64, input io.Reader) error {
	lower := strings.ToLower(name)
	switch {
	case strings.HasSuffix(lower, ".tar.gz"), strings.HasSuffix(lower, ".tgz"):
		gz, err := gzip.NewReader(input)
		if err != nil {
			return err
		}
		defer gz.Close()
		return u.receiveTar(gz)
	case strings.HasSuffix(lower, ".tar.bz2"), strings.HasSuffix(lower, ".tbz2"), strings.HasSuffix(lower, ".tbz"):
		return u.receiveTar(bzip2.NewReader(input))
	case strings.HasSuffix(lower, ".tar"):
		return u.receiveTar(input)
	case strings.HasSuffix(lower, ".rar"):
		rar, err := rardecode.NewReader(input, rardecode.MaxDictionarySize(256<<20))
		if err != nil {
			return err
		}
		for {
			head, err := rar.Next()
			if err == io.EOF {
				_, err = io.Copy(io.Discard, input)
				return err
			}
			if err != nil {
				return err
			}
			if head.Encrypted || head.HeaderEncrypted || head.LinkTarget != "" || head.LinkType != 0 {
				return fmt.Errorf("固件包不允许加密或链接")
			}
			if head.IsDir {
				continue
			}
			size := head.UnPackedSize
			if head.UnKnownSize {
				size = -1
			}
			if err := u.receive(head.Name, size, rar); err != nil {
				return err
			}
		}
	case strings.HasSuffix(lower, ".zip"), strings.HasSuffix(lower, ".7z"):
		return u.receiveRandomArchive(lower, input)
	case strings.HasSuffix(lower, ".gz"):
		inner := name[:len(name)-3]
		if _, err := checkFirmwareName(inner); err != nil {
			return err
		}
		gz, err := gzip.NewReader(input)
		if err != nil {
			return err
		}
		defer gz.Close()
		return u.receive(inner, -1, gz)
	case strings.HasSuffix(lower, ".bz2"):
		inner := name[:len(name)-4]
		if _, err := checkFirmwareName(inner); err != nil {
			return err
		}
		return u.receive(inner, -1, bzip2.NewReader(input))
	default:
		if _, err := checkFirmwareName(name); err != nil {
			return err
		}
		return u.receive(name, declared, input)
	}
}

func (u *firmwareReceiver) receiveRandomArchive(name string, input io.Reader) error {
	file, err := os.CreateTemp(filepath.Dir(u.dir), "compressed-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	var fs syscall.Statfs_t
	if err := syscall.Statfs(filepath.Dir(u.dir), &fs); err != nil {
		return err
	}
	limit := int64(fs.Bavail) * fs.Bsize
	// Leave room for a staged small image; large single images bypass it.
	limit -= 256 << 20
	if limit <= 0 {
		return fmt.Errorf("压缩包暂存空间不足，请改用 .img.gz 或 .img.bz2")
	}
	n, err := io.CopyBuffer(file, io.LimitReader(input, limit+1), make([]byte, blockSize))
	if err != nil {
		return err
	}
	if n > limit {
		return fmt.Errorf("ZIP/7z 压缩包本身超过暂存空间，请改用 .img.gz 或 .img.bz2")
	}
	upSetStage("extracting")
	if strings.HasSuffix(name, ".zip") {
		reader, err := zip.NewReader(file, n)
		if err != nil {
			return err
		}
		regular := 0
		for _, entry := range reader.File {
			if !entry.FileInfo().IsDir() {
				regular++
			}
		}
		for _, entry := range reader.File {
			if _, err := safeArchiveName(entry.Name); err != nil {
				return err
			}
			if entry.FileInfo().IsDir() {
				continue
			}
			if !entry.Mode().IsRegular() || entry.UncompressedSize64 > math.MaxInt64 ||
				(regular != 1 && entry.UncompressedSize64 > uint64(u.threshold)) {
				return fmt.Errorf("大镜像压缩包必须仅包含一个普通镜像文件")
			}
		}
		for _, entry := range reader.File {
			if entry.FileInfo().IsDir() {
				continue
			}
			r, err := entry.Open()
			if err != nil {
				return err
			}
			err = u.receive(entry.Name, int64(entry.UncompressedSize64), r)
			closeErr := r.Close()
			if err != nil {
				return err
			}
			if closeErr != nil {
				return closeErr
			}
		}
		return nil
	}
	reader, err := sevenzip.NewReader(file, n)
	if err != nil {
		return err
	}
	regular := 0
	for _, entry := range reader.File {
		if !entry.FileInfo().IsDir() {
			regular++
		}
	}
	for _, entry := range reader.File {
		if _, err := safeArchiveName(entry.Name); err != nil {
			return err
		}
		if entry.FileInfo().IsDir() {
			continue
		}
		if !entry.Mode().IsRegular() || entry.UncompressedSize > math.MaxInt64 ||
			(regular != 1 && entry.UncompressedSize > uint64(u.threshold)) {
			return fmt.Errorf("大镜像压缩包必须仅包含一个普通镜像文件")
		}
	}
	for _, entry := range reader.File {
		if entry.FileInfo().IsDir() {
			continue
		}
		r, err := entry.Open()
		if err != nil {
			return err
		}
		err = u.receive(entry.Name, int64(entry.UncompressedSize), r)
		closeErr := r.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}

type firmwareUploadRuntime struct {
	openTarget func(string) (*streamTarget, error)
	commit     func(context.Context, *streamImage) error
	flash      func(*FlashTask)
	dir        string
	threshold  int64
}

func handleFirmwareUpload(w http.ResponseWriter, r *http.Request) {
	runtime := firmwareUploadRuntime{
		openTarget: openStreamTarget,
		commit:     func(ctx context.Context, s *streamImage) error { return s.commit(ctx) },
		flash:      doFlashLocked,
		dir:        uploadDir, threshold: directWriteThreshold,
	}
	runtime.serveHTTP(w, r)
}

func (runtime firmwareUploadRuntime) serveHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonErr(w, http.StatusMethodNotAllowed, "仅支持 POST")
		return
	}
	if !releaseWriteMu.TryLock() {
		jsonErr(w, http.StatusConflict, "已有刷写、备份或升级任务")
		return
	}
	handedOff := false
	defer func() {
		if !handedOff {
			releaseWriteMu.Unlock()
		}
	}()
	upBegin(r.ContentLength)
	defer upEnd()
	r.Body = &countingBody{r.Body}
	mr, err := r.MultipartReader()
	if err != nil {
		jsonErr(w, http.StatusBadRequest, err.Error())
		return
	}
	var target *streamTarget
	var receiver *firmwareReceiver
	declared := int64(-1)
	received := false
	defer func() {
		if target != nil && target.file != nil {
			target.file.Close()
		}
		if !handedOff && receiver != nil {
			os.RemoveAll(runtime.dir)
		}
	}()
	fail := func(err error) {
		if receiver != nil && receiver.direct != nil {
			message := "流式刷写失败，目标系统可能不完整，请重新刷写；" + err.Error()
			updateTask(receiver.direct.task.ID, func(t *FlashTask) { t.Status, t.Error = "error", message })
			err = fmt.Errorf("%s", message)
		}
		advLog("error", "固件上传失败: %v", err)
		jsonErr(w, http.StatusBadRequest, err.Error())
	}
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			fail(err)
			return
		}
		switch part.FormName() {
		case "target_device":
			if target != nil || received {
				fail(fmt.Errorf("目标设备字段重复或顺序无效"))
				return
			}
			data, err := io.ReadAll(io.LimitReader(part, 1025))
			if err != nil || len(data) > 1024 {
				fail(fmt.Errorf("目标设备字段无效"))
				return
			}
			target, err = runtime.openTarget(strings.TrimSpace(string(data)))
			if err != nil {
				fail(err)
				return
			}
		case "firmware_size":
			if received || declared >= 0 {
				fail(fmt.Errorf("固件大小字段重复或顺序无效"))
				return
			}
			data, err := io.ReadAll(io.LimitReader(part, 32))
			if err != nil || len(data) == 32 {
				fail(fmt.Errorf("固件大小字段无效"))
				return
			}
			declared, err = strconv.ParseInt(string(data), 10, 64)
			if err != nil || declared < 0 {
				fail(fmt.Errorf("固件大小字段无效"))
				return
			}
		case "firmware":
			if target == nil || received {
				fail(fmt.Errorf("必须先选择目标设备，再上传一个固件文件"))
				return
			}
			received = true
			if err := os.RemoveAll(runtime.dir); err != nil {
				fail(err)
				return
			}
			unpack := filepath.Join(runtime.dir, "unpack")
			if err := os.MkdirAll(unpack, 0700); err != nil {
				fail(err)
				return
			}
			receiver = &firmwareReceiver{ctx: r.Context(), target: target, dir: unpack, threshold: runtime.threshold}
			name := filepath.Base(part.FileName())
			upSet(name, "streaming")
			if err := receiver.receiveArchive(name, declared, contextReader{r.Context(), part}); err != nil {
				fail(err)
				return
			}
		default:
			fail(fmt.Errorf("未知上传字段"))
			return
		}
		if err := part.Close(); err != nil {
			fail(err)
			return
		}
	}
	if !received {
		fail(fmt.Errorf("请选择固件文件"))
		return
	}
	if err := r.Context().Err(); err != nil {
		fail(err)
		return
	}
	if receiver.direct != nil {
		s := receiver.direct
		if err := s.verify(r.Context()); err != nil {
			fail(err)
			return
		}
		if err := runtime.commit(r.Context(), s); err != nil {
			fail(err)
			return
		}
		updateTask(s.task.ID, func(t *FlashTask) {
			t.Status, t.Progress, t.Written, t.Total = "done", 100, s.written, s.written
		})
		jsonOK(w, map[string]string{"task_id": s.task.ID, "message": "流式直写完成，SHA256 读回校验通过"})
		return
	}
	task, message, err := buildTaskFromExtracted(receiver.files, receiver.dir, target.path)
	if err != nil {
		fail(err)
		return
	}
	handedOff = true
	go func() {
		defer releaseWriteMu.Unlock()
		defer os.RemoveAll(runtime.dir)
		runtime.flash(task)
	}()
	jsonOK(w, map[string]string{"task_id": task.ID, "message": message})
}
