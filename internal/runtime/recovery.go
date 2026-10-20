package runtime

import (
	"context"
	"errors"

	"github.com/ravenmk2/wispjar/internal/session"
	"github.com/ravenmk2/wispjar/internal/store"
)

// RecoverComputer retires runtime state after the lifecycle service has verified
// the old sandbox is gone and terminated its persisted runs. The computer must
// remain in recovering status until this hook succeeds.
func RecoverComputer(ctx context.Context, orch Orchestrator, computerID string) error {
	o, ok := orch.(*orchestrator)
	if !ok {
		return errors.New("runtime computer recovery is unavailable")
	}
	return o.recoverComputer(ctx, computerID)
}

func (o *orchestrator) commitRun(ctx context.Context, sessionID string, rs *runState, inputs []session.EventInput) ([]store.SessionEvent, error) {
	if rs == nil {
		return o.commit(ctx, sessionID, inputs)
	}
	rs.eventMu.Lock()
	defer rs.eventMu.Unlock()
	if rs.retired {
		return nil, errParentRunEnded
	}
	if rs.turn > 0 && len(inputs) > 0 {
		inputs[0].ExpectedTurn = &rs.turn
	}
	return o.commit(ctx, sessionID, inputs)
}

func (o *orchestrator) publishRun(sessionID string, rs *runState, frame FollowFrame) {
	rs.eventMu.Lock()
	defer rs.eventMu.Unlock()
	if !rs.retired {
		o.deps.Bus.Publish(sessionID, frame)
	}
}

func (o *orchestrator) recoverComputer(ctx context.Context, computerID string) error {
	sessions, err := o.deps.Sessions.RunningSessions(ctx)
	if err != nil {
		return err
	}
	candidates := map[string]struct{}{}
	for _, sess := range sessions {
		if sess.ComputerID != nil && *sess.ComputerID == computerID {
			candidates[sess.ID] = struct{}{}
		}
	}
	o.mu.RLock()
	current := make(map[string]*runState, len(o.runs))
	for sid, rs := range o.runs {
		current[sid] = rs
	}
	o.mu.RUnlock()
	for sid := range current {
		sess, err := o.deps.Sessions.SessionByID(ctx, sid)
		if err != nil {
			return err
		}
		if sess.ComputerID != nil && *sess.ComputerID == computerID {
			candidates[sid] = struct{}{}
		}
	}
	for sid := range candidates {
		if rs := current[sid]; rs != nil {
			if rs.contextCancel != nil {
				rs.contextCancel()
			}
			rs.resourceMu.Lock()
			rs.ending = true
			rs.resourcesFinished = true // Lifecycle recovery already terminated DB runs.
			if !rs.preparing && rs.execution != nil {
				rs.execution.CloseAdmission()
			}
			rs.resourceMu.Unlock()
			rs.eventMu.Lock()
			rs.retired = true
			rs.eventMu.Unlock()
			rs.endOnce.Do(func() {})
			o.forgetRun(sid, rs)
		}
		sess, err := o.deps.Sessions.SessionByID(ctx, sid)
		if err != nil {
			return err
		}
		if !sess.Running {
			continue
		}
		asked, err := o.deps.Sessions.UndecidedApprovals(ctx, sid)
		if err != nil {
			return err
		}
		turn, err := o.deps.Sessions.TurnCount(ctx, sid)
		if err != nil {
			return err
		}
		inputs := make([]session.EventInput, 0, len(asked)+1)
		for _, a := range asked {
			inputs = append(inputs, session.EventInput{Type: session.EventApprovalDecided, Data: session.ApprovalDecidedData{
				CallID: a.CallID, Decision: "deny", DecidedBy: "system", Auto: true,
			}})
		}
		inputs = append(inputs, session.EventInput{Type: session.EventTurnEnd, Data: session.TurnEndData{
			Turn: turn, Reason: "failed", ErrorCode: "computer/recovered", Error: "云电脑已恢复，旧任务已终止",
		}})
		inputs[0].ExpectedLastSeq = &sess.LastSeq
		if _, err := o.commit(ctx, sid, inputs); err != nil {
			return err
		}
	}
	return nil
}
