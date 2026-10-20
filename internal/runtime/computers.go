package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/ravenmk2/wispjar/internal/agent"
	"github.com/ravenmk2/wispjar/internal/id"
	"github.com/ravenmk2/wispjar/internal/runtime/tools"
	"github.com/ravenmk2/wispjar/internal/sandbox"
	"github.com/ravenmk2/wispjar/internal/session"
	"github.com/ravenmk2/wispjar/internal/skill"
	"github.com/ravenmk2/wispjar/internal/store"
)

type ComputerService interface {
	GetOwned(context.Context, string, string, string) (store.Computer, error)
	EnsureRunning(context.Context, string, string, string) (store.Computer, error)
	Handle(context.Context, string, string, string) (*sandbox.Handle, error)
	RegisterRun(context.Context, store.ComputerRun, uint64) error
	FinishRun(context.Context, string, string, string) error
	AcquireUse(context.Context, string, string, string, string, uint64) (string, error)
	AcquireSessionUse(context.Context, string, string, string, string, string, uint64) (string, error)
}
type SkillCoordinator interface {
	Acquire(context.Context, skill.AcquireRequest, skill.Installer) (skill.Manifest, error)
	Release(context.Context, string, string, string, bool) error
}

var errParentRunEnded = errors.New("parent run has ended")

// Reserve the in-memory child slot before the durable registration can block.
// finishResources holds the same mutex, so it cannot release the root lease
// while a child registration (and therefore future execution) is still possible.
func (o *orchestrator) registerChildRun(ctx context.Context, parent *runState, child *tools.Context) error {
	parent.resourceMu.Lock()
	if parent.ending || parent.resourcesFinishing || parent.resourcesFinished {
		parent.resourceMu.Unlock()
		return errParentRunEnded
	}
	parent.childActive++
	parent.resourceMu.Unlock()
	parentID := parent.toolCtx.RunID
	err := o.deps.Computers.RegisterRun(ctx, store.ComputerRun{
		ID: child.RunID, OrgID: child.OrgID, UserID: child.UserID, ComputerID: child.ComputerID,
		SessionID: child.SessionID, ParentRunID: &parentID, AgentID: child.AgentID, Owner: "runtime/subagent",
	}, child.Generation)
	if err != nil {
		parent.resourceMu.Lock()
		parent.childActive--
		parent.resourceMu.Unlock()
		o.finishResources(parent, "")
	}
	return err
}

func workspaceAvailable(raw json.RawMessage) error {
	return session.WorkspaceAvailable(raw)
}

func (o *orchestrator) prepareComputer(ctx context.Context, sess *store.Session, snap *agent.Snapshot, rs *runState) error {
	if sess.ComputerID == nil {
		return workspaceAvailable(sess.Workspace)
	}
	if err := workspaceAvailable(sess.Workspace); err != nil {
		return err
	}
	if o.deps.Computers == nil || o.deps.Skills == nil {
		return fmt.Errorf("computer runtime unavailable")
	}
	c, err := o.deps.Computers.EnsureRunning(ctx, sess.OrgID, sess.UserID, *sess.ComputerID)
	if err != nil {
		return err
	}
	tc := rs.toolCtx
	var configured struct {
		Env map[string]string `json:"env"`
	}
	if err := json.Unmarshal(c.Config, &configured); err != nil {
		return err
	}
	tc.ComputerEnv = configured.Env
	tc.ComputerID, tc.Generation, tc.RunID, tc.AgentID = c.ID, c.Generation, id.New(), sess.AgentID
	tc.WorkDir = session.WorkspaceDir(sess.ID)
	tc.SkillsDir = "/agents/" + sess.AgentID + "/skills"
	tc.AgentFilesDir = "/agents/" + sess.AgentID + "/files"
	execution, _ := json.Marshal(map[string]any{"generation": c.Generation, "kind": "managed-supervisor"})
	if err = o.deps.Computers.RegisterRun(ctx, store.ComputerRun{ID: tc.RunID, OrgID: sess.OrgID, UserID: sess.UserID, ComputerID: c.ID, SessionID: sess.ID, AgentID: sess.AgentID, Owner: "runtime", Execution: execution}, c.Generation); err != nil {
		return err
	}
	rs.registered = true
	h, err := o.deps.Computers.Handle(ctx, sess.OrgID, sess.UserID, c.ID)
	if err != nil {
		return err
	}
	agents := []string{sess.AgentID}
	for _, sub := range snap.SubAgents {
		agents = append(agents, sub.ID)
	}
	if err = initializeRunWorkspace(ctx, h, sess.ID, agents, rs); err != nil {
		return err
	}
	tracker := tools.NewExecutionHandle(h)
	tracker.OnSettled = func() { o.finishResources(rs, "") }
	rs.execution = tracker
	tc.BoundHandle, tc.BoundSandboxID = tracker, h.SandboxID
	installer := skill.NewSandboxInstaller(h.UploadFile, func(ctx context.Context, cmd, cwd string) (int, string, error) {
		installCtx, cancel := context.WithTimeout(context.Background(), 130*time.Second)
		defer cancel()
		result, err := h.RunCommand(installCtx, cmd, cwd, tools.PlatformEnv(), 120000, nil)
		if err != nil {
			rs.controlUncertain = true
			return -1, "", err
		}
		return result.ExitCode, result.Output, nil
	})
	rs.skillAcquired = true // Release also reconciles an ambiguous Acquire commit.
	var manifest skill.Manifest
	waiting := false
	for {
		manifest, err = o.deps.Skills.Acquire(ctx, skill.AcquireRequest{OrgID: sess.OrgID, UserID: sess.UserID, RootRunID: tc.RunID, AgentIDs: agents}, installer)
		var pending *skill.Error
		if err == nil || !errors.As(err, &pending) || pending.Code != "skill/update_pending" {
			break
		}
		if !waiting {
			waiting = true
			_, _ = o.commitRun(ctx, sess.ID, rs, []session.EventInput{{Type: "runtime/environment", Data: map[string]any{"computer_id": c.ID, "run_id": tc.RunID, "status": "waiting_skills", "reason": pending.Code}}})
		}
		timer := time.NewTimer(500 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	if err != nil {
		return err
	}
	rs.skillAcquired = true
	snap.Skills = manifestSkills(manifest, sess.AgentID)
	tc.Skills = snap.Skills
	for i := range snap.SubAgents {
		snap.SubAgents[i].Skills = manifestSkills(manifest, snap.SubAgents[i].ID)
	}
	_, err = o.commitRun(ctx, sess.ID, rs, []session.EventInput{
		{Type: "workspace/ready", Data: map[string]any{"root": tc.WorkDir, "layout_version": 1}},
		{Type: "runtime/environment", Data: map[string]any{"computer_id": c.ID, "generation": c.Generation, "run_id": tc.RunID, "skills": manifest}},
	})
	return err
}
func manifestSkills(manifest skill.Manifest, agentID string) []agent.SnapshotSkill {
	out := []agent.SnapshotSkill{}
	for _, sk := range manifest.Skills {
		if sk.AgentID == agentID {
			out = append(out, agent.SnapshotSkill{ID: sk.ID, Name: sk.Name, Description: sk.Description})
		}
	}
	return out
}

// StoppedControlError means a synchronous platform command has returned an
// explicit exit code. Its failure must not be confused with a lost connection,
// where an unseen remote command may still be running.
type StoppedControlError struct{ Err error }

func (e *StoppedControlError) Error() string { return e.Err.Error() }
func (e *StoppedControlError) Unwrap() error { return e.Err }

func controlCommandStopped(err error) bool {
	var stopped *StoppedControlError
	return err == nil || errors.As(err, &stopped)
}

func initializeRunWorkspace(ctx context.Context, h tools.Handle, sessionID string, agentIDs []string, rs *runState) error {
	rs.controlUncertain = true
	err := initializeWorkspace(ctx, h, sessionID, agentIDs)
	rs.controlUncertain = !controlCommandStopped(err)
	return err
}

func initializeWorkspace(ctx context.Context, h tools.Handle, sessionID string, agentIDs []string) error {
	payload, _ := json.Marshal(map[string]any{"session": sessionID, "agents": agentIDs})
	result, err := h.RunCommand(ctx, "python3 -I -c "+quoteShell(workspacePython)+" "+quoteShell(string(payload)), "/", tools.PlatformEnv(), 30000, nil)
	if err != nil {
		return err
	}
	if result == nil || result.ExitCode < 0 {
		return fmt.Errorf("workspace initialization completion is unknown")
	}
	if result.ExitCode != 0 {
		return &StoppedControlError{Err: fmt.Errorf("workspace initialization failed (exit %d): %s", result.ExitCode, result.Stderr)}
	}
	return nil
}
func quoteShell(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'" }

const workspacePython = `
import os,sys,json,stat,re
p=json.loads(sys.argv[1])
ids=[p["session"]]+p["agents"]
if any(not re.fullmatch("[a-f0-9]{32}",v) for v in ids): raise RuntimeError("invalid identity")
def directory(path,writable=False):
    try: os.mkdir(path,0o755)
    except FileExistsError: pass
    fd=os.open(path,os.O_RDONLY|os.O_DIRECTORY|os.O_NOFOLLOW)
    try:
        os.fchown(fd,65532 if writable else 0,65532 if writable else 0)
        os.fchmod(fd,0o755)
    finally: os.close(fd)
directory("/storage",True)
directory("/sessions")
directory("/agents")
root="/sessions/"+p["session"]
directory(root,True)
directory(root+"/inputs",True)
directory(root+"/outputs",True)
for agent in p["agents"]:
    root="/agents/"+agent
    directory(root)
    directory(root+"/files",True)
    directory(root+"/skills")
`

// Called after normal completion, cancellation, or a failed preparation. A lost
// transport keeps both the computer occupancy and skill leases for recovery.
func (o *orchestrator) finishResources(rs *runState, status string) {
	rs.resourceMu.Lock()
	if status != "" {
		rs.ending = true
		rs.endStatus = status
	}
	if rs.preparing {
		rs.resourceMu.Unlock()
		return
	}
	if rs.ending && rs.execution != nil {
		rs.execution.CloseAdmission()
	}
	if !rs.registered || rs.resourcesFinished || rs.resourcesFinishing || !rs.ending || rs.childActive != 0 || rs.childUncertain || rs.controlUncertain || (rs.execution != nil && !rs.execution.CommandsStopped()) {
		rs.resourceMu.Unlock()
		return
	}
	rs.resourcesFinishing = true
	finalStatus := rs.endStatus
	rs.resourceMu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	tc := rs.toolCtx
	success := false
	defer func() {
		rs.resourceMu.Lock()
		rs.resourcesFinishing = false
		rs.resourcesFinished = success
		retry := !success && rs.finishRetries < 3
		if retry {
			rs.finishRetries++
		}
		rs.resourceMu.Unlock()
		if retry {
			time.AfterFunc(time.Second, func() { o.finishResources(rs, "") })
		}
	}()
	if rs.skillAcquired {
		if err := o.deps.Skills.Release(ctx, tc.OrgID, tc.UserID, tc.RunID, true); err != nil {
			log.Printf("runtime: release skills for %s: %v", tc.RunID, err)
			return
		}
	}
	status = finalStatus
	if status != "completed" && status != "canceled" {
		status = "failed"
	}
	if err := o.deps.Computers.FinishRun(ctx, tc.RunID, tc.UserID, status); err != nil {
		log.Printf("runtime: finish computer run %s: %v", tc.RunID, err)
		return
	}
	success = true
}

// A canceled ADK iterator may finish before a tool's remote cleanup completes.
// Keep its child occupancy until the command tracker calls back with proof.
func (o *orchestrator) childFinalizer(parent *runState, tc *tools.Context, tracker *tools.ExecutionHandle) func(string) {
	var mu sync.Mutex
	ending, finishing, finished := false, false, false
	status := "failed"
	retries := 0
	var attempt func()
	attempt = func() {
		mu.Lock()
		if !ending || finishing || finished || !tracker.CommandsStopped() {
			mu.Unlock()
			return
		}
		finishing = true
		finalStatus := status
		mu.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		err := o.deps.Computers.FinishRun(ctx, tc.RunID, tc.UserID, finalStatus)
		cancel()
		mu.Lock()
		finishing = false
		finished = err == nil
		retry := err != nil && retries < 3
		if retry {
			retries++
		}
		mu.Unlock()
		if err != nil {
			log.Printf("runtime: finish child run %s: %v", tc.RunID, err)
			if retry {
				time.AfterFunc(time.Second, attempt)
			}
			return
		}
		parent.resourceMu.Lock()
		parent.childActive--
		parent.resourceMu.Unlock()
		o.finishResources(parent, "")
	}
	tracker.OnSettled = attempt
	return func(reason string) {
		mu.Lock()
		ending = true
		status = reason
		tracker.CloseAdmission()
		mu.Unlock()
		attempt()
	}
}
