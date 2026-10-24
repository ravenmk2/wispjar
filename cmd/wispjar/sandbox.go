package main

import (
	"fmt"

	"github.com/ravenmk2/wispjar/internal/config"
	"github.com/ravenmk2/wispjar/internal/sandbox"
	"github.com/ravenmk2/wispjar/internal/sandbox/opensandbox"
)

// Select one implementation at startup and inject that same backend everywhere.
// Future implementations add a case here, not a per-computer backend registry.
func newSandboxBackend(cfg *config.Config) (sandbox.Backend, error) {
	switch cfg.Sandbox.Backend {
	case "opensandbox":
		// No URL means the sandbox service is disabled.
		if cfg.OpenSandbox.LifecycleURL == "" {
			return nil, nil
		}
		return opensandbox.NewComputerBackend(opensandbox.NewClient(cfg.OpenSandbox.LifecycleURL, cfg.OpenSandbox.APIKey, cfg.OpenSandbox.UseServerProxy)), nil
	default:
		return nil, fmt.Errorf("unsupported sandbox backend %q", cfg.Sandbox.Backend)
	}
}
