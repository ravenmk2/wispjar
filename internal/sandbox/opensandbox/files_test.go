package opensandbox

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"testing"

	opensandbox "github.com/alibaba/OpenSandbox/sdks/sandbox/go"
)

func TestUserFilesIdentityTransportAndErrors(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Header.Get("X-EXECD-ACCESS-TOKEN") != "test-token" {
			t.Error("lost execd authentication")
		}
		name := r.URL.Query().Get("path")
		if name == "/missing" {
			w.WriteHeader(404)
			if _, err := io.WriteString(w, `{"code":"FILE_NOT_FOUND"}`); err != nil {
				t.Errorf("write missing file response: %v", err)
			}
			return
		}
		if name == "/unsupported" {
			w.WriteHeader(404)
			if _, err := io.WriteString(w, `404 page not found`); err != nil {
				t.Errorf("write unsupported API response: %v", err)
			}
			return
		}
		if name != "/storage/中文 &.txt" {
			t.Errorf("path encoding: %q", name)
		}
		switch r.URL.Path {
		case "/v1/filesystem/65532/65532/files/info":
			if _, err := io.WriteString(w, `{"/storage/中文 &.txt":{"path":"/storage/中文 &.txt","type":"file","size":3}}`); err != nil {
				t.Errorf("write file info response: %v", err)
			}
		case "/v1/filesystem/65532/65532/files/download":
			if r.Header.Get("Range") != "bytes=0-2" {
				t.Errorf("range: %s", r.Header.Get("Range"))
			}
			w.WriteHeader(206)
			if _, err := w.Write([]byte{0, 255, 1}); err != nil {
				t.Errorf("write file download response: %v", err)
			}
		default:
			t.Errorf("unexpected or privileged endpoint: %s", r.URL.Path)
			w.WriteHeader(500)
		}
	}))
	defer server.Close()
	h := &Handle{downloadClient: fileStreamClient(opensandbox.ConnectionConfig{}, server.URL, "test-token", nil)}
	f, err := h.UserFiles()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	info, err := f.Stat(ctx, "/storage/中文 &.txt")
	if err != nil || info.Size != 3 || info.Kind != "file" {
		t.Fatalf("stat: %+v %v", info, err)
	}
	body, err := f.Open(ctx, info.Path, 3)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(body)
	if closeErr := body.Close(); closeErr != nil {
		t.Errorf("close file download: %v", closeErr)
	}
	if err != nil || string(data) != string([]byte{0, 255, 1}) {
		t.Fatalf("binary payload: %v %v", data, err)
	}
	_, err = f.Stat(ctx, "/missing")
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("missing: %v", err)
	}
	_, err = f.Stat(ctx, "/unsupported")
	if !errors.Is(err, ErrFileAPIUnavailable) {
		t.Fatalf("unsupported: %v", err)
	}
	if requests != 4 {
		t.Fatalf("unexpected fallback/retry: %d requests", requests)
	}
}
