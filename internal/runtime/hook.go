package runtime

import (
	"context"
	"fmt"

	"github.com/ravenmk2/wispjar/internal/sandbox"
	"github.com/ravenmk2/wispjar/internal/session"
)

// SandboxEventHook 把沙箱生命周期事件写成 session/sandbox 会话事件并同步 session.sandbox 投影。
func SandboxEventHook(repo SessionRepo, events EventStore, bus *Bus) func(sandbox.Event) {
	return func(e sandbox.Event) {
		ctx := context.Background()
		status := "running"
		errMsg := ""
		switch e.Action {
		case sandbox.EventRecycled:
			status = "recycled"
		case sandbox.EventPaused:
			status = "paused"
		case sandbox.EventResumed:
			status = "running"
		case sandbox.EventFailed:
			status = "failed"
			errMsg = e.Reason
		}
		_ = repo.SetSandboxStatus(ctx, e.SessionID, e.SandboxID, status)
		evs, err := events.AppendBatch(ctx, e.SessionID, []session.EventInput{
			{Type: session.EventSessionSandbox, Data: session.SessionSandboxData{
				Action: e.Action, SandboxID: e.SandboxID, Error: errMsg,
			}},
		})
		if err != nil {
			return
		}
		for _, ev := range evs {
			e2 := ev
			bus.Publish(e.SessionID, EventFrame(session.Event{Seq: e2.Seq, Type: e2.Type, Time: e2.TimeMs, Data: e2.Data}))
		}
	}
}

// recordSkillUploadFailure 技能目录上传沙箱失败时写 session/sandbox 事件（detail 记录原因，不阻断 turn）。
func (o *orchestrator) recordSkillUploadFailure(sessionID, skillName, sandboxID string, cause error) {
	evs, err := o.deps.Events.AppendBatch(context.Background(), sessionID, []session.EventInput{
		{Type: session.EventSessionSandbox, Data: session.SessionSandboxData{
			Action:    "skills_upload",
			SandboxID: sandboxID,
			Error:     fmt.Sprintf("skill %s 上传失败: %v", skillName, cause),
		}},
	})
	if err != nil {
		return
	}
	for _, ev := range evs {
		e := ev
		o.deps.Bus.Publish(sessionID, EventFrame(session.Event{Seq: e.Seq, Type: e.Type, Time: e.TimeMs, Data: e.Data}))
	}
}
