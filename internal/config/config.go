package config

import (
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"time"

	"github.com/ravenmk2/wispjar/internal/volume/naming"
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
		DataDir string        `yaml:"data_dir"`
		Volumes VolumesConfig `yaml:"volumes"`
	} `yaml:"storage"`
	Runtime struct {
		MaxParallelTasks int `yaml:"max_parallel_tasks"`
	} `yaml:"runtime"`
	OpenSandbox struct {
		LegacySessionsEnabled bool   `yaml:"legacy_sessions_enabled"`
		LifecycleURL          string `yaml:"lifecycle_url"`
		APIKey                string `yaml:"api_key"`
		UseServerProxy        bool   `yaml:"use_server_proxy"`
		IdleTTLSeconds        int    `yaml:"idle_ttl_seconds"`
		SandboxTTLSeconds     int    `yaml:"sandbox_ttl_seconds"`
	} `yaml:"opensandbox"`

	AdminPassword string `yaml:"-"`

	masterKey []byte
}

func Load(path string) (*Config, error) {
	cfg := &Config{}
	cfg.Server.Addr = ":8080"
	cfg.Auth.TokenTTL = Duration(24 * time.Hour)
	cfg.Storage.DataDir = "./data"
	cfg.Storage.Volumes = DefaultVolumesConfig()
	cfg.Runtime.MaxParallelTasks = 4
	cfg.OpenSandbox.IdleTTLSeconds = 28800
	cfg.OpenSandbox.SandboxTTLSeconds = 3600
	cfg.OpenSandbox.LegacySessionsEnabled = true

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
	if cfg.Runtime.MaxParallelTasks < 1 || cfg.Runtime.MaxParallelTasks > 32 {
		return nil, errors.New("runtime.max_parallel_tasks must be between 1 and 32")
	}
	if err := cfg.Storage.Volumes.Validate(); err != nil {
		return nil, fmt.Errorf("storage.volumes: %w", err)
	}

	if cfg.Crypto.MasterKey != "" {
		key, err := DecodeMasterKey(cfg.Crypto.MasterKey)
		if err != nil {
			return nil, err
		}
		cfg.masterKey = key
	}
	return cfg, nil
}

type VolumesConfig struct {
	Backend        string `yaml:"backend"`
	BackendID      string `yaml:"backend_id"`
	Namespace      string `yaml:"namespace"`
	NamePattern    string `yaml:"name_pattern"`
	DockerEndpoint string `yaml:"docker_endpoint"`
}

func DefaultVolumesConfig() VolumesConfig {
	return VolumesConfig{Backend: "docker", BackendID: "local", Namespace: "main", NamePattern: naming.DefaultPattern}
}

func (c VolumesConfig) Validate() error {
	if c.BackendID == "" || len(c.BackendID) > 64 {
		return fmt.Errorf("backend_id must contain 1 to 64 characters")
	}
	if c.Backend != "docker" {
		return fmt.Errorf("unsupported backend %q", c.Backend)
	}
	if err := naming.Validate(c.NamePattern, c.Namespace); err != nil {
		return err
	}
	// An absent endpoint preserves legacy startup; resource provisioning reports
	// unavailable instead of silently creating ephemeral computers.
	if c.DockerEndpoint == "" {
		return nil
	}
	u, err := url.Parse(c.DockerEndpoint)
	if err != nil {
		return fmt.Errorf("invalid docker_endpoint: %w", err)
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("docker_endpoint must not contain credentials, query or fragment")
	}
	switch u.Scheme {
	case "unix":
		if u.Host != "" || u.Path == "" {
			return fmt.Errorf("docker_endpoint requires an absolute Unix socket path")
		}
	case "tcp", "http", "https":
		if u.Host == "" || (u.Path != "" && u.Path != "/") {
			return fmt.Errorf("docker_endpoint requires a host and no path")
		}
	default:
		return fmt.Errorf("unsupported docker_endpoint scheme %q", u.Scheme)
	}
	return nil
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
