package sandbox

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	opensandbox "github.com/alibaba/OpenSandbox/sdks/sandbox/go"
)

type pauseRecorder struct {
	mu    sync.Mutex
	calls int
	err   error
}

func (p *pauseRecorder) fn() func(context.Context) error {
	return func(context.Context) error {
		p.mu.Lock()
		p.calls++
		p.mu.Unlock()
		return p.err
	}
}

func (p *pauseRecorder) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

func collectEvents(svc *Service) *[]Event {
	evs := &[]Event{}
	var mu sync.Mutex
	svc.OnEvent(func(e Event) {
		mu.Lock()
		*evs = append(*evs, e)
		mu.Unlock()
	})
	return evs
}

func TestPauseIdleTransitions(t *testing.T) {
	now := time.Now()
	svc := NewService(nil, 60, nil)
	evs := collectEvents(svc)

	active := &pauseRecorder{}
	idle := &pauseRecorder{}
	pausedH := &pauseRecorder{}
	expiredH := &pauseRecorder{}

	svc.handles = map[string]*Handle{
		// 活跃 → 不暂停
		"active": newTestHandle("active", 3600, now, now.Add(time.Hour), nil, 0),
		// 空闲超 idleTTL → 暂停
		"idle": newTestHandle("idle", 3600, now.Add(-2*time.Minute), now.Add(time.Hour), nil, 0),
		// 已 paused → 跳过
		"paused": newTestHandle("paused", 3600, now.Add(-2*time.Minute), now.Add(time.Hour), nil, 0),
		// 硬 TTL 到期 → Recycle（无论是否空闲）
		"expired": newTestHandle("expired", 3600, now, now.Add(-time.Second), nil, 0),
	}
	svc.handles["active"].pauseFn = active.fn()
	svc.handles["idle"].pauseFn = idle.fn()
	svc.handles["paused"].pauseFn = pausedH.fn()
	svc.handles["paused"].setPaused(true)
	svc.handles["expired"].pauseFn = expiredH.fn()

	svc.pauseIdle()

	if got := active.callCount(); got != 0 {
		t.Fatalf("active: want no pause, got %d", got)
	}
	if got := idle.callCount(); got != 1 {
		t.Fatalf("idle: want 1 pause, got %d", got)
	}
	if !svc.handles["idle"].Paused() {
		t.Fatal("idle: handle not marked paused")
	}
	if got := pausedH.callCount(); got != 0 {
		t.Fatalf("paused: want no re-pause, got %d", got)
	}
	if got := expiredH.callCount(); got != 0 {
		t.Fatalf("expired: want recycle not pause, got %d pauses", got)
	}
	if _, ok := svc.handles["expired"]; ok {
		t.Fatal("expired: handle should be recycled")
	}

	var sawPaused, sawRecycled bool
	for _, e := range *evs {
		if e.Action == EventPaused && e.SessionID == "idle" && e.Reason == "idle_ttl" {
			sawPaused = true
		}
		if e.Action == EventRecycled && e.SessionID == "expired" && e.Reason == "ttl_expired" {
			sawRecycled = true
		}
	}
	if !sawPaused || !sawRecycled {
		t.Fatalf("missing events: %+v", *evs)
	}
}

func TestPauseIdleNotFoundRecycles(t *testing.T) {
	now := time.Now()
	svc := NewService(nil, 60, nil)
	evs := collectEvents(svc)

	gone := &pauseRecorder{err: &opensandbox.APIError{StatusCode: 404}}
	h := newTestHandle("gone", 3600, now.Add(-2*time.Minute), now.Add(time.Hour), nil, 0)
	h.pauseFn = gone.fn()
	svc.handles = map[string]*Handle{"gone": h}

	svc.pauseIdle()

	if _, ok := svc.handles["gone"]; ok {
		t.Fatal("gone: handle should be dropped after 404 pause")
	}
	var saw bool
	for _, e := range *evs {
		if e.Action == EventRecycled && e.Reason == "pause_not_found" {
			saw = true
		}
	}
	if !saw {
		t.Fatalf("missing recycle event: %+v", *evs)
	}
}

func TestPauseIdleTransientErrorKeepsHandle(t *testing.T) {
	now := time.Now()
	svc := NewService(nil, 60, nil)

	flaky := &pauseRecorder{err: errors.New("connection refused")}
	h := newTestHandle("flaky", 3600, now.Add(-2*time.Minute), now.Add(time.Hour), nil, 0)
	h.pauseFn = flaky.fn()
	svc.handles = map[string]*Handle{"flaky": h}

	svc.pauseIdle()

	keep, ok := svc.handles["flaky"]
	if !ok {
		t.Fatal("flaky: handle should be kept for retry")
	}
	if keep.Paused() {
		t.Fatal("flaky: must not be marked paused after failed pause")
	}
}

func TestEnsureResumedConcurrent(t *testing.T) {
	svc := NewService(nil, 1800, nil)
	evs := collectEvents(svc)

	var resumeCalls atomic.Int32
	h := newTestHandle("s1", 3600, time.Now(), time.Now().Add(time.Hour), nil, 0)
	h.resumeFn = func(context.Context) (*opensandbox.Sandbox, error) {
		time.Sleep(10 * time.Millisecond)
		resumeCalls.Add(1)
		return nil, nil
	}
	h.setPaused(true)
	svc.handles = map[string]*Handle{"s1": h}

	const n = 8
	var wg sync.WaitGroup
	handles := make([]*Handle, n)
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			handles[i], errs[i] = svc.Get(context.Background(), "s1", Options{Image: "img"})
		}(i)
	}
	wg.Wait()

	for i := 0; i < n; i++ {
		if errs[i] != nil {
			t.Fatalf("get %d: %v", i, errs[i])
		}
		if handles[i] != h {
			t.Fatalf("get %d: want resumed handle, got %+v", i, handles[i])
		}
	}
	if got := resumeCalls.Load(); got != 1 {
		t.Fatalf("want exactly 1 resume call, got %d", got)
	}
	if h.Paused() {
		t.Fatal("handle still marked paused")
	}
	var saw bool
	for _, e := range *evs {
		if e.Action == EventResumed && e.SessionID == "s1" {
			saw = true
		}
	}
	if !saw {
		t.Fatalf("missing resumed event: %+v", *evs)
	}
}

func TestEnsureResumedFailureDropsHandle(t *testing.T) {
	svc := NewService(nil, 1800, nil)

	h := newTestHandle("s1", 3600, time.Now(), time.Now().Add(time.Hour), nil, 0)
	h.resumeFn = func(context.Context) (*opensandbox.Sandbox, error) {
		return nil, &opensandbox.APIError{StatusCode: 404}
	}
	h.setPaused(true)
	svc.handles = map[string]*Handle{"s1": h}

	if svc.ensureResumed(context.Background(), "s1", h) {
		t.Fatal("want false on resume failure")
	}
	if _, ok := svc.handles["s1"]; ok {
		t.Fatal("handle should be removed after failed resume")
	}
	if !h.Paused() {
		t.Fatal("failed resume must not clear paused flag")
	}
}

func TestAdoptable(t *testing.T) {
	exists := func(context.Context, string) bool { return true }
	missing := func(context.Context, string) bool { return false }
	mk := func(state opensandbox.SandboxState) opensandbox.SandboxInfo {
		return opensandbox.SandboxInfo{Status: opensandbox.SandboxStatus{State: state}}
	}

	svc := NewService(nil, 1800, nil)
	if svc.adoptable(context.Background(), "s1", mk(opensandbox.StateRunning)) {
		t.Fatal("no session checker → never adoptable")
	}
	svc.SetSessionChecker(missing)
	if svc.adoptable(context.Background(), "s1", mk(opensandbox.StateRunning)) {
		t.Fatal("deleted/missing session → not adoptable")
	}
	svc.SetSessionChecker(exists)
	for _, st := range []opensandbox.SandboxState{
		opensandbox.StateRunning, opensandbox.StatePaused, opensandbox.StatePending,
	} {
		if !svc.adoptable(context.Background(), "s1", mk(st)) {
			t.Fatalf("state %s should be adoptable", st)
		}
	}
	for _, st := range []opensandbox.SandboxState{
		opensandbox.StateStopping, opensandbox.StateTerminated, opensandbox.StateFailed,
	} {
		if svc.adoptable(context.Background(), "s1", mk(st)) {
			t.Fatalf("state %s should not be adoptable", st)
		}
	}
}

func TestAdoptRegistersPausedHandle(t *testing.T) {
	svc := NewService(nil, 1800, nil)
	evs := collectEvents(svc)
	svc.SetSessionChecker(func(context.Context, string) bool { return true })
	svc.connectFn = func(context.Context, string) (*opensandbox.Sandbox, error) {
		return &opensandbox.Sandbox{}, nil
	}

	expires := time.Now().Add(30 * time.Minute)
	info := opensandbox.SandboxInfo{
		ID:         "sb-orphan",
		Status:     opensandbox.SandboxStatus{State: opensandbox.StatePaused},
		ExpiresAt:  &expires,
		Metadata:   map[string]string{"wispjar-session": "s1"},
		CreatedAt:  time.Now().Add(-time.Hour),
		Entrypoint: nil,
	}
	if err := svc.adopt(context.Background(), "s1", info); err != nil {
		t.Fatalf("adopt: %v", err)
	}
	h, ok := svc.handles["s1"]
	if !ok {
		t.Fatal("adopted handle not registered")
	}
	if !h.Paused() {
		t.Fatal("adopted handle must be paused")
	}
	if h.SandboxID != "sb-orphan" {
		t.Fatalf("sandbox id: %q", h.SandboxID)
	}
	if got := h.ExpiresAt(); !got.Equal(expires) {
		t.Fatalf("expiresAt: want %s got %s", expires, got)
	}
	var saw bool
	for _, e := range *evs {
		if e.Action == EventPaused && e.Reason == "adopted" && e.SandboxID == "sb-orphan" {
			saw = true
		}
	}
	if !saw {
		t.Fatalf("missing adopted event: %+v", *evs)
	}
}
