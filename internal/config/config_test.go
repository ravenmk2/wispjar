package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestVolumeConfigDefaultsAndValidation(t *testing.T) {
	t.Setenv("WISPJAR_MASTER_KEY", "")
	path := filepath.Join(t.TempDir(), "config.yaml")
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Storage.Volumes.BackendID != "local" || cfg.Storage.Volumes.Namespace != "main" || cfg.Storage.Volumes.DockerEndpoint != "" {
		t.Fatalf("bad defaults: %+v", cfg.Storage.Volumes)
	}
	if cfg.Runtime.MaxParallelTasks != 4 {
		t.Fatalf("parallel task default: %d", cfg.Runtime.MaxParallelTasks)
	}
	if cfg.Storage.Downloads.MaxBytes != 20<<30 || cfg.Storage.Downloads.MaxStagedBytes != 40<<30 {
		t.Fatal("large download defaults missing")
	}
	for _, value := range []string{
		`storage: {volumes: {name_pattern: "wj-{kind}"}}`,
		`storage: {volumes: {name_pattern: "wj-{unknown}-{volume_id}"}}`,
		`storage: {volumes: {namespace: "Main"}}`,
		`storage: {volumes: {backend: "unknown"}}`,
		`storage: {volumes: {backend_id: ""}}`,
		`storage: {volumes: {docker_endpoint: "ftp://localhost"}}`,
		`storage: {volumes: {docker_endpoint: "http://user:pass@localhost"}}`,
		`storage: {volumes: {name_pattern: "{org_id}-{user_id}-{volume_id}"}}`,
		`runtime: {max_parallel_tasks: 0}`,
		`runtime: {max_parallel_tasks: 33}`,
		`storage: {downloads: {max_bytes: 0}}`,
		`storage: {downloads: {max_bytes: 1073741824, max_staged_bytes: 1}}`,
		`storage: {downloads: {ttl: 0s}}`,
	} {
		if err = os.WriteFile(path, []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err = Load(path); err == nil {
			t.Errorf("accepted invalid config %s", value)
		}
	}
	if err = os.WriteFile(path, []byte(`storage: {volumes: {docker_endpoint: "http://127.0.0.1:2375"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = Load(path); err != nil {
		t.Fatal(err)
	}
}

func TestLegacySessionsExplicitlyDisabled(t *testing.T) {
	t.Setenv("WISPJAR_MASTER_KEY", "")
	path := filepath.Join(t.TempDir(), "config.yaml")
	cfg, err := Load(path)
	if err != nil || !cfg.OpenSandbox.LegacySessionsEnabled {
		t.Fatalf("legacy compatibility default: %v %+v", err, cfg)
	}
	if err = os.WriteFile(path, []byte("opensandbox:\n  legacy_sessions_enabled: false\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err = Load(path)
	if err != nil || cfg.OpenSandbox.LegacySessionsEnabled {
		t.Fatalf("explicit false ignored: %v %+v", err, cfg)
	}
}
