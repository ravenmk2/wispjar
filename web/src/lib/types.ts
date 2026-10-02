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

export interface ModelCapabilities {
  tools: boolean
  reasoning: boolean
  vision: boolean
  streaming: boolean
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
}

export type UsageGroupBy = "user" | "agent" | "model" | "day" | "org"
