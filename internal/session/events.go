package session

import "encoding/json"

const (
	EventTurnStart           = "turn/start"
	EventTurnEnd             = "turn/end"
	EventStepStart           = "step/start"
	EventStepEnd             = "step/end"
	EventUserMessage         = "user/message"
	EventAssistantMessage    = "assistant/message"
	EventAssistantAttempt    = "assistant/attempt"
	EventToolCall            = "tool/call"
	EventToolResult          = "tool/result"
	EventSessionTitle        = "session/title"
	EventSessionSandbox      = "session/sandbox"
	EventSessionApprovalMode = "session/approval-mode"
	EventSessionEffort       = "session/effort"
	EventModelSelection      = "model/selection"
	EventRequestHeader       = "request/header"
	EventApprovalAsked       = "approval/asked"
	EventApprovalDecided     = "approval/decided"
	EventCompactionSummary   = "compaction/summary"
)

const (
	RoleUser      = "user"
	RoleAssistant = "assistant"
	RoleTool      = "tool"
)

type Usage struct {
	InputTokens      int64 `json:"input_tokens"`
	OutputTokens     int64 `json:"output_tokens"`
	CacheReadTokens  int64 `json:"cache_read_tokens"`
	CacheWriteTokens int64 `json:"cache_write_tokens"`
	ReasoningTokens  int64 `json:"reasoning_tokens"`
}

type ContentBlock struct {
	Type      string `json:"type"`
	Text      string `json:"text,omitempty"`
	ID        string `json:"id,omitempty"`
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

type TurnStartData struct {
	Turn      int    `json:"turn"`
	RequestID string `json:"request_id"`
}

type TurnEndData struct {
	Turn   int    `json:"turn"`
	Reason string `json:"reason"`
	Error  string `json:"error,omitempty"`
}

type StepData struct {
	Turn int `json:"turn"`
	Step int `json:"step"`
}

type UserMessageData struct {
	Turn      int    `json:"turn"`
	Content   string `json:"content"`
	RequestID string `json:"request_id"`
}

type AssistantMessage struct {
	Role    string         `json:"role"`
	Content []ContentBlock `json:"content"`
}

type AssistantMessageData struct {
	Turn        int              `json:"turn"`
	Step        int              `json:"step"`
	Message     AssistantMessage `json:"message"`
	Usage       *Usage           `json:"usage,omitempty"`
	Interrupted bool             `json:"interrupted,omitempty"`
}

type AssistantAttemptData struct {
	Turn   int             `json:"turn"`
	Step   int             `json:"step"`
	Chunks json.RawMessage `json:"chunks"`
	Error  string          `json:"error,omitempty"`
}

type ToolCallData struct {
	Turn         int    `json:"turn"`
	Step         int    `json:"step"`
	CallID       string `json:"call_id"`
	Name         string `json:"name"`
	Arguments    string `json:"arguments"`
	ParentCallID string `json:"parent_call_id,omitempty"`
}

type ToolResultMeta struct {
	DurationMS int64  `json:"duration_ms"`
	Render     string `json:"render"`
	Truncated  bool   `json:"truncated,omitempty"`
	SandboxID  string `json:"sandbox_id,omitempty"`
	Diff       string `json:"diff,omitempty"`
}

type ToolResultData struct {
	Turn         int             `json:"turn"`
	Step         int             `json:"step"`
	CallID       string          `json:"call_id"`
	Name         string          `json:"name"`
	Content      string          `json:"content"`
	Error        string          `json:"error,omitempty"`
	Meta         *ToolResultMeta `json:"meta,omitempty"`
	ParentCallID string          `json:"parent_call_id,omitempty"`
}

type SessionTitleData struct {
	Title string `json:"title"`
	Auto  bool   `json:"auto"`
}

type SessionSandboxData struct {
	Action    string `json:"action"`
	SandboxID string `json:"sandbox_id"`
	Image     string `json:"image"`
	Error     string `json:"error,omitempty"`
}

type ApprovalModeData struct {
	Mode      string `json:"mode"`
	ChangedBy string `json:"changed_by"`
}

type ModelSelectionData struct {
	ModelID   string `json:"model_id"`
	ChangedBy string `json:"changed_by"`
}

type SessionEffortData struct {
	Effort    string `json:"effort"`
	ChangedBy string `json:"changed_by"`
}

type RequestHeaderData struct {
	Turn       int             `json:"turn"`
	ProviderID string          `json:"provider_id"`
	ModelID    string          `json:"model_id"`
	ModelCode  string          `json:"model_code"`
	Params     json.RawMessage `json:"params"`
	Tools      []string        `json:"tools"`
}

type ApprovalAskedData struct {
	Turn         int    `json:"turn"`
	Step         int    `json:"step"`
	CallID       string `json:"call_id"`
	Name         string `json:"name"`
	Arguments    string `json:"arguments"`
	Risk         string `json:"risk"`
	ParentCallID string `json:"parent_call_id,omitempty"`
}

type ApprovalDecidedData struct {
	CallID    string `json:"call_id"`
	Decision  string `json:"decision"`
	DecidedBy string `json:"decided_by"`
	Auto      bool   `json:"auto"`
}

type SeqRange struct {
	StartSeq int64 `json:"start_seq"`
	EndSeq   int64 `json:"end_seq"`
}

type CompactionSummaryData struct {
	Covers  SeqRange `json:"covers"`
	Summary string   `json:"summary"`
}
