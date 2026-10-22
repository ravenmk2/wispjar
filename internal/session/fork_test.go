package session

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/ravenmk2/wispjar/internal/store"
)

func assistantEvent(seq int64, u *Usage) store.SessionEvent {
	data, _ := json.Marshal(AssistantMessageData{Turn: 1, Step: int(seq), Usage: u})
	return store.SessionEvent{SessionID: "src", Seq: seq, Type: EventAssistantMessage, TimeMs: 1000 + seq, Data: data}
}

func TestPlanFork(t *testing.T) {
	src := &store.Session{
		ID:           "src",
		OrgID:        "org-1",
		UserID:       "user-1",
		AgentID:      "agent-1",
		Title:        "原始标题",
		TitlePinned:  true,
		ApprovalMode: ApprovalStrict,
		ModelID:      "model-1",
		ModelParams:  json.RawMessage(`{"reasoning_effort":"high"}`),
		Sandbox:      json.RawMessage(`{"id":"sb-1","status":"running"}`),
		LastSeq:      4,
		Running:      true,
		Status:       StatusActive,
	}
	events := []store.SessionEvent{
		{SessionID: "src", Seq: 1, Type: EventUserMessage, TimeMs: 1001, Data: json.RawMessage(`{"content":"hi"}`)},
		assistantEvent(2, &Usage{InputTokens: 10, OutputTokens: 5, CacheReadTokens: 2}),
		assistantEvent(3, &Usage{InputTokens: 20, OutputTokens: 7, ReasoningTokens: 3}),
		{SessionID: "src", Seq: 4, Type: EventTurnEnd, TimeMs: 1004, Data: json.RawMessage(`{}`)},
	}
	now := time.Now()
	forked, copied, err := planFork(src, events[:2], 2, "fork-1", now, nil)
	if err != nil {
		t.Fatal(err)
	}
	if forked.ID != "fork-1" || forked.OrgID != "org-1" || forked.UserID != "user-1" || forked.AgentID != "agent-1" {
		t.Fatalf("unexpected identity %+v", forked)
	}
	if forked.Title != "原始标题（副本）" || !forked.TitlePinned {
		t.Fatalf("unexpected title %q pinned=%v", forked.Title, forked.TitlePinned)
	}
	if forked.ApprovalMode != ApprovalStrict || forked.ModelID != "model-1" {
		t.Fatalf("unexpected mode/model %+v", forked)
	}
	if string(forked.ModelParams) != `{"reasoning_effort":"high"}` {
		t.Fatalf("model_params must be copied, got %s", forked.ModelParams)
	}
	if string(forked.Sandbox) != string(defaultSandbox) {
		t.Fatalf("sandbox must be fresh none, got %s", forked.Sandbox)
	}
	if forked.Running || forked.Status != StatusActive || forked.LastSeq != 2 {
		t.Fatalf("unexpected state running=%v status=%s last_seq=%d", forked.Running, forked.Status, forked.LastSeq)
	}
	var u Usage
	if err := json.Unmarshal(forked.UsageTotal, &u); err != nil {
		t.Fatal(err)
	}
	if u.InputTokens != 10 || u.OutputTokens != 5 || u.CacheReadTokens != 2 || u.ReasoningTokens != 0 {
		t.Fatalf("unexpected usage_total %+v", u)
	}
	if len(copied) != 2 {
		t.Fatalf("want 2 copied events, got %d", len(copied))
	}
	for _, ev := range copied {
		if ev.SessionID != "fork-1" {
			t.Fatalf("copied event not rebound: %+v", ev)
		}
	}
	if copied[1].TimeMs != events[1].TimeMs {
		t.Fatal("time_ms must be preserved")
	}
}

func TestPlanForkUsageRecompute(t *testing.T) {
	src := &store.Session{ID: "src", LastSeq: 3, Status: StatusActive}
	events := []store.SessionEvent{
		assistantEvent(1, &Usage{InputTokens: 10, OutputTokens: 5}),
		{SessionID: "src", Seq: 2, Type: EventToolCall, TimeMs: 1002, Data: json.RawMessage(`{"usage":{"input_tokens":999}}`)},
		assistantEvent(3, nil),
	}
	forked, _, err := planFork(src, events, 3, "fork-1", time.Now(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var u Usage
	if err := json.Unmarshal(forked.UsageTotal, &u); err != nil {
		t.Fatal(err)
	}
	if u.InputTokens != 10 || u.OutputTokens != 5 {
		t.Fatalf("usage_total must sum assistant/message only, got %+v", u)
	}
}

func TestPlanForkInvalidSeq(t *testing.T) {
	src := &store.Session{ID: "src", LastSeq: 3}
	for _, atSeq := range []int64{0, -1, 4} {
		if _, _, err := planFork(src, nil, atSeq, "fork-1", time.Now(), nil); err == nil {
			t.Fatalf("want error for at_seq=%d", atSeq)
		}
	}
}

func TestPlanForkCustomTitle(t *testing.T) {
	src := &store.Session{ID: "src", Title: "原始标题", LastSeq: 1}
	for _, tc := range []struct {
		name    string
		title   string
		want    string
		pinned  bool
		invalid bool
	}{
		{name: "custom title is trimmed and pinned", title: "  新的方向  ", want: "新的方向", pinned: true},
		{name: "blank uses default", title: " \t ", want: "原始标题（副本）"},
		{name: "unicode limit", title: strings.Repeat("题", 256), want: strings.Repeat("题", 256), pinned: true},
		{name: "over limit", title: strings.Repeat("题", 257), invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			forked, _, err := planFork(src, nil, 1, "new", time.Now(), &tc.title)
			if tc.invalid {
				if err == nil {
					t.Fatal("want invalid title error")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if forked.Title != tc.want || forked.TitlePinned != tc.pinned {
				t.Fatalf("title=%q pinned=%v", forked.Title, forked.TitlePinned)
			}
			if src.Title != "原始标题" || src.TitlePinned {
				t.Fatal("source session was changed")
			}
		})
	}
}

func TestForkTitle(t *testing.T) {
	if got := forkTitle("abc"); got != "abc（副本）" {
		t.Fatalf("unexpected %q", got)
	}
	if got := forkTitle(""); got != "（副本）" {
		t.Fatalf("unexpected %q", got)
	}
	long := strings.Repeat("题", 300)
	got := forkTitle(long)
	if n := len([]rune(got)); n != 256 {
		t.Fatalf("want 256 runes, got %d", n)
	}
	if !strings.HasSuffix(got, "（副本）") {
		t.Fatal("suffix must be preserved after truncation")
	}
}
