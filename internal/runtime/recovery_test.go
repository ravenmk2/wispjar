package runtime

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/ravenmk2/wispjar/internal/session"
)

func TestComputerRecoveryFencesOldCallbacksAndPreservesNewTurn(t *testing.T) {
	env := newTestEnv(t, session.ApprovalAuto, nil)
	o := env.orch.(*orchestrator)
	computerID := "test-computer"
	env.session.ComputerID = &computerID
	env.session.Running = true
	ctx, cancel := context.WithCancel(context.Background())
	old := &runState{runContext: ctx, contextCancel: cancel, turn: 1}
	o.runs[env.session.ID] = old
	env.prompt(t, "first", "request")
	if err := RecoverComputer(context.Background(), env.orch, computerID); err != nil {
		t.Fatal(err)
	}
	if ctx.Err() == nil {
		t.Fatal("old runtime context not canceled")
	}
	fresh := &runState{turn: 2}
	o.runs[env.session.ID] = fresh
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			o.endTurn(env.session.ID, old, "completed", "")
			_, err := o.commitRun(context.Background(), env.session.ID, old, []session.EventInput{{Type: session.EventToolResult, Data: map[string]string{"late": "result"}}})
			if !errors.Is(err, errParentRunEnded) {
				t.Errorf("old callback accepted: %v", err)
			}
			o.registerPending(&pendingApproval{sessionID: env.session.ID, callID: "old-approval", root: old})
		})
	}
	wg.Wait()
	if o.runs[env.session.ID] != fresh || len(o.pending) != 0 {
		t.Fatal("old callbacks modified the new run")
	}
	if len(env.es.byType(session.EventTurnEnd)) != 1 || len(env.es.byType(session.EventToolResult)) != 0 {
		t.Fatal("old callbacks appended after recovery")
	}
}

func TestComputerRecoveryEndsRestartOrphanWithoutMemory(t *testing.T) {
	env := newTestEnv(t, session.ApprovalAuto, nil)
	computerID := "test-computer"
	env.session.ComputerID = &computerID
	env.session.Running = true
	env.prompt(t, "start", "request")
	_, _ = env.es.AppendBatch(context.Background(), env.session.ID, []session.EventInput{{
		Type: session.EventApprovalAsked, Data: session.ApprovalAskedData{CallID: "pending"},
	}})
	if err := RecoverComputer(context.Background(), env.orch, computerID); err != nil {
		t.Fatal(err)
	}
	if len(env.es.byType(session.EventTurnEnd)) != 1 || len(env.es.byType(session.EventApprovalDecided)) != 1 {
		t.Fatal("orphan turn and approval not terminated")
	}
}
