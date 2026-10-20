package volume

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/ravenmk2/wispjar/internal/store"
	"github.com/ravenmk2/wispjar/internal/volume/naming"
)

const testOrg = "00000000000000000000000000000001"
const testUser = "00000000000000000000000000000002"

type memoryRepo struct {
	mu        sync.Mutex
	gate      chan struct{}
	rows      map[string]store.UserVolume
	failReady bool
}

func newRepo() *memoryRepo {
	return &memoryRepo{gate: make(chan struct{}, 1), rows: map[string]store.UserVolume{}}
}
func (r *memoryRepo) InstallationID(context.Context) (string, error) {
	return "00000000000000000000000000000003", nil
}
func (r *memoryRepo) Lock(ctx context.Context, _, _ string) (func() error, error) {
	select {
	case r.gate <- struct{}{}:
		return func() error { <-r.gate; return nil }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
func (r *memoryRepo) GetOrCreate(_ context.Context, v store.UserVolume) (store.UserVolume, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if old, ok := r.rows[v.Kind]; ok {
		return old, nil
	}
	r.rows[v.Kind] = v
	return v, nil
}
func (r *memoryRepo) List(context.Context, string, string) ([]store.UserVolume, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	rows := []store.UserVolume{}
	for _, v := range r.rows {
		rows = append(rows, v)
	}
	return rows, nil
}
func (r *memoryRepo) SetState(ctx context.Context, id, state, reason string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if state == "ready" && r.failReady {
		r.failReady = false
		return fmt.Errorf("database write failed")
	}
	for kind, v := range r.rows {
		if v.ID == id {
			v.Status = state
			v.LastError = reason
			r.rows[kind] = v
			return nil
		}
	}
	return ErrNotFound
}

type memoryProvider struct {
	mu           sync.Mutex
	rows         map[string]Info
	creates      int
	loseResponse bool
	entered      chan struct{}
	resume       chan struct{}
}

func newProvider() *memoryProvider { return &memoryProvider{rows: map[string]Info{}} }
func (p *memoryProvider) Inspect(_ context.Context, name string) (Info, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	v, ok := p.rows[name]
	if !ok {
		return Info{}, ErrNotFound
	}
	return v, nil
}
func (p *memoryProvider) Ensure(ctx context.Context, spec Spec) (Info, error) {
	p.mu.Lock()
	v, ok := p.rows[spec.Name]
	if !ok {
		v = Info(spec)
		p.rows[spec.Name] = v
		p.creates++
	}
	lose := p.loseResponse
	p.loseResponse = false
	entered, resume := p.entered, p.resume
	p.entered = nil
	p.mu.Unlock()
	if entered != nil {
		close(entered)
		select {
		case <-resume:
		case <-ctx.Done():
			return Info{}, ctx.Err()
		}
	}
	if lose {
		return Info{}, context.DeadlineExceeded
	}
	return v, checkIdentity(v, spec)
}
func service(t *testing.T, r Repository, p Provider) *Service {
	t.Helper()
	s, err := NewService(r, p, Options{BackendID: "local", Namespace: "main", NamePattern: naming.DefaultPattern})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestEnsureRetryPreservesIdentity(t *testing.T) {
	for _, scenario := range []string{"lost-response", "db-write"} {
		t.Run(scenario, func(t *testing.T) {
			r, p := newRepo(), newProvider()
			if scenario == "lost-response" {
				p.loseResponse = true
			} else {
				r.failReady = true
			}
			s := service(t, r, p)
			if _, err := s.Ensure(context.Background(), testOrg, testUser); err == nil {
				t.Fatal("expected transient failure")
			}
			original := r.rows["storage"]
			s.options.Namespace = "new"
			mounts, err := s.Ensure(context.Background(), testOrg, testUser)
			if err != nil {
				t.Fatal(err)
			}
			if len(mounts) != 3 || mounts[0].ClaimName != original.BackendName || p.creates != 3 {
				t.Fatalf("retry created duplicate: %+v / %d", mounts, p.creates)
			}
		})
	}
}
func TestReadyMissingNeverRecreated(t *testing.T) {
	r, p := newRepo(), newProvider()
	s := service(t, r, p)
	mounts, err := s.Ensure(context.Background(), testOrg, testUser)
	if err != nil {
		t.Fatal(err)
	}
	delete(p.rows, mounts[0].ClaimName)
	for i := 0; i < 2; i++ {
		if _, err = s.Ensure(context.Background(), testOrg, testUser); !errors.Is(err, ErrMissing) {
			t.Fatalf("expected missing: %v", err)
		}
	}
	if p.creates != 3 || r.rows["storage"].Status != "missing" {
		t.Fatal("missing volume was recreated")
	}
}
func TestRejectForeignLabelsAndStorageDomain(t *testing.T) {
	r, p := newRepo(), newProvider()
	s := service(t, r, p)
	mounts, err := s.Ensure(context.Background(), testOrg, testUser)
	if err != nil {
		t.Fatal(err)
	}
	v := p.rows[mounts[0].ClaimName]
	v.Labels = map[string]string{"wispjar.managed": "true"}
	p.rows[v.Name] = v
	if _, err = s.Ensure(context.Background(), testOrg, testUser); !errors.Is(err, ErrConflict) {
		t.Fatalf("foreign volume accepted: %v", err)
	}
	s.options.BackendID = "elsewhere"
	if _, err = s.Ensure(context.Background(), testOrg, testUser); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("wrong domain accepted: %v", err)
	}
	if p.creates != 3 {
		t.Fatal("created outside registered domain")
	}
}
func TestConcurrentEnsureAndCanceledWaiter(t *testing.T) {
	r, p := newRepo(), newProvider()
	s := service(t, r, p)
	p.entered, p.resume = make(chan struct{}), make(chan struct{})
	entered, resume := p.entered, p.resume
	result := make(chan error, 1)
	go func() { _, err := s.Ensure(context.Background(), testOrg, testUser); result <- err }()
	<-entered
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, err := s.Ensure(ctx, testOrg, testUser); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("wait did not cancel: %v", err)
	}
	close(resume)
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Go(func() {
			if _, err := s.Ensure(context.Background(), testOrg, testUser); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if p.creates != 3 {
		t.Fatalf("created %d volumes", p.creates)
	}
}
func TestInspectDoesNotProvision(t *testing.T) {
	r, p := newRepo(), newProvider()
	s := service(t, r, p)
	rows, err := s.InspectOwner(context.Background(), testOrg, testUser)
	if err != nil || len(rows) != 0 || p.creates != 0 {
		t.Fatalf("inspection mutated resources: %v", err)
	}
}
