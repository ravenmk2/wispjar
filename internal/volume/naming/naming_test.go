package naming

import (
	"strings"
	"testing"
)

func TestRenderNeverTruncatesOrInterpretsTemplates(t *testing.T) {
	id := strings.Repeat("a", 32)
	got, err := Render(DefaultPattern, "main", "sessions", id, id, id)
	if err != nil || got != "wj-main-sessions-"+id {
		t.Fatalf("%q %v", got, err)
	}
	for _, pattern := range []string{
		"{volume_id}-{unknown}", "{volume_id}{", "{volume_id}}",
		"${volume_id}", "{volume_id}/path", "-{volume_id}", "{volume_id}-",
		strings.Repeat("x", 32) + "{volume_id}",
	} {
		if _, err := Render(pattern, "main", "storage", id, id, id); err == nil {
			t.Errorf("accepted invalid pattern %q", pattern)
		}
	}
	got, err = Render(strings.Repeat("x", 31)+"{volume_id}", "main", "storage", id, id, id)
	if err != nil || len(got) != 63 {
		t.Fatalf("63-byte name rejected: %v", err)
	}
}
