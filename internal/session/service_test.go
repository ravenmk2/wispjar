package session

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/ravenmk2/wispjar/internal/store"
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

func TestPlanEffortUpdate(t *testing.T) {
	caps := capabilities{ReasoningEfforts: []string{"low", "medium", "high"}}

	t.Run("locked after conversation started", func(t *testing.T) {
		sess := &store.Session{LastSeq: 5, ModelParams: json.RawMessage(`{"reasoning_effort":"low"}`)}
		_, err := planEffortUpdate(sess, caps, "high")
		var se *Error
		if !errors.As(err, &se) {
			t.Fatalf("want *Error, got %v", err)
		}
		if se.Status != http.StatusConflict || se.Code != "session/effort_locked" {
			t.Fatalf("want 409 session/effort_locked, got %d %s", se.Status, se.Code)
		}
		details, ok := se.Details.(map[string]any)
		if !ok || details["reasoning_effort"] != "low" {
			t.Fatalf("details must carry current effort, got %v", se.Details)
		}
	})
	t.Run("value must be within model efforts", func(t *testing.T) {
		sess := &store.Session{}
		for _, bad := range []string{"max", "ultra"} {
			if _, err := planEffortUpdate(sess, caps, bad); err == nil {
				t.Fatalf("want 400 for %q", bad)
			} else {
				var se *Error
				if !errors.As(err, &se) || se.Status != http.StatusBadRequest {
					t.Fatalf("want 400 for %q, got %v", bad, err)
				}
			}
		}
	})
	t.Run("model without efforts rejects", func(t *testing.T) {
		sess := &store.Session{}
		if _, err := planEffortUpdate(sess, capabilities{}, "low"); err == nil {
			t.Fatal("want 400 when model declares no efforts")
		}
	})
	t.Run("success merges into existing model_params", func(t *testing.T) {
		sess := &store.Session{ModelParams: json.RawMessage(`{"temperature":0.5,"reasoning_effort":"low"}`)}
		raw, err := planEffortUpdate(sess, caps, "high")
		if err != nil {
			t.Fatal(err)
		}
		m := map[string]any{}
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatal(err)
		}
		if m["reasoning_effort"] != "high" || m["temperature"] != 0.5 {
			t.Fatalf("unexpected merged params: %v", m)
		}
	})
	t.Run("default removes effort and preserves other params", func(t *testing.T) {
		sess := &store.Session{ModelParams: json.RawMessage(`{"temperature":0.5,"reasoning_effort":"high"}`)}
		raw, err := planEffortUpdate(sess, caps, "")
		if err != nil {
			t.Fatal(err)
		}
		var params map[string]any
		if err := json.Unmarshal(raw, &params); err != nil {
			t.Fatal(err)
		}
		if _, exists := params["reasoning_effort"]; exists || params["temperature"] != 0.5 {
			t.Fatalf("reset must only remove effort: %s", raw)
		}
	})
	t.Run("default restores configured model effort", func(t *testing.T) {
		sess := &store.Session{ModelParams: json.RawMessage(`{"reasoning_effort":"high"}`)}
		withDefault := capabilities{ReasoningEfforts: caps.ReasoningEfforts, ReasoningEffortDefault: "medium"}
		raw, err := planEffortUpdate(sess, withDefault, "")
		if err != nil {
			t.Fatal(err)
		}
		if currentEffort(raw) != "medium" {
			t.Fatalf("want model default medium, got %s", raw)
		}
	})
	t.Run("reset empty params yields NULL", func(t *testing.T) {
		for _, params := range []json.RawMessage{nil, json.RawMessage(`null`), json.RawMessage(`{"reasoning_effort":"high"}`)} {
			raw, err := planEffortUpdate(&store.Session{ModelParams: params}, capabilities{}, "")
			if err != nil || raw != nil {
				t.Fatalf("want NULL params after reset, got %s, %v", raw, err)
			}
		}
	})
	t.Run("cannot reset locked effort", func(t *testing.T) {
		_, err := planEffortUpdate(&store.Session{LastSeq: 1}, caps, "")
		var se *Error
		if !errors.As(err, &se) || se.Code != "session/effort_locked" {
			t.Fatalf("want effort_locked for reset, got %v", err)
		}
	})
}

func TestDetailEffortLocked(t *testing.T) {
	if detailOf(&store.Session{LastSeq: 0}).EffortLocked {
		t.Fatal("last_seq=0 must not be locked")
	}
	if !detailOf(&store.Session{LastSeq: 3}).EffortLocked {
		t.Fatal("last_seq>0 must be locked")
	}
}
