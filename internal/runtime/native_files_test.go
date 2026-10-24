package runtime

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/ravenmk2/wispjar/internal/sandbox"
)

type nativeReaderFixture struct {
	kind, data string
	size       int64
	opened     bool
	limit      int64
}

func (f *nativeReaderFixture) List(context.Context, string) ([]sandbox.FileEntry, error) {
	return []sandbox.FileEntry{{Path: "/storage/sub/你好.txt", Kind: "file"}, {Path: "/storage/sub/assets", Kind: "directory"}}, nil
}
func (f *nativeReaderFixture) Stat(context.Context, string) (sandbox.FileEntry, error) {
	return sandbox.FileEntry{Kind: f.kind, Size: f.size}, nil
}
func (f *nativeReaderFixture) Open(_ context.Context, _ string, limit int64) (io.ReadCloser, error) {
	f.opened = true
	f.limit = limit
	return io.NopCloser(strings.NewReader(f.data)), nil
}

func TestNativeReadBoundsAndMetadata(t *testing.T) {
	for _, tc := range []struct {
		name, kind, data           string
		size                       int64
		op                         string
		want                       string
		truncated, opened, wantErr bool
	}{
		{"binary", "file", "a\x00\xffb", 4, "read", "a\x00\xff", true, true, false},
		{"exact", "file", "abc", 3, "read", "abc", false, true, false},
		{"empty", "file", "", 0, "read", "", false, false, false},
		{"growth", "file", "abcdef", 1, "read", "abc", true, true, false},
		{"metadata", "file", "abcdef", 6, "stat", "", false, false, false},
		{"directory", "directory", "", 0, "read", "", false, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &nativeReaderFixture{kind: tc.kind, data: tc.data, size: tc.size}
			result, err := nativeFileOperation(context.Background(), f, "/storage", "x", tc.op, map[string]any{"max_bytes": 3})
			if (err != nil) != tc.wantErr || string(result.Content) != tc.want || result.Truncated != tc.truncated || f.opened != tc.opened {
				t.Fatalf("result=%+v opened=%v err=%v", result, f.opened, err)
			}
			if f.opened && f.limit != 4 {
				t.Fatalf("unbounded read: %d", f.limit)
			}
		})
	}
}

func TestNativeListRelativePaths(t *testing.T) {
	result, err := nativeFileOperation(context.Background(), &nativeReaderFixture{}, "/storage", "sub", "list", nil)
	if err != nil || len(result.Items) != 2 || result.Items[0]["path"] != "sub/assets" || result.Items[0]["type"] != "dir" || result.Items[1]["name"] != "你好.txt" {
		t.Fatalf("list=%+v err=%v", result, err)
	}
}

type readOnlyInstance struct{ files sandbox.FileReader }

func (readOnlyInstance) Ref() sandbox.InstanceRef             { return sandbox.InstanceRef{InstanceID: "readonly"} }
func (i readOnlyInstance) Files() (sandbox.FileReader, error) { return i.files, nil }

func TestFileReadNeedsNoMaintenanceOrCommandHandle(t *testing.T) {
	f := &nativeReaderFixture{kind: "file", data: "hello", size: 5}
	// No Handler, maintenance provider or execution tracker is available.
	access := &fileAccess{instance: readOnlyInstance{files: f}}
	got, err := access.operate(context.Background(), "/storage", "x", "read", map[string]any{"max_bytes": 10})
	if err != nil || string(got.Content) != "hello" || access.handle != nil || access.execution != nil {
		t.Fatalf("unexpected privileged access: %+v %v", got, err)
	}
}
