package sandbox

import (
	"context"
	"sync"
	"time"

	"gorm.io/gorm"
)

type SettingsSource interface {
	Load(ctx context.Context) (Settings, error)
}

type dbSettingsSource struct{ db *gorm.DB }

func NewDBSettingsSource(db *gorm.DB) SettingsSource { return dbSettingsSource{db: db} }

func (s dbSettingsSource) Load(ctx context.Context) (Settings, error) {
	return LoadSettings(ctx, s.db)
}

type staticSettingsSource struct{ s Settings }

func StaticSettings(s Settings) SettingsSource { return staticSettingsSource{s: s} }

func (s staticSettingsSource) Load(context.Context) (Settings, error) { return s.s, nil }

type cachedSettingsSource struct {
	src SettingsSource
	ttl time.Duration

	mu      sync.Mutex
	cached  Settings
	expires time.Time
	loaded  bool
}

func NewCachedSettingsSource(src SettingsSource, ttl time.Duration) SettingsSource {
	if ttl <= 0 {
		ttl = 30 * time.Second
	}
	return &cachedSettingsSource{src: src, ttl: ttl}
}

func (s *cachedSettingsSource) Load(ctx context.Context) (Settings, error) {
	s.mu.Lock()
	if s.loaded && time.Now().Before(s.expires) {
		c := s.cached
		s.mu.Unlock()
		return c, nil
	}
	s.mu.Unlock()
	st, err := s.src.Load(ctx)
	if err != nil {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.loaded {
			return s.cached, nil
		}
		return Settings{}, err
	}
	s.mu.Lock()
	s.cached = st
	s.expires = time.Now().Add(s.ttl)
	s.loaded = true
	s.mu.Unlock()
	return st, nil
}
