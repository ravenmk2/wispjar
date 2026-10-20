package computer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"strings"
	"sync"
	"time"

	"github.com/ravenmk2/wispjar/internal/id"
	"github.com/ravenmk2/wispjar/internal/sandbox"
	"github.com/ravenmk2/wispjar/internal/store"
)

type Service struct {
	repo                    Repository
	volumes                 Volumes
	backend                 Backend
	installation, backendID string
	settings                sandbox.SettingsSource
	cancel                  context.CancelFunc
	wg                      sync.WaitGroup
	wake                    chan struct{}
	hookMu                  sync.RWMutex
	onChange                func(store.Computer)
	onRecovery              func(context.Context, store.Computer) error
}

// SetRecoveryHandler reconciles runtime and skill state after the old instance
// is proven absent and runs are terminal, while the computer is still fenced.
// Configure before Start. A missing handler keeps recovery fenced.
func (s *Service) SetRecoveryHandler(fn func(context.Context, store.Computer) error) {
	s.onRecovery = fn
}

func (s *Service) Recover(ctx context.Context, org, user, computerID, key string, in RecoverInput) (store.ResourceOperation, error) {
	if !in.ConfirmStopAll {
		return store.ResourceOperation{}, ErrInvalid
	}
	c, err := s.GetOwned(ctx, org, user, computerID)
	if err != nil {
		return store.ResourceOperation{}, err
	}
	if err = s.available(); err != nil {
		return store.ResourceOperation{}, err
	}
	op, err := newOperation(c.OrgID, user, c.ID, "recover", key, struct {
		Computer string       `json:"computer_id"`
		Input    RecoverInput `json:"input"`
	}{c.ID, in})
	if err != nil {
		return op, err
	}
	op, err = s.repo.Intent(ctx, nil, op)
	if err == nil {
		s.changed(ctx, c.ID)
		s.notify()
	}
	return op, err
}

func (s *Service) SetOnChange(fn func(store.Computer)) {
	s.hookMu.Lock()
	defer s.hookMu.Unlock()
	s.onChange = fn
}
func (s *Service) changed(ctx context.Context, computerID string) {
	s.hookMu.RLock()
	fn := s.onChange
	s.hookMu.RUnlock()
	if fn == nil {
		return
	}
	if row, err := s.repo.Get(ctx, computerID); err == nil {
		fn(row)
	}
}

func NewService(repo Repository, volumes Volumes, backend Backend, installation, backendID string, settings sandbox.SettingsSource) *Service {
	return &Service{repo: repo, volumes: volumes, backend: backend, installation: installation, backendID: backendID, settings: settings, wake: make(chan struct{}, 1)}
}
func (s *Service) available() error {
	if s.backend == nil || s.volumes == nil || s.installation == "" {
		return ErrUnavailable
	}
	return nil
}
func (s *Service) GetOwned(ctx context.Context, org, user, id string) (store.Computer, error) {
	c, err := s.repo.Get(ctx, id)
	if err != nil {
		return c, err
	}
	if c.UserID != user || (org != "" && c.OrgID != org) || c.Status == "deleted" {
		return store.Computer{}, ErrNotFound
	}
	if err = s.repo.Member(ctx, c.OrgID, user); err != nil {
		return store.Computer{}, err
	}
	return c, nil
}
func (s *Service) List(ctx context.Context, org, user string) ([]store.Computer, error) {
	if err := s.repo.Member(ctx, org, user); err != nil {
		return nil, err
	}
	return s.repo.List(ctx, org, user)
}
func (s *Service) Bindings(ctx context.Context, org, user string) ([]store.UserAgentBinding, error) {
	if err := s.repo.Member(ctx, org, user); err != nil {
		return nil, err
	}
	return s.repo.Bindings(ctx, org, user)
}
func (s *Service) Bind(ctx context.Context, org, user, agent, computer string) error {
	if _, err := s.GetOwned(ctx, org, user, computer); err != nil {
		return err
	}
	if _, err := s.repo.Template(ctx, org, agent); err != nil {
		return err
	}
	return s.repo.Bind(ctx, store.UserAgentBinding{OrgID: org, UserID: user, AgentID: agent, ComputerID: computer})
}
func (s *Service) Unbind(ctx context.Context, org, user, agent string) error {
	if err := s.repo.Member(ctx, org, user); err != nil {
		return err
	}
	return s.repo.Unbind(ctx, org, user, agent)
}
func (s *Service) ResolveBinding(ctx context.Context, org, user, agent, explicit string) (store.Computer, error) {
	if explicit != "" {
		return s.GetOwned(ctx, org, user, explicit)
	}
	rows, err := s.Bindings(ctx, org, user)
	if err != nil {
		return store.Computer{}, err
	}
	for _, b := range rows {
		if b.AgentID == agent {
			return s.GetOwned(ctx, org, user, b.ComputerID)
		}
	}
	return store.Computer{}, ErrRequired
}
func (s *Service) resolvedConfig(ctx context.Context, org, agent string, explicit Config) (Config, error) {
	if len(explicit.NetworkPolicy) > 0 {
		return Config{}, fmt.Errorf("%w: network policy is platform managed", ErrInvalid)
	}
	st := sandbox.DefaultSettings()
	if s.settings != nil {
		var err error
		st, err = s.settings.Load(ctx)
		if err != nil {
			return Config{}, err
		}
	}
	c := Config{Image: st.DefaultImage, Resources: st.ResourceLimits, Env: st.Env, TTLSeconds: st.TTLSeconds, NetworkPolicy: st.EgressPolicy}
	if agent != "" {
		tmpl, err := s.repo.Template(ctx, org, agent)
		if err != nil {
			return c, err
		}
		c = mergeConfig(c, tmpl)
	}
	c = mergeConfig(c, explicit)
	c.NetworkPolicy = st.EgressPolicy
	if !allowedImage(st, c.Image) {
		return c, fmt.Errorf("%w: image is not enabled", ErrInvalid)
	}
	return c, validateConfig(c)
}
func allowedImage(settings sandbox.Settings, image string) bool {
	if image == settings.DefaultImage {
		return true
	}
	for _, allowed := range settings.Images {
		if image == allowed {
			return true
		}
	}
	return false
}
func hashRequest(value any) string {
	raw, _ := json.Marshal(value)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
func newOperation(org, user, resource, action, key string, request any) (store.ResourceOperation, error) {
	if len(key) > 128 || strings.ContainsAny(key, "\r\n\x00") {
		return store.ResourceOperation{}, ErrInvalid
	}
	op := store.ResourceOperation{ID: id.New(), OrgID: org, UserID: user, ResourceType: "computer", ResourceID: resource, Action: "computer/" + action, Status: "pending", RequestHash: hashRequest(request)}
	if key != "" {
		op.IdempotencyKey = &key
	}
	return op, nil
}
func (s *Service) notify() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}
func (s *Service) Create(ctx context.Context, org, user, key string, input CreateInput) (store.ResourceOperation, error) {
	if err := s.repo.Member(ctx, org, user); err != nil {
		return store.ResourceOperation{}, err
	}
	if err := s.available(); err != nil {
		return store.ResourceOperation{}, err
	}
	if strings.TrimSpace(input.Name) == "" || len([]rune(input.Name)) > 128 {
		return store.ResourceOperation{}, ErrInvalid
	}
	c, err := s.resolvedConfig(ctx, org, input.AgentID, input.Config)
	if err != nil {
		return store.ResourceOperation{}, err
	}
	raw, _ := json.Marshal(c)
	row := store.Computer{ID: id.New(), OrgID: org, UserID: user, Name: input.Name, BackendID: s.backendID, Generation: 1, Status: "stopped", Config: raw}
	op, err := newOperation(org, user, row.ID, "create", key, input)
	if err != nil {
		return op, err
	}
	op, err = s.repo.Intent(ctx, &row, op)
	if err == nil {
		s.changed(ctx, op.ResourceID)
		s.notify()
	}
	return op, err
}
func (s *Service) Action(ctx context.Context, org, user, computer, action, key string, input RebuildInput) (store.ResourceOperation, error) {
	c, err := s.GetOwned(ctx, org, user, computer)
	if err != nil {
		return store.ResourceOperation{}, err
	}
	if err = s.available(); err != nil {
		return store.ResourceOperation{}, err
	}
	if action != "start" && action != "pause" && action != "rebuild" && action != "delete" {
		return store.ResourceOperation{}, ErrInvalid
	}
	op, err := newOperation(c.OrgID, user, c.ID, action, key, struct {
		Computer string       `json:"computer_id"`
		Input    RebuildInput `json:"input"`
	}{c.ID, input})
	if err != nil {
		return op, err
	}
	if action == "start" && c.SandboxID != nil {
		spec, e := s.spec(c)
		if e != nil {
			return op, e
		}
		state, e := s.backend.Inspect(ctx, spec)
		if errors.Is(e, sandbox.ErrComputerMissing) || (e == nil && state == "failed") {
			op.Progress, _ = json.Marshal(map[string]uint64{"replace_missing": c.Generation})
		} else if e != nil {
			return op, e
		}
	}
	if action == "rebuild" && input.Config != nil {
		if len(input.Config.NetworkPolicy) > 0 {
			return op, fmt.Errorf("%w: network policy is platform managed", ErrInvalid)
		}
		var old Config
		if err = json.Unmarshal(c.Config, &old); err != nil {
			return op, err
		}
		merged := mergeConfig(old, *input.Config)
		st := sandbox.DefaultSettings()
		if s.settings != nil {
			st, err = s.settings.Load(ctx)
			if err != nil {
				return op, err
			}
		}
		if !allowedImage(st, merged.Image) {
			return op, fmt.Errorf("%w: image is not enabled", ErrInvalid)
		}
		merged.NetworkPolicy = st.EgressPolicy
		if err = validateConfig(merged); err != nil {
			return op, err
		}
		input.Config = &merged
		op.Progress, _ = json.Marshal(input)
	}
	op, err = s.repo.Intent(ctx, nil, op)
	if err == nil {
		s.changed(ctx, op.ResourceID)
		s.notify()
	}
	return op, err
}
func (s *Service) Rename(ctx context.Context, org, user, computer, name string) error {
	if strings.TrimSpace(name) == "" || len([]rune(name)) > 128 {
		return ErrInvalid
	}
	c, err := s.GetOwned(ctx, org, user, computer)
	if err != nil {
		return err
	}
	if err := s.repo.Rename(ctx, c.ID, c.OrgID, user, name); err != nil {
		return err
	}
	s.changed(ctx, c.ID)
	return nil
}
func (s *Service) Operation(ctx context.Context, user, id string) (store.ResourceOperation, error) {
	op, err := s.repo.Operation(ctx, id)
	if err != nil {
		return op, err
	}
	if op.UserID != user {
		return store.ResourceOperation{}, ErrNotFound
	}
	if err = s.repo.Member(ctx, op.OrgID, user); err != nil {
		return store.ResourceOperation{}, err
	}
	return op, nil
}
func (s *Service) Runs(ctx context.Context, org, user, id string) ([]store.ComputerRun, error) {
	if _, err := s.GetOwned(ctx, org, user, id); err != nil {
		return nil, err
	}
	return s.repo.Runs(ctx, id)
}
func (s *Service) Volumes(ctx context.Context, org, user string) ([]store.UserVolume, error) {
	if err := s.repo.Member(ctx, org, user); err != nil {
		return nil, err
	}
	if s.volumes == nil {
		return nil, ErrUnavailable
	}
	return s.volumes.List(ctx, org, user)
}
func (s *Service) spec(c store.Computer) (sandbox.ComputerSpec, error) {
	var cfg Config
	if err := json.Unmarshal(c.Config, &cfg); err != nil {
		return sandbox.ComputerSpec{}, err
	}
	if c.BackendID != s.backendID {
		return sandbox.ComputerSpec{}, ErrUnavailable
	}
	return sandbox.ComputerSpec{Installation: s.installation, ComputerID: c.ID, OrgID: c.OrgID, UserID: c.UserID, BackendID: c.BackendID, Generation: c.Generation, SandboxID: c.SandboxID, Options: cfg.Options()}, nil
}

// EnsureRunning waits for the durable operation. Call RegisterRun afterwards:
// if a concurrent maintenance intent won, registration returns ErrBusy.
func (s *Service) EnsureRunning(ctx context.Context, org, user, id string) (store.Computer, error) {
	if err := s.available(); err != nil {
		return store.Computer{}, err
	}
	for {
		c, err := s.GetOwned(ctx, org, user, id)
		if err != nil {
			return c, err
		}
		if c.Status == "running" {
			spec, err := s.spec(c)
			if err != nil {
				return c, err
			}
			state, err := s.backend.Inspect(ctx, spec)
			if err == nil && state == "running" {
				return c, nil
			}
			if err == nil && state == "failed" {
				err = sandbox.ErrComputerMissing
			}
			if err == nil {
				return c, ErrBusy
			}
			if !errors.Is(err, sandbox.ErrComputerMissing) {
				return c, err
			}
			// Rebuild preserves shared volumes but does not replay commands.
			// Active or uncertain runs prevent this through Intent's row lock.
			op, err := s.Action(ctx, org, user, id, "rebuild", "", RebuildInput{})
			if err != nil {
				return c, err
			}
			if err = s.Process(ctx, op.ID); err != nil {
				return c, err
			}
			continue
		}
		if c.Status == "starting" || c.Status == "rebuilding" || c.Status == "pausing" {
			select {
			case <-ctx.Done():
				return c, ctx.Err()
			case <-time.After(200 * time.Millisecond):
				continue
			}
		}
		if c.Status == "deleting" || c.Status == "recovering" {
			return c, ErrBusy
		}
		op, err := s.Action(ctx, org, user, id, "start", "", RebuildInput{})
		if errors.Is(err, ErrBusy) {
			continue
		}
		if err != nil {
			return c, err
		}
		if err = s.Process(ctx, op.ID); err != nil {
			return c, err
		}
		op, err = s.repo.Operation(ctx, op.ID)
		if err != nil {
			return c, err
		}
		if op.Status == "failed" {
			return c, ErrUnavailable
		}
	}
}
func (s *Service) Handle(ctx context.Context, org, user, id string) (*sandbox.Handle, error) {
	if err := s.available(); err != nil {
		return nil, err
	}
	c, err := s.GetOwned(ctx, org, user, id)
	if err != nil {
		return nil, err
	}
	if c.Status != "running" {
		return nil, ErrBusy
	}
	spec, err := s.spec(c)
	if err != nil {
		return nil, err
	}
	return s.backend.Ensure(ctx, spec, false)
}
func (s *Service) RegisterRun(ctx context.Context, run store.ComputerRun, generation uint64) error {
	if _, err := s.GetOwned(ctx, run.OrgID, run.UserID, run.ComputerID); err != nil {
		return err
	}
	if run.ID == "" {
		return ErrInvalid
	}
	if strings.HasPrefix(run.Owner, "runtime/files:") {
		return ErrInvalid
	}
	return s.repo.RegisterRun(ctx, run, generation)
}

// AcquireSessionUse fences file operations against session migration/switching,
// while allowing normal user file browsing during an active main task.
func (s *Service) AcquireSessionUse(ctx context.Context, org, user, computerID, sessionID, owner string, generation uint64) (string, error) {
	if sessionID == "" {
		return "", ErrInvalid
	}
	if _, err := s.GetOwned(ctx, org, user, computerID); err != nil {
		return "", err
	}
	runID := id.New()
	err := s.repo.RegisterRun(ctx, store.ComputerRun{ID: runID, OrgID: org, UserID: user, ComputerID: computerID, SessionID: sessionID, Owner: "runtime/files:" + owner}, generation)
	return runID, err
}

// FinishRun requires the caller to have confirmed all remote executions stopped.
func (s *Service) FinishRun(ctx context.Context, runID, user, status string) error {
	return s.repo.FinishRun(ctx, runID, user, status)
}

// AcquireUse registers a maintenance occupant without inventing a session.
// It uses the same computer row lock as task registration and pause/delete.
func (s *Service) AcquireUse(ctx context.Context, org, user, id, owner string, generation uint64) (string, error) {
	runID := idNew()
	err := s.RegisterRun(ctx, store.ComputerRun{ID: runID, OrgID: org, UserID: user, ComputerID: id, Owner: owner}, generation)
	return runID, err
}

var idNew = id.New
