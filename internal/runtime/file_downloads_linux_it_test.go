//go:build linux

package runtime

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	stdruntime "runtime"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/ravenmk2/wispjar/internal/session"
)

func exportFixture(t *testing.T, root, relative string, archive bool, limit int64) exportResult {
	t.Helper()
	key := strings.Repeat("a", 32) + fmt.Sprintf("%032x", time.Now().UnixNano())
	args := map[string]any{"op": "export", "id": key, "root": root, "path": relative, "archive": archive, "name": "成果", "max_bytes": limit, "max_entries": 1000, "timeout": 300}
	result, stopped, err := exportFile(context.Background(), localFileHandle{}, args, 5*time.Minute)
	t.Cleanup(func() {
		_, _, cleanupErr := exportFile(context.Background(), localFileHandle{}, map[string]any{"op": "remove", "id": key}, 10*time.Second)
		if cleanupErr != nil {
			t.Error(cleanupErr)
		}
	})
	if err != nil || !stopped {
		t.Fatalf("export stopped=%v err=%v", stopped, err)
	}
	return result
}

func downloadIntegrationRoot(t *testing.T) string {
	t.Helper()
	if os.Getenv("WISPJAR_EXECUTION_TEST") != "1" {
		t.Skip("requires disposable root Linux container")
	}
	if os.Geteuid() != 0 {
		t.Fatal("root required")
	}
	if err := os.MkdirAll("/storage", 0755); err != nil {
		t.Fatal(err)
	}
	root, err := os.MkdirTemp("/storage", "download-test-")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(root, 0755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	return root
}

func TestScopedDownloadExportIntegration(t *testing.T) {
	root := downloadIntegrationRoot(t)
	for _, name := range []string{"empty", "nested"} {
		if err := os.Mkdir(filepath.Join(root, name), 0755); err != nil {
			t.Fatal(err)
		}
	}
	for name, content := range map[string]string{"中文'\"$().txt": "content\n", ".hidden": "hidden", "nested/data.txt": "nested data"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	single := exportFixture(t, root, "中文'\"$().txt", false, 1<<20)
	data, err := os.ReadFile(single.Path)
	if err != nil || string(data) != "content\n" {
		t.Fatalf("%q %v", data, err)
	}
	archive := exportFixture(t, root, ".", true, 1<<20)
	z, err := zip.OpenReader(archive.Path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = z.Close() }()
	names := map[string]bool{}
	for _, f := range z.File {
		names[f.Name] = true
	}
	for _, name := range []string{"成果/", "成果/empty/", "成果/.hidden", "成果/nested/data.txt", "成果/中文'\"$().txt"} {
		if !names[name] {
			t.Fatalf("missing archive entry %q", name)
		}
	}
	// The task identity cannot read the immutable platform artifact.
	res, err := localFileHandle{}.RunCommand(context.Background(), "su -s /bin/sh nobody -c 'cat "+single.Path+"'", "/", nil, 1000, nil)
	if err == nil && res.ExitCode == 0 {
		t.Fatal("task identity read platform export")
	}
	if err = os.Symlink("/etc/passwd", filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	for _, relative := range []string{"link", "../etc/passwd", "."} {
		key := strings.Repeat("b", 32) + fmt.Sprintf("%032x", time.Now().UnixNano())
		_, stopped, err := exportFile(context.Background(), localFileHandle{}, map[string]any{"op": "export", "id": key, "root": root, "path": relative, "archive": relative == ".", "name": "test", "max_bytes": 1 << 20, "max_entries": 1000, "timeout": 10}, 15*time.Second)
		if err == nil || !stopped {
			t.Fatalf("unsafe export %q stopped=%v err=%v", relative, stopped, err)
		}
		if _, statErr := os.Stat("/tmp/.wispjar-downloads/" + key); !os.IsNotExist(statErr) {
			t.Fatal("failed export left artifact")
		}
	}
	if err = os.Remove(filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	key := strings.Repeat("c", 64)
	_, stopped, err := exportFile(context.Background(), localFileHandle{}, map[string]any{"op": "export", "id": key, "root": root, "path": "nested/data.txt", "archive": false, "name": "test", "max_bytes": 2, "max_entries": 1000, "timeout": 10}, 15*time.Second)
	if err == nil || !stopped {
		t.Fatal("size limit not enforced")
	}
	// Cancel before the export starts; no artifact or unknown child may remain.
	_, _, err = exportFile(context.Background(), localFileHandle{}, map[string]any{"op": "cancel", "id": key}, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	_, stopped, err = exportFile(context.Background(), localFileHandle{}, map[string]any{"op": "export", "id": key, "root": root, "path": ".", "archive": true, "name": "test", "max_bytes": 1 << 20, "max_entries": 1000, "timeout": 10}, 15*time.Second)
	// A tiny export can finish before noticing cancellation; either outcome is
	// stopped and cleanup must always work.
	if !stopped {
		t.Fatalf("cancel completion unknown: %v", err)
	}
	_, _, err = exportFile(context.Background(), localFileHandle{}, map[string]any{"op": "remove", "id": key}, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
}

func TestLargeDownloadIntegration(t *testing.T) {
	if os.Getenv("WISPJAR_LARGE_DOWNLOAD_TEST") != "1" {
		t.Skip("requires disposable disk space for 3/5 GiB transfer")
	}
	root := downloadIntegrationRoot(t)
	gin.SetMode(gin.TestMode)
	for _, size := range []int64{3<<30 + 17, 5<<30 + 31} {
		t.Run(fmt.Sprintf("%d-bytes", size), func(t *testing.T) {
			file := filepath.Join(root, "large.bin")
			f, err := os.OpenFile(file, os.O_CREATE|os.O_TRUNC|os.O_RDWR, 0644)
			if err != nil {
				t.Fatal(err)
			}
			if err = f.Truncate(size); err != nil {
				t.Fatal(err)
			}
			if _, err = f.WriteAt([]byte("begin"), 0); err != nil {
				t.Fatal(err)
			}
			if _, err = f.WriteAt([]byte("the-end"), size-7); err != nil {
				t.Fatal(err)
			}
			expected := sha256.New()
			if _, err = io.Copy(expected, f); err != nil {
				t.Fatal(err)
			}
			_ = f.Close()
			result := exportFixture(t, root, "large.bin", false, 20<<30)
			if result.Bytes != size {
				t.Fatalf("size=%d want=%d", result.Bytes, size)
			}
			serve := func(snapshot exportResult, expectedHash []byte) {
				h := &Handler{sessions: fakeSessionService{detail: &session.Detail{}}, downloads: map[string]*fileDownload{"large": {Status: "ready", Name: "large.bin", uid: "owner", sessionID: "s", local: snapshot.Path, etag: `"version"`, expires: time.Now().Add(time.Hour)}}}
				r := gin.New()
				r.GET("/downloads/:downloadId/file", h.DownloadContent)
				server := httptest.NewServer(r)
				defer server.Close()
				response, err := http.Get(server.URL + "/downloads/large/file")
				if err != nil {
					t.Fatal(err)
				}
				actual := sha256.New()
				count, err := io.Copy(actual, response.Body)
				_ = response.Body.Close()
				if err != nil || response.StatusCode != 200 || count != snapshot.Bytes {
					t.Fatalf("HTTP transfer: %d %d %v", response.StatusCode, count, err)
				}
				if expectedHash != nil && hex.EncodeToString(actual.Sum(nil)) != hex.EncodeToString(expectedHash) {
					t.Fatal("large transfer hash mismatch")
				}
				req, _ := http.NewRequest("GET", server.URL+"/downloads/large/file", nil)
				req.Header.Set("Range", fmt.Sprintf("bytes=%d-", snapshot.Bytes-7))
				req.Header.Set("If-Range", `"version"`)
				response, err = http.DefaultClient.Do(req)
				if err != nil {
					t.Fatal(err)
				}
				tail, err := io.ReadAll(response.Body)
				_ = response.Body.Close()
				if err != nil || response.StatusCode != 206 || len(tail) != 7 {
					t.Fatalf("large Range: code=%d bytes=%d err=%v", response.StatusCode, len(tail), err)
				}
				stdruntime.GC()
				var memory stdruntime.MemStats
				stdruntime.ReadMemStats(&memory)
				if memory.HeapAlloc > 128<<20 {
					t.Fatalf("download heap too large: %d", memory.HeapAlloc)
				}
				t.Logf("HTTP streamed %d bytes; sha256=%x; live heap=%d bytes; tail Range returned 206", count, actual.Sum(nil), memory.HeapAlloc)
			}
			serve(result, expected.Sum(nil))
			if err = os.Remove(result.Path); err != nil {
				t.Fatal(err)
			}
			if size > 4<<30 {
				archive := exportFixture(t, root, ".", true, 20<<30)
				if archive.Bytes <= 4<<30 {
					t.Fatalf("archive is not over 4 GiB: %d", archive.Bytes)
				}
				z, err := zip.OpenReader(archive.Path)
				if err != nil {
					t.Fatal(err)
				}
				found := false
				for _, entry := range z.File {
					if entry.Name == "成果/large.bin" {
						found = entry.UncompressedSize64 == uint64(size) && entry.ReaderVersion >= 45
					}
				}
				_ = z.Close()
				if !found {
					t.Fatal("ZIP64 entry missing")
				}
				serve(archive, nil)
			}
		})
	}
}
