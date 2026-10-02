package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"github.com/ravenmk2/wispjar/internal/agent"
	"github.com/ravenmk2/wispjar/internal/id"
	"github.com/ravenmk2/wispjar/internal/runtime/tools"
	"github.com/ravenmk2/wispjar/internal/sandbox"
	"github.com/ravenmk2/wispjar/internal/session"
	"github.com/ravenmk2/wispjar/internal/store"
)

// ModelFactory 由 internal/provider.ChatModelFactory 实现（密钥不出 provider 包）。
type ModelFactory interface {
	NewChatModel(ctx context.Context, modelID string, params map[string]any) (model.ToolCallingChatModel, error)
}

type Deps struct {
	Events      EventStore
	Sessions    SessionRepo
	Models      ModelFactory
	Sandbox     sandbox.Manager
	Checkpoints func(sessionID string) adk.CheckPointStore
	Bus         *Bus
	DataDir     string
	ContextSize func(ctx context.Context, modelID string) int
	// ToolContextFactory 覆盖工具上下文装配（测试注入用；生产为 nil）。
	ToolContextFactory func(sess *store.Session, snap *agent.Snapshot) *tools.Context
}

// Orchestrator 是运行时总链路的入口（runtime.md §1）。
type Orchestrator interface {
	StartTurn(ctx context.Context, sessionID string) error
	Cancel(sessionID string) error
	DecideApproval(sessionID, callID string, allow bool, by string) error
	Follow(sessionID string, afterSeq int64) (*Snapshot, <-chan FollowFrame, func(), error)
	RecoverPendingApprovals(ctx context.Context) error
}

type runState struct {
	agent           adk.Agent
	cancel          adk.AgentCancelFunc
	cancelRequested bool
	checkpointID    string
	turn            int
	stepMu          sync.Mutex // 保护 step：task 工具 goroutine 与 consume goroutine 并发访问
	step            int
	lastSeq         int64
	attempt         *attemptState
	denied          map[string]struct{}
	toolCtx         *tools.Context
}

func (rs *runState) incrStep() int {
	rs.stepMu.Lock()
	defer rs.stepMu.Unlock()
	rs.step++
	return rs.step
}

func (rs *runState) currentStep() int {
	rs.stepMu.Lock()
	defer rs.stepMu.Unlock()
	return rs.step
}

type orchestrator struct {
	deps Deps

	mu      sync.RWMutex
	runs    map[string]*runState
	pending map[string]*pendingApproval

	titled sync.Map // sessionID -> struct{}：每进程只尝试一次自动标题
}

func NewOrchestrator(deps Deps) Orchestrator {
	return &orchestrator{
		deps:    deps,
		runs:    map[string]*runState{},
		pending: map[string]*pendingApproval{},
	}
}

// StartTurn 执行一个 turn；会话已有进行中 turn 时返回 ErrSessionRunning。
// 事件序列：derive 历史 → 构建 agent → runner 流式运行 → 翻译/落库/广播 → turn/end。
func (o *orchestrator) StartTurn(ctx context.Context, sessionID string) error {
	o.mu.Lock()
	if _, ok := o.runs[sessionID]; ok {
		o.mu.Unlock()
		return ErrSessionRunning
	}
	o.runs[sessionID] = &runState{denied: map[string]struct{}{}}
	o.mu.Unlock()

	rs := o.runs[sessionID]
	if err := o.prepare(ctx, sessionID, rs); err != nil {
		if rs.turn > 0 {
			_, _ = o.commit(context.Background(), sessionID, []session.EventInput{
				{Type: session.EventTurnEnd, Data: session.TurnEndData{Turn: rs.turn, Reason: "failed", Error: err.Error()}},
			})
		}
		o.mu.Lock()
		delete(o.runs, sessionID)
		o.mu.Unlock()
		return err
	}

	iterCtx := tools.WithContext(context.Background(), rs.toolCtx)
	cancelOpt, cancelFn := adk.WithCancel()
	rs.cancel = cancelFn
	runner := adk.NewRunner(iterCtx, adk.RunnerConfig{
		Agent:           rs.agent,
		EnableStreaming: true,
		CheckPointStore: o.deps.Checkpoints(sessionID),
	})
	history, err := o.history(ctx, sessionID)
	if err != nil {
		o.failTurn(sessionID, rs, err)
		return nil
	}
	iter := runner.Run(iterCtx, history, cancelOpt, adk.WithCheckPointID(rs.checkpointID))
	o.consume(sessionID, rs, iter)
	return nil
}

func (o *orchestrator) prepare(ctx context.Context, sessionID string, rs *runState) error {
	sess, err := o.deps.Sessions.SessionByID(ctx, sessionID)
	if err != nil {
		return err
	}
	var snap agent.Snapshot
	if err := json.Unmarshal(sess.AgentSnapshot, &snap); err != nil {
		return fmt.Errorf("agent_snapshot 解析失败: %w", err)
	}
	turn, err := o.deps.Sessions.TurnCount(ctx, sessionID)
	if err != nil {
		return err
	}
	rs.turn = turn
	rs.checkpointID = id.New()
	rs.toolCtx = o.buildToolContext(sess, &snap)
	a, _, err := o.buildAgent(ctx, sess, &snap, rs.toolCtx, rs)
	if err != nil {
		return err
	}
	rs.agent = a
	return nil
}

// history derive 事件历史为模型消息（protocol.md §6.4），并按上下文窗口裁剪（runtime.md §8.1）。
func (o *orchestrator) history(ctx context.Context, sessionID string) ([]*schema.Message, error) {
	events, err := o.deps.Events.Events(ctx, sessionID, 0, 0)
	if err != nil {
		return nil, err
	}
	derived := session.DeriveMessages(events)
	msgs := make([]*schema.Message, 0, len(derived))
	for _, m := range derived {
		switch m.Role {
		case session.RoleUser:
			msgs = append(msgs, schema.UserMessage(m.Content))
		case session.RoleAssistant:
			calls := make([]schema.ToolCall, 0, len(m.ToolCalls))
			for _, tc := range m.ToolCalls {
				calls = append(calls, schema.ToolCall{
					ID:       tc.ID,
					Type:     "function",
					Function: schema.FunctionCall{Name: tc.Name, Arguments: tc.Arguments},
				})
			}
			msgs = append(msgs, schema.AssistantMessage(m.Content, calls))
		case session.RoleTool:
			msgs = append(msgs, schema.ToolMessage(m.Content, m.ToolCallID, schema.WithToolName(m.ToolName)))
		}
	}
	return o.trimContext(ctx, sessionID, msgs)
}

func (o *orchestrator) trimContext(ctx context.Context, sessionID string, msgs []*schema.Message) ([]*schema.Message, error) {
	limit := 64000 * 8 / 10
	sess, err := o.deps.Sessions.SessionByID(ctx, sessionID)
	if err == nil && o.deps.ContextSize != nil {
		if n := o.deps.ContextSize(ctx, sess.ModelID); n > 0 {
			limit = n * 8 / 10
		}
	}
	estimate := func(ms []*schema.Message) int {
		total := 0
		for _, m := range ms {
			total += len(m.Content) + len(m.ReasoningContent)
			for _, tc := range m.ToolCalls {
				total += len(tc.Function.Arguments)
			}
		}
		return total / 4
	}
	for estimate(msgs) > limit && len(msgs) > 1 {
		drop := 1
		if len(msgs[0].ToolCalls) > 0 {
			for drop < len(msgs)-1 && msgs[drop].Role == schema.Tool {
				drop++
			}
		}
		msgs = msgs[drop:]
	}
	if estimate(msgs) > limit {
		return nil, errors.New("上下文超出模型窗口，建议开启新会话")
	}
	return msgs, nil
}

// consume 消费事件迭代器：翻译 → 落库 → 广播；返回时 turn 已收尾或已暂停（审批中断）。
func (o *orchestrator) consume(sessionID string, rs *runState, iter *adk.AsyncIterator[*adk.AgentEvent]) {
	for {
		event, ok := iter.Next()
		if !ok {
			break
		}
		if event.Err != nil {
			var cancelErr *adk.CancelError
			if errors.As(event.Err, &cancelErr) {
				o.finishInterrupted(sessionID, rs)
			} else {
				o.failTurn(sessionID, rs, event.Err)
			}
			return
		}
		if event.Action != nil && event.Action.Interrupted != nil {
			o.pause(sessionID, rs, event.Action.Interrupted)
			return
		}
		mv := event.Output
		if mv == nil || mv.MessageOutput == nil {
			continue
		}
		out := mv.MessageOutput
		switch {
		case out.Role == schema.Assistant && out.IsStreaming:
			if !o.stepStream(sessionID, rs, out.MessageStream) {
				return
			}
		case out.Role == schema.Assistant && out.Message != nil:
			if !o.stepMessage(sessionID, rs, out.Message) {
				return
			}
		case out.Role == schema.Tool && out.Message != nil:
			if !o.toolResult(sessionID, rs, out.Message) {
				return
			}
		}
	}
	o.endTurn(sessionID, rs, "completed", "")
}

// stepStream 处理一次流式模型调用（一个 step）。
func (o *orchestrator) stepStream(sessionID string, rs *runState, stream *schema.StreamReader[*schema.Message]) bool {
	rs.incrStep()
	attempt := newAttempt(rs.turn, rs.step)
	rs.attempt = attempt
	startedAfter := rs.lastSeq
	if !o.commitOK(sessionID, rs, []session.EventInput{
		{Type: session.EventStepStart, Data: session.StepData{Turn: rs.turn, Step: rs.step}},
	}) {
		return false
	}
	o.deps.Bus.Publish(sessionID, AssistantStreamFrame(StreamStart{
		Type: "start", AttemptID: attempt.id, Turn: rs.turn, Step: rs.step, StartedAfterSeq: startedAfter,
	}))
	err := attempt.drainStream(stream, func(fc framedChunk) {
		o.deps.Bus.Publish(sessionID, AssistantStreamFrame(StreamChunkFrame{
			Type: "chunk", AttemptID: attempt.id, Index: fc.index, Time: nowMs(), Chunk: fc.chunk,
		}))
	})
	if err != nil {
		rs.attempt = nil
		if rs.cancelRequested {
			o.finishInterruptedAttempt(sessionID, rs, attempt)
			o.endTurn(sessionID, rs, "interrupted", "")
		} else {
			o.failAttempt(sessionID, rs, attempt, err)
		}
		return false
	}
	for _, fc := range attempt.endChunks(false) {
		o.deps.Bus.Publish(sessionID, AssistantStreamFrame(StreamChunkFrame{
			Type: "chunk", AttemptID: attempt.id, Index: fc.index, Time: nowMs(), Chunk: fc.chunk,
		}))
	}
	rs.attempt = nil
	seq := o.commitAssistant(sessionID, rs, attempt, false)
	o.deps.Bus.Publish(sessionID, AssistantStreamFrame(StreamEnd{
		Type: "end", AttemptID: attempt.id,
		Outcome: StreamOutcome{Kind: "committed", EventType: session.EventAssistantMessage, Seq: seq},
	}))
	return true
}

// stepMessage 处理一次非流式完整 assistant 消息（防御性分支）。
func (o *orchestrator) stepMessage(sessionID string, rs *runState, msg *schema.Message) bool {
	rs.incrStep()
	attempt := newAttempt(rs.turn, rs.step)
	attempt.feed(msg)
	inputs := []session.EventInput{
		{Type: session.EventStepStart, Data: session.StepData{Turn: rs.turn, Step: rs.step}},
	}
	inputs = append(inputs, o.assistantInputs(rs, attempt, false)...)
	return o.commitOK(sessionID, rs, inputs)
}

func (o *orchestrator) assistantInputs(rs *runState, attempt *attemptState, interrupted bool) []session.EventInput {
	asst := attempt.assistantMessage()
	inputs := []session.EventInput{
		{Type: session.EventAssistantMessage, Data: session.AssistantMessageData{
			Turn: rs.turn, Step: rs.step, Message: asst,
			Usage: eventUsage(attempt.usage), Interrupted: interrupted,
		}},
	}
	for _, b := range asst.Content {
		if b.Type != "tool-call" {
			continue
		}
		inputs = append(inputs, session.EventInput{Type: session.EventToolCall, Data: session.ToolCallData{
			Turn: rs.turn, Step: rs.step, CallID: b.ID, Name: b.Name, Arguments: b.Arguments,
		}})
	}
	inputs = append(inputs, session.EventInput{Type: session.EventStepEnd, Data: session.StepData{Turn: rs.turn, Step: rs.step}})
	return inputs
}

// commitAssistant 落库 assistant/message(+tool/call)+step/end，返回 assistant/message 的 seq。
func (o *orchestrator) commitAssistant(sessionID string, rs *runState, attempt *attemptState, interrupted bool) int64 {
	events, err := o.commit(context.Background(), sessionID, o.assistantInputs(rs, attempt, interrupted))
	if err != nil {
		o.failTurn(sessionID, rs, err)
		return 0
	}
	return events[0].Seq
}

func (o *orchestrator) toolResult(sessionID string, rs *runState, msg *schema.Message) bool {
	data := session.ToolResultData{
		Turn:    rs.turn,
		Step:    rs.step,
		CallID:  msg.ToolCallID,
		Name:    msg.ToolName,
		Content: msg.Content,
	}
	if m := rs.toolCtx.TakeMeta(msg.ToolCallID); m != nil {
		data.Meta = &session.ToolResultMeta{
			DurationMS: m.DurationMS,
			Render:     m.Render,
			Truncated:  m.Truncated,
			SandboxID:  m.SandboxID,
			Diff:       m.Diff,
		}
	}
	if _, denied := rs.denied[msg.ToolCallID]; denied {
		delete(rs.denied, msg.ToolCallID)
		data.Error = deniedResult
		data.Content = deniedResult
	}
	return o.commitOK(sessionID, rs, []session.EventInput{{Type: session.EventToolResult, Data: data}})
}

// pause 审批中断：approval/asked 落库 + 登记 pending；turn 保持 running。
func (o *orchestrator) pause(sessionID string, rs *runState, info *adk.InterruptInfo) {
	ai, rootID, ok := approvalFromInterrupt(info)
	if !ok {
		o.failTurn(sessionID, rs, errors.New("unknown interrupt (not an approval request)"))
		return
	}
	if _, err := o.commit(context.Background(), sessionID, []session.EventInput{
		{Type: session.EventApprovalAsked, Data: session.ApprovalAskedData{
			Turn: rs.turn, Step: rs.step, CallID: ai.CallID,
			Name: ai.Name, Arguments: ai.Arguments, Risk: ai.Risk,
		}},
	}); err != nil {
		o.failTurn(sessionID, rs, err)
		return
	}
	o.registerPending(&pendingApproval{
		sessionID:    sessionID,
		checkpointID: rs.checkpointID,
		interruptID:  rootID,
		callID:       ai.CallID,
	})
	// 暂停：保留 runs 条目，等待 DecideApproval 恢复
}

// finishInterrupted 用户停止：已产出前缀 finalize（runtime.md §8.2）。
func (o *orchestrator) finishInterrupted(sessionID string, rs *runState) {
	if rs.attempt != nil {
		attempt := rs.attempt
		rs.attempt = nil
		o.finishInterruptedAttempt(sessionID, rs, attempt)
	}
	o.endTurn(sessionID, rs, "interrupted", "")
}

func (o *orchestrator) finishInterruptedAttempt(sessionID string, rs *runState, attempt *attemptState) {
	for _, fc := range attempt.endChunks(true) {
		o.deps.Bus.Publish(sessionID, AssistantStreamFrame(StreamChunkFrame{
			Type: "chunk", AttemptID: attempt.id, Index: fc.index, Time: nowMs(), Chunk: fc.chunk,
		}))
	}
	if len(attempt.chunks) == 0 {
		return
	}
	seq := o.commitAssistant(sessionID, rs, attempt, true)
	o.deps.Bus.Publish(sessionID, AssistantStreamFrame(StreamEnd{
		Type: "end", AttemptID: attempt.id,
		Outcome: StreamOutcome{Kind: "committed", EventType: session.EventAssistantMessage, Seq: seq},
	}))
}

// failTurn 失败路径：assistant/attempt 留痕 + turn/end{failed}。
func (o *orchestrator) failTurn(sessionID string, rs *runState, err error) {
	if rs.attempt != nil {
		attempt := rs.attempt
		rs.attempt = nil
		o.failAttempt(sessionID, rs, attempt, err)
		return
	}
	o.endTurn(sessionID, rs, "failed", err.Error())
}

func (o *orchestrator) failAttempt(sessionID string, rs *runState, attempt *attemptState, cause error) {
	chunks, _ := json.Marshal(attempt.chunks)
	events, err := o.commit(context.Background(), sessionID, []session.EventInput{
		{Type: session.EventAssistantAttempt, Data: session.AssistantAttemptData{
			Turn: attempt.turn, Step: attempt.step, Chunks: chunks, Error: cause.Error(),
		}},
	})
	if err == nil && len(events) > 0 {
		o.deps.Bus.Publish(sessionID, AssistantStreamFrame(StreamEnd{
			Type: "end", AttemptID: attempt.id,
			Outcome: StreamOutcome{Kind: "abandoned", EventType: session.EventAssistantAttempt, Seq: events[0].Seq},
		}))
	}
	o.endTurn(sessionID, rs, "failed", cause.Error())
}

// endTurn 任何路径的 turn 收尾（turn/end + 释放会话锁 + 清理 checkpoint）。
func (o *orchestrator) endTurn(sessionID string, rs *runState, reason, errMsg string) {
	_, _ = o.commit(context.Background(), sessionID, []session.EventInput{
		{Type: session.EventTurnEnd, Data: session.TurnEndData{Turn: rs.turn, Reason: reason, Error: errMsg}},
	})
	if rs.checkpointID != "" {
		if d, ok := o.deps.Checkpoints(sessionID).(interface {
			Delete(context.Context, string) error
		}); ok {
			_ = d.Delete(context.Background(), rs.checkpointID)
		}
	}
	o.mu.Lock()
	delete(o.runs, sessionID)
	for id, p := range o.pending {
		if p.sessionID == sessionID {
			if p.timer != nil {
				p.timer.Stop()
			}
			if p.sub != nil {
				select {
				case p.sub.ch <- false:
				default:
				}
			}
			delete(o.pending, id)
		}
	}
	o.mu.Unlock()
}

// Cancel 用户停止：先 CancelAfterToolCalls（5s 超时自动降级 CancelImmediate）。
func (o *orchestrator) Cancel(sessionID string) error {
	o.mu.Lock()
	rs, ok := o.runs[sessionID]
	if !ok {
		o.mu.Unlock()
		return ErrSessionRunning
	}
	rs.cancelRequested = true
	cancel := rs.cancel
	o.mu.Unlock()
	if cancel != nil {
		_, _ = cancel(adk.WithAgentCancelMode(adk.CancelAfterToolCalls), adk.WithAgentCancelTimeout(5*time.Second))
	}
	return nil
}

// commit 持久事件落库（成功后才广播 event 帧——"模型可见 ⟺ 已落库"不变量）。
func (o *orchestrator) commit(ctx context.Context, sessionID string, inputs []session.EventInput) ([]store.SessionEvent, error) {
	events, err := o.deps.Events.AppendBatch(ctx, sessionID, inputs)
	if err != nil {
		return nil, err
	}
	for _, ev := range events {
		e := ev
		o.deps.Bus.Publish(sessionID, EventFrame(session.Event{Seq: e.Seq, Type: e.Type, Time: e.TimeMs, Data: e.Data}))
	}
	if n := len(events); n > 0 {
		o.mu.Lock()
		if rs := o.runs[sessionID]; rs != nil {
			rs.lastSeq = events[n-1].Seq
		}
		o.mu.Unlock()
	}
	go o.publishControl(sessionID)
	return events, nil
}

func (o *orchestrator) commitOK(sessionID string, rs *runState, inputs []session.EventInput) bool {
	if _, err := o.commit(context.Background(), sessionID, inputs); err != nil {
		o.failTurn(sessionID, rs, err)
		return false
	}
	return true
}

// publishControl control 流投影变更（title/running/usage/last_active），事件落库后发布。
func (o *orchestrator) publishControl(sessionID string) {
	PublishControlProjection(o.deps.Sessions, o.deps.Bus, sessionID)
}

// PublishControlProjection 发布单会话的 control 流投影（upsert/remove）。
// 供编排器事件提交后与 session.Service.AfterEvents 挂钩共用。
func PublishControlProjection(sessions SessionRepo, bus *Bus, sessionID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	sess, err := sessions.SessionByID(ctx, sessionID)
	if err != nil {
		return
	}
	if sess.Status == session.StatusDeleted {
		bus.PublishControl(sess.OrgID, ControlFrame{Type: "remove", Remove: sess.ID})
		return
	}
	usage := session.Usage{}
	_ = json.Unmarshal(sess.UsageTotal, &usage)
	bus.PublishControl(sess.OrgID, ControlFrame{Type: "upsert", Session: &ControlSession{
		SessionID:    sess.ID,
		Title:        sess.Title,
		Running:      sess.Running,
		AgentID:      sess.AgentID,
		UsageTotal:   usage,
		LastActiveAt: sess.LastActiveAt.UnixMilli(),
		Status:       sess.Status,
	}})
}
