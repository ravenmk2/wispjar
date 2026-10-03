package session

import (
	"encoding/json"
	"testing"

	"github.com/ravenmk2/wispjar/internal/store"
)

func ev(seq int64, typ string, data any) store.SessionEvent {
	raw, err := json.Marshal(data)
	if err != nil {
		panic(err)
	}
	return store.SessionEvent{SessionID: "s1", Seq: seq, Type: typ, TimeMs: seq * 1000, Data: raw}
}

func assistantMsg(turn, step int, blocks ...ContentBlock) AssistantMessageData {
	return AssistantMessageData{
		Turn:    turn,
		Step:    step,
		Message: AssistantMessage{Role: RoleAssistant, Content: blocks},
	}
}

func TestDeriveNormalSequence(t *testing.T) {
	events := []store.SessionEvent{
		ev(1, EventTurnStart, TurnStartData{Turn: 1, RequestID: "r1"}),
		ev(2, EventUserMessage, UserMessageData{Turn: 1, Content: "你好", RequestID: "r1"}),
		ev(3, EventAssistantMessage, assistantMsg(1, 1, ContentBlock{Type: "text", Text: "你好，有什么可以帮你？"})),
		ev(4, EventTurnEnd, TurnEndData{Turn: 1, Reason: "completed"}),
		ev(5, EventTurnStart, TurnStartData{Turn: 2, RequestID: "r2"}),
		ev(6, EventUserMessage, UserMessageData{Turn: 2, Content: "列一下目录", RequestID: "r2"}),
		ev(7, EventAssistantMessage, assistantMsg(2, 1,
			ContentBlock{Type: "text", Text: "好的"},
			ContentBlock{Type: "tool-call", ID: "call_1", Name: "exec", Arguments: `{"command":"ls"}`},
		)),
		ev(8, EventToolCall, ToolCallData{Turn: 2, Step: 1, CallID: "call_1", Name: "exec", Arguments: `{"command":"ls"}`}),
		ev(9, EventToolResult, ToolResultData{Turn: 2, Step: 1, CallID: "call_1", Name: "exec", Content: "file.txt"}),
		ev(10, EventAssistantMessage, assistantMsg(2, 2, ContentBlock{Type: "text", Text: "目录里有 file.txt"})),
		ev(11, EventTurnEnd, TurnEndData{Turn: 2, Reason: "completed"}),
		ev(12, EventTurnStart, TurnStartData{Turn: 3, RequestID: "r3"}),
		ev(13, EventUserMessage, UserMessageData{Turn: 3, Content: "继续", RequestID: "r3"}),
	}
	msgs := DeriveMessages(events)
	if len(msgs) != 6 {
		t.Fatalf("want 6 messages, got %d: %+v", len(msgs), msgs)
	}
	wantRoles := []string{RoleUser, RoleAssistant, RoleUser, RoleAssistant, RoleTool, RoleAssistant}
	for i, role := range wantRoles {
		if msgs[i].Role != role {
			t.Fatalf("msg %d: want role %s, got %s", i, role, msgs[i].Role)
		}
	}
	if msgs[0].Content != "你好" || msgs[1].Content != "你好，有什么可以帮你？" {
		t.Fatalf("turn 1 content mismatch: %+v", msgs[:2])
	}
	if msgs[3].Content != "好的" || len(msgs[3].ToolCalls) != 1 || msgs[3].ToolCalls[0].ID != "call_1" {
		t.Fatalf("assistant tool-call expand mismatch: %+v", msgs[3])
	}
	if msgs[4].ToolCallID != "call_1" || msgs[4].ToolName != "exec" || msgs[4].Content != "file.txt" {
		t.Fatalf("tool result message mismatch: %+v", msgs[4])
	}
	if msgs[5].Content != "目录里有 file.txt" {
		t.Fatalf("final assistant message mismatch: %+v", msgs[5])
	}
}

func TestDeriveInterruptedDanglingCall(t *testing.T) {
	events := []store.SessionEvent{
		ev(1, EventTurnStart, TurnStartData{Turn: 1, RequestID: "r1"}),
		ev(2, EventUserMessage, UserMessageData{Turn: 1, Content: "跑两个命令", RequestID: "r1"}),
		ev(3, EventAssistantMessage, assistantMsg(1, 1,
			ContentBlock{Type: "tool-call", ID: "call_a", Name: "exec", Arguments: `{"command":"ls"}`},
			ContentBlock{Type: "tool-call", ID: "call_b", Name: "read", Arguments: `{"path":"a.txt"}`},
		)),
		ev(4, EventToolCall, ToolCallData{Turn: 1, Step: 1, CallID: "call_a", Name: "exec", Arguments: `{"command":"ls"}`}),
		ev(5, EventToolResult, ToolResultData{Turn: 1, Step: 1, CallID: "call_a", Name: "exec", Content: "ok"}),
		ev(6, EventToolCall, ToolCallData{Turn: 1, Step: 1, CallID: "call_b", Name: "read", Arguments: `{"path":"a.txt"}`}),
		ev(7, EventTurnEnd, TurnEndData{Turn: 1, Reason: "interrupted"}),
		ev(8, EventTurnStart, TurnStartData{Turn: 2, RequestID: "r2"}),
		ev(9, EventUserMessage, UserMessageData{Turn: 2, Content: "再来", RequestID: "r2"}),
	}
	msgs := DeriveMessages(events)
	if len(msgs) != 4 {
		t.Fatalf("want 4 messages, got %d: %+v", len(msgs), msgs)
	}
	if msgs[2].ToolCallID != "call_a" || msgs[2].Content != "ok" {
		t.Fatalf("real tool result mismatch: %+v", msgs[2])
	}
	synth := msgs[3]
	if synth.Role != RoleTool || synth.ToolCallID != "call_b" || synth.Content != interruptedResult {
		t.Fatalf("want synthetic interrupted result for call_b, got %+v", synth)
	}
	if synth.ToolName != "read" {
		t.Fatalf("synthetic result name should come from paired tool/call, got %q", synth.ToolName)
	}
}

func TestDeriveNestedEventsFiltered(t *testing.T) {
	events := []store.SessionEvent{
		ev(1, EventTurnStart, TurnStartData{Turn: 1, RequestID: "r1"}),
		ev(2, EventUserMessage, UserMessageData{Turn: 1, Content: "委派子任务", RequestID: "r1"}),
		ev(3, EventAssistantMessage, assistantMsg(1, 1,
			ContentBlock{Type: "tool-call", ID: "call_1", Name: "task", Arguments: `{"sub_agent_id":"sub1"}`},
		)),
		ev(4, EventToolCall, ToolCallData{Turn: 1, Step: 1, CallID: "call_1", Name: "task", Arguments: `{"sub_agent_id":"sub1"}`}),
		ev(5, EventToolCall, ToolCallData{Turn: 1, Step: 1, CallID: "call_sub", Name: "exec", Arguments: `{"command":"ls"}`, ParentCallID: "call_1"}),
		ev(6, EventToolResult, ToolResultData{Turn: 1, Step: 1, CallID: "call_sub", Name: "exec", Content: "nested output", ParentCallID: "call_1"}),
		ev(7, EventToolResult, ToolResultData{Turn: 1, Step: 1, CallID: "call_1", Name: "task", Content: "子任务完成"}),
		ev(8, EventTurnEnd, TurnEndData{Turn: 1, Reason: "completed"}),
		ev(9, EventTurnStart, TurnStartData{Turn: 2, RequestID: "r2"}),
	}
	msgs := DeriveMessages(events)
	if len(msgs) != 3 {
		t.Fatalf("want 3 messages, got %d: %+v", len(msgs), msgs)
	}
	for _, m := range msgs {
		if m.ToolCallID == "call_sub" {
			t.Fatalf("nested tool result leaked into main flow: %+v", m)
		}
	}
	if msgs[2].ToolCallID != "call_1" || msgs[2].Content != "子任务完成" {
		t.Fatalf("main task result mismatch: %+v", msgs[2])
	}
}

func TestDeriveAttemptExcluded(t *testing.T) {
	events := []store.SessionEvent{
		ev(1, EventTurnStart, TurnStartData{Turn: 1, RequestID: "r1"}),
		ev(2, EventUserMessage, UserMessageData{Turn: 1, Content: "hi", RequestID: "r1"}),
		ev(3, EventStepStart, StepData{Turn: 1, Step: 1}),
		ev(4, EventAssistantAttempt, AssistantAttemptData{Turn: 1, Step: 1, Chunks: json.RawMessage(`[{"type":"text-delta","index":0,"text":"半截"}]`), Error: "upstream timeout"}),
		ev(5, EventTurnEnd, TurnEndData{Turn: 1, Reason: "failed", Error: "upstream timeout"}),
		ev(6, EventTurnStart, TurnStartData{Turn: 2, RequestID: "r2"}),
		ev(7, EventUserMessage, UserMessageData{Turn: 2, Content: "重试", RequestID: "r2"}),
	}
	msgs := DeriveMessages(events)
	if len(msgs) != 1 || msgs[0].Role != RoleUser || msgs[0].Content != "hi" {
		t.Fatalf("attempt must not enter model history, got %+v", msgs)
	}
}

func TestDeriveCompactionSummary(t *testing.T) {
	events := []store.SessionEvent{
		ev(1, EventTurnStart, TurnStartData{Turn: 1, RequestID: "r1"}),
		ev(2, EventUserMessage, UserMessageData{Turn: 1, Content: "旧问题", RequestID: "r1"}),
		ev(3, EventAssistantMessage, assistantMsg(1, 1, ContentBlock{Type: "text", Text: "旧回答"})),
		ev(4, EventTurnEnd, TurnEndData{Turn: 1, Reason: "completed"}),
		ev(5, EventCompactionSummary, CompactionSummaryData{Covers: SeqRange{StartSeq: 2, EndSeq: 3}, Summary: "此前讨论了旧问题"}),
		ev(6, EventTurnStart, TurnStartData{Turn: 2, RequestID: "r2"}),
		ev(7, EventUserMessage, UserMessageData{Turn: 2, Content: "新问题", RequestID: "r2"}),
	}
	msgs := DeriveMessages(events)
	if len(msgs) != 1 || msgs[0].Role != RoleUser || msgs[0].Content != "此前讨论了旧问题" {
		t.Fatalf("covered range must be replaced by summary message, got %+v", msgs)
	}
}

func TestDeriveEmpty(t *testing.T) {
	msgs := DeriveMessages(nil)
	if msgs == nil || len(msgs) != 0 {
		t.Fatalf("want empty non-nil slice, got %+v", msgs)
	}
}

func TestDeriveUserMessageAttachments(t *testing.T) {
	ref := AttachmentRef{
		ID:   "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		MIME: "image/png", Bytes: 10, Width: 2, Height: 2, Name: "a.png",
	}
	events := []store.SessionEvent{
		ev(1, EventTurnStart, TurnStartData{Turn: 1, RequestID: "r1"}),
		ev(2, EventUserMessage, UserMessageData{Turn: 1, Content: "看图", RequestID: "r1", Attachments: []AttachmentRef{ref}}),
		ev(3, EventAssistantMessage, assistantMsg(1, 1, ContentBlock{Type: "text", Text: "是一只猫"})),
		ev(4, EventTurnEnd, TurnEndData{Turn: 1, Reason: "completed"}),
		ev(5, EventTurnStart, TurnStartData{Turn: 2, RequestID: "r2"}),
		ev(6, EventUserMessage, UserMessageData{Turn: 2, Content: "继续", RequestID: "r2"}),
	}
	msgs := DeriveMessages(events)
	if len(msgs) != 2 {
		t.Fatalf("want 2 messages, got %+v", msgs)
	}
	if len(msgs[0].Attachments) != 1 || msgs[0].Attachments[0].ID != ref.ID || msgs[0].Attachments[0].MIME != "image/png" {
		t.Fatalf("attachments must project onto derived user message: %+v", msgs[0])
	}
	if msgs[1].Attachments != nil {
		t.Fatalf("text-only message must have no attachments: %+v", msgs[1])
	}
}

func TestDeriveToolResultAttachments(t *testing.T) {
	ref := AttachmentRef{
		ID:   "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		MIME: "image/png", Bytes: 20, Width: 4, Height: 2, Name: "a.png",
	}
	events := []store.SessionEvent{
		ev(1, EventTurnStart, TurnStartData{Turn: 1, RequestID: "r1"}),
		ev(2, EventUserMessage, UserMessageData{Turn: 1, Content: "看看沙箱里的图", RequestID: "r1"}),
		ev(3, EventAssistantMessage, assistantMsg(1, 1,
			ContentBlock{Type: "tool-call", ID: "call_1", Name: "read_media", Arguments: `{"path":"/workspace/a.png"}`},
		)),
		ev(4, EventToolCall, ToolCallData{Turn: 1, Step: 1, CallID: "call_1", Name: "read_media", Arguments: `{"path":"/workspace/a.png"}`}),
		ev(5, EventToolResult, ToolResultData{
			Turn: 1, Step: 1, CallID: "call_1", Name: "read_media",
			Content:     `{"path":"/workspace/a.png"}`,
			Attachments: []AttachmentRef{ref},
		}),
		ev(6, EventAssistantMessage, assistantMsg(1, 2, ContentBlock{Type: "text", Text: "是一张截图"})),
		ev(7, EventTurnEnd, TurnEndData{Turn: 1, Reason: "completed"}),
		ev(8, EventTurnStart, TurnStartData{Turn: 2, RequestID: "r2"}),
		ev(9, EventUserMessage, UserMessageData{Turn: 2, Content: "继续", RequestID: "r2"}),
	}
	msgs := DeriveMessages(events)
	if len(msgs) != 4 {
		t.Fatalf("want 4 messages, got %+v", msgs)
	}
	tool := msgs[2]
	if tool.Role != RoleTool || tool.ToolCallID != "call_1" {
		t.Fatalf("msg[2] must be the tool message: %+v", tool)
	}
	if len(tool.Attachments) != 1 || tool.Attachments[0].ID != ref.ID || tool.Attachments[0].Width != 4 {
		t.Fatalf("tool result attachments must project onto derived tool message: %+v", tool)
	}
}
