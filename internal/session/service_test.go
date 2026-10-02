package session

import (
	"testing"
	"time"
)

func TestCursorRoundTrip(t *testing.T) {
	at := time.UnixMilli(1735000000123)
	cursor := formatCursor(at, "0123456789abcdef0123456789abcdef")
	got, id, err := parseCursor(cursor)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Equal(at) || id != "0123456789abcdef0123456789abcdef" {
		t.Fatalf("round trip mismatch: %v %s", got, id)
	}
}

func TestParseCursorInvalid(t *testing.T) {
	for _, c := range []string{"", "abc", "123", "_id", "xyz_id"} {
		if _, _, err := parseCursor(c); err == nil {
			t.Fatalf("want error for cursor %q", c)
		}
	}
}

func TestLikePattern(t *testing.T) {
	if got := likePattern(`50%_off\`); got != `%50\%\_off\\%` {
		t.Fatalf("unexpected pattern: %q", got)
	}
	if got := likePattern("hello"); got != "%hello%" {
		t.Fatalf("unexpected pattern: %q", got)
	}
}
