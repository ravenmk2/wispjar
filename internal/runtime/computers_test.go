package runtime

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/ravenmk2/wispjar/internal/agent"
	"github.com/ravenmk2/wispjar/internal/runtime/tools"
	"github.com/ravenmk2/wispjar/internal/session"
	"github.com/ravenmk2/wispjar/internal/store"
)

func TestUnboundSessionCannotStartComputerRun(t *testing.T) {
	o := &orchestrator{}
	err := o.prepareComputer(context.Background(), &store.Session{ID: "session"}, &agent.Snapshot{}, &runState{})
	var se *session.Error
	if !errors.As(err, &se) || se.Code != "computer/required" {
		t.Fatalf("unbound session accepted: %v", err)
	}
}

type resourceCalls struct {
	mu          sync.Mutex
	values      []string
	failRelease bool
}

type blockingRegisterComputer struct {
	resourceComputer
	entered     chan struct{}
	unblock     chan struct{}
	registerErr error
}

func (c blockingRegisterComputer) RegisterRun(context.Context, store.ComputerRun, uint64) error {
	close(c.entered)
	<-c.unblock
	return c.registerErr
}
func TestChildRegistrationReservationSurvivesConcurrentEnd(t *testing.T) {
	for _, failed := range []bool{false, true} {
		t.Run(map[bool]string{false: "registered", true: "registration failed"}[failed], func(t *testing.T) {
			o, rs, calls := resourceRuntime()
			backend := blockingRegisterComputer{resourceComputer: resourceComputer{calls: calls}, entered: make(chan struct{}), unblock: make(chan struct{})}
			if failed {
				backend.registerErr = errors.New("database unavailable")
			}
			o.deps.Computers = backend
			child := rs.toolCtx.Fork("child-agent", "child", nil)
			done := make(chan error, 1)
			go func() { done <- o.registerChildRun(context.Background(), rs, child) }()
			<-backend.entered // The database registration has started but not returned.
			o.finishResources(rs, "canceled")
			if len(calls.snapshot()) != 0 {
				t.Fatal("root lease released during child registration")
			}
			close(backend.unblock)
			err := <-done
			if failed {
				if err == nil {
					t.Fatal("registration should fail")
				}
				if got := calls.snapshot(); !reflect.DeepEqual(got, []string{"release:root", "finish:root:canceled"}) {
					t.Fatalf("failed reservation not returned: %v", got)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				if len(calls.snapshot()) != 0 {
					t.Fatal("registered child no longer protects root")
				}
				o.childFinalizer(rs, child, tools.NewExecutionHandle(newFakeHandle()))("canceled")
				if got := calls.snapshot(); !reflect.DeepEqual(got, []string{"finish:child:canceled", "release:root", "finish:root:canceled"}) {
					t.Fatalf("release ordering: %v", got)
				}
			}
			if err := o.registerChildRun(context.Background(), rs, child); !errors.Is(err, errParentRunEnded) {
				t.Fatalf("ended parent admitted child: %v", err)
			}
		})
	}
}

func TestPreparationReservationDefersResourceRelease(t *testing.T) {
	o, rs, calls := resourceRuntime()
	rs.preparing = true
	o.finishResources(rs, "canceled")
	if len(calls.snapshot()) != 0 {
		t.Fatal("preparation still able to issue controls but resources released")
	}
	rs.resourceMu.Lock()
	rs.preparing = false
	rs.resourceMu.Unlock()
	o.finishResources(rs, "")
	if got := calls.snapshot(); !reflect.DeepEqual(got, []string{"release:root", "finish:root:canceled"}) {
		t.Fatalf("settled preparation not released: %v", got)
	}
}

func (c *resourceCalls) add(value string) {
	c.mu.Lock()
	c.values = append(c.values, value)
	c.mu.Unlock()
}
func (c *resourceCalls) snapshot() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string{}, c.values...)
}

type resourceComputer struct {
	ComputerService
	calls *resourceCalls
}

func (c resourceComputer) FinishRun(_ context.Context, runID, user, status string) error {
	c.calls.add("finish:" + runID + ":" + status)
	return nil
}

type resourceSkills struct {
	SkillCoordinator
	calls *resourceCalls
}

func (s resourceSkills) Release(_ context.Context, org, user, run string, stopped bool) error {
	if !stopped {
		return errors.New("unconfirmed release")
	}
	s.calls.mu.Lock()
	defer s.calls.mu.Unlock()
	if s.calls.failRelease {
		s.calls.failRelease = false
		return errors.New("temporary DB failure")
	}
	s.calls.values = append(s.calls.values, "release:"+run)
	return nil
}
func resourceRuntime() (*orchestrator, *runState, *resourceCalls) {
	calls := &resourceCalls{}
	o := &orchestrator{deps: Deps{Computers: resourceComputer{calls: calls}, Skills: resourceSkills{calls: calls}}}
	tc := tools.NewContext("session", "org")
	tc.UserID = "user"
	tc.RunID = "root"
	rs := &runState{registered: true, skillAcquired: true, toolCtx: tc}
	return o, rs, calls
}
func TestRunResourcesWaitForChildAndPreserveStatus(t *testing.T) {
	o, rs, calls := resourceRuntime()
	rs.childActive = 1
	o.finishResources(rs, "completed")
	if len(calls.snapshot()) != 0 {
		t.Fatal("root released while child active")
	}
	child := rs.toolCtx.Fork("sub", "child", nil)
	finish := o.childFinalizer(rs, child, tools.NewExecutionHandle(newFakeHandle()))
	finish("canceled")
	want := []string{"finish:child:canceled", "release:root", "finish:root:completed"}
	if got := calls.snapshot(); !reflect.DeepEqual(got, want) {
		t.Fatalf("%v != %v", got, want)
	}
	o.finishResources(rs, "completed")
	if got := calls.snapshot(); !reflect.DeepEqual(got, want) {
		t.Fatal("resources released twice")
	}
}
func TestRunResourcesKeepUnknownCommandOccupied(t *testing.T) {
	o, rs, calls := resourceRuntime()
	rs.execution = tools.NewExecutionHandle(newFakeHandle())
	_, _ = rs.execution.RunCommand(context.Background(), "true", "/", nil, 1000, nil)
	o.finishResources(rs, "canceled")
	if len(calls.snapshot()) != 0 {
		t.Fatal("uncertain remote command released leases")
	}
}
func TestRunResourceReleaseRetriesDatabaseFailure(t *testing.T) {
	o, rs, calls := resourceRuntime()
	calls.failRelease = true
	o.finishResources(rs, "completed")
	if len(calls.snapshot()) != 0 {
		t.Fatal("finish executed before successful skill release")
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if reflect.DeepEqual(calls.snapshot(), []string{"release:root", "finish:root:completed"}) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("temporary DB failure left occupancy permanently active")
}

func TestWorkspaceInitializationFailureOccupancy(t *testing.T) {
	for _, tc := range []struct {
		name      string
		code      int
		transport error
		stopped   bool
	}{
		{name: "symlink rejected", code: 1, stopped: true},
		{name: "permission denied", code: 13, stopped: true},
		{name: "transport disconnected", transport: errors.New("connection reset")},
		{name: "exit not reported", code: -1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := newFakeHandle()
			raw.runErr = tc.transport
			raw.execFn = func(string) (int, string, string) { return tc.code, "", "O_NOFOLLOW refused workspace path" }
			t.Run("root", func(t *testing.T) {
				o, rs, calls := resourceRuntime()
				rs.skillAcquired = false // Initialization precedes acquiring skills.
				err := initializeRunWorkspace(context.Background(), raw, "session", []string{"agent"}, rs)
				if err == nil {
					t.Fatal("expected initialization refusal")
				}
				if controlCommandStopped(err) != tc.stopped {
					t.Fatalf("wrong completion classification: %v", err)
				}
				o.finishResources(rs, "failed")
				expected := []string{}
				if tc.stopped {
					expected = []string{"finish:root:failed"}
				}
				if actual := calls.snapshot(); !reflect.DeepEqual(actual, expected) {
					t.Fatalf("occupancy: %v != %v", actual, expected)
				}
			})
			t.Run("files", func(t *testing.T) {
				calls := &resourceCalls{}
				handler := &Handler{computers: resourceComputer{calls: calls}}
				sess := &session.Detail{Session: store.Session{ID: "session", AgentID: "agent", UserID: "user"}}
				err := handler.initializeFileWorkspace(context.Background(), raw, sess, "file-use")
				if err == nil {
					t.Fatal("expected initialization refusal")
				}
				expected := []string{}
				if tc.stopped {
					expected = []string{"finish:file-use:failed"}
				}
				if actual := calls.snapshot(); !reflect.DeepEqual(actual, expected) {
					t.Fatalf("occupancy: %v != %v", actual, expected)
				}
			})
		})
	}
}
