package ws

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/ravenmk2/wispjar/internal/runtime"
	"github.com/ravenmk2/wispjar/internal/session"
)

func TestClientFrameDecode(t *testing.T) {
	var open clientFrame
	if err := json.Unmarshal([]byte(`{"type":"open","streamId":"s1","endpoint":"session.follow","payload":{"orgId":"o1","sessionId":"sess1","afterSeq":0}}`), &open); err != nil {
		t.Fatal(err)
	}
	if open.Type != "open" || open.StreamID != "s1" || open.Endpoint != "session.follow" {
		t.Fatalf("open frame mismatch: %+v", open)
	}
	var p followOpenPayload
	if err := json.Unmarshal(open.Payload, &p); err != nil || p.OrgID != "o1" || p.SessionID != "sess1" {
		t.Fatalf("payload mismatch: %+v err=%v", p, err)
	}

	var item clientFrame
	if err := json.Unmarshal([]byte(`{"type":"item","streamId":"s1","value":{"type":"approval-response","callId":"c1","decision":"allow"}}`), &item); err != nil {
		t.Fatal(err)
	}
	var up approvalResponseUplink
	if err := json.Unmarshal(item.Value, &up); err != nil || up.CallID != "c1" || up.Decision != "allow" {
		t.Fatalf("uplink value mismatch: %+v err=%v", up, err)
	}

	for _, typ := range []string{"end", "cancel"} {
		var f clientFrame
		if err := json.Unmarshal([]byte(`{"type":"`+typ+`","streamId":"s9"}`), &f); err != nil {
			t.Fatal(err)
		}
		if f.Type != typ || f.StreamID != "s9" {
			t.Fatalf("%s frame mismatch: %+v", typ, f)
		}
	}
}

func TestServerFrameEncode(t *testing.T) {
	raw, err := json.Marshal(itemFrame("s1", map[string]any{"type": "event"}))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if m["type"] != "item" || m["streamId"] != "s1" || m["value"].(map[string]any)["type"] != "event" {
		t.Fatalf("item frame mismatch: %s", raw)
	}
	if _, ok := m["error"]; ok {
		t.Fatalf("item frame must not carry error: %s", raw)
	}

	raw, _ = json.Marshal(errorFrame("s1", "stream/not_found", "未知 endpoint", nil))
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	e := m["error"].(map[string]any)
	if m["type"] != "error" || e["code"] != "stream/not_found" || e["message"] == "" {
		t.Fatalf("error frame mismatch: %s", raw)
	}

	raw, _ = json.Marshal(endFrame("s1"))
	if string(raw) != `{"type":"end","streamId":"s1"}` {
		t.Fatalf("end frame mismatch: %s", raw)
	}
}

func TestSnapshotValueEncode(t *testing.T) {
	snap := &runtime.Snapshot{
		Header:  runtime.SnapshotHeader{SessionID: "sess1", Title: "t", LastSeq: 7},
		Records: []session.Event{{Seq: 7, Type: "turn/start", Time: 1, Data: json.RawMessage(`{"turn":1}`)}},
		HasMore: true,
	}
	raw, err := json.Marshal(itemFrame("s1", snapshotValue{Type: "snapshot", Snapshot: snap}))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	v := m["value"].(map[string]any)
	if v["type"] != "snapshot" {
		t.Fatalf("snapshot discriminator missing: %s", raw)
	}
	if v["header"].(map[string]any)["sessionId"] != "sess1" || v["hasMore"] != true {
		t.Fatalf("snapshot fields mismatch: %s", raw)
	}
	if recs := v["records"].([]any); len(recs) != 1 || recs[0].(map[string]any)["seq"].(float64) != 7 {
		t.Fatalf("snapshot records mismatch: %s", raw)
	}
	if _, ok := v["assistantStream"]; ok {
		t.Fatalf("assistantStream must be omitted when nil: %s", raw)
	}
}

func TestStreamErrCode(t *testing.T) {
	code, _ := streamErrCode(runtime.ErrApprovalNotPending)
	if code != "approval/not_pending" {
		t.Fatalf("runtime error mapping mismatch: %s", code)
	}
	code, _ = streamErrCode(&session.Error{Code: "session/not_found", Message: "x"})
	if code != "session/not_found" {
		t.Fatalf("session error mapping mismatch: %s", code)
	}
	code, _ = streamErrCode(errTest)
	if code != "internal" {
		t.Fatalf("generic error mapping mismatch: %s", code)
	}
}

func TestFollowFrameValuePassthrough(t *testing.T) {
	ev := runtime.EventFrame(session.Event{Seq: 3, Type: "session/title", Time: 9, Data: json.RawMessage(`{"title":"x"}`)})
	raw, err := json.Marshal(itemFrame("s1", ev.MarshalValue()))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"type":"event"`) || !strings.Contains(string(raw), `"seq":3`) {
		t.Fatalf("event frame passthrough mismatch: %s", raw)
	}

	as := runtime.AssistantStreamFrame(runtime.StreamChunkFrame{Type: "chunk", AttemptID: "a1", Index: 2, Chunk: runtime.StreamChunk{Type: "text-delta", Text: "hi"}})
	raw, _ = json.Marshal(itemFrame("s1", as.MarshalValue()))
	if !strings.Contains(string(raw), `"type":"assistant-stream"`) || !strings.Contains(string(raw), `"attemptId":"a1"`) {
		t.Fatalf("assistant-stream passthrough mismatch: %s", raw)
	}

	to := runtime.ToolOutputLiveFrame("c1", "stdout", "chunk")
	raw, _ = json.Marshal(itemFrame("s1", to.MarshalValue()))
	if !strings.Contains(string(raw), `"type":"tool-output"`) || !strings.Contains(string(raw), `"callId":"c1"`) {
		t.Fatalf("tool-output passthrough mismatch: %s", raw)
	}
}
