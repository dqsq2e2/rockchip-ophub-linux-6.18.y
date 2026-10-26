package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func testStreamReceiver(t *testing.T, threshold, capacity int64) *firmwareReceiver {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "target.img")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { file.Close() })
	if err := file.Truncate(capacity); err != nil {
		t.Fatal(err)
	}
	spool := filepath.Join(dir, "upload", "unpack")
	if parent := os.Getenv("LUNZN_TEST_STREAM_SPOOL_DIR"); parent != "" {
		spool, err = os.MkdirTemp(parent, "stream-spool-")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.RemoveAll(spool) })
	}
	if err := os.MkdirAll(spool, 0700); err != nil {
		t.Fatal(err)
	}
	return &firmwareReceiver{ctx: context.Background(), dir: spool, threshold: threshold,
		target: &streamTarget{file: file, path: path, sectors: uint64(capacity / 512), limit: capacity}}
}

func assertDirectContents(t *testing.T, u *firmwareReceiver, want []byte) {
	t.Helper()
	if u.direct == nil || len(u.files) != 0 {
		t.Fatalf("not direct: %v, files %v", u.direct != nil, u.files)
	}
	if err := u.direct.verify(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, len(want))
	if _, err := u.target.file.ReadAt(got, u.target.offset); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("direct contents differ")
	}
	var staged int64
	filepath.Walk(u.dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			staged += info.Size()
		}
		return nil
	})
	if staged != 0 {
		t.Fatalf("expanded image left in tmp: %d bytes", staged)
	}
}

func TestDirectThresholdAndStaging(t *testing.T) {
	if directWriteThreshold != 4294967296 {
		t.Fatal("4 GiB boundary changed")
	}
	for _, size := range []int{512, 4096, 4608, 131072} {
		for _, unknown := range []bool{false, true} {
			t.Run(strconv.Itoa(size)+strconvBool(unknown), func(t *testing.T) {
				u := testStreamReceiver(t, 4096, 1<<20)
				data := bytes.Repeat([]byte{0x54}, size)
				declared := int64(size)
				if unknown {
					declared = -1
				}
				if err := u.receive("disk.img", declared, bytes.NewReader(data)); err != nil {
					t.Fatal(err)
				}
				if size > 4096 || unknown {
					assertDirectContents(t, u, data)
				} else {
					if u.direct != nil || len(u.files) != 1 {
						t.Fatal("small image did not use staged path")
					}
					got, _ := os.ReadFile(u.files[0])
					if !bytes.Equal(got, data) {
						t.Fatal("staged contents differ")
					}
					probe := make([]byte, size)
					u.target.file.ReadAt(probe, 0)
					if !bytes.Equal(probe, make([]byte, size)) {
						t.Fatal("small staged image modified destination")
					}
				}
			})
		}
	}
}

func strconvBool(value bool) string {
	if value {
		return "-unknown"
	}
	return "-known"
}

func TestStreamCapacityAndInvalidInput(t *testing.T) {
	data := bytes.Repeat([]byte{0x41}, 131072)
	for _, test := range []struct {
		name string
		size int64
		data []byte
	}{
		{"over-capacity.img", 2 << 20, data},
		{"../escape.img", int64(len(data)), data},
		{"/absolute.img", int64(len(data)), data},
		{"C:\\escape.img", int64(len(data)), data},
		{"disk.txt", int64(len(data)), data},
		{"disk.img.gz", int64(len(data)), data},
	} {
		t.Run(test.name, func(t *testing.T) {
			u := testStreamReceiver(t, 4096, 1<<20)
			if err := u.receive(test.name, test.size, bytes.NewReader(test.data)); err == nil {
				t.Fatal("invalid input accepted")
			}
			if u.direct != nil {
				t.Fatal("invalid input began writing")
			}
		})
	}
	t.Run("truncated", func(t *testing.T) {
		u := testStreamReceiver(t, 4096, 1<<20)
		if err := u.receive("disk.img", 2*int64(len(data)), bytes.NewReader(data)); err != nil {
			t.Fatal(err)
		}
		if err := u.direct.verify(context.Background()); err == nil {
			t.Fatal("truncated direct stream accepted")
		}
	})
	t.Run("overflow", func(t *testing.T) {
		u := testStreamReceiver(t, 4096, 1<<20)
		if err := u.receive("disk.img", 65536, bytes.NewReader(data)); err == nil {
			t.Fatal("oversized direct stream accepted")
		}
	})
	t.Run("cancelled", func(t *testing.T) {
		u := testStreamReceiver(t, 4096, 1<<20)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		u.ctx = ctx
		if err := u.receive("disk.img", int64(len(data)), bytes.NewReader(data)); err == nil || u.direct != nil {
			t.Fatal("cancelled upload began writing")
		}
	})
	t.Run("readback", func(t *testing.T) {
		u := testStreamReceiver(t, 4096, 1<<20)
		if err := u.receive("disk.img", int64(len(data)), bytes.NewReader(data)); err != nil {
			t.Fatal(err)
		}
		u.target.file.WriteAt([]byte{0xff}, 600)
		if err := u.direct.verify(context.Background()); err == nil {
			t.Fatal("corrupt readback accepted")
		}
	})
}

func gzipData(t *testing.T, data []byte) []byte {
	t.Helper()
	var output bytes.Buffer
	gz := gzip.NewWriter(&output)
	if _, err := gz.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func tarData(t *testing.T, data []byte, second bool) []byte {
	t.Helper()
	var output bytes.Buffer
	tr := tar.NewWriter(&output)
	tr.WriteHeader(&tar.Header{Name: "disk.img", Mode: 0600, Size: int64(len(data))})
	tr.Write(data)
	if second {
		tr.WriteHeader(&tar.Header{Name: "other.img", Mode: 0600, Size: 512})
		tr.Write(make([]byte, 512))
	}
	if err := tr.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func TestStreamingCompressedImages(t *testing.T) {
	data := bytes.Repeat([]byte{0x7a}, 256*1024)
	cases := map[string][]byte{
		"disk.img.gz": gzipData(t, data),
		"disk.tar.gz": gzipData(t, tarData(t, data, false)),
		"disk.tar":    tarData(t, data, false),
	}
	if binary, err := exec.LookPath("bzip2"); err == nil {
		cmd := exec.Command(binary, "-c")
		cmd.Stdin = bytes.NewReader(data)
		packed, err := cmd.Output()
		if err != nil {
			t.Fatal(err)
		}
		cases["disk.img.bz2"] = packed
	}
	for name, compressed := range cases {
		t.Run(name, func(t *testing.T) {
			u := testStreamReceiver(t, 4096, 1<<20)
			if err := u.receiveArchive(name, int64(len(compressed)), bytes.NewReader(compressed)); err != nil {
				t.Fatal(err)
			}
			assertDirectContents(t, u, data)
		})
	}
	t.Run("gzip-crc", func(t *testing.T) {
		u := testStreamReceiver(t, 4096, 1<<20)
		compressed := gzipData(t, data)
		compressed[len(compressed)-8] ^= 0x10
		if err := u.receiveArchive("disk.img.gz", -1, bytes.NewReader(compressed)); err == nil {
			t.Fatal("gzip CRC failure ignored")
		}
	})
	t.Run("tar-gzip-trailer", func(t *testing.T) {
		u := testStreamReceiver(t, 4096, 1<<20)
		compressed := gzipData(t, tarData(t, data, false))
		compressed = compressed[:len(compressed)-4]
		if err := u.receiveArchive("disk.tar.gz", -1, bytes.NewReader(compressed)); err == nil {
			t.Fatal("truncated gzip trailer accepted after tar EOF")
		}
	})
	t.Run("multiple-large-files", func(t *testing.T) {
		u := testStreamReceiver(t, 4096, 1<<20)
		if err := u.receiveArchive("disk.tar", -1, bytes.NewReader(tarData(t, data, true))); err == nil {
			t.Fatal("multiple image direct archive accepted")
		}
	})
}

func TestZIPAndSevenZipStream(t *testing.T) {
	data := bytes.Repeat([]byte{0x18}, 256*1024)
	var zipData bytes.Buffer
	zw := zip.NewWriter(&zipData)
	f, _ := zw.Create("images/disk.img")
	f.Write(data)
	zw.Close()
	u := testStreamReceiver(t, 4096, 1<<20)
	if err := u.receiveArchive("disk.zip", int64(zipData.Len()), bytes.NewReader(zipData.Bytes())); err != nil {
		t.Fatal(err)
	}
	assertDirectContents(t, u, data)
	if _, err := exec.LookPath("7z"); err != nil {
		t.Skip("7z encoder unavailable")
	}
	dir := t.TempDir()
	image := filepath.Join(dir, "disk.img")
	archive := filepath.Join(dir, "disk.7z")
	os.WriteFile(image, data, 0600)
	if out, err := exec.Command("7z", "a", "-t7z", archive, image).CombinedOutput(); err != nil {
		t.Fatalf("7z: %v: %s", err, out)
	}
	compressed, _ := os.ReadFile(archive)
	u = testStreamReceiver(t, 4096, 1<<20)
	if err := u.receiveArchive("disk.7z", int64(len(compressed)), bytes.NewReader(compressed)); err != nil {
		t.Fatal(err)
	}
	assertDirectContents(t, u, data)
	remaining, _ := os.ReadDir(filepath.Dir(u.dir))
	if len(remaining) != 1 || remaining[0].Name() != "unpack" {
		t.Fatal("compressed spool not cleaned up")
	}
}

func TestStreamSystemAreaProtection(t *testing.T) {
	if _, err := exec.LookPath("sgdisk"); err != nil {
		t.Skip("requires sgdisk")
	}
	dir := t.TempDir()
	source := filepath.Join(dir, "source.img")
	sparseDisk(t, source, 16<<20, "-o", "-n", "1:2048:8191", "-c", "1:BOOT", "-A", "1:set:60")
	data, _ := os.ReadFile(source)
	u := testStreamReceiver(t, 1<<20, 512<<20)
	if out, err := exec.Command("sgdisk", "-o", "-n", "1:65536:524253", "-c", "1:LUNZN_RECOVERY",
		"-n", "2:524288:0", "-c", "2:LUNZN_SYSTEM", u.target.path).CombinedOutput(); err != nil {
		t.Fatalf("target: %v: %s", err, out)
	}
	current, err := readGPT(u.target.path)
	if err != nil {
		t.Fatal(err)
	}
	markers := []int64{0, 512, 16 << 20, (16 << 20) + 131072, 32 << 20, (256 << 20) - 4096}
	original := make(map[int64][32]byte)
	for _, offset := range markers {
		block := make([]byte, 4096)
		u.target.file.ReadAt(block, offset)
		original[offset] = sha256.Sum256(block)
	}
	u.target.system, u.target.current = true, current
	u.target.offset = int64(systemStartSector * 512)
	u.target.limit -= u.target.offset + gptBackupSectors*512
	compressed := gzipData(t, data)
	if err := u.receiveArchive("disk.img.gz", -1, bytes.NewReader(compressed)); err != nil {
		t.Fatal(err)
	}
	assertDirectContents(t, u, data)
	if u.direct.minimum != int64(len(data)) {
		t.Fatal("GPT length not detected before decompression")
	}
	for _, offset := range markers {
		block := make([]byte, 4096)
		u.target.file.ReadAt(block, offset)
		if sha256.Sum256(block) != original[offset] {
			t.Fatalf("protected eMMC region changed: %d", offset)
		}
	}
	sourceParts, _ := readGPT(source)
	next := u.direct.nextGPT
	if len(next) != 1 || next[0].UUID != sourceParts[0].UUID || next[0].Attributes != sourceParts[0].Attributes ||
		next[0].Start != sourceParts[0].Start+systemStartSector {
		t.Fatalf("bad stream GPT projection: %+v", next)
	}
	args, err := systemPartitionArgs(current, next)
	if err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("sgdisk", append(args, u.target.path)...).CombinedOutput(); err != nil {
		t.Fatalf("commit: %v: %s", err, out)
	}
	after, err := readGPT(u.target.path)
	if err != nil || after[0] != current[0] || after[1] != next[0] {
		t.Fatal("stream GPT commit changed recovery or source identity")
	}
}

func TestStreamInvalidGPTBeforeWriting(t *testing.T) {
	prefix := make([]byte, streamProbeBytes)
	copy(prefix[512:], "EFI PART")
	u := testStreamReceiver(t, 4096, 1<<20)
	if err := u.receive("disk.img", int64(len(prefix)), bytes.NewReader(prefix)); err == nil || u.direct != nil {
		t.Fatal("invalid GPT began writing")
	}
}

func TestStreamPreWriteRecheck(t *testing.T) {
	u := testStreamReceiver(t, 4096, 1<<20)
	checks := 0
	u.target.check = func() error {
		checks++
		return fmt.Errorf("target became busy")
	}
	data := bytes.Repeat([]byte{0x54}, 131072)
	if err := u.receive("disk.img", int64(len(data)), bytes.NewReader(data)); err == nil {
		t.Fatal("pre-write recheck failure ignored")
	}
	if checks != 1 || u.direct != nil {
		t.Fatal("write started before final device check")
	}
	got := make([]byte, len(data))
	if _, err := u.target.file.ReadAt(got, 0); err != nil || !bytes.Equal(got, make([]byte, len(data))) {
		t.Fatal("busy target was modified")
	}
}

func TestGPTStreamMinimumAndPadding(t *testing.T) {
	if _, err := exec.LookPath("sgdisk"); err != nil {
		t.Skip("requires sgdisk")
	}
	path := filepath.Join(t.TempDir(), "source.img")
	sparseDisk(t, path, 2<<20, "-o", "-n", "1:2048:3071")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	originalSize := len(data)
	data = append(data, bytes.Repeat([]byte{0x71}, 4096)...)
	u := testStreamReceiver(t, 4096, 4<<20)
	if err := u.receive("disk.img", -1, bytes.NewReader(data)); err != nil {
		t.Fatal(err)
	}
	if u.direct.minimum != int64(originalSize) || u.direct.expected != -1 {
		t.Fatal("GPT lower bound incorrectly treated as exact length")
	}
	assertDirectContents(t, u, data)
	u = testStreamReceiver(t, 4096, 4<<20)
	short := data[:originalSize-512]
	if err := u.receive("disk.img", -1, bytes.NewReader(short)); err != nil {
		t.Fatal(err)
	}
	if err := u.direct.verify(context.Background()); err == nil {
		t.Fatal("image shorter than GPT lower bound accepted")
	}
	u = testStreamReceiver(t, 4096, 4<<20)
	if err := u.receive("disk.img", int64(len(short)), bytes.NewReader(short)); err == nil || u.direct != nil {
		t.Fatal("declared length below GPT lower bound began writing")
	}
}

func TestUploadWriteReservation(t *testing.T) {
	releaseWriteMu.Lock()
	for _, endpoint := range []struct {
		handler http.HandlerFunc
		method  string
		path    string
	}{
		{handleFirmwareUpload, http.MethodPost, "/upload"},
		{handleBackupStream, http.MethodGet, "/api/backup/stream?disk=/dev/nvme0n1"},
		{handleRestoreStream, http.MethodPost, "/api/restore/stream?disk=/dev/nvme0n1"},
		{handleRecoveryUpgrade, http.MethodPost, "/api/recovery_upgrade"},
	} {
		recorder := httptest.NewRecorder()
		endpoint.handler(recorder, httptest.NewRequest(endpoint.method, endpoint.path, strings.NewReader("")))
		if recorder.Code != http.StatusConflict {
			t.Errorf("%s: concurrent disk task not rejected: %d", endpoint.path, recorder.Code)
		}
	}
	releaseWriteMu.Unlock()
	recorder := httptest.NewRecorder()
	handleFirmwareUpload(recorder, httptest.NewRequest(http.MethodGet, "/upload", nil))
	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatal("GET accepted")
	}
}

func TestRealLargeStreamImage(t *testing.T) {
	path := os.Getenv("LUNZN_TEST_STREAM_IMAGE")
	if path == "" {
		t.Skip("set LUNZN_TEST_STREAM_IMAGE for full-size stream test")
	}
	source, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	info, _ := source.Stat()
	if info.Size() <= directWriteThreshold {
		t.Fatal("test image does not cross 4 GiB")
	}
	u := testStreamReceiver(t, directWriteThreshold, info.Size()+1<<20)
	if err := u.receiveArchive(filepath.Base(path), info.Size(), source); err != nil {
		t.Fatal(err)
	}
	if u.direct == nil || u.direct.written != info.Size() {
		t.Fatal("real large image did not stream")
	}
	if err := u.direct.verify(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertIndependentImageHash(t, u, path)
	files, _ := os.ReadDir(u.dir)
	if len(files) != 0 {
		t.Fatal("real expanded image staged in tmp")
	}
	t.Logf("streamed and readback verified %d bytes; no expanded temporary image", info.Size())
}

func assertIndependentImageHash(t *testing.T, u *firmwareReceiver, sourcePath string) {
	t.Helper()
	if sourcePath == "" {
		t.Fatal("set LUNZN_TEST_STREAM_IMAGE to independently validate decoded image bytes")
	}
	source, err := os.Open(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	want, got := sha256.New(), sha256.New()
	size, err := io.CopyBuffer(want, source, make([]byte, blockSize))
	if err != nil {
		t.Fatal(err)
	}
	if size != u.direct.written {
		t.Fatalf("source image size %d differs from output %d", size, u.direct.written)
	}
	n, err := io.CopyBuffer(got, io.NewSectionReader(u.target.file, u.target.offset, size), make([]byte, blockSize))
	if err != nil || n != size {
		t.Fatalf("read independent output hash: %d bytes, %v", n, err)
	}
	if !bytes.Equal(want.Sum(nil), got.Sum(nil)) {
		t.Fatalf("output SHA256 %x differs from original image %x", got.Sum(nil), want.Sum(nil))
	}
	t.Logf("independent source/output SHA256: %x", want.Sum(nil))
}

func TestGPTStreamSizeOverflow(t *testing.T) {
	data := make([]byte, streamProbeBytes)
	binary.LittleEndian.PutUint64(data[544:552], ^uint64(0))
	if _, _, err := streamGPT(data); err == nil {
		t.Fatal("invalid GPT size accepted")
	}
}

func TestRealLargeStreamArchive(t *testing.T) {
	path := os.Getenv("LUNZN_TEST_STREAM_ARCHIVE")
	if path == "" {
		t.Skip("set LUNZN_TEST_STREAM_ARCHIVE for full-size archive test")
	}
	source, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	info, _ := source.Stat()
	u := testStreamReceiver(t, directWriteThreshold, 8<<30)
	if err := u.receiveArchive(filepath.Base(path), info.Size(), source); err != nil {
		t.Fatal(err)
	}
	if u.direct == nil || u.direct.written <= directWriteThreshold {
		t.Fatal("real archive did not stream a large image")
	}
	if err := u.direct.verify(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertIndependentImageHash(t, u, os.Getenv("LUNZN_TEST_STREAM_IMAGE"))
	files, _ := os.ReadDir(u.dir)
	if len(files) != 0 {
		t.Fatal("real expanded image staged in tmp")
	}
	t.Logf("archive %d bytes -> direct %d bytes, SHA256 readback verified", info.Size(), u.direct.written)
}

func multipartFirmware(t *testing.T, data []byte, name string, targetFirst bool, extra bool) (*bytes.Buffer, string) {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if targetFirst {
		writer.WriteField("target_device", "/dev/nvme0n1")
		writer.WriteField("firmware_size", strconv.Itoa(len(data)))
	}
	file, _ := writer.CreateFormFile("firmware", name)
	file.Write(data)
	if !targetFirst {
		writer.WriteField("target_device", "/dev/nvme0n1")
	}
	if extra {
		writer.WriteField("unexpected", "bad")
	}
	writer.Close()
	return &body, writer.FormDataContentType()
}

func TestFirmwareHTTPStreamLifecycle(t *testing.T) {
	data := bytes.Repeat([]byte{0x32}, 256<<10)
	for _, test := range []struct {
		name    string
		corrupt bool
		extra   bool
		first   bool
		want    int
	}{
		{"success", false, false, true, http.StatusOK},
		{"crc-failure", true, false, true, http.StatusBadRequest},
		{"trailing-field", false, true, true, http.StatusBadRequest},
		{"target-after-firmware", false, false, false, http.StatusBadRequest},
	} {
		t.Run(test.name, func(t *testing.T) {
			u := testStreamReceiver(t, 4096, 1<<20)
			commits := 0
			runtime := firmwareUploadRuntime{
				openTarget: func(path string) (*streamTarget, error) {
					if path != "/dev/nvme0n1" {
						return nil, fmt.Errorf("bad target")
					}
					return u.target, nil
				},
				commit: func(ctx context.Context, stream *streamImage) error { commits++; return ctx.Err() },
				flash:  func(task *FlashTask) { t.Error("unexpected staged flash") },
				dir:    filepath.Dir(u.dir), threshold: 4096,
			}
			compressed := gzipData(t, data)
			if test.corrupt {
				compressed[len(compressed)-8] ^= 1
			}
			body, contentType := multipartFirmware(t, compressed, "disk.img.gz", test.first, test.extra)
			req := httptest.NewRequest(http.MethodPost, "/upload", body)
			req.Header.Set("Content-Type", contentType)
			resp := httptest.NewRecorder()
			runtime.serveHTTP(resp, req)
			if resp.Code != test.want {
				t.Fatalf("status %d, want %d: %s", resp.Code, test.want, resp.Body)
			}
			if (commits == 1) != (test.want == http.StatusOK) {
				t.Fatalf("commit on invalid/incomplete request: %d", commits)
			}
			if test.want == http.StatusOK && !strings.Contains(resp.Body.String(), "SHA256") {
				t.Fatal("successful response omitted readback result")
			}
			if _, err := os.Stat(runtime.dir); !os.IsNotExist(err) {
				if test.first {
					t.Fatal("upload scratch files leaked")
				}
			}
		})
	}
}

func TestFirmwareHTTPDisconnectedUpload(t *testing.T) {
	u := testStreamReceiver(t, 4096, 1<<20)
	committed := false
	runtime := firmwareUploadRuntime{
		openTarget: func(string) (*streamTarget, error) { return u.target, nil },
		commit:     func(context.Context, *streamImage) error { committed = true; return nil },
		dir:        filepath.Dir(u.dir), threshold: 4096,
	}
	data := bytes.Repeat([]byte{0x31}, 256<<10)
	body, contentType := multipartFirmware(t, data, "disk.img", true, false)
	truncated := body.Bytes()[:body.Len()-1000]
	req := httptest.NewRequest(http.MethodPost, "/upload", io.NopCloser(bytes.NewReader(truncated)))
	req.Header.Set("Content-Type", contentType)
	resp := httptest.NewRecorder()
	runtime.serveHTTP(resp, req)
	if resp.Code != http.StatusBadRequest || committed {
		t.Fatalf("disconnected upload committed: %d %v", resp.Code, committed)
	}
}

func TestFirmwareHTTPStagedHandoff(t *testing.T) {
	u := testStreamReceiver(t, 1<<20, 1<<20)
	u.target.path = "/dev/nvme0n1"
	started := make(chan *FlashTask, 1)
	finish := make(chan struct{})
	runtime := firmwareUploadRuntime{
		openTarget: func(string) (*streamTarget, error) { return u.target, nil },
		commit: func(context.Context, *streamImage) error {
			t.Error("small image unexpectedly used direct commit")
			return nil
		},
		flash: func(task *FlashTask) { started <- task; <-finish },
		dir:   filepath.Dir(u.dir), threshold: 1 << 20,
	}
	defer func() {
		close(finish)
		releaseWriteMu.Lock()
		releaseWriteMu.Unlock()
		if _, err := os.Stat(runtime.dir); !os.IsNotExist(err) {
			t.Error("staged task scratch files leaked")
		}
	}()
	data := bytes.Repeat([]byte{0x21}, 131072)
	body, contentType := multipartFirmware(t, data, "disk.img", true, false)
	req := httptest.NewRequest(http.MethodPost, "/upload", body)
	req.Header.Set("Content-Type", contentType)
	resp := httptest.NewRecorder()
	runtime.serveHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("staged request failed: %d: %s", resp.Code, resp.Body)
	}
	select {
	case task := <-started:
		if task.Mode != "single" || len(task.Items) != 1 {
			t.Fatal("invalid staged task")
		}
		got, err := os.ReadFile(task.Items[0].Path)
		if err != nil || !bytes.Equal(got, data) {
			t.Fatal("scratch image removed before staged task finished")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("staged flash not started")
	}
	if releaseWriteMu.TryLock() {
		releaseWriteMu.Unlock()
		t.Fatal("write reservation released before staged task finished")
	}
}
