package ws

import (
	"encoding/json"
	"testing"

	"github.com/ravenmk2/wispjar/internal/runtime"
	"github.com/ravenmk2/wispjar/internal/session"
)

func TestFollowFlow(t *testing.T) {
	env := newTestEnv(t, nil)
	env.orch.snapshot = &runtime.Snapshot{
		Header: runtime.SnapshotHeader{SessionID: "sess1", Title: "hello", LastSeq: 2, Running: true},
		Records: []session.Event{
			{Seq: 1, Type: "user/message", Time: 1, Data: json.RawMessage(`{"turn":1,"content":"hi"}`)},
			{Seq: 2, Type: "turn/start", Time: 2, Data: json.RawMessage(`{"turn":1}`)},
		},
		HasMore: true,
	}
	c := env.dial(t, "u1", nil)
	writeClient(t, c, `{"type":"open","streamId":"s1","endpoint":"session.follow","payload":{"orgId":"o1","sessionId":"sess1","afterSeq":42}}`)

	f := readFrame(t, c)
	if f.Type != "item" || f.StreamID != "s1" {
		t.Fatalf("want snapshot item frame, got %+v", f)
	}
	var v map[string]any
	if err := json.Unmarshal(f.Value, &v); err != nil {
		t.Fatal(err)
	}
	if v["type"] != "snapshot" {
		t.Fatalf("want snapshot value, got %s", f.Value)
	}
	header := v["header"].(map[string]any)
	if header["sessionId"] != "sess1" || header["title"] != "hello" || header["running"] != true {
		t.Fatalf("snapshot header mismatch: %s", f.Value)
	}
	if recs := v["records"].([]any); len(recs) != 2 {
		t.Fatalf("snapshot records mismatch: %s", f.Value)
	}
	if env.orch.lastAfterSeq() != 0 {
		t.Fatalf("records must always use tail window: Follow called with afterSeq=%d", env.orch.lastAfterSeq())
	}

	env.orch.frames <- runtime.EventFrame(session.Event{Seq: 3, Type: "session/title", Time: 3, Data: json.RawMessage(`{"title":"x","auto":true}`)})
	f = readFrame(t, c)
	if err := json.Unmarshal(f.Value, &v); err != nil {
		t.Fatal(err)
	}
	if f.Type != "item" || v["type"] != "event" || v["event"].(map[string]any)["seq"].(float64) != 3 {
		t.Fatalf("event frame passthrough mismatch: %s", f.Value)
	}

	env.orch.frames <- runtime.AssistantStreamFrame(runtime.StreamStart{Type: "start", AttemptID: "a1", Turn: 1, Step: 1, StartedAfterSeq: 2})
	f = readFrame(t, c)
	_ = json.Unmarshal(f.Value, &v)
	if v["type"] != "assistant-stream" || v["frame"].(map[string]any)["attemptId"] != "a1" {
		t.Fatalf("assistant-stream passthrough mismatch: %s", f.Value)
	}

	env.orch.frames <- runtime.ToolOutputLiveFrame("call_1", "stdout", "partial")
	f = readFrame(t, c)
	_ = json.Unmarshal(f.Value, &v)
	if v["type"] != "tool-output" || v["callId"] != "call_1" || v["delta"] != "partial" {
		t.Fatalf("tool-output passthrough mismatch: %s", f.Value)
	}

	writeClient(t, c, `{"type":"item","streamId":"s1","value":{"type":"approval-response","callId":"call_1","decision":"allow"}}`)
	waitFor(t, "DecideApproval", func() bool { return env.orch.decideCallCount() == 1 })
	d := env.orch.lastDecide()
	if d.sessionID != "sess1" || d.callID != "call_1" || !d.allow || d.by != "u1" {
		t.Fatalf("DecideApproval args mismatch: %+v", d)
	}

	writeClient(t, c, `{"type":"item","streamId":"s1","value":{"type":"approval-response","callId":"call_1","decision":"maybe"}}`)
	f = readFrame(t, c)
	if f.Type != "error" || f.Error == nil || f.Error.Code != "request/invalid" {
		t.Fatalf("bad decision: want request/invalid, got %+v", f)
	}
	if env.orch.decideCallCount() != 1 {
		t.Fatal("bad decision must not reach DecideApproval")
	}

	writeClient(t, c, `{"type":"item","streamId":"s1","value":{"type":"unknown"}}`)
	f = readFrame(t, c)
	if f.Type != "error" || f.Error == nil || f.Error.Code != "request/invalid" {
		t.Fatalf("unknown uplink: want request/invalid, got %+v", f)
	}

	writeClient(t, c, `{"type":"item","streamId":"s1","value":{"type":"approval-response","callId":"call_2","decision":"deny"}}`)
	waitFor(t, "DecideApproval after errors (stream must stay open)", func() bool {
		return env.orch.decideCallCount() == 2
	})
	if d := env.orch.lastDecide(); d.callID != "call_2" || d.allow {
		t.Fatalf("deny decision mismatch: %+v", d)
	}

	writeClient(t, c, `{"type":"end","streamId":"s1"}`)
	waitFor(t, "unsubscribe on end", func() bool { return env.orch.unsubs() == 1 })

	writeClient(t, c, `{"type":"open","streamId":"s2","endpoint":"session.follow","payload":{"orgId":"o1","sessionId":"sess1"}}`)
	if f := readFrame(t, c); f.Type != "item" {
		t.Fatalf("connection must survive stream end, got %+v", f)
	}
}

func TestFollowCancelClosesStream(t *testing.T) {
	env := newTestEnv(t, nil)
	c := env.dial(t, "u1", nil)
	writeClient(t, c, `{"type":"open","streamId":"s1","endpoint":"session.follow","payload":{"orgId":"o1","sessionId":"sess1"}}`)
	if f := readFrame(t, c); f.Type != "item" {
		t.Fatalf("want snapshot, got %+v", f)
	}
	writeClient(t, c, `{"type":"cancel","streamId":"s1"}`)
	waitFor(t, "unsubscribe on cancel", func() bool { return env.orch.unsubs() == 1 })
}

func TestFollowUpstreamChannelClose(t *testing.T) {
	env := newTestEnv(t, nil)
	c := env.dial(t, "u1", nil)
	writeClient(t, c, `{"type":"open","streamId":"s1","endpoint":"session.follow","payload":{"orgId":"o1","sessionId":"sess1"}}`)
	if f := readFrame(t, c); f.Type != "item" {
		t.Fatalf("want snapshot, got %+v", f)
	}
	close(env.orch.frames)
	if f := readFrame(t, c); f.Type != "end" || f.StreamID != "s1" {
		t.Fatalf("want end frame on upstream close, got %+v", f)
	}
}

func TestFollowDecideError(t *testing.T) {
	env := newTestEnv(t, func(d *Deps) {
		d.Orchestrator.(*fakeOrchestrator).decideErr = runtime.ErrApprovalNotPending
	})
	c := env.dial(t, "u1", nil)
	writeClient(t, c, `{"type":"open","streamId":"s1","endpoint":"session.follow","payload":{"orgId":"o1","sessionId":"sess1"}}`)
	if f := readFrame(t, c); f.Type != "item" {
		t.Fatalf("want snapshot, got %+v", f)
	}
	writeClient(t, c, `{"type":"item","streamId":"s1","value":{"type":"approval-response","callId":"call_1","decision":"allow"}}`)
	f := readFrame(t, c)
	if f.Type != "error" || f.Error == nil || f.Error.Code != "approval/not_pending" {
		t.Fatalf("want approval/not_pending error frame, got %+v", f)
	}
}
