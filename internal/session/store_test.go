package session

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"
)

func TestMarshalEventData(t *testing.T) {
	raw, err := marshalEventData(nil)
	if err != nil || string(raw) != `{}` {
		t.Fatalf("nil data: want {}, got %q err=%v", raw, err)
	}
	raw, err = marshalEventData(json.RawMessage(`{"turn":1}`))
	if err != nil || string(raw) != `{"turn":1}` {
		t.Fatalf("raw passthrough: got %q err=%v", raw, err)
	}
	raw, err = marshalEventData(TurnStartData{Turn: 2, RequestID: "r"})
	if err != nil || string(raw) != `{"turn":2,"request_id":"r"}` {
		t.Fatalf("struct marshal: got %q err=%v", raw, err)
	}
	if _, err = marshalEventData(json.RawMessage(`{bad`)); err == nil {
		t.Fatal("want error for invalid JSON raw message")
	}
}

func TestRunningAfter(t *testing.T) {
	if !runningAfter(EventTurnStart, false) {
		t.Fatal("turn/start must set running")
	}
	if runningAfter(EventTurnEnd, true) {
		t.Fatal("turn/end must clear running")
	}
	if !runningAfter(EventAssistantMessage, true) || runningAfter(EventAssistantMessage, false) {
		t.Fatal("other events must keep running unchanged")
	}
}

func TestUsageFromEventData(t *testing.T) {
	u, ok := usageFromEventData(json.RawMessage(`{"usage":{"input_tokens":3,"output_tokens":4}}`))
	if !ok || u.InputTokens != 3 || u.OutputTokens != 4 {
		t.Fatalf("want usage parsed, got %+v ok=%v", u, ok)
	}
	if _, ok = usageFromEventData(json.RawMessage(`{"turn":1}`)); ok {
		t.Fatal("want ok=false when usage absent")
	}
	if _, ok = usageFromEventData(json.RawMessage(`{bad`)); ok {
		t.Fatal("want ok=false for invalid JSON")
	}
}

func TestAddUsage(t *testing.T) {
	total := addUsage(nil, Usage{InputTokens: 10, OutputTokens: 5})
	total = addUsage(total, Usage{InputTokens: 2, CacheReadTokens: 7, ReasoningTokens: 1})
	var u Usage
	if err := json.Unmarshal(total, &u); err != nil {
		t.Fatal(err)
	}
	if u.InputTokens != 12 || u.OutputTokens != 5 || u.CacheReadTokens != 7 || u.ReasoningTokens != 1 {
		t.Fatalf("usage accumulation mismatch: %+v", u)
	}
}

func TestFirstPromptSequenceGuard(t *testing.T) {
	expected := int64(4)
	inputs := []EventInput{{Type: EventUserMessage, ExpectedLastSeq: &expected}, {Type: EventTurnStart}}
	if err := validateEventSequence(4, inputs); err != nil {
		t.Fatal(err)
	}
	err := validateEventSequence(5, inputs)
	var se *Error
	if !errors.As(err, &se) || se.Status != http.StatusConflict || se.Code != "session/config_changed" {
		t.Fatalf("concurrent PATCH must reject stale first prompt: %v", err)
	}
	if err := validateEventSequence(5, []EventInput{{Type: EventToolCall}}); err != nil {
		t.Fatal("ordinary event append must remain unaffected")
	}
}
