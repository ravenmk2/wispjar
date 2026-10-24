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
	if cfg.Storage.Volumes.Namespace != "main" || cfg.Storage.Volumes.DockerEndpoint != "" {
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

func TestSingleSandboxBackendConfiguration(t *testing.T) {
	t.Setenv("WISPJAR_MASTER_KEY", "")
	t.Setenv("WISPJAR_SANDBOX_KEY", "test-env-key")
	for _, tc := range []struct {
		name, body string
		invalid    bool
	}{
		{"legacy", "opensandbox: {lifecycle_url: 'http://localhost:8081', use_server_proxy: true}", false},
		{"explicit", "sandbox: {backend: opensandbox}\nopensandbox: {lifecycle_url: 'http://localhost:8081', use_server_proxy: true}", false},
		{"unknown", "sandbox: {backend: unknown}", true},
		{"empty", "sandbox: {backend: ''}", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yaml")
			if err := os.WriteFile(path, []byte(tc.body), 0600); err != nil {
				t.Fatal(err)
			}
			cfg, err := Load(path)
			if tc.invalid {
				if err == nil {
					t.Fatal("accepted unsupported backend")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Sandbox.Backend != "opensandbox" || cfg.OpenSandbox.LifecycleURL != "http://localhost:8081" || !cfg.OpenSandbox.UseServerProxy || cfg.OpenSandbox.APIKey != "test-env-key" {
				t.Fatal("legacy provider configuration or environment override changed")
			}
		})
	}
}
