package ws

import (
	"encoding/json"
	"testing"

	"github.com/ravenmk2/wispjar/internal/runtime"
	"github.com/ravenmk2/wispjar/internal/session"
)

func TestControlFlow(t *testing.T) {
	env := newTestEnv(t, func(d *Deps) {
		d.Store.(*fakeStore).projections = []runtime.ControlSession{
			{SessionID: "sess1", Title: "a", Running: true, AgentID: "ag1", Status: "active", LastActiveAt: 100},
			{SessionID: "sess2", Title: "b", AgentID: "ag2", Status: "archived", LastActiveAt: 90},
		}
	})
	c := env.dial(t, "u1", nil)
	writeClient(t, c, `{"type":"open","streamId":"s1","endpoint":"control","payload":{"orgId":"o1"}}`)

	f := readFrame(t, c)
	if f.Type != "item" || f.StreamID != "s1" {
		t.Fatalf("want baseline item frame, got %+v", f)
	}
	var v map[string]any
	if err := json.Unmarshal(f.Value, &v); err != nil {
		t.Fatal(err)
	}
	if v["type"] != "baseline" {
		t.Fatalf("want baseline value, got %s", f.Value)
	}
	sessions := v["sessions"].([]any)
	if len(sessions) != 2 {
		t.Fatalf("baseline sessions mismatch: %s", f.Value)
	}
	first := sessions[0].(map[string]any)
	if first["sessionId"] != "sess1" || first["running"] != true || first["status"] != "active" {
		t.Fatalf("baseline session projection mismatch: %s", f.Value)
	}

	env.bus.PublishControl("o1", runtime.ControlFrame{Type: "upsert", Session: &runtime.ControlSession{
		SessionID: "sess1", Title: "a2", Running: false, AgentID: "ag1",
		UsageTotal: session.Usage{InputTokens: 10}, LastActiveAt: 200, Status: "active",
	}})
	f = readFrame(t, c)
	_ = json.Unmarshal(f.Value, &v)
	if f.Type != "item" || v["type"] != "upsert" {
		t.Fatalf("want upsert frame, got %s", f.Value)
	}
	sess := v["session"].(map[string]any)
	if sess["sessionId"] != "sess1" || sess["title"] != "a2" || sess["running"] != false {
		t.Fatalf("upsert session mismatch: %s", f.Value)
	}

	env.bus.PublishControl("o1", runtime.ControlFrame{Type: "remove", Remove: "sess2", UserID: "u1"})
	f = readFrame(t, c)
	_ = json.Unmarshal(f.Value, &v)
	if v["type"] != "remove" || v["sessionId"] != "sess2" {
		t.Fatalf("want remove frame, got %s", f.Value)
	}

	env.bus.PublishControl("other-org", runtime.ControlFrame{Type: "remove", Remove: "x"})
	writeClient(t, c, `{"type":"end","streamId":"s1"}`)
	writeClient(t, c, `{"type":"open","streamId":"s2","endpoint":"control","payload":{"orgId":"o1"}}`)
	f = readFrame(t, c)
	_ = json.Unmarshal(f.Value, &v)
	if v["type"] != "baseline" {
		t.Fatalf("other-org frame must not leak; want fresh baseline, got %s", f.Value)
	}
}

func TestControlEmptyBaseline(t *testing.T) {
	env := newTestEnv(t, nil)
	c := env.dial(t, "u1", nil)
	writeClient(t, c, `{"type":"open","streamId":"s1","endpoint":"control","payload":{"orgId":"o1"}}`)
	f := readFrame(t, c)
	var v map[string]any
	if err := json.Unmarshal(f.Value, &v); err != nil {
		t.Fatal(err)
	}
	sessions, ok := v["sessions"].([]any)
	if !ok || len(sessions) != 0 {
		t.Fatalf("empty baseline must be [], got %s", f.Value)
	}
	computers, ok := v["computers"].([]any)
	if !ok || len(computers) != 0 {
		t.Fatalf("empty computers baseline must be [], got %s", f.Value)
	}
}

func TestControlPrivateComputersAndSessions(t *testing.T) {
	env := newTestEnv(t, func(d *Deps) {
		d.Store.(*fakeStore).computers = []any{map[string]any{"id": "mine", "status": "running"}}
	})
	c := env.dial(t, "u1", nil)
	writeClient(t, c, `{"type":"open","streamId":"s1","endpoint":"control","payload":{"orgId":"o1"}}`)
	f := readFrame(t, c)
	var baseline struct {
		Computers []map[string]any `json:"computers"`
	}
	if err := json.Unmarshal(f.Value, &baseline); err != nil || len(baseline.Computers) != 1 || baseline.Computers[0]["id"] != "mine" {
		t.Fatalf("computer baseline: %s / %v", f.Value, err)
	}
	env.bus.PublishControl("o1", runtime.ControlFrame{Type: "computer/upsert", UserID: "other", Computer: map[string]any{"id": "private"}})
	env.bus.PublishControl("o1", runtime.ControlFrame{Type: "computer/upsert", Computer: map[string]any{"id": "ownerless"}})
	env.bus.PublishControl("o1", runtime.ControlFrame{Type: "upsert", Session: &runtime.ControlSession{SessionID: "not-owned"}})
	env.bus.PublishControl("o1", runtime.ControlFrame{Type: "remove", UserID: "other", Remove: "foreign"})
	env.bus.PublishControl("o1", runtime.ControlFrame{Type: "computer/remove", UserID: "u1", ComputerID: "mine"})
	f = readFrame(t, c)
	var value map[string]any
	if err := json.Unmarshal(f.Value, &value); err != nil {
		t.Fatal(err)
	}
	if value["type"] != "computer/remove" || value["computerId"] != "mine" {
		t.Fatalf("private resource leaked before owner's update: %s", f.Value)
	}
	if _, exists := value["UserID"]; exists {
		t.Fatal("internal routing identity leaked into payload")
	}
}
