export interface ApiErrorBody {
  code: string
  message: string
  details?: Record<string, unknown>
}

export interface ErrorEnvelope {
  error: ApiErrorBody
}

export interface List<T> {
  items: T[]
  next_cursor?: string
}

export type Role = "admin" | "member"
export type Status = "active" | "disabled"

export interface User {
  id: string
  username: string
  display_name: string
  must_change_password: boolean
}

export interface UserOrg {
  id: string
  name: string
  role: Role
  is_platform: boolean
}

export interface LoginResponse {
  token: string
  user: User
  orgs: UserOrg[]
}

export interface MeResponse {
  user: User
  orgs: UserOrg[]
}

export interface ChangePasswordResponse {
  ok: boolean
  token?: string
}

export interface OrgSummary {
  id: string
  name: string
  is_platform: boolean
  status: Status
  member_count: number
  agent_count: number
  created_at: string
}

export interface OrgMember {
  user_id: string
  username: string
  display_name: string
  role: Role
  created_at: string
}

export interface OrgDetail extends OrgSummary {
  members: OrgMember[]
}

export interface PlatformUserOrg {
  org_id: string
  org_name: string
  role: Role
}

export interface PlatformUser {
  id: string
  username: string
  display_name: string
  status: Status
  must_change_password: boolean
  orgs: PlatformUserOrg[]
  created_at: string
}

export interface CreateUserResponse {
  user: PlatformUser
  password?: string
}

export type ProviderType = "deepseek" | "ark" | "qwen" | "openai_compat"

export interface ProviderLastCheck {
  ok: boolean
  at: string
  latency_ms: number
}

export interface ProviderExtra {
  remark?: string
  last_check?: ProviderLastCheck
}

export interface Provider {
  id: string
  name: string
  type: ProviderType
  base_url: string
  masked_key: string
  extra: ProviderExtra | null
  status: Status
  created_at: string
}

export const REASONING_EFFORT_OPTIONS = ["none", "minimal", "low", "medium", "high", "xhigh", "max"]

export interface ModelCapabilities {
  tools: boolean
  reasoning: boolean
  vision: boolean
  streaming: boolean
  reasoning_efforts?: string[]
  reasoning_effort_default?: string
}

export interface Model {
  id: string
  provider_id: string
  provider_name?: string
  model_code: string
  display_name: string
  capabilities: ModelCapabilities
  context_size: number
  default_params: Record<string, unknown> | null
  status: Status
  created_at: string
}

export type EgressAction = "allow" | "deny"

export interface EgressRule {
  action: EgressAction
  target: string
}

export interface EgressPolicy {
  defaultAction: EgressAction
  rules: EgressRule[]
}

export interface ResourceLimits {
  cpu: string
  memory: string
}

export const SETTING_KEYS = {
  defaultImage: "sandbox.default_image",
  images: "sandbox.images",
  egressPolicy: "sandbox.egress_policy",
  resourceLimits: "sandbox.resource_limits",
  idleTtl: "sandbox.idle_ttl_seconds",
  ttl: "sandbox.ttl_seconds",
  env: "sandbox.env",
} as const

export type SettingKey = (typeof SETTING_KEYS)[keyof typeof SETTING_KEYS]

export type SettingsMap = Partial<Record<SettingKey, unknown>>

export interface UsageItem {
  key: string
  requests: number
  input_tokens: number
  output_tokens: number
  cache_read_tokens: number
  cache_write_tokens: number
  reasoning_tokens: number
  total_tokens: number
}

export interface UsageTotals {
  requests: number
  input_tokens: number
  output_tokens: number
  cache_read_tokens: number
  cache_write_tokens: number
  reasoning_tokens: number
  total_tokens: number
}

export interface UsageResponse {
  items: UsageItem[]
  totals: UsageTotals
  names?: Record<string, string>
}

export type UsageGroupBy = "user" | "agent" | "model" | "day" | "org"

export interface AuditItem {
  id: string
  org_id: string
  org_name?: string
  actor_id: string
  actor_name: string
  action: string
  target: string
  detail?: Record<string, unknown> | string | null
  created_at: string
}

// ============ protocol.md：WS mux 协议类型 ============

export type ApprovalMode = "strict" | "default" | "auto"

export type SandboxStatus = "none" | "creating" | "running" | "paused" | "recycled"

export type SessionStatus = "active" | "archived" | "deleted"

export interface Usage {
  input_tokens: number
  output_tokens: number
  cache_read_tokens: number
  cache_write_tokens: number
  reasoning_tokens: number
  /** false/缺省 = 供应商未上报缓存用量（区别于命中为 0） */
  cache_reported?: boolean
}

export interface ChunkUsage {
  inputTokens: number
  outputTokens: number
  cacheReadTokens: number
  cacheWriteTokens: number
  reasoningTokens: number
  cacheReported?: boolean
}

export type ContentBlock =
  | { type: "text"; text: string }
  // durationMs 由服务端在 reasoning 块上下发（流式窗口埋点，block-end/快照/历史事件均携带）；
  // streaming 为前端折叠流式 chunk 时补充的展示标记（仅 live 中的末位 pending 思考块为 true）
  | { type: "reasoning"; text: string; durationMs?: number; streaming?: boolean }
  | { type: "tool-call"; id: string; name: string; arguments: string }

export interface AssistantMessagePayload {
  role: "assistant"
  content: ContentBlock[]
}

export interface AttachmentRef {
  id: string
  mime: string
  bytes: number
  width: number
  height: number
  name: string
}

export type StreamChunk =
  | { type: "block-start"; index: number; blockType: "text" | "reasoning" | "tool-call" }
  | { type: "text-delta"; index: number; text: string }
  | { type: "reasoning-delta"; index: number; text: string }
  | { type: "tool-call-delta"; index: number; id?: string; name?: string; argumentsDelta?: string }
  | { type: "block-end"; index: number; block: ContentBlock }
  | { type: "usage"; usage: ChunkUsage }
  | { type: "finish"; reason: "stop" | "tool-calls" | "max-tokens" | "aborted" | "error" }

// ============ protocol.md §6：会话事件词表 ============

export type TurnEndReason = "completed" | "interrupted" | "failed"

export type ToolRender = "terminal" | "diff" | "search" | "read" | "generic"

// runtime.md §4.9：ask_user_question 提问；无 options=自由输入，有 options 按 multi_select 分单/多选
export interface QuestionOption {
  label: string
  description?: string
}

export interface Question {
  id: string
  question: string
  header?: string
  options?: QuestionOption[]
  multi_select?: boolean
}

// 显式跳过 = selected 为空且无 custom（仍算已作答）
export interface QuestionAnswer {
  id: string
  selected: string[]
  custom?: string
}

export interface ToolResultMeta {
  duration_ms?: number
  render?: ToolRender
  diff?: string
  operation?: "create" | "update"
  truncated?: boolean
  sandbox_id?: string
}

export interface SessionEventBase {
  seq: number
  time: number
}

export type SessionEvent = SessionEventBase &
  (
    | { type: "turn/start"; data: { turn: number; request_id: string } }
    | { type: "turn/end"; data: { turn: number; reason: TurnEndReason; error?: string } }
    | { type: "step/start"; data: { turn: number; step: number } }
    | { type: "step/end"; data: { turn: number; step: number } }
    | {
        type: "user/message"
        data: {
          turn: number
          content: string
          request_id: string
          attachments?: AttachmentRef[]
          source?: { kind: string; call_id: string }
        }
      }
    | {
        type: "assistant/message"
        data: { turn: number; step: number; message: AssistantMessagePayload; usage?: Usage; interrupted?: boolean }
      }
    | { type: "assistant/attempt"; data: { turn: number; step: number; chunks: StreamChunk[]; error?: string } }
    | {
        type: "tool/call"
        data: { turn: number; step: number; call_id: string; name: string; arguments: string; parent_call_id?: string }
      }
    | {
        type: "tool/result"
        data: {
          turn: number
          step: number
          call_id: string
          name: string
          content: string
          error?: string
          meta?: ToolResultMeta
          attachments?: AttachmentRef[]
          parent_call_id?: string
        }
      }
    | { type: "session/title"; data: { title: string; auto: boolean } }
    | {
        type: "session/sandbox"
        data: { action: "created" | "recycled" | "failed"; sandbox_id: string; image: string; error?: string }
      }
    | { type: "session/approval-mode"; data: { mode: ApprovalMode; changed_by: string } }
    | { type: "session/effort"; data: { effort: string; changed_by: string } }
    | { type: "model/selection"; data: { model_id: string; changed_by: string } }
    | {
        type: "request/header"
        data: { turn: number; provider_id: string; model_id: string; model_code: string; params: unknown; tools: string[] }
      }
    | {
        type: "approval/asked"
        data: {
          turn: number
          step: number
          call_id: string
          name: string
          arguments: string
          risk: "high" | "medium"
          parent_call_id?: string
        }
      }
    | {
        type: "approval/decided"
        data: { call_id: string; decision: "allow" | "deny"; decided_by: string; auto: boolean }
      }
    | { type: "question/asked"; data: { turn: number; step: number; call_id: string; questions: Question[] } }
    | { type: "question/answered"; data: { call_id: string; answers: QuestionAnswer[]; answered_by: string } }
    | { type: "compaction/summary"; data: { covers: { start_seq: number; end_seq: number }; summary: string } }
  )

export type SessionEventType = SessionEvent["type"]

// ============ protocol.md §4：session.follow 帧 ============

export interface SessionHeader {
  modelSettingsLocked: boolean
  sessionId: string
  title: string
  titlePinned: boolean
  agentId: string
  agentName: string
  agentAvatar: string
  modelId: string
  approvalMode: ApprovalMode
  sandboxStatus: SandboxStatus
  running: boolean
  lastSeq: number
  usageTotal: Usage
  createdAt: string
}

export interface AssistantStreamSnapshot {
  attemptId: string
  turn: number
  step: number
  startedAfterSeq: number
  compactChunks: StreamChunk[]
  nextIndex: number
}

export type AssistantStreamOutcome =
  | { kind: "committed"; eventType: string; seq: number }
  | { kind: "abandoned" }

export type AssistantStreamFrame =
  | { type: "start"; attemptId: string; turn: number; step: number; startedAfterSeq: number }
  | { type: "chunk"; attemptId: string; index: number; time: number; chunk: StreamChunk }
  | { type: "end"; attemptId: string; outcome: AssistantStreamOutcome }

export type FollowFrame =
  | { type: "snapshot"; header: SessionHeader; records: SessionEvent[]; hasMore: boolean; assistantStream?: AssistantStreamSnapshot }
  | { type: "event"; event: SessionEvent }
  | { type: "assistant-stream"; frame: AssistantStreamFrame }
  | { type: "tool-output"; callId: string; stream: "stdout" | "stderr"; delta: string }

export interface FollowOpenPayload {
  orgId: string
  sessionId: string
  afterSeq: number
}

export interface ApprovalResponseUplink {
  type: "approval-response"
  callId: string
  decision: "allow" | "deny"
}

// ============ protocol.md §5：control 流 ============

export interface SessionProjection {
  sessionId: string
  title: string
  running: boolean
  agentId: string
  usageTotal: Usage
  lastActiveAt: string
  status: SessionStatus
}

export type ControlFrame =
  | { type: "baseline"; sessions: SessionProjection[] }
  | { type: "upsert"; session: SessionProjection }
  | { type: "remove"; sessionId: string }

// ============ protocol.md §2：mux 帧 ============

export type MuxClientFrame =
  | { type: "open"; streamId: string; endpoint: string; payload: unknown }
  | { type: "item"; streamId: string; value: unknown }
  | { type: "end"; streamId: string }
  | { type: "cancel"; streamId: string }

export type MuxServerFrame =
  | { type: "item"; streamId: string; value: unknown }
  | { type: "error"; streamId: string; error: ApiErrorBody }
  | { type: "end"; streamId: string }

// ============ api.md §3/§4：会话域 REST DTO ============

export interface SessionSummary {
  id: string
  agent_id: string
  agent_name: string
  title: string
  running: boolean
  approval_mode: ApprovalMode
  model_id: string
  usage_total: Usage
  last_active_at: string
  created_at: string
}

export interface SessionAgentInfo {
  name: string
  description: string
  avatar: string
}

export interface SessionDetail extends SessionSummary {
  title_pinned?: boolean
  sandbox_status?: SandboxStatus
  status?: SessionStatus
  agent?: SessionAgentInfo
  model_params?: Record<string, unknown> | null
  effort_locked?: boolean
  model_settings_locked?: boolean
}

export interface CreateSessionRequest {
  model_id?: string
  agent_id: string
  title?: string
  reasoning_effort?: string
}

export interface UpdateSessionRequest {
  title?: string
  approval_mode?: ApprovalMode
  status?: "active" | "archived"
  model_id?: string
  reasoning_effort?: string
}

export interface PromptRequest {
  content: string
  request_id: string
  attachments?: string[]
}

export interface PromptResponse {
  accepted: boolean
  seq: number
}

export interface ForkResponse {
  id: string
}

export interface SessionEventsResponse {
  items: SessionEvent[]
  last_seq: number
}

export interface ApprovalDecisionRequest {
  decision: "allow" | "deny"
}

export interface AnswerQuestionRequest {
  answers: QuestionAnswer[]
}

// open=前台等待中的工具直接投递；continued=迟到答复，合成 user/message 开新 turn
export type AnswerQuestionResponse =
  | { ok: true; delivered: "open" }
  | { accepted: true; delivered: "continued"; seq: number }

export interface SandboxFileItem {
  name: string
  path: string
  type: "file" | "dir"
  size: number
  modified_at: string
}

export interface SandboxFilesResponse {
  items: SandboxFileItem[]
}

export interface SandboxFileResponse {
  path: string
  content: string
  truncated: boolean
}

export interface AgentSummary {
  id: string
  name: string
  description: string
  avatar: string
  model_id: string
  model_display_name: string
}

export interface OrgModel {
  id: string
  display_name: string
  model_code: string
  provider_name: string
  capabilities: ModelCapabilities
  context_size: number
}

// ============ api.md §5：机构管理（智能体 / Skills）DTO ============

export type AgentStatus = "draft" | "published" | "archived"

export interface AgentToolsEnabled {
  exec: boolean
  read: boolean
  write: boolean
  edit: boolean
  glob: boolean
  grep: boolean
}

export interface AgentModelParams {
  temperature?: number
  max_tokens?: number
}

export interface AgentSandboxResources {
  cpu?: string
  memory?: string
}

export interface AgentSandboxConfig {
  image: string
  resources: AgentSandboxResources | null
  env?: Record<string, string> | null
}

export interface AgentBody {
  name: string
  description: string
  avatar: string
  system_prompt: string
  model_id: string
  model_params: AgentModelParams | null
  max_iterations: number
  tools_enabled: AgentToolsEnabled
  skill_ids: string[]
  sub_agent_ids: string[]
  sandbox_config: AgentSandboxConfig
}

export interface AdminAgentSummary {
  id: string
  name: string
  description: string
  avatar: string
  status: AgentStatus
  model_id: string
  model_display_name?: string
  skill_count?: number
  sub_agent_count?: number
  updated_at: string
  created_at?: string
}

export interface AdminAgentDetail extends AgentBody {
  id: string
  status: AgentStatus
  updated_at: string
  created_at?: string
}

export interface SkillReference {
  agent_id: string
  agent_name: string
}

export interface SkillSummary {
  id: string
  name: string
  description: string
  size_bytes: number
  uploader_name: string
  created_at: string
  referenced_by: SkillReference[]
}

export interface SkillFileEntry {
  path: string
  size: number
  type?: "file" | "dir"
}

export interface SkillDetail extends SkillSummary {
  meta: Record<string, unknown>
  files: SkillFileEntry[]
}

export interface SkillFileResponse {
  path: string
  content: string
  truncated?: boolean
}
