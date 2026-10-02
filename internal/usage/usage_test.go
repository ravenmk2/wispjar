package usage

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/ravenmk2/wispjar/internal/session"
	"github.com/ravenmk2/wispjar/internal/store"
)

func TestDeltaFromEvent(t *testing.T) {
	data, _ := json.Marshal(session.AssistantMessageData{
		Turn: 1, Step: 1,
		Usage: &session.Usage{InputTokens: 100, OutputTokens: 50, CacheReadTokens: 20, CacheWriteTokens: 5, ReasoningTokens: 10},
	})
	ev := store.SessionEvent{
		SessionID: "s1", Seq: 3, Type: session.EventAssistantMessage,
		TimeMs: time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC).UnixMilli(),
		Data:   data,
	}
	d, ok := deltaFromEvent(ev)
	if !ok {
		t.Fatal("want delta")
	}
	if got := d.date.Format("2006-01-02"); got != "2025-06-01" {
		t.Fatalf("unexpected date %s", got)
	}
	if d.usage.InputTokens != 100 || d.usage.OutputTokens != 50 || d.usage.CacheReadTokens != 20 ||
		d.usage.CacheWriteTokens != 5 || d.usage.ReasoningTokens != 10 {
		t.Fatalf("unexpected usage %+v", d.usage)
	}
}

func TestDeltaFromEventDateBoundaryUTC8(t *testing.T) {
	data, _ := json.Marshal(session.AssistantMessageData{Usage: &session.Usage{InputTokens: 1}})
	ev := store.SessionEvent{
		Type:   session.EventAssistantMessage,
		TimeMs: time.Date(2025, 6, 1, 16, 30, 0, 0, time.UTC).UnixMilli(), // 2025-06-02 00:30 +08:00
		Data:   data,
	}
	d, ok := deltaFromEvent(ev)
	if !ok {
		t.Fatal("want delta")
	}
	if got := d.date.Format("2006-01-02"); got != "2025-06-02" {
		t.Fatalf("want 2025-06-02 in +08:00, got %s", got)
	}
	if got := d.date.Location().String(); got != "UTC+8" {
		t.Fatalf("unexpected location %s", got)
	}
}

func TestDeltaFromEventSkipped(t *testing.T) {
	noUsage, _ := json.Marshal(session.AssistantMessageData{Turn: 1, Step: 1})
	cases := []store.SessionEvent{
		{Type: session.EventToolCall, Data: noUsage},
		{Type: session.EventAssistantMessage, Data: noUsage},
		{Type: session.EventAssistantMessage, Data: json.RawMessage(`{"usage":null}`)},
		{Type: session.EventAssistantMessage, Data: json.RawMessage(`not-json`)},
		{Type: session.EventAssistantAttempt, Data: json.RawMessage(`{"usage":{"input_tokens":1}}`)},
	}
	for i, ev := range cases {
		if _, ok := deltaFromEvent(ev); ok {
			t.Fatalf("case %d: want skip", i)
		}
	}
}

func TestGroupKeyExpr(t *testing.T) {
	want := map[string]string{
		GroupByOrg:   "org_id",
		GroupByUser:  "user_id",
		GroupByAgent: "agent_id",
		GroupByModel: "model_id",
		GroupByDay:   "DATE_FORMAT(date, '%Y-%m-%d')",
	}
	for gb, expr := range want {
		got, err := groupKeyExpr(gb)
		if err != nil || got != expr {
			t.Fatalf("group_by %s: got %q err %v", gb, got, err)
		}
	}
	if _, err := groupKeyExpr("hour"); err == nil {
		t.Fatal("want error for unknown group_by")
	}
}

func TestParseReportQuery(t *testing.T) {
	q, err := parseReportQuery("2025-06-01", "2025-06-30", "user", "org-1", false)
	if err != nil {
		t.Fatal(err)
	}
	if q.from != "2025-06-01" || q.to != "2025-06-30" || q.groupBy != "user" || q.orgID != "org-1" || q.platform {
		t.Fatalf("unexpected query %+v", q)
	}
	if _, err := parseReportQuery("2025-06-01", "2025-06-30", "org", "", true); err != nil {
		t.Fatalf("platform group_by=org should be allowed: %v", err)
	}

	invalid := [][5]string{
		{"", "2025-06-30", "user", "org-1", "false"},
		{"2025-06-01", "", "user", "org-1", "false"},
		{"2025/06/01", "2025-06-30", "user", "org-1", "false"},
		{"2025-06-01", "20250630", "user", "org-1", "false"},
		{"2025-06-30", "2025-06-01", "user", "org-1", "false"},
		{"2025-06-01", "2025-06-30", "hour", "org-1", "false"},
	}
	for _, c := range invalid {
		if _, err := parseReportQuery(c[0], c[1], c[2], c[3], c[4] == "true"); err == nil {
			t.Fatalf("want error for %+v", c)
		}
	}
	if _, err := parseReportQuery("2025-06-01", "2025-06-30", "org", "org-1", false); err == nil {
		t.Fatal("org-scope group_by=org should be rejected")
	}
}
