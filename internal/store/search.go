package store

import "time"

type SearchProvider struct {
	ID        string    `gorm:"primaryKey;size:32" json:"id"`
	Name      string    `json:"name"`
	Type      string    `json:"type"`
	BaseURL   string    `json:"base_url"`
	APIKeyEnc []byte    `json:"-"`
	Status    string    `json:"status"`
	CreatedBy string    `json:"-"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (SearchProvider) TableName() string { return "search_provider" }

type SearchConfig struct {
	ID                int     `gorm:"primaryKey" json:"-"`
	DefaultProviderID *string `json:"default_provider_id"`
}

func (SearchConfig) TableName() string { return "search_config" }

// SearchCall is separate from token usage. It deliberately contains no query,
// upstream error body, key or foreign key to the deletable provider instance.
type SearchCall struct {
	ID          string `gorm:"primaryKey;size:32"`
	ProviderID  string
	OrgID       string
	UserID      string
	AgentID     string
	SessionID   string
	CallID      string
	Success     bool
	ResultCount int
	DurationMS  int64
	Credits     *float64
	ErrorCode   string
	CreatedAt   time.Time
}

func (SearchCall) TableName() string { return "search_call" }
