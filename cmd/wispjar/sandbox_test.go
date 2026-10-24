package main

import (
	"testing"

	"github.com/ravenmk2/wispjar/internal/config"
	"github.com/ravenmk2/wispjar/internal/sandbox"
)

func TestSandboxBackendSelection(t *testing.T) {
	cfg := &config.Config{}
	cfg.Sandbox.Backend = "opensandbox"
	b, err := newSandboxBackend(cfg)
	if err != nil || b != nil {
		t.Fatalf("empty URL should keep sandbox disabled: %v %v", b, err)
	}
	cfg.OpenSandbox.LifecycleURL = "http://localhost:8081"
	b, err = newSandboxBackend(cfg)
	if err != nil || b == nil {
		t.Fatalf("configured backend missing: %v", err)
	}
	if _, ok := b.(sandbox.MaintenanceProvider); !ok {
		t.Fatal("configured backend missing maintenance")
	}
	if _, ok := b.(sandbox.Pausable); !ok {
		t.Fatal("configured backend missing pause")
	}
	if _, ok := b.(sandbox.Renewable); !ok {
		t.Fatal("configured backend missing renewal")
	}
	cfg.Sandbox.Backend = "unknown"
	if b, err = newSandboxBackend(cfg); err == nil || b != nil {
		t.Fatal("unknown backend silently selected OpenSandbox")
	}
}
