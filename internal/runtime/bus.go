package runtime

import (
	"sync"

	"github.com/ravenmk2/wispjar/internal/session"
)

// StreamChunk 是 protocol.md §4.5 的瞬态增量帧载体。
type StreamChunk struct {
	Type           string       `json:"type"`
	Index          int          `json:"index,omitempty"`
	BlockType      string       `json:"blockType,omitempty"`
	Text           string       `json:"text,omitempty"`
	ID             string       `json:"id,omitempty"`
	Name           string       `json:"name,omitempty"`
	ArgumentsDelta string       `json:"argumentsDelta,omitempty"`
	Block          any          `json:"block,omitempty"`
	Usage          *StreamUsage `json:"usage,omitempty"`
	Reason         string       `json:"reason,omitempty"`
}

type StreamUsage struct {
	InputTokens      int64 `json:"inputTokens"`
	OutputTokens     int64 `json:"outputTokens"`
	CacheReadTokens  int64 `json:"cacheReadTokens"`
	CacheWriteTokens int64 `json:"cacheWriteTokens"`
	ReasoningTokens  int64 `json:"reasoningTokens"`
}

const (
	ChunkBlockStart     = "block-start"
	ChunkTextDelta      = "text-delta"
	ChunkReasoningDelta = "reasoning-delta"
	ChunkToolCallDelta  = "tool-call-delta"
	ChunkBlockEnd       = "block-end"
	ChunkUsage          = "usage"
	ChunkFinish         = "finish"

	BlockText      = "text"
	BlockReasoning = "reasoning"
	BlockToolCall  = "tool-call"
)

const (
	FinishStop      = "stop"
	FinishToolCalls = "tool-calls"
	FinishMaxTokens = "max-tokens"
	FinishAborted   = "aborted"
	FinishError     = "error"
)

// StreamStart / StreamChunkFrame / StreamEnd 是 assistant-stream 帧（§4.4）。
type StreamStart struct {
	Type            string `json:"type"`
	AttemptID       string `json:"attemptId"`
	Turn            int    `json:"turn"`
	Step            int    `json:"step"`
	StartedAfterSeq int64  `json:"startedAfterSeq"`
}

type StreamChunkFrame struct {
	Type      string      `json:"type"`
	AttemptID string      `json:"attemptId"`
	Index     int         `json:"index"`
	Time      int64       `json:"time"`
	Chunk     StreamChunk `json:"chunk"`
}

type StreamOutcome struct {
	Kind      string `json:"kind"`
	EventType string `json:"eventType,omitempty"`
	Seq       int64  `json:"seq,omitempty"`
}

type StreamEnd struct {
	Type      string        `json:"type"`
	AttemptID string        `json:"attemptId"`
	Outcome   StreamOutcome `json:"outcome"`
}

type ToolOutputFrame struct {
	Type   string `json:"type"`
	CallID string `json:"callId"`
	Stream string `json:"stream"`
	Delta  string `json:"delta"`
}

// FollowFrame 是 session.follow 下行消息（§4.3/§4.4 + runtime.md §9）。
type FollowFrame struct {
	Type       string           `json:"type"`
	Event      *session.Event   `json:"event,omitempty"`
	Frame      any              `json:"frame,omitempty"`
	ToolOutput *ToolOutputFrame `json:"-"`
}

func EventFrame(ev session.Event) FollowFrame {
	return FollowFrame{Type: "event", Event: &ev}
}

func AssistantStreamFrame(frame any) FollowFrame {
	return FollowFrame{Type: "assistant-stream", Frame: frame}
}

func ToolOutputLiveFrame(callID, stream, delta string) FollowFrame {
	return FollowFrame{
		Type:       "tool-output",
		ToolOutput: &ToolOutputFrame{Type: "tool-output", CallID: callID, Stream: stream, Delta: delta},
	}
}

// MarshalValue 返回该帧在 session.follow 下行消息中的 value 载荷。
func (f FollowFrame) MarshalValue() any {
	switch f.Type {
	case "event":
		return map[string]any{"type": "event", "event": f.Event}
	case "assistant-stream":
		return map[string]any{"type": "assistant-stream", "frame": f.Frame}
	case "tool-output":
		return f.ToolOutput
	}
	return nil
}

// ControlFrame 是 control 流下行消息（§5）。
type ControlFrame struct {
	Type    string          `json:"type"`
	Session *ControlSession `json:"session,omitempty"`
	Remove  string          `json:"sessionId,omitempty"`
}

type ControlSession struct {
	SessionID    string        `json:"sessionId"`
	Title        string        `json:"title"`
	Running      bool          `json:"running"`
	AgentID      string        `json:"agentId"`
	UsageTotal   session.Usage `json:"usageTotal"`
	LastActiveAt int64         `json:"lastActiveAt"`
	Status       string        `json:"status"`
}

// Bus 是按 session_id / org_id 频道的内存 pub/sub（单实例部署）。
type Bus struct {
	mu       sync.RWMutex
	next     uint64
	sessions map[string]map[uint64]chan FollowFrame
	control  map[string]map[uint64]chan ControlFrame
}

func NewBus() *Bus {
	return &Bus{
		sessions: map[string]map[uint64]chan FollowFrame{},
		control:  map[string]map[uint64]chan ControlFrame{},
	}
}

func (b *Bus) Publish(sessionID string, f FollowFrame) {
	b.mu.RLock()
	subs := b.sessions[sessionID]
	b.mu.RUnlock()
	for _, ch := range subs {
		select {
		case ch <- f:
		default:
		}
	}
}

func (b *Bus) SubscribeSession(sessionID string, buf int) (<-chan FollowFrame, func()) {
	if buf <= 0 {
		buf = 256
	}
	ch := make(chan FollowFrame, buf)
	b.mu.Lock()
	b.next++
	id := b.next
	if b.sessions[sessionID] == nil {
		b.sessions[sessionID] = map[uint64]chan FollowFrame{}
	}
	b.sessions[sessionID][id] = ch
	b.mu.Unlock()
	cancel := func() {
		b.mu.Lock()
		if subs, ok := b.sessions[sessionID]; ok {
			delete(subs, id)
			if len(subs) == 0 {
				delete(b.sessions, sessionID)
			}
		}
		b.mu.Unlock()
		close(ch)
	}
	return ch, cancel
}

func (b *Bus) PublishControl(orgID string, f ControlFrame) {
	b.mu.RLock()
	subs := b.control[orgID]
	b.mu.RUnlock()
	for _, ch := range subs {
		select {
		case ch <- f:
		default:
		}
	}
}

func (b *Bus) SubscribeControl(orgID string, buf int) (<-chan ControlFrame, func()) {
	if buf <= 0 {
		buf = 64
	}
	ch := make(chan ControlFrame, buf)
	b.mu.Lock()
	b.next++
	id := b.next
	if b.control[orgID] == nil {
		b.control[orgID] = map[uint64]chan ControlFrame{}
	}
	b.control[orgID][id] = ch
	b.mu.Unlock()
	cancel := func() {
		b.mu.Lock()
		if subs, ok := b.control[orgID]; ok {
			delete(subs, id)
			if len(subs) == 0 {
				delete(b.control, orgID)
			}
		}
		b.mu.Unlock()
		close(ch)
	}
	return ch, cancel
}
