package runtime

import (
	"context"

	"fmt"
	"net/http"
	"time"

	"github.com/cloudwego/eino/adk"
	etool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"

	"github.com/ravenmk2/wispjar/internal/runtime/tools"
	"github.com/ravenmk2/wispjar/internal/session"
)

const (
	RiskHigh   = "high"
	RiskMedium = "medium"
	RiskLow    = "low"
)

var toolRisk = map[string]string{
	"exec":           RiskHigh,
	"read":           RiskLow,
	"write":          RiskMedium,
	"edit":           RiskMedium,
	"glob":           RiskLow,
	"grep":           RiskLow,
	"activate_skill": RiskLow,
	"task":           RiskLow,
}

func riskOf(toolName string) string {
	if r, ok := toolRisk[toolName]; ok {
		return r
	}
	return RiskLow
}

// needsApproval 三模式判定表（runtime.md §5.2）。
func needsApproval(mode, toolName string) bool {
	switch mode {
	case session.ApprovalStrict:
		return riskOf(toolName) == RiskHigh || riskOf(toolName) == RiskMedium
	case session.ApprovalAuto:
		return false
	default: // default
		return riskOf(toolName) == RiskHigh
	}
}

// ApprovalInterruptInfo 是审批 interrupt 的用户可见载体（随 checkpoint gob 持久化）。
type ApprovalInterruptInfo struct {
	CallID    string
	Name      string
	Arguments string
	Risk      string
}

func init() {
	schema.RegisterName[ApprovalInterruptInfo]("_wispjar_approval_interrupt_info")
}

const deniedResult = "denied by user"

// approvalMiddleware 在工具执行前按会话 approval_mode 拦截（runtime.md §5.1）。
func approvalMiddleware(mode string) compose.ToolMiddleware {
	return compose.ToolMiddleware{
		Invokable: func(next compose.InvokableToolEndpoint) compose.InvokableToolEndpoint {
			return func(ctx context.Context, input *compose.ToolInput) (*compose.ToolOutput, error) {
				if !needsApproval(mode, input.Name) {
					return next(ctx, input)
				}
				wasInterrupted, _, _ := etool.GetInterruptState[any](ctx)
				if !wasInterrupted {
					return nil, etool.Interrupt(ctx, ApprovalInterruptInfo{
						CallID:    input.CallID,
						Name:      input.Name,
						Arguments: input.Arguments,
						Risk:      riskOf(input.Name),
					})
				}
				isTarget, hasData, allow := etool.GetResumeContext[bool](ctx)
				if isTarget && hasData && !allow {
					return &compose.ToolOutput{Result: deniedResult}, nil
				}
				return next(ctx, input)
			}
		},
	}
}

const approvalTimeout = 10 * time.Minute

// approvalFromInterrupt 从 InterruptInfo 提取审批载体与根 interrupt ID（主/子 Agent 共用）。
func approvalFromInterrupt(info *adk.InterruptInfo) (ApprovalInterruptInfo, string, bool) {
	var root *adk.InterruptCtx
	var walk func(ic *adk.InterruptCtx)
	walk = func(ic *adk.InterruptCtx) {
		if ic == nil {
			return
		}
		if ic.IsRootCause {
			root = ic
			return
		}
		walk(ic.Parent)
	}
	for _, ic := range info.InterruptContexts {
		walk(ic)
	}
	if root == nil && len(info.InterruptContexts) > 0 {
		root = info.InterruptContexts[len(info.InterruptContexts)-1]
	}
	var ai ApprovalInterruptInfo
	if root != nil {
		switch v := root.Info.(type) {
		case ApprovalInterruptInfo:
			ai = v
		case *ApprovalInterruptInfo:
			ai = *v
		}
	}
	if root == nil || ai.CallID == "" {
		return ApprovalInterruptInfo{}, "", false
	}
	return ai, root.ID, true
}

type pendingApproval struct {
	sessionID    string
	checkpointID string
	interruptID  string
	callID       string
	timer        *time.Timer
	// 子 Agent 嵌套审批：parentCallID 非空，decide 投递到 sub.ch 而非 resume 主 runner。
	parentCallID string
	sub          *subApproval
}

var (
	ErrSessionRunning     = &Error{Status: http.StatusConflict, Code: "session/running", Message: "会话有进行中的 turn"}
	ErrApprovalNotPending = &Error{Status: http.StatusConflict, Code: "approval/not_pending", Message: "审批已决定或不存在"}
	ErrApprovalExpired    = &Error{Status: http.StatusGone, Code: "approval/expired", Message: "审批已超时"}
	ErrSandboxNotFound    = &Error{Status: http.StatusNotFound, Code: "sandbox/not_found", Message: "会话没有可用沙箱"}
)

// Error 是 runtime 的语义化错误（handlers 映射为统一错误信封）。
type Error struct {
	Status  int
	Code    string
	Message string
	Details any
}

func (e *Error) Error() string { return e.Message }

// registerPending 登记内存审批表并启动超时兜底（fail-closed）。
func (o *orchestrator) registerPending(p *pendingApproval) {
	o.mu.Lock()
	o.pending[p.callID] = p
	p.timer = time.AfterFunc(approvalTimeout, func() {
		_ = o.decide(p.sessionID, p.callID, false, "system", true)
	})
	o.mu.Unlock()
}

// DecideApproval 实现 Orchestrator 接口。
func (o *orchestrator) DecideApproval(sessionID, callID string, allow bool, by string) error {
	return o.decide(sessionID, callID, allow, by, false)
}

func (o *orchestrator) decide(sessionID, callID string, allow bool, by string, auto bool) error {
	o.mu.Lock()
	p, ok := o.pending[callID]
	if ok {
		delete(o.pending, callID)
	}
	o.mu.Unlock()
	if !ok {
		decided, wasAuto, err := o.deps.Sessions.ApprovalDecision(context.Background(), sessionID, callID)
		if err != nil {
			return err
		}
		if decided && wasAuto {
			return ErrApprovalExpired
		}
		return ErrApprovalNotPending
	}
	if p.sessionID != sessionID {
		return ErrApprovalNotPending
	}
	if p.timer != nil {
		p.timer.Stop()
	}

	decision := "deny"
	if allow {
		decision = "allow"
	}
	var decidedData any = session.ApprovalDecidedData{
		CallID: callID, Decision: decision, DecidedBy: by, Auto: auto,
	}
	if p.parentCallID != "" {
		decidedData = nestedApprovalDecidedData{
			ApprovalDecidedData: session.ApprovalDecidedData{
				CallID: callID, Decision: decision, DecidedBy: by, Auto: auto,
			},
			ParentCallID: p.parentCallID,
		}
	}
	if _, err := o.commit(context.Background(), sessionID, []session.EventInput{
		{Type: session.EventApprovalDecided, Data: decidedData},
	}); err != nil {
		return fmt.Errorf("approval/decided 落库失败: %w", err)
	}

	if !allow {
		if p.sub != nil {
			p.sub.denied.add(callID)
		} else {
			o.mu.Lock()
			if rs := o.runs[sessionID]; rs != nil {
				rs.denied[callID] = struct{}{}
			}
			o.mu.Unlock()
		}
	}
	if p.sub != nil {
		select {
		case p.sub.ch <- allow:
		default:
		}
		return nil
	}
	o.resume(p, allow)
	return nil
}

// resume 用审批结果恢复被中断的 turn（新的消费 goroutine）。
func (o *orchestrator) resume(p *pendingApproval, allow bool) {
	o.mu.RLock()
	rs := o.runs[p.sessionID]
	o.mu.RUnlock()
	if rs == nil {
		return
	}
	go func() {
		ctx := tools.WithContext(context.Background(), rs.toolCtx)
		runner := adk.NewRunner(ctx, adk.RunnerConfig{
			Agent:           rs.agent,
			EnableStreaming: true,
			CheckPointStore: o.deps.Checkpoints(p.sessionID),
		})
		cancelOpt, cancelFn := adk.WithCancel()
		o.mu.Lock()
		if cur := o.runs[p.sessionID]; cur != nil {
			cur.cancel = cancelFn
			if cur.cancelRequested {
				_, _ = cancelFn(adk.WithAgentCancelMode(adk.CancelImmediate))
			}
		}
		o.mu.Unlock()
		iter, err := runner.ResumeWithParams(ctx, p.checkpointID,
			&adk.ResumeParams{Targets: map[string]any{p.interruptID: allow}},
			adk.WithCheckPointID(p.checkpointID), cancelOpt)
		if err != nil {
			o.failTurn(p.sessionID, rs, fmt.Errorf("resume: %w", err))
			return
		}
		o.consume(p.sessionID, rs, iter)
		// checkpoint 清理由 endTurn 统一负责：审批暂停只是 turn 的中断，不是结束
	}()
}

// RecoverPendingApprovals 启动恢复：无 decided 的 asked 一律 fail-closed 补 deny；
// 仍处于 running 的会话补 turn/end（重启不会恢复内存中的 turn）。
func (o *orchestrator) RecoverPendingApprovals(ctx context.Context) error {
	sessions, err := o.deps.Sessions.RunningSessions(ctx)
	if err != nil {
		return err
	}
	for _, sess := range sessions {
		asked, err := o.deps.Sessions.UndecidedApprovals(ctx, sess.ID)
		if err != nil {
			return err
		}
		turn, err := o.deps.Sessions.TurnCount(ctx, sess.ID)
		if err != nil {
			return err
		}
		inputs := []session.EventInput{}
		for _, a := range asked {
			inputs = append(inputs, session.EventInput{Type: session.EventApprovalDecided, Data: session.ApprovalDecidedData{
				CallID: a.CallID, Decision: "deny", DecidedBy: "system", Auto: true,
			}})
		}
		inputs = append(inputs, session.EventInput{Type: session.EventTurnEnd, Data: session.TurnEndData{
			Turn: turn, Reason: "failed", Error: "interrupted by service restart",
		}})
		if _, err := o.deps.Events.AppendBatch(ctx, sess.ID, inputs); err != nil {
			return err
		}
	}
	return nil
}
