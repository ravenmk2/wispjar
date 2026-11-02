// Package requestlog stores best-effort HTTP request snapshots independently of conversations.
package requestlog

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/ravenmk2/wispjar/internal/store"
)

const (
	settingKey = "model_request_log"
	maxPayload = int64(32 << 20)
	maxPending = int64(128 << 20)
)

type Config struct {
	Enabled         bool  `json:"enabled"`
	RetentionDays   int   `json:"retention_days"`
	MaxStorageBytes int64 `json:"max_storage_bytes"`
}

type ModelSetting struct {
	ModelID   string    `json:"model_id" gorm:"primaryKey"`
	Enabled   bool      `json:"enabled"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (ModelSetting) TableName() string { return "model_request_log_setting" }

type snapshot struct {
	Config
	Models map[string]bool
}

type Metadata struct {
	ModelName    string `json:"model_name"`
	ModelCode    string `json:"model_code"`
	ProviderName string `json:"provider_name"`
	ProviderType string `json:"provider_type"`
	Protocol     string `json:"api_protocol"`
	Method       string `json:"method"`
	URL          string `json:"url"`
	ContentType  string `json:"content_type"`
}

type Record struct {
	ID            string          `json:"id" gorm:"primaryKey"`
	StartedAt     time.Time       `json:"started_at"`
	ModelID       string          `json:"model_id"`
	ProviderID    string          `json:"provider_id"`
	Status        string          `json:"status"`
	PayloadPath   string          `json:"payload_path"`
	PayloadBytes  int64           `json:"payload_bytes"`
	PayloadSHA256 string          `json:"payload_sha256"`
	Metadata      json.RawMessage `json:"metadata" gorm:"type:json"`
	ErrorDetail   json.RawMessage `json:"error_detail,omitempty" gorm:"type:json"`
	UpdatedAt     time.Time       `json:"updated_at"`
}

func (Record) TableName() string { return "model_request_log" }

type entry struct {
	record   Record
	body     []byte
	reserved int64
}

type Service struct {
	db                *gorm.DB
	root              string
	config            atomic.Pointer[snapshot]
	configMu          sync.Mutex
	pending           atomic.Int64
	droppedFull       atomic.Uint64
	droppedLarge      atomic.Uint64
	droppedIncomplete atomic.Uint64
	failures          atomic.Uint64
	saved             atomic.Uint64
	closed            atomic.Bool
	enqueueMu         sync.RWMutex
	queue             chan entry
	clear             chan time.Time
	clearing          atomic.Bool
	done              chan struct{}
	clearedAt         time.Time // worker-owned cutoff includes captures still in flight
}

func New(db *gorm.DB, dataDir string) (*Service, error) {
	s := &Service{db: db, root: filepath.Join(dataDir, "model-request-logs"), queue: make(chan entry, 128), clear: make(chan time.Time, 1), done: make(chan struct{})}
	snap := &snapshot{Config: Config{RetentionDays: 7, MaxStorageBytes: 5 << 30}, Models: map[string]bool{}}
	var setting store.PlatformSetting
	err := db.First(&setting, "`key` = ?", settingKey).Error
	if err == nil {
		if err = json.Unmarshal(setting.Value, &snap.Config); err != nil {
			return nil, err
		}
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	if err = validateConfig(snap.Config); err != nil {
		return nil, err
	}
	var models []ModelSetting
	if err = db.Find(&models).Error; err != nil {
		return nil, err
	}
	for _, m := range models {
		snap.Models[m.ModelID] = m.Enabled
	}
	s.config.Store(snap)
	go s.run()
	return s, nil
}

func validateConfig(c Config) error {
	if c.RetentionDays < 1 || c.RetentionDays > 90 || c.MaxStorageBytes < 1<<20 || c.MaxStorageBytes > 100<<30 {
		return errors.New("保留时间须为 1–90 天，容量须为 1 MiB–100 GiB")
	}
	return nil
}

func (s *Service) enabled(modelID string) bool {
	if s == nil || s.closed.Load() {
		return false
	}
	snap := s.config.Load()
	return snap != nil && snap.Enabled && snap.Models[modelID]
}

func (s *Service) SetConfig(ctx context.Context, c Config, actor string) error {
	if err := validateConfig(c); err != nil {
		return err
	}
	s.configMu.Lock()
	defer s.configMu.Unlock()
	raw, err := json.Marshal(c)
	if err != nil {
		return err
	}
	row := store.PlatformSetting{Key: settingKey, Value: raw, UpdatedBy: actor, UpdatedAt: time.Now()}
	if err = s.db.WithContext(ctx).Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "key"}}, DoUpdates: clause.AssignmentColumns([]string{"value", "updated_by", "updated_at"})}).Create(&row).Error; err != nil {
		return err
	}
	old := s.config.Load()
	s.config.Store(&snapshot{Config: c, Models: old.Models})
	return nil
}

func (s *Service) SetModel(ctx context.Context, modelID string, enabled bool) error {
	s.configMu.Lock()
	defer s.configMu.Unlock()
	var m store.Model
	if err := s.db.WithContext(ctx).Select("id").First(&m, "id = ?", modelID).Error; err != nil {
		return err
	}
	row := ModelSetting{ModelID: modelID, Enabled: enabled, UpdatedAt: time.Now()}
	if err := s.db.WithContext(ctx).Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "model_id"}}, DoUpdates: clause.AssignmentColumns([]string{"enabled", "updated_at"})}).Create(&row).Error; err != nil {
		return err
	}
	old := s.config.Load()
	models := make(map[string]bool, len(old.Models)+1)
	for k, v := range old.Models {
		models[k] = v
	}
	models[modelID] = enabled
	s.config.Store(&snapshot{Config: old.Config, Models: models})
	return nil
}

func (s *Service) reserve(n int64) bool {
	for {
		old := s.pending.Load()
		if old+n > maxPending {
			return false
		}
		if s.pending.CompareAndSwap(old, old+n) {
			return true
		}
	}
}

func (s *Service) enqueue(e entry) {
	s.enqueueMu.RLock()
	defer s.enqueueMu.RUnlock()
	if !s.closed.Load() {
		select {
		case s.queue <- e:
			return
		default:
		}
	}
	s.pending.Add(-e.reserved)
	s.droppedFull.Add(1)
}

// Close stops accepting captures and drains the queue within the caller's deadline.
func (s *Service) Close(ctx context.Context) error {
	s.enqueueMu.Lock()
	if s.closed.CompareAndSwap(false, true) {
		close(s.queue)
	}
	s.enqueueMu.Unlock()
	select {
	case <-s.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Service) run() {
	defer close(s.done)
	s.maintenance(true)
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case e, ok := <-s.queue:
			if !ok {
				return
			}
			if e.record.StartedAt.After(s.clearedAt) {
				if err := s.persist(e); err != nil {
					s.failures.Add(1)
					log.Printf("requestlog: persist failed: %v", err)
				} else {
					s.saved.Add(1)
				}
			}
			s.pending.Add(-e.reserved)
		case cutoff := <-s.clear:
			s.clearedAt = cutoff
			if err := s.markAndDelete("started_at <= ?", cutoff); err != nil {
				s.failures.Add(1)
				log.Printf("requestlog: clear failed: %v", err)
			}
			s.clearing.Store(false)
		case <-ticker.C:
			s.maintenance(true)
		}
	}
}
