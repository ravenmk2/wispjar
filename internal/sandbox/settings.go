package sandbox

import (
	"context"
	"encoding/json"

	"github.com/ravenmk2/wispjar/internal/store"
	"gorm.io/gorm"
)

const (
	KeyDefaultImage   = "sandbox.default_image"
	KeyImages         = "sandbox.images"
	KeyEgressPolicy   = "sandbox.egress_policy"
	KeyResourceLimits = "sandbox.resource_limits"
	KeyEnv            = "sandbox.env"
	KeyIdleTTLSeconds = "sandbox.idle_ttl_seconds"
	KeyTTLSeconds     = "sandbox.ttl_seconds"
)

type Settings struct {
	DefaultImage   string            `json:"default_image"`
	Images         []string          `json:"images"`
	EgressPolicy   json.RawMessage   `json:"egress_policy"`
	ResourceLimits map[string]string `json:"resource_limits"`
	Env            map[string]string `json:"env"`
	IdleTTLSeconds int               `json:"idle_ttl_seconds"`
	TTLSeconds     int               `json:"ttl_seconds"`
}

func DefaultSettings() Settings {
	return Settings{
		DefaultImage:   "python:3.12",
		Images:         []string{"python:3.12"},
		EgressPolicy:   json.RawMessage(`{"defaultAction":"allow","rules":[]}`),
		ResourceLimits: map[string]string{"cpu": "1", "memory": "1Gi"},
		Env:            map[string]string{},
		IdleTTLSeconds: 28800,
		TTLSeconds:     3600,
	}
}

func LoadSettings(ctx context.Context, db *gorm.DB) (Settings, error) {
	s := DefaultSettings()
	var rows []store.PlatformSetting
	err := db.WithContext(ctx).
		Where("`key` IN ?", []string{
			KeyDefaultImage, KeyImages, KeyEgressPolicy,
			KeyResourceLimits, KeyEnv, KeyIdleTTLSeconds, KeyTTLSeconds,
		}).
		Find(&rows).Error
	if err != nil {
		return s, err
	}
	for _, row := range rows {
		switch row.Key {
		case KeyDefaultImage:
			_ = json.Unmarshal(row.Value, &s.DefaultImage)
		case KeyImages:
			_ = json.Unmarshal(row.Value, &s.Images)
		case KeyEgressPolicy:
			if json.Valid(row.Value) {
				s.EgressPolicy = row.Value
			}
		case KeyResourceLimits:
			_ = json.Unmarshal(row.Value, &s.ResourceLimits)
		case KeyEnv:
			_ = json.Unmarshal(row.Value, &s.Env)
		case KeyIdleTTLSeconds:
			_ = json.Unmarshal(row.Value, &s.IdleTTLSeconds)
		case KeyTTLSeconds:
			_ = json.Unmarshal(row.Value, &s.TTLSeconds)
		}
	}
	return s, nil
}

func (s Settings) Options(imageOverride string, resources map[string]string) Options {
	o := Options{
		Image:          s.DefaultImage,
		TTLSeconds:     s.TTLSeconds,
		ResourceLimits: s.ResourceLimits,
		NetworkPolicy:  s.EgressPolicy,
	}
	if imageOverride != "" {
		o.Image = imageOverride
	}
	if len(resources) > 0 {
		o.ResourceLimits = resources
	}
	return o
}
