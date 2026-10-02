package id

import (
	"strings"
	"testing"
)

func TestNewFormat(t *testing.T) {
	got := New()
	if len(got) != 32 {
		t.Fatalf("length = %d, want 32", len(got))
	}
	for _, r := range got {
		if !strings.ContainsRune("0123456789abcdef", r) {
			t.Fatalf("non-hex character %q in %q", r, got)
		}
	}
	if got[12] != '7' {
		t.Fatalf("version nibble = %c, want 7", got[12])
	}
}

func TestNewUnique(t *testing.T) {
	seen := make(map[string]struct{}, 1000)
	for i := 0; i < 1000; i++ {
		got := New()
		if _, ok := seen[got]; ok {
			t.Fatalf("duplicate id %q", got)
		}
		seen[got] = struct{}{}
	}
}
