package config

import (
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

type Duration time.Duration

func (d *Duration) UnmarshalYAML(value *yaml.Node) error {
	var s string
	if err := value.Decode(&s); err != nil {
		return err
	}
	v, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", s, err)
	}
	*d = Duration(v)
	return nil
}

type Config struct {
	Server struct {
		Addr           string   `yaml:"addr"`
		AllowedOrigins []string `yaml:"allowed_origins"`
	} `yaml:"server"`
	Database struct {
		DSN string `yaml:"dsn"`
	} `yaml:"database"`
	Auth struct {
		JWTSecret string   `yaml:"jwt_secret"`
		TokenTTL  Duration `yaml:"token_ttl"`
	} `yaml:"auth"`
	Crypto struct {
		MasterKey string `yaml:"master_key"`
	} `yaml:"crypto"`
	Storage struct {
		DataDir string `yaml:"data_dir"`
	} `yaml:"storage"`
	OpenSandbox struct {
		LifecycleURL      string `yaml:"lifecycle_url"`
		APIKey            string `yaml:"api_key"`
		IdleTTLSeconds    int    `yaml:"idle_ttl_seconds"`
		SandboxTTLSeconds int    `yaml:"sandbox_ttl_seconds"`
	} `yaml:"opensandbox"`

	AdminPassword string `yaml:"-"`

	masterKey []byte
}

func Load(path string) (*Config, error) {
	cfg := &Config{}
	cfg.Server.Addr = ":8080"
	cfg.Auth.TokenTTL = Duration(24 * time.Hour)
	cfg.Storage.DataDir = "./data"
	cfg.OpenSandbox.IdleTTLSeconds = 1800
	cfg.OpenSandbox.SandboxTTLSeconds = 3600

	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		if err := yaml.Unmarshal(data, cfg); err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
	case errors.Is(err, os.ErrNotExist):
	default:
		return nil, err
	}

	if v := os.Getenv("WISPJAR_ADDR"); v != "" {
		cfg.Server.Addr = v
	}
	if v := os.Getenv("WISPJAR_JWT_SECRET"); v != "" {
		cfg.Auth.JWTSecret = v
	}
	if v := os.Getenv("WISPJAR_MASTER_KEY"); v != "" {
		cfg.Crypto.MasterKey = v
	}
	if v := os.Getenv("WISPJAR_SANDBOX_KEY"); v != "" {
		cfg.OpenSandbox.APIKey = v
	}
	cfg.AdminPassword = os.Getenv("WISPJAR_ADMIN_PASSWORD")

	if cfg.Crypto.MasterKey != "" {
		key, err := DecodeMasterKey(cfg.Crypto.MasterKey)
		if err != nil {
			return nil, err
		}
		cfg.masterKey = key
	}
	return cfg, nil
}

func (c *Config) TokenTTL() time.Duration {
	return time.Duration(c.Auth.TokenTTL)
}

func (c *Config) MasterKey() []byte {
	return c.masterKey
}

func DecodeMasterKey(s string) ([]byte, error) {
	if b, err := base64.StdEncoding.DecodeString(s); err == nil && len(b) == 32 {
		return b, nil
	}
	if b, err := hex.DecodeString(s); err == nil && len(b) == 32 {
		return b, nil
	}
	return nil, errors.New("crypto.master_key must be 32 bytes, base64 or hex encoded")
}
