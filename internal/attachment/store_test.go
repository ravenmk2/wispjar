package attachment

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBlobStoreRoundTrip(t *testing.T) {
	s := NewBlobStore(t.TempDir())
	data := []byte("hello attachment")
	hexSum, err := s.Put(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(hexSum) != 64 {
		t.Fatalf("want 64 hex chars, got %q", hexSum)
	}
	got, err := s.Get(hexSum)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(data) {
		t.Fatalf("round trip mismatch: %q", got)
	}
	// 分片目录：<root>/<hex[:2]>/<hex>
	if _, err := os.Stat(filepath.Join(s.root, hexSum[:2], hexSum)); err != nil {
		t.Fatalf("blob not at expected sharded path: %v", err)
	}
}

func TestBlobStoreDedup(t *testing.T) {
	root := t.TempDir()
	s := NewBlobStore(root)
	data := []byte("dedup me")
	h1, err := s.Put(data)
	if err != nil {
		t.Fatal(err)
	}
	h2, err := s.Put(data)
	if err != nil {
		t.Fatal(err)
	}
	if h1 != h2 {
		t.Fatalf("same content must map to same id: %s vs %s", h1, h2)
	}
	count := 0
	_ = filepath.Walk(root, func(_ string, info os.FileInfo, _ error) error {
		if info != nil && info.Mode().IsRegular() {
			count++
		}
		return nil
	})
	if count != 1 {
		t.Fatalf("want exactly 1 blob file after dedup, got %d", count)
	}
}

func TestBlobStoreTamperDetect(t *testing.T) {
	root := t.TempDir()
	s := NewBlobStore(root)
	hexSum, err := s.Put([]byte("original bytes"))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, hexSum[:2], hexSum)
	if err := os.WriteFile(path, []byte("tampered bytes!"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(hexSum); err == nil {
		t.Fatal("want corruption error for tampered blob")
	}
}

func TestBlobStoreMissing(t *testing.T) {
	s := NewBlobStore(t.TempDir())
	if _, err := s.Get("0000000000000000000000000000000000000000000000000000000000000000"); err == nil {
		t.Fatal("want error for missing blob")
	}
}
