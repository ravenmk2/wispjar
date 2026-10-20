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
	EventQuestionAsked       = "question/asked"
	EventQuestionAnswered    = "question/answered"
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
	// CacheReported 区分「未命中」与「供应商未上报缓存用量」（runtime.md §3）：
	// 原始 usage 旁路抓取到该家缓存字段时为 true；抓取失败/无字段时为 false。
	CacheReported bool `json:"cache_reported,omitempty"`
}

type ContentBlock struct {
	Type      string `json:"type"`
	Text      string `json:"text,omitempty"`
	ID        string `json:"id,omitempty"`
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
	// DurationMs 仅 reasoning 块使用：思考首帧到末帧的耗时（流式窗口埋点）。
	DurationMs int64 `json:"durationMs,omitempty"`
}

type TurnStartData struct {
	Turn      int    `json:"turn"`
	RequestID string `json:"request_id"`
}

type TurnEndData struct {
	Turn      int    `json:"turn"`
	Reason    string `json:"reason"`
	Error     string `json:"error,omitempty"`
	ErrorCode string `json:"error_code,omitempty"`
}

type StepData struct {
	Turn int `json:"turn"`
	Step int `json:"step"`
}

// AttachmentRef 是图片附件的持久引用（不透明 id，绝无 base64/路径/URL）。
type AttachmentRef struct {
	ID     string `json:"id"` // "sha256:<64 hex>"
	MIME   string `json:"mime"`
	Bytes  int64  `json:"bytes"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
	Name   string `json:"name"`
}

// MessageSource 标记合成 user/message 的来源（迟到提问答复等）；普通用户消息为空。
type MessageSource struct {
	Kind   string `json:"kind"`
	CallID string `json:"call_id"`
}

type UserMessageData struct {
	Turn        int             `json:"turn"`
	Content     string          `json:"content"`
	RequestID   string          `json:"request_id"`
	Attachments []AttachmentRef `json:"attachments,omitempty"`
	Source      *MessageSource  `json:"source,omitempty"`
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
	ComputerID string `json:"computer_id,omitempty"`
	RunID      string `json:"run_id,omitempty"`
	DurationMS int64  `json:"duration_ms"`
	Render     string `json:"render"`
	Truncated  bool   `json:"truncated,omitempty"`
	SandboxID  string `json:"sandbox_id,omitempty"`
	Diff       string `json:"diff,omitempty"`
	Operation  string `json:"operation,omitempty"`
}

type ToolResultData struct {
	Turn    int             `json:"turn"`
	Step    int             `json:"step"`
	CallID  string          `json:"call_id"`
	Name    string          `json:"name"`
	Content string          `json:"content"`
	Error   string          `json:"error,omitempty"`
	Meta    *ToolResultMeta `json:"meta,omitempty"`
	// Attachments 工具产出图片的持久引用（read_media；与 user/message.attachments 同结构，
	// base64 不进事件，回放时按引用从附件存储读字节重组多模态 tool 消息）。
	Attachments  []AttachmentRef `json:"attachments,omitempty"`
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
	// ImageOffload 图片卸载策略版本（本 turn 携带附件时记录；规则变更=历史前缀失效，审计用）。
	ImageOffload string `json:"image_offload,omitempty"`
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

// QuestionOption 是提问选项；答案回传 label 而非下标（对齐模型最不易错的形态）。
type QuestionOption struct {
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}

// Question 是 question 工具的一道题：无 options=自由输入；有 options 按 multi_select 分单/多选。
type Question struct {
	ID          string           `json:"id"`
	Question    string           `json:"question"`
	Header      string           `json:"header,omitempty"`
	Options     []QuestionOption `json:"options,omitempty"`
	MultiSelect bool             `json:"multi_select,omitempty"`
}

// QuestionAnswer 是一道题的答案；selected 为选项 label 列表，custom 为自由文本；
// 显式跳过 = selected 为空且无 custom（仍算已作答）。
type QuestionAnswer struct {
	ID       string   `json:"id"`
	Selected []string `json:"selected"`
	Custom   string   `json:"custom,omitempty"`
}

type QuestionAskedData struct {
	Turn      int        `json:"turn"`
	Step      int        `json:"step"`
	CallID    string     `json:"call_id"`
	Questions []Question `json:"questions"`
}

type QuestionAnsweredData struct {
	CallID     string           `json:"call_id"`
	Answers    []QuestionAnswer `json:"answers"`
	AnsweredBy string           `json:"answered_by"`
}

type SeqRange struct {
	StartSeq int64 `json:"start_seq"`
	EndSeq   int64 `json:"end_seq"`
}

type CompactionSummaryData struct {
	Covers  SeqRange `json:"covers"`
	Summary string   `json:"summary"`
}
