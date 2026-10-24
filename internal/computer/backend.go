package computer

import (
	"context"

	"github.com/ravenmk2/wispjar/internal/sandbox"
)

func (s *Service) ensure(ctx context.Context, spec sandbox.ComputerSpec, policy sandbox.EnsurePolicy) (sandbox.Instance, error) {
	instance, err := s.backend.Ensure(ctx, spec, policy)
	if err != nil {
		return nil, err
	}
	if instance == nil {
		return nil, sandbox.ErrComputerIdentity
	}
	ref := instance.Ref()
	if ref.InstanceID == "" || ref.ComputerID != spec.ComputerID || ref.Generation != spec.Generation || (spec.SandboxID != nil && ref.InstanceID != *spec.SandboxID) {
		return nil, sandbox.ErrComputerIdentity
	}
	return instance, nil
}

func (s *Service) supportsPause() bool {
	_, ok := s.backend.(sandbox.Pausable)
	return ok
}

func (s *Service) pause(ctx context.Context, spec sandbox.ComputerSpec) error {
	backend, ok := s.backend.(sandbox.Pausable)
	if !ok {
		return sandbox.ErrUnsupported
	}
	return backend.Pause(ctx, spec)
}

func (s *Service) renew(ctx context.Context, spec sandbox.ComputerSpec) error {
	// No lease capability means the platform has nothing to renew.
	backend, ok := s.backend.(sandbox.Renewable)
	if !ok {
		return nil
	}
	return backend.Renew(ctx, spec)
}
