package store

import (
	"encoding/json"
	"fmt"
	"time"

	"gorm.io/gorm"
)

type Computer struct {
	ID           string          `gorm:"column:id;primaryKey;size:32" json:"id"`
	OrgID        string          `gorm:"column:org_id;size:32" json:"org_id"`
	UserID       string          `gorm:"column:user_id;size:32" json:"user_id"`
	Name         string          `gorm:"column:name;size:128" json:"name"`
	SandboxID    *string         `gorm:"column:sandbox_id;size:128" json:"sandbox_id"`
	Generation   uint64          `gorm:"column:generation" json:"generation"`
	Status       string          `gorm:"column:status;size:16" json:"status"`
	Config       json.RawMessage `gorm:"column:config;type:json" json:"config"`
	LastActiveAt *time.Time      `gorm:"column:last_active_at" json:"last_active_at"`
	LastError    string          `gorm:"column:last_error;type:text" json:"last_error"`
	CreatedAt    time.Time       `json:"created_at"`
	UpdatedAt    time.Time       `json:"updated_at"`
}

func (Computer) TableName() string            { return "computer" }
func (m *Computer) BeforeSave(*gorm.DB) error { return jsonDefaults(&m.Config) }

type UserVolume struct {
	ID          string          `gorm:"column:id;primaryKey;size:32" json:"id"`
	OrgID       string          `gorm:"column:org_id;size:32" json:"org_id"`
	UserID      string          `gorm:"column:user_id;size:32" json:"user_id"`
	Kind        string          `gorm:"column:kind;size:16" json:"kind"`
	BackendName string          `gorm:"column:backend_name;size:63" json:"-"`
	Status      string          `gorm:"column:status;size:16" json:"status"`
	Policy      json.RawMessage `gorm:"column:policy;type:json" json:"policy"`
	Usage       json.RawMessage `gorm:"column:usage;type:json" json:"usage"`
	LastError   string          `gorm:"column:last_error;type:text" json:"last_error"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
}

func (UserVolume) TableName() string            { return "user_volume" }
func (m *UserVolume) BeforeSave(*gorm.DB) error { return jsonDefaults(&m.Policy, &m.Usage) }

type UserAgentBinding struct {
	OrgID      string    `gorm:"column:org_id;primaryKey;size:32" json:"org_id"`
	UserID     string    `gorm:"column:user_id;primaryKey;size:32" json:"user_id"`
	AgentID    string    `gorm:"column:agent_id;primaryKey;size:32" json:"agent_id"`
	ComputerID string    `gorm:"column:computer_id;size:32" json:"computer_id"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

func (UserAgentBinding) TableName() string { return "user_agent_binding" }

type UserSkillState struct {
	OrgID           string     `gorm:"column:org_id;primaryKey;size:32" json:"org_id"`
	UserID          string     `gorm:"column:user_id;primaryKey;size:32" json:"user_id"`
	SkillID         string     `gorm:"column:skill_id;primaryKey;size:32" json:"skill_id"`
	DesiredRevision uint64     `json:"desired_revision"`
	AppliedRevision uint64     `json:"applied_revision"`
	Status          string     `gorm:"size:16" json:"status"`
	LockOwner       string     `gorm:"size:128" json:"-"`
	LockUntil       *time.Time `json:"lock_until"`
	Fence           uint64     `json:"fence"`
	LastError       string     `gorm:"type:text" json:"last_error"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

func (UserSkillState) TableName() string { return "user_skill_state" }

type ComputerRun struct {
	ID          string          `gorm:"column:id;primaryKey;size:32" json:"id"`
	OrgID       string          `gorm:"size:32" json:"org_id"`
	UserID      string          `gorm:"size:32" json:"user_id"`
	ComputerID  string          `gorm:"size:32" json:"computer_id"`
	SessionID   string          `gorm:"size:32" json:"session_id"`
	ParentRunID *string         `gorm:"size:32" json:"parent_run_id"`
	AgentID     string          `gorm:"size:32" json:"agent_id"`
	Status      string          `gorm:"size:16" json:"status"`
	Owner       string          `gorm:"size:128" json:"-"`
	LeaseUntil  *time.Time      `json:"lease_until"`
	Execution   json.RawMessage `gorm:"type:json" json:"execution"`
	Manifest    json.RawMessage `gorm:"type:json" json:"manifest"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
}

func (ComputerRun) TableName() string            { return "computer_run" }
func (m *ComputerRun) BeforeSave(*gorm.DB) error { return jsonDefaults(&m.Execution, &m.Manifest) }

type RunSkillLease struct {
	RunID     string    `gorm:"column:run_id;primaryKey;size:32" json:"run_id"`
	OrgID     string    `gorm:"size:32" json:"org_id"`
	UserID    string    `gorm:"size:32" json:"user_id"`
	SkillID   string    `gorm:"column:skill_id;primaryKey;size:32" json:"skill_id"`
	Revision  uint64    `json:"revision"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (RunSkillLease) TableName() string { return "run_skill_lease" }

type ResourceOperation struct {
	ID             string          `gorm:"column:id;primaryKey;size:32" json:"id"`
	OrgID          string          `gorm:"size:32" json:"org_id"`
	UserID         string          `gorm:"size:32" json:"user_id"`
	ResourceType   string          `gorm:"size:32" json:"resource_type"`
	ResourceID     string          `gorm:"size:32" json:"resource_id"`
	Action         string          `gorm:"size:64" json:"action"`
	Status         string          `gorm:"size:16" json:"status"`
	IdempotencyKey *string         `gorm:"size:128" json:"-"`
	RequestHash    string          `gorm:"size:64" json:"-"`
	Progress       json.RawMessage `gorm:"type:json" json:"progress"`
	Error          string          `gorm:"type:text" json:"error"`
	LeaseUntil     *time.Time      `json:"lease_until"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
}

func (ResourceOperation) TableName() string            { return "resource_operation" }
func (m *ResourceOperation) BeforeSave(*gorm.DB) error { return jsonDefaults(&m.Progress) }

func jsonDefaults(values ...*json.RawMessage) error {
	for _, value := range values {
		if len(*value) == 0 {
			*value = json.RawMessage(`{}`)
		}
		if !json.Valid(*value) {
			return fmt.Errorf("invalid JSON model field")
		}
	}
	return nil
}
