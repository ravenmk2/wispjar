package runtime

import (
	"context"

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
