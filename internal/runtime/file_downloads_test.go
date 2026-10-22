package runtime

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/ravenmk2/wispjar/internal/session"
)

func TestDownloadSnapshotRangesAndRevocation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	file := filepath.Join(t.TempDir(), "snapshot")
	if err := os.WriteFile(file, []byte("0123456789"), 0600); err != nil {
		t.Fatal(err)
	}
	h := &Handler{sessions: fakeSessionService{detail: &session.Detail{}}, downloads: map[string]*fileDownload{
		"capability": {ID: "capability", Status: "ready", Name: "中文.txt", uid: "owner", sessionID: "session", local: file, etag: `"fixed-version"`, expires: time.Now().Add(time.Hour)},
	}}
	router := gin.New()
	router.GET("/downloads/:downloadId/file", h.DownloadContent)
	router.HEAD("/downloads/:downloadId/file", h.DownloadContent)
	for _, test := range []struct {
		name, method, key, ranges, ifRange string
		status                             int
		body, contentRange                 string
	}{
		{"full", "GET", "capability", "", "", 200, "0123456789", ""},
		{"partial", "GET", "capability", "bytes=3-5", "", 206, "345", "bytes 3-5/10"},
		{"suffix", "GET", "capability", "bytes=-2", "", 206, "89", "bytes 8-9/10"},
		{"matching version", "GET", "capability", "bytes=5-", `"fixed-version"`, 206, "56789", "bytes 5-9/10"},
		{"changed version", "GET", "capability", "bytes=5-", `"different-version"`, 200, "0123456789", ""},
		{"invalid range", "GET", "capability", "bytes=99-", "", 416, "", "bytes */10"},
		{"head", "HEAD", "capability", "", "", 200, "", ""},
		{"wrong capability", "GET", "unknown", "", "", 404, "", ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			req := httptest.NewRequest(test.method, "/downloads/"+test.key+"/file", nil)
			if test.ranges != "" {
				req.Header.Set("Range", test.ranges)
			}
			if test.ifRange != "" {
				req.Header.Set("If-Range", test.ifRange)
			}
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			if w.Code != test.status || (test.status < 400 && w.Body.String() != test.body) || w.Header().Get("Content-Range") != test.contentRange {
				t.Fatalf("code=%d body=%q headers=%v", w.Code, w.Body.String(), w.Header())
			}
			if test.status < 400 && !strings.Contains(w.Header().Get("Content-Disposition"), "%E4%B8%AD%E6%96%87.txt") {
				t.Fatal("missing UTF-8 download filename")
			}
		})
	}
	h.sessions = fakeSessionService{err: &session.Error{Status: 403, Code: "org/not_member", Message: "membership revoked"}}
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest("GET", "/downloads/capability/file", nil))
	if w.Code != 403 {
		t.Fatalf("revoked membership got %d", w.Code)
	}
	h.downloads["capability"].expires = time.Now().Add(-time.Second)
	if h.DownloadOwner("capability") != "" {
		t.Fatal("expired capability has an owner")
	}
	w = httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest("GET", "/downloads/capability/file", nil))
	if w.Code != 404 {
		t.Fatalf("expired download got %d", w.Code)
	}
}

type pausedDownloadWriter struct {
	*httptest.ResponseRecorder
	entered, resume chan struct{}
	once            sync.Once
}

func (w *pausedDownloadWriter) Write(data []byte) (int, error) {
	w.once.Do(func() { close(w.entered); <-w.resume })
	return w.ResponseRecorder.Write(data)
}

func TestExpiredActiveDownloadKeepsDiskQuotaUntilStreamCloses(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "content")
	if err := os.WriteFile(file, []byte("fixed snapshot"), 0600); err != nil {
		t.Fatal(err)
	}
	h := &Handler{sessions: fakeSessionService{detail: &session.Detail{}}, downloads: map[string]*fileDownload{
		"key": {ID: "key", Status: "ready", Name: "file.txt", Bytes: 14, local: file, directory: dir, uid: "owner", sessionID: "s", expires: time.Now().Add(time.Hour)},
	}}
	r := gin.New()
	r.GET("/downloads/:downloadId/file", h.DownloadContent)
	w := &pausedDownloadWriter{ResponseRecorder: httptest.NewRecorder(), entered: make(chan struct{}), resume: make(chan struct{})}
	done := make(chan struct{})
	go func() { r.ServeHTTP(w, httptest.NewRequest("GET", "/downloads/key/file", nil)); close(done) }()
	select {
	case <-w.entered:
	case <-time.After(2 * time.Second):
		close(w.resume)
		t.Fatal("stream did not start")
	}
	h.expireDownload("key")
	h.downloadMu.Lock()
	job := h.downloads["key"]
	retained := job != nil && job.Bytes == 14 && job.readers == 1
	h.downloadMu.Unlock()
	if !retained || h.DownloadOwner("key") != "" {
		close(w.resume)
		t.Fatal("expired active stream lost quota or still accepts new requests")
	}
	close(w.resume)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("stream did not complete")
	}
	if w.Body.String() != "fixed snapshot" || len(h.downloads) != 0 {
		t.Fatal("expiry truncated the active stream or leaked quota")
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatal("expired snapshot not removed after last reader")
	}
}

func TestDownloadStatusOwnershipAndCleanup(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dir := t.TempDir()
	file := filepath.Join(dir, "content")
	if err := os.WriteFile(file, []byte("snapshot"), 0600); err != nil {
		t.Fatal(err)
	}
	_, cancel := context.WithCancel(context.Background())
	h := &Handler{sessions: fakeSessionService{detail: &session.Detail{}}, downloads: map[string]*fileDownload{
		"key": {ID: "key", uid: "owner", sessionID: "s", Status: "ready", local: file, directory: dir, expires: time.Now().Add(time.Hour), cancel: cancel},
	}}
	router := gin.New()
	router.Use(func(c *gin.Context) { c.Set("wispjar.uid", c.GetHeader("Test-UID")) })
	router.GET("/sessions/:id/downloads/:downloadId", h.DownloadStatus)
	router.DELETE("/sessions/:id/downloads/:downloadId", h.CancelDownload)
	for _, test := range []struct {
		uid, sid string
		status   int
	}{{"other", "s", 404}, {"owner", "other-session", 404}, {"owner", "s", 200}} {
		req := httptest.NewRequest("GET", "/sessions/"+test.sid+"/downloads/key", nil)
		req.Header.Set("Test-UID", test.uid)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if w.Code != test.status {
			t.Fatalf("%s/%s: %d", test.uid, test.sid, w.Code)
		}
	}
	req := httptest.NewRequest("DELETE", "/sessions/s/downloads/key", nil)
	req.Header.Set("Test-UID", "owner")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK || len(h.downloads) != 0 {
		t.Fatal("cancel did not revoke capability")
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatalf("snapshot not removed: %v", err)
	}
}
