package session

import (
	"encoding/json"

	"github.com/ravenmk2/wispjar/internal/store"
)

const interruptedResult = "interrupted"

type ToolCall struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type Message struct {
	Role        string          `json:"role"`
	Content     string          `json:"content"`
	Attachments []AttachmentRef `json:"attachments,omitempty"`
	ToolCalls   []ToolCall      `json:"tool_calls,omitempty"`
	ToolCallID  string          `json:"tool_call_id,omitempty"`
	ToolName    string          `json:"tool_name,omitempty"`
}

func DeriveMessages(events []store.SessionEvent) []Message {
	msgs := []Message{}
	cut := -1
	for i, ev := range events {
		if ev.Type == EventTurnStart {
			cut = i
		}
	}
	if cut >= 0 {
		events = events[:cut]
	}

	type window struct{ start, end int64 }
	windows := []window{}
	summaries := map[int64]string{}
	for _, ev := range events {
		if ev.Type != EventCompactionSummary {
			continue
		}
		var d CompactionSummaryData
		if err := json.Unmarshal(ev.Data, &d); err != nil {
			continue
		}
		windows = append(windows, window{start: d.Covers.StartSeq, end: d.Covers.EndSeq})
		summaries[ev.Seq] = d.Summary
	}
	covered := func(seq int64) bool {
		for _, w := range windows {
			if seq >= w.start && seq <= w.end {
				return true
			}
		}
		return false
	}

	resultByCall := map[string]bool{}
	callName := map[string]string{}
	for _, ev := range events {
		if covered(ev.Seq) || hasParentCall(ev.Data) {
			continue
		}
		switch ev.Type {
		case EventToolResult:
			var d ToolResultData
			if err := json.Unmarshal(ev.Data, &d); err == nil && d.CallID != "" {
				resultByCall[d.CallID] = true
			}
		case EventToolCall:
			var d ToolCallData
			if err := json.Unmarshal(ev.Data, &d); err == nil && d.CallID != "" {
				callName[d.CallID] = d.Name
			}
		}
	}

	pending := []ToolCall{}
	flush := func() {
		for _, tc := range pending {
			if !resultByCall[tc.ID] {
				msgs = append(msgs, Message{
					Role:       RoleTool,
					ToolCallID: tc.ID,
					ToolName:   tc.Name,
					Content:    interruptedResult,
				})
			}
		}
		pending = nil
	}

	for _, ev := range events {
		if covered(ev.Seq) || hasParentCall(ev.Data) {
			continue
		}
		switch ev.Type {
		case EventUserMessage:
			flush()
			var d UserMessageData
			if err := json.Unmarshal(ev.Data, &d); err != nil {
				continue
			}
			msgs = append(msgs, Message{Role: RoleUser, Content: d.Content, Attachments: d.Attachments})
		case EventAssistantMessage:
			flush()
			var d AssistantMessageData
			if err := json.Unmarshal(ev.Data, &d); err != nil {
				continue
			}
			m := Message{Role: RoleAssistant}
			for _, b := range d.Message.Content {
				switch b.Type {
				case "text":
					m.Content += b.Text
				case "tool-call":
					m.ToolCalls = append(m.ToolCalls, ToolCall{ID: b.ID, Name: b.Name, Arguments: b.Arguments})
				}
			}
			msgs = append(msgs, m)
			pending = append(pending, m.ToolCalls...)
		case EventToolResult:
			var d ToolResultData
			if err := json.Unmarshal(ev.Data, &d); err != nil {
				continue
			}
			name := callName[d.CallID]
			if name == "" {
				name = d.Name
			}
			content := d.Content
			if d.Error != "" {
				content = d.Error
			}
			msgs = append(msgs, Message{Role: RoleTool, ToolCallID: d.CallID, ToolName: name, Content: content})
			for i, tc := range pending {
				if tc.ID == d.CallID {
					pending = append(pending[:i], pending[i+1:]...)
					break
				}
			}
		case EventCompactionSummary:
			flush()
			msgs = append(msgs, Message{Role: RoleUser, Content: summaries[ev.Seq]})
		}
	}
	flush()
	return msgs
}

func hasParentCall(raw json.RawMessage) bool {
	var probe struct {
		ParentCallID string `json:"parent_call_id"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return false
	}
	return probe.ParentCallID != ""
}
