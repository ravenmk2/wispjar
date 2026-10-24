package volume

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/ravenmk2/wispjar/internal/id"
	"github.com/ravenmk2/wispjar/internal/store"
	"github.com/ravenmk2/wispjar/internal/volume/naming"
)

type Options struct{ Namespace, NamePattern string }
type Service struct {
	repo     Repository
	provider Provider
	options  Options
}

func NewService(repo Repository, provider Provider, options Options) (*Service, error) {
	if repo == nil || provider == nil {
		return nil, fmt.Errorf("%w: missing volume dependency", ErrUnavailable)
	}
	if err := naming.Validate(options.NamePattern, options.Namespace); err != nil {
		return nil, err
	}
	return &Service{repo: repo, provider: provider, options: options}, nil
}

func validOwner(orgID, userID string) error {
	for _, value := range []string{orgID, userID} {
		if len(value) != 32 {
			return fmt.Errorf("invalid volume owner ID")
		}
		if _, err := hex.DecodeString(value); err != nil {
			return fmt.Errorf("invalid volume owner ID")
		}
	}
	return nil
}

func (s *Service) List(ctx context.Context, orgID, userID string) ([]store.UserVolume, error) {
	if err := validOwner(orgID, userID); err != nil {
		return nil, err
	}
	return s.repo.List(ctx, orgID, userID)
}

// Ensure lazily creates the three intents. Existing names are always read
// from the database, even when the configured naming pattern has changed.
func (s *Service) Ensure(ctx context.Context, orgID, userID string) (mounts []Mount, err error) {
	if err = validOwner(orgID, userID); err != nil {
		return nil, err
	}
	unlock, err := s.repo.Lock(ctx, orgID, userID)
	if err != nil {
		return nil, err
	}
	defer func() {
		err = errors.Join(err, unlock())
		if err != nil {
			mounts = nil
		}
	}()
	installation, err := s.repo.InstallationID(ctx)
	if err != nil {
		return nil, err
	}
	existing, err := s.repo.List(ctx, orgID, userID)
	if err != nil {
		return nil, err
	}
	byKind := map[string]store.UserVolume{}
	for _, v := range existing {
		byKind[v.Kind] = v
	}
	for _, kind := range []string{"storage", "agents", "sessions"} {
		row, ok := byKind[kind]
		if !ok {
			volumeID := id.New()
			name, nameErr := naming.Render(s.options.NamePattern, s.options.Namespace, kind, volumeID, orgID, userID)
			if nameErr != nil {
				return nil, nameErr
			}
			row, err = s.repo.GetOrCreate(ctx, store.UserVolume{ID: volumeID, OrgID: orgID, UserID: userID, Kind: kind,
				BackendName: name, Status: "pending"})
			if err != nil {
				return nil, err
			}
		}
		if err = s.reconcile(ctx, installation, row, true); err != nil {
			return nil, err
		}
		mounts = append(mounts, Mount{Name: kind, ClaimName: row.BackendName, MountPath: "/" + kind})
	}
	return mounts, nil
}

// InspectOwner verifies registered volumes without creating missing intents
// or backend resources. A missing ready volume is durably marked missing.
func (s *Service) InspectOwner(ctx context.Context, orgID, userID string) (rows []store.UserVolume, err error) {
	if err = validOwner(orgID, userID); err != nil {
		return nil, err
	}
	unlock, err := s.repo.Lock(ctx, orgID, userID)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, unlock()) }()
	installation, err := s.repo.InstallationID(ctx)
	if err != nil {
		return nil, err
	}
	rows, err = s.repo.List(ctx, orgID, userID)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		if checkErr := s.reconcile(ctx, installation, row, false); checkErr != nil {
			err = errors.Join(err, checkErr)
		}
	}
	if err != nil {
		return rows, err
	}
	return s.repo.List(ctx, orgID, userID)
}

func (s *Service) reconcile(ctx context.Context, installation string, row store.UserVolume, create bool) error {
	spec := Spec{Name: row.BackendName, Labels: map[string]string{
		"wispjar.managed": "true", "wispjar.installation": installation, "wispjar.volume-id": row.ID,
		"wispjar.org-id": row.OrgID, "wispjar.user-id": row.UserID, "wispjar.kind": row.Kind,
	}}
	switch row.Status {
	case "ready", "missing":
		info, err := s.provider.Inspect(ctx, row.BackendName)
		if errors.Is(err, ErrNotFound) {
			return errors.Join(ErrMissing, s.repo.SetState(ctx, row.ID, "missing", "registered volume is absent"))
		}
		if err != nil {
			return err
		}
		if err = checkIdentity(info, spec); err != nil {
			return err
		}
		// An explicitly restored, identity-matching volume can recover.
		if row.Status == "missing" {
			return s.repo.SetState(ctx, row.ID, "ready", "")
		}
		return nil
	case "pending", "provisioning", "error":
		if !create {
			info, err := s.provider.Inspect(ctx, row.BackendName)
			if err != nil {
				return err
			}
			return checkIdentity(info, spec)
		}
	default:
		return fmt.Errorf("%w: unsupported volume state %q", ErrConflict, row.Status)
	}
	if err := s.repo.SetState(ctx, row.ID, "provisioning", ""); err != nil {
		return err
	}
	info, err := s.provider.Ensure(ctx, spec)
	if err == nil {
		err = checkIdentity(info, spec)
	}
	if err != nil {
		// The external create may have succeeded. Keep the recorded identity
		// and let a retry inspect it; do not generate another resource name.
		return errors.Join(err, s.repo.SetState(ctx, row.ID, "error", err.Error()))
	}
	return s.repo.SetState(ctx, row.ID, "ready", "")
}
