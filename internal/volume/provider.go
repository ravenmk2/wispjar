package volume

import (
	"context"
	"errors"
	"fmt"
)

var (
	ErrNotFound    = errors.New("volume/not_found")
	ErrMissing     = errors.New("volume/missing")
	ErrConflict    = errors.New("volume/conflict")
	ErrUnavailable = errors.New("volume/unavailable")
)

type Spec struct {
	Name   string
	Labels map[string]string
}

type Info struct {
	Name   string
	Labels map[string]string
}

// Provider owns no resource deletion policy. Ensure must refuse a resource
// whose identity labels differ, including after an ambiguous create response.
type Provider interface {
	Ensure(context.Context, Spec) (Info, error)
	Inspect(context.Context, string) (Info, error)
}

type Mount struct {
	Name      string
	ClaimName string
	MountPath string
	BackendID string
}

func checkIdentity(info Info, spec Spec) error {
	if info.Name != spec.Name {
		return fmt.Errorf("%w: backend returned another volume", ErrConflict)
	}
	for k, v := range spec.Labels {
		if info.Labels[k] != v {
			return fmt.Errorf("%w: identity label %s differs", ErrConflict, k)
		}
	}
	return nil
}
