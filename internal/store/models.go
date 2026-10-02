package store

import (
	"encoding/json"
	"time"
)

type Organization struct {
	ID         string    `gorm:"column:id;primaryKey;size:32" json:"id"`
	Name       string    `gorm:"column:name;size:128" json:"name"`
	IsPlatform bool      `gorm:"column:is_platform" json:"is_platform"`
	Status     string    `gorm:"column:status;size:16" json:"status"`
	CreatedAt  time.Time `gorm:"column:created_at" json:"created_at"`
	UpdatedAt  time.Time `gorm:"column:updated_at" json:"updated_at"`
}

func (Organization) TableName() string { return "organization" }

type User struct {
	ID                 string    `gorm:"column:id;primaryKey;size:32" json:"id"`
	Username           string    `gorm:"column:username;size:64" json:"username"`
	PasswordHash       string    `gorm:"column:password_hash;size:128" json:"-"`
	DisplayName        string    `gorm:"column:display_name;size:128" json:"display_name"`
	Status             string    `gorm:"column:status;size:16" json:"status"`
	MustChangePassword bool      `gorm:"column:must_change_password" json:"must_change_password"`
	CreatedAt          time.Time `gorm:"column:created_at" json:"created_at"`
	UpdatedAt          time.Time `gorm:"column:updated_at" json:"updated_at"`
}

func (User) TableName() string { return "user" }

type OrgMember struct {
	ID        string    `gorm:"column:id;primaryKey;size:32" json:"id"`
	OrgID     string    `gorm:"column:org_id;size:32" json:"org_id"`
	UserID    string    `gorm:"column:user_id;size:32" json:"user_id"`
	Role      string    `gorm:"column:role;size:16" json:"role"`
	CreatedAt time.Time `gorm:"column:created_at" json:"created_at"`
}

func (OrgMember) TableName() string { return "org_member" }

type ModelProvider struct {
	ID        string          `gorm:"column:id;primaryKey;size:32" json:"id"`
	Name      string          `gorm:"column:name;size:128" json:"name"`
	Type      string          `gorm:"column:type;size:32" json:"type"`
	BaseURL   string          `gorm:"column:base_url;size:512" json:"base_url"`
	APIKeyEnc []byte          `gorm:"column:api_key_enc" json:"-"`
	Extra     json.RawMessage `gorm:"column:extra;type:json" json:"extra"`
	Status    string          `gorm:"column:status;size:16" json:"status"`
	CreatedBy string          `gorm:"column:created_by;size:32" json:"created_by"`
	CreatedAt time.Time       `gorm:"column:created_at" json:"created_at"`
	UpdatedAt time.Time       `gorm:"column:updated_at" json:"updated_at"`
}

func (ModelProvider) TableName() string { return "model_provider" }

type Model struct {
	ID            string          `gorm:"column:id;primaryKey;size:32" json:"id"`
	ProviderID    string          `gorm:"column:provider_id;size:32" json:"provider_id"`
	ModelCode     string          `gorm:"column:model_code;size:128" json:"model_code"`
	DisplayName   string          `gorm:"column:display_name;size:128" json:"display_name"`
	Capabilities  json.RawMessage `gorm:"column:capabilities;type:json" json:"capabilities"`
	ContextSize   int             `gorm:"column:context_size" json:"context_size"`
	DefaultParams json.RawMessage `gorm:"column:default_params;type:json" json:"default_params"`
	Status        string          `gorm:"column:status;size:16" json:"status"`
	CreatedAt     time.Time       `gorm:"column:created_at" json:"created_at"`
	UpdatedAt     time.Time       `gorm:"column:updated_at" json:"updated_at"`
}

func (Model) TableName() string { return "model" }

type PlatformSetting struct {
	Key       string          `gorm:"column:key;primaryKey;size:64" json:"key"`
	Value     json.RawMessage `gorm:"column:value;type:json" json:"value"`
	UpdatedBy string          `gorm:"column:updated_by;size:32" json:"updated_by"`
	UpdatedAt time.Time       `gorm:"column:updated_at" json:"updated_at"`
}

func (PlatformSetting) TableName() string { return "platform_setting" }

type Agent struct {
	ID            string          `gorm:"column:id;primaryKey;size:32" json:"id"`
	OrgID         string          `gorm:"column:org_id;size:32" json:"org_id"`
	Name          string          `gorm:"column:name;size:128" json:"name"`
	Description   string          `gorm:"column:description;size:1024" json:"description"`
	Avatar        string          `gorm:"column:avatar;size:512" json:"avatar"`
	SystemPrompt  string          `gorm:"column:system_prompt;type:mediumtext" json:"system_prompt"`
	ModelID       string          `gorm:"column:model_id;size:32" json:"model_id"`
	ModelParams   json.RawMessage `gorm:"column:model_params;type:json" json:"model_params"`
	MaxIterations int             `gorm:"column:max_iterations" json:"max_iterations"`
	ToolsEnabled  json.RawMessage `gorm:"column:tools_enabled;type:json" json:"tools_enabled"`
	SandboxConfig json.RawMessage `gorm:"column:sandbox_config;type:json" json:"sandbox_config"`
	Status        string          `gorm:"column:status;size:16" json:"status"`
	CreatedBy     string          `gorm:"column:created_by;size:32" json:"created_by"`
	CreatedAt     time.Time       `gorm:"column:created_at" json:"created_at"`
	UpdatedAt     time.Time       `gorm:"column:updated_at" json:"updated_at"`
}

func (Agent) TableName() string { return "agent" }

type Skill struct {
	ID          string          `gorm:"column:id;primaryKey;size:32" json:"id"`
	OrgID       string          `gorm:"column:org_id;size:32" json:"org_id"`
	Name        string          `gorm:"column:name;size:64" json:"name"`
	Description string          `gorm:"column:description;size:1024" json:"description"`
	Meta        json.RawMessage `gorm:"column:meta;type:json" json:"meta"`
	StoragePath string          `gorm:"column:storage_path;size:512" json:"-"`
	ZipSize     int64           `gorm:"column:zip_size" json:"zip_size"`
	Status      string          `gorm:"column:status;size:16" json:"status"`
	CreatedBy   string          `gorm:"column:created_by;size:32" json:"created_by"`
	CreatedAt   time.Time       `gorm:"column:created_at" json:"created_at"`
	UpdatedAt   time.Time       `gorm:"column:updated_at" json:"updated_at"`
}

func (Skill) TableName() string { return "skill" }

type AgentSkill struct {
	AgentID string `gorm:"column:agent_id;primaryKey;size:32" json:"agent_id"`
	SkillID string `gorm:"column:skill_id;primaryKey;size:32" json:"skill_id"`
	Sort    int    `gorm:"column:sort" json:"sort"`
}

func (AgentSkill) TableName() string { return "agent_skill" }

type AgentSubagent struct {
	AgentID    string `gorm:"column:agent_id;primaryKey;size:32" json:"agent_id"`
	SubAgentID string `gorm:"column:sub_agent_id;primaryKey;size:32" json:"sub_agent_id"`
	Sort       int    `gorm:"column:sort" json:"sort"`
}

func (AgentSubagent) TableName() string { return "agent_subagent" }

type Session struct {
	ID            string          `gorm:"column:id;primaryKey;size:32" json:"id"`
	OrgID         string          `gorm:"column:org_id;size:32" json:"org_id"`
	UserID        string          `gorm:"column:user_id;size:32" json:"user_id"`
	AgentID       string          `gorm:"column:agent_id;size:32" json:"agent_id"`
	AgentSnapshot json.RawMessage `gorm:"column:agent_snapshot;type:json" json:"agent_snapshot"`
	Title         string          `gorm:"column:title;size:256" json:"title"`
	TitlePinned   bool            `gorm:"column:title_pinned" json:"title_pinned"`
	ApprovalMode  string          `gorm:"column:approval_mode;size:16" json:"approval_mode"`
	ModelID       string          `gorm:"column:model_id;size:32" json:"model_id"`
	Sandbox       json.RawMessage `gorm:"column:sandbox;type:json" json:"sandbox"`
	LastSeq       int64           `gorm:"column:last_seq" json:"last_seq"`
	Running       bool            `gorm:"column:running" json:"running"`
	UsageTotal    json.RawMessage `gorm:"column:usage_total;type:json" json:"usage_total"`
	Status        string          `gorm:"column:status;size:16" json:"status"`
	LastActiveAt  time.Time       `gorm:"column:last_active_at" json:"last_active_at"`
	CreatedAt     time.Time       `gorm:"column:created_at" json:"created_at"`
	UpdatedAt     time.Time       `gorm:"column:updated_at" json:"updated_at"`
}

func (Session) TableName() string { return "session" }

type SessionEvent struct {
	SessionID string          `gorm:"column:session_id;primaryKey;size:32" json:"session_id"`
	Seq       int64           `gorm:"column:seq;primaryKey" json:"seq"`
	Type      string          `gorm:"column:type;size:64" json:"type"`
	TimeMs    int64           `gorm:"column:time_ms" json:"time_ms"`
	Data      json.RawMessage `gorm:"column:data;type:json" json:"data"`
}

func (SessionEvent) TableName() string { return "session_event" }

type AgentCheckpoint struct {
	ID        string    `gorm:"column:id;primaryKey;size:32" json:"id"`
	SessionID string    `gorm:"column:session_id;size:32" json:"session_id"`
	Data      []byte    `gorm:"column:data;type:mediumblob" json:"-"`
	CreatedAt time.Time `gorm:"column:created_at" json:"created_at"`
}

func (AgentCheckpoint) TableName() string { return "agent_checkpoint" }

type UsageDaily struct {
	Date             time.Time `gorm:"column:date;primaryKey;type:date" json:"date"`
	OrgID            string    `gorm:"column:org_id;primaryKey;size:32" json:"org_id"`
	UserID           string    `gorm:"column:user_id;primaryKey;size:32" json:"user_id"`
	AgentID          string    `gorm:"column:agent_id;primaryKey;size:32" json:"agent_id"`
	ModelID          string    `gorm:"column:model_id;primaryKey;size:32" json:"model_id"`
	Requests         int       `gorm:"column:requests" json:"requests"`
	InputTokens      int64     `gorm:"column:input_tokens" json:"input_tokens"`
	OutputTokens     int64     `gorm:"column:output_tokens" json:"output_tokens"`
	CacheReadTokens  int64     `gorm:"column:cache_read_tokens" json:"cache_read_tokens"`
	CacheWriteTokens int64     `gorm:"column:cache_write_tokens" json:"cache_write_tokens"`
	ReasoningTokens  int64     `gorm:"column:reasoning_tokens" json:"reasoning_tokens"`
}

func (UsageDaily) TableName() string { return "usage_daily" }

type AuditLog struct {
	ID        string          `gorm:"column:id;primaryKey;size:32" json:"id"`
	OrgID     string          `gorm:"column:org_id;size:32" json:"org_id"`
	ActorID   string          `gorm:"column:actor_id;size:32" json:"actor_id"`
	Action    string          `gorm:"column:action;size:64" json:"action"`
	Target    string          `gorm:"column:target;size:128" json:"target"`
	Detail    json.RawMessage `gorm:"column:detail;type:json" json:"detail"`
	CreatedAt time.Time       `gorm:"column:created_at" json:"created_at"`
}

func (AuditLog) TableName() string { return "audit_log" }
