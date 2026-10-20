package computer

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ravenmk2/wispjar/internal/sandbox"
	"github.com/ravenmk2/wispjar/internal/store"
	"github.com/ravenmk2/wispjar/internal/volume"
)

// The in-memory boundary only implements paths exercised by worker tests.
// SQL row-lock semantics are covered by the opt-in MySQL acceptance tests.
type memoryRepo struct {
	Repository
	mu         sync.Mutex
	worker     sync.Mutex
	computer   store.Computer
	op         store.ResourceOperation
	failCommit bool
	recovered  int
}

func (r *memoryRepo) Member(context.Context, string, string) error { return nil }
func (r *memoryRepo) Get(context.Context, string) (store.Computer, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.computer, nil
}
func (r *memoryRepo) Operation(context.Context, string) (store.ResourceOperation, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.op, nil
}
func (r *memoryRepo) Lock(context.Context, string) (func() error, error) {
	r.worker.Lock()
	return func() error { r.worker.Unlock(); return nil }, nil
}
func (r *memoryRepo) Progress(_ context.Context, _ string, p operationPlan) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.op.Progress, _ = json.Marshal(p)
	r.op.Status = "running"
	return nil
}
func (r *memoryRepo) Complete(_ context.Context, op store.ResourceOperation, sid *string, status string, cause error) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.failCommit {
		r.failCommit = false
		return errors.New("database connection lost after backend success")
	}
	r.computer.Status = status
	r.computer.SandboxID = sid
	r.op.Status = "completed"
	if cause != nil {
		r.computer.Status = "failed"
		r.op.Status = "failed"
		if op.Action == "computer/recover" {
			r.computer.Status = "recovering"
			r.op.Status = "running"
		}
	} else if op.Action == "computer/recover" {
		r.computer.Generation++
	}
	return nil
}
func (r *memoryRepo) RecoverRuns(_ context.Context, _ store.ResourceOperation) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	var plan operationPlan
	_ = json.Unmarshal(r.op.Progress, &plan)
	if !plan.RunsRecovered {
		r.recovered++
		plan.RunsRecovered = true
		r.op.Progress, _ = json.Marshal(plan)
	}
	return nil
}

type memoryBackend struct {
	mu               sync.Mutex
	exists           bool
	creates, deletes int
	allow            []bool
	err              error
}

func (b *memoryBackend) Ensure(_ context.Context, _ sandbox.ComputerSpec, create bool) (*sandbox.Handle, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.allow = append(b.allow, create)
	if b.err != nil {
		return nil, b.err
	}
	if !b.exists {
		if !create {
			return nil, sandbox.ErrComputerMissing
		}
		b.creates++
		b.exists = true
	}
	return &sandbox.Handle{SandboxID: "instance"}, nil
}
func (b *memoryBackend) Inspect(context.Context, sandbox.ComputerSpec) (string, error) {
	if !b.exists {
		return "", sandbox.ErrComputerMissing
	}
	return "running", nil
}
func (b *memoryBackend) Pause(context.Context, sandbox.ComputerSpec) error { return b.err }
func (b *memoryBackend) Delete(context.Context, sandbox.ComputerSpec) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.deletes++
	return b.err
}
func (b *memoryBackend) Renew(context.Context, sandbox.ComputerSpec) error { return nil }

type memoryVolumes struct{}

func (memoryVolumes) Ensure(context.Context, string, string) ([]volume.Mount, error) { return nil, nil }
func (memoryVolumes) List(context.Context, string, string) ([]store.UserVolume, error) {
	return nil, nil
}
func fixture() (*Service, *memoryRepo, *memoryBackend) {
	r := &memoryRepo{computer: store.Computer{ID: "computer", OrgID: "org", UserID: "user", BackendID: "local", Generation: 1, Status: "starting", Config: json.RawMessage(`{"image":"python:3.12","ttl_seconds":3600}`)},
		op: store.ResourceOperation{ID: "op", OrgID: "org", UserID: "user", ResourceID: "computer", Action: "computer/create", Status: "pending", Progress: json.RawMessage(`{"generation":1}`)}}
	b := &memoryBackend{}
	return NewService(r, memoryVolumes{}, b, "installation", "local", nil), r, b
}
func TestWorkerRecoversBackendSuccessBeforeCommit(t *testing.T) {
	s, r, b := fixture()
	r.failCommit = true
	if err := s.Process(context.Background(), "op"); err == nil {
		t.Fatal("expected commit failure")
	}
	if r.op.Status != "running" {
		t.Fatal("lost recoverable intent")
	}
	if err := s.Process(context.Background(), "op"); err != nil {
		t.Fatal(err)
	}
	if b.creates != 1 || len(b.allow) != 2 || b.allow[1] || r.computer.Status != "running" {
		t.Fatalf("unsafe recovery: %+v %+v", b, r.computer)
	}
}
func TestConcurrentWorkersDoNotDuplicateCreate(t *testing.T) {
	s, r, b := fixture()
	var wg sync.WaitGroup
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.Process(context.Background(), "op"); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if b.creates != 1 || r.op.Status != "completed" {
		t.Fatalf("creates=%d status=%s", b.creates, r.op.Status)
	}
}
func TestRecoveryDoesNotBlindlyRepeatAmbiguousCreate(t *testing.T) {
	s, r, b := fixture()
	r.op.Progress = json.RawMessage(`{"generation":1,"create_attempted":true}`)
	if err := s.Process(context.Background(), "op"); err != nil {
		t.Fatal(err)
	}
	if b.creates != 0 || r.op.Status != "failed" {
		t.Fatal("ambiguous operation was recreated")
	}
}
func TestGenerationFenceAndOwnership(t *testing.T) {
	s, r, b := fixture()
	r.computer.Generation = 2
	if err := s.Process(context.Background(), "op"); !errors.Is(err, ErrConflict) {
		t.Fatalf("want fence error: %v", err)
	}
	if b.creates != 0 {
		t.Fatal("stale worker made external call")
	}
	if _, err := s.GetOwned(context.Background(), "org", "other", "computer"); !errors.Is(err, ErrNotFound) {
		t.Fatal("cross-user access")
	}
	if _, err := s.GetOwned(context.Background(), "other", "user", "computer"); !errors.Is(err, ErrNotFound) {
		t.Fatal("cross-org access")
	}
}
func TestIntentGuardsAndRebuildGeneration(t *testing.T) {
	cases := []struct {
		action, status string
		busy, bound    bool
		want           error
	}{
		{"pause", "running", true, false, ErrBusy}, {"rebuild", "running", true, false, ErrBusy},
		{"delete", "running", false, true, ErrInUse}, {"start", "starting", false, false, ErrBusy},
		{"start", "failed", true, false, ErrBusy},
	}
	for _, tt := range cases {
		t.Run(tt.action+tt.status, func(t *testing.T) {
			c := store.Computer{OrgID: "o", UserID: "u", Generation: 3, Status: tt.status}
			op := store.ResourceOperation{OrgID: "o", UserID: "u", Action: "computer/" + tt.action}
			if err := planIntent(&c, &op, tt.busy, tt.bound); !errors.Is(err, tt.want) {
				t.Fatalf("got %v", err)
			}
		})
	}
	sid := "old"
	c := store.Computer{OrgID: "o", UserID: "u", Generation: 3, Status: "running", SandboxID: &sid}
	op := store.ResourceOperation{OrgID: "o", UserID: "u", Action: "computer/rebuild"}
	if err := planIntent(&c, &op, false, false); err != nil {
		t.Fatal(err)
	}
	var p operationPlan
	_ = json.Unmarshal(op.Progress, &p)
	if c.Generation != 4 || c.SandboxID != nil || p.PreviousGeneration != 3 || *p.PreviousSandboxID != "old" {
		t.Fatal("lost old identity")
	}
}
func TestRunningStartDoesNotBlockActiveRuns(t *testing.T) {
	c := store.Computer{OrgID: "o", UserID: "u", Generation: 1, Status: "running"}
	op := store.ResourceOperation{OrgID: "o", UserID: "u", Action: "computer/start"}
	if err := planIntent(&c, &op, true, false); err != nil {
		t.Fatal(err)
	}
	if c.Status != "running" || op.Status != "completed" {
		t.Fatal("idempotent start mutated an active computer")
	}
}
func TestRequestHashAndPublicView(t *testing.T) {
	a, _ := newOperation("o", "u", "c", "rebuild", "same", map[string]any{"config": map[string]string{"image": "a"}})
	b, _ := newOperation("o", "u", "c", "rebuild", "same", map[string]any{"config": map[string]string{"image": "b"}})
	if a.RequestHash == b.RequestHash {
		t.Fatal("different requests alias")
	}
	if hashRequest(map[string]string{"a": "1", "b": "2"}) != hashRequest(map[string]string{"b": "2", "a": "1"}) {
		t.Fatal("hash depends on map iteration")
	}
	raw, _ := json.Marshal(View(store.Computer{Config: json.RawMessage(`{"env":{"SECRET":"password"},"image":"python"}`)}))
	if strings.Contains(string(raw), "password") || strings.Contains(string(raw), "SECRET") {
		t.Fatal("public view leaks env")
	}
}
func TestConfigurationPolicy(t *testing.T) {
	s, _, _ := fixture()
	for _, cfg := range []Config{
		{Image: "unlisted"},
		{Env: map[string]string{"WISPJAR": "no"}},
		{NetworkPolicy: json.RawMessage(`{"defaultAction":"allow"}`)},
		{Resources: map[string]string{"gpu": "9"}},
		{Resources: map[string]string{"cpu": "-1"}},
	} {
		if _, err := s.resolvedConfig(context.Background(), "org", "", cfg); !errors.Is(err, ErrInvalid) {
			t.Fatalf("accepted %+v: %v", cfg, err)
		}
	}
	c, err := s.resolvedConfig(context.Background(), "org", "", Config{Env: map[string]string{"A": "b"}})
	if err != nil || c.Env["A"] != "b" {
		t.Fatalf("valid config rejected: %v", err)
	}
}

func TestIdlePauseChecksFreshActivityUnderLock(t *testing.T) {
	now := time.Now()
	c := store.Computer{OrgID: "o", UserID: "u", Status: "running", LastActiveAt: &now}
	progress, _ := json.Marshal(map[string]any{"idle_before": now.Add(-time.Hour)})
	op := store.ResourceOperation{OrgID: "o", UserID: "u", Action: "computer/pause", Progress: progress}
	if err := planIntent(&c, &op, false, false); !errors.Is(err, ErrBusy) {
		t.Fatalf("freshly finished run was paused: %v", err)
	}
}

type recoveryBackend struct {
	*memoryBackend
	deleteErr, errorInspect error
	stillExists             bool
}

func (b *recoveryBackend) Delete(context.Context, sandbox.ComputerSpec) error {
	b.deletes++
	return b.deleteErr
}
func (b *recoveryBackend) Inspect(context.Context, sandbox.ComputerSpec) (string, error) {
	if b.errorInspect != nil {
		return "", b.errorInspect
	}
	if b.stillExists {
		return "running", nil
	}
	return "", sandbox.ErrComputerMissing
}
func recoveryFixture() (*Service, *memoryRepo, *recoveryBackend) {
	s, r, b := fixture()
	r.computer.Status = "recovering"
	r.op.Action = "computer/recover"
	backend := &recoveryBackend{memoryBackend: b}
	s.backend = backend
	s.SetRecoveryHandler(func(context.Context, store.Computer) error { return nil })
	return s, r, backend
}
func TestRecoveryRequiresAuthoritativeAbsence(t *testing.T) {
	for _, tt := range []struct {
		name                  string
		deleteErr, inspectErr error
		exists                bool
	}{
		{"delete transport", errors.New("connection lost"), nil, false},
		{"identity", sandbox.ErrComputerIdentity, nil, false},
		{"inspect transport", nil, errors.New("connection lost"), false},
		{"async delete", nil, nil, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s, r, b := recoveryFixture()
			b.deleteErr = tt.deleteErr
			b.errorInspect = tt.inspectErr
			b.stillExists = tt.exists
			hooks := 0
			s.SetRecoveryHandler(func(context.Context, store.Computer) error { hooks++; return nil })
			if err := s.Process(context.Background(), "op"); err != nil {
				t.Fatal(err)
			}
			if r.recovered != 0 || hooks != 0 || r.computer.Status != "recovering" || r.op.Status != "running" || r.computer.Generation != 1 {
				t.Fatalf("released uncertain run or lost barrier: %+v", r)
			}
		})
	}
}
func TestRecoveryResumesAfterProofAndCleanupFailure(t *testing.T) {
	s, r, b := recoveryFixture()
	attempts := 0
	s.SetRecoveryHandler(func(_ context.Context, c store.Computer) error {
		attempts++
		if r.recovered != 1 || c.Status != "recovering" || c.Generation != 1 {
			t.Fatal("cleanup ran before terminalization or after reopening")
		}
		if attempts == 1 {
			return errors.New("skill database unavailable")
		}
		return nil
	})
	if err := s.Process(context.Background(), "op"); err != nil {
		t.Fatal(err)
	}
	if r.computer.Status != "recovering" || r.op.Status != "running" {
		t.Fatal("cleanup failure reopened computer")
	}
	if err := s.Process(context.Background(), "op"); err != nil {
		t.Fatal(err)
	}
	if r.recovered != 1 || r.computer.Generation != 2 || r.computer.Status != "stopped" || r.op.Status != "completed" {
		t.Fatalf("recovery failed: %+v", r)
	}
	if err := s.Process(context.Background(), "op"); err != nil {
		t.Fatal(err)
	}
	if attempts != 2 || b.deletes != 2 {
		t.Fatal("completed recovery was executed again")
	}
}
func TestRecoveryIntentFencesActiveAndBoundComputer(t *testing.T) {
	sid := "old"
	c := store.Computer{OrgID: "o", UserID: "u", Generation: 3, Status: "running", SandboxID: &sid}
	op := store.ResourceOperation{OrgID: "o", UserID: "u", Action: "computer/recover"}
	if err := planIntent(&c, &op, true, true); err != nil {
		t.Fatal(err)
	}
	var plan operationPlan
	_ = json.Unmarshal(op.Progress, &plan)
	if c.Status != "recovering" || c.Generation != 3 || plan.PreviousGeneration != 3 || plan.PreviousSandboxID == nil {
		t.Fatal("lost recovery identity")
	}
	for _, action := range []string{"start", "pause", "rebuild", "delete", "recover"} {
		next := store.ResourceOperation{OrgID: "o", UserID: "u", Action: "computer/" + action}
		if err := planIntent(&c, &next, false, false); !errors.Is(err, ErrBusy) {
			t.Fatalf("barrier allowed %s", action)
		}
	}
}

func TestRecoveryRequiresOwnerAndExplicitTerminationConsent(t *testing.T) {
	s, _, _ := fixture()
	if _, err := s.Recover(context.Background(), "org", "user", "computer", "key", RecoverInput{}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("missing confirmation accepted: %v", err)
	}
	if _, err := s.Recover(context.Background(), "org", "other", "computer", "key", RecoverInput{ConfirmStopAll: true}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("another user can recover computer: %v", err)
	}
}
