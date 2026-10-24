package computer

import (
	"context"
	"errors"
	"testing"

	"github.com/ravenmk2/wispjar/internal/sandbox"
)

// Deliberately hides optional interfaces from the underlying backend. This
// verifies that a second backend need not implement no-op pause/renew methods.
type coreOnlyBackend struct{ sandbox.Backend }

func TestBackendOptionalCapabilities(t *testing.T) {
	b := coreOnlyBackend{Backend: &memoryBackend{}}
	s := &Service{backend: b}
	if s.supportsPause() {
		t.Fatal("pause advertised without capability")
	}
	if err := s.pause(context.Background(), sandbox.ComputerSpec{}); !errors.Is(err, sandbox.ErrUnsupported) {
		t.Fatalf("pause=%v", err)
	}
	if err := s.renew(context.Background(), sandbox.ComputerSpec{}); err != nil {
		t.Fatalf("lease-free backend must not be renewed: %v", err)
	}
	if _, err := s.Maintainer(memoryInstance{}); !errors.Is(err, sandbox.ErrUnsupported) {
		t.Fatalf("implicit maintenance fallback: %v", err)
	}
}

type identityBackend struct {
	sandbox.Backend
	instance sandbox.Instance
}

func (b identityBackend) Ensure(context.Context, sandbox.ComputerSpec, sandbox.EnsurePolicy) (sandbox.Instance, error) {
	return b.instance, nil
}

func TestServiceRejectsBackendIdentityMismatch(t *testing.T) {
	id := "existing"
	spec := sandbox.ComputerSpec{ComputerID: "computer", Generation: 2, SandboxID: &id}
	valid := sandbox.InstanceRef{ComputerID: "computer", Generation: 2, InstanceID: id}
	for _, field := range []string{"computer", "generation", "instance", "empty", "nil", "valid"} {
		t.Run(field, func(t *testing.T) {
			ref := valid
			switch field {
			case "computer":
				ref.ComputerID = "other"
			case "generation":
				ref.Generation++
			case "instance":
				ref.InstanceID = "replacement"
			case "empty":
				ref.InstanceID = ""
			}
			var instance sandbox.Instance = memoryInstance{ref: ref}
			if field == "nil" {
				instance = nil
			}
			s := &Service{backend: identityBackend{instance: instance}}
			_, err := s.ensure(context.Background(), spec, sandbox.EnsurePolicy{})
			if field == "valid" {
				if err != nil {
					t.Fatal(err)
				}
			} else if !errors.Is(err, sandbox.ErrComputerIdentity) {
				t.Fatalf("unexpected result: %v", err)
			}
		})
	}
}
