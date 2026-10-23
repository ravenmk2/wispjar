package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"github.com/ravenmk2/wispjar/internal/agent"
	"github.com/ravenmk2/wispjar/internal/id"
	"github.com/ravenmk2/wispjar/internal/provider"
	"github.com/ravenmk2/wispjar/internal/runtime/tools"
	"github.com/ravenmk2/wispjar/internal/session"
	"github.com/ravenmk2/wispjar/internal/store"
)

// ModelFactory 由 internal/provider.ChatModelFactory 实现（密钥不出 provider 包）。
type ModelFactory interface {
	NewChatModel(ctx context.Context, modelID string, params map[string]any) (model.ToolCallingChatModel, error)
}

// AttachmentReader 按引用读附件字节（internal/attachment.Service 实现；读时校验 sha256）。
type AttachmentReader interface {
	ReadBytes(ctx context.Context, id string) ([]byte, error)
}

type Deps struct {
	WebSearch        tools.WebSearcher
	AgentEnvs        func(context.Context, string, []string) (map[string]map[string]string, error)
	Computers        ComputerService
	Skills           SkillCoordinator
	MaxParallelTasks int
	Events           EventStore
	Sessions         SessionRepo
	Models           ModelFactory
	Checkpoints      func(sessionID string) adk.CheckPointStore
	Bus              *Bus
	ContextSize      func(ctx context.Context, modelID string) int
	// ProviderType 按 model_id 查供应商类型（ark 注入 prompt_cache_key 用；测试可为 nil，
	// 为 nil 时回退快照中的 provider_type）。
	ProviderType func(ctx context.Context, modelID string) string
	// ToolContextFactory 注入完整电脑执行上下文并跳过远端准备（仅测试；生产为 nil）。
	ToolContextFactory func(sess *store.Session, snap *agent.Snapshot) *tools.Context
	// Attachments 附件字节读取（图片组装用；历史含附件时必配）。
	Attachments AttachmentReader
	// ImageSaver read_media 图片入库（*attachment.Service；nil 时 read_media 拒绝执行）。
	ImageSaver tools.ImageSaver
	ToolImages func(ctx context.Context, modelID string) bool
	// QuestionWaitTimeout 提问工具前台等待窗口（0 = 默认 10 分钟；测试注入小值）。
	QuestionWaitTimeout time.Duration
}

// Orchestrator 是运行时总链路的入口（runtime.md §1）。
type Orchestrator interface {
	StartTurn(ctx context.Context, sessionID string) error
	Cancel(sessionID string) error
	DecideApproval(sessionID, callID string, allow bool, by string) error
	// AnswerQuestion 投递提问答案；open 状态返回 true，无内存等待（continued）返回 false。
	AnswerQuestion(sessionID, callID string, answers []session.QuestionAnswer, by string) (bool, error)
	Follow(sessionID string, afterSeq int64) (*Snapshot, <-chan FollowFrame, func(), error)
	RecoverPendingApprovals(ctx context.Context) error
}

type runState struct {
	eventMu            sync.Mutex
	retired            bool
	resourceMu         sync.Mutex
	preparing          bool
	registered         bool
	skillAcquired      bool
	controlUncertain   bool
	execution          *tools.ExecutionHandle
	childActive        int
	childUncertain     bool
	ending             bool
	endStatus          string
	resourcesFinished  bool
	resourcesFinishing bool
	finishRetries      int
	taskSlots          chan struct{}
	runContext         context.Context
	contextCancel      context.CancelFunc
	endOnce            sync.Once
	agent              adk.Agent
	cancel             adk.AgentCancelFunc
	cancelRequested    bool
	checkpointID       string
	turn               int
	stepMu             sync.Mutex // 保护 step：task 工具 goroutine 与 consume goroutine 并发访问
	step               int
	lastSeq            int64
	attempt            *attemptState
	denied             map[string]struct{}
	toolCtx            *tools.Context
	// usageSink 本 turn 的原始 usage 抓取收集器（挂 iterCtx，transport 旁路写入）。
	usageSink *provider.UsageSink
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

	mu        sync.RWMutex
	runs      map[string]*runState
	pending   map[string]*pendingApproval
	questions map[string]*pendingQuestion

	questionTimeout time.Duration

	titled sync.Map // sessionID -> struct{}：每进程只尝试一次自动标题
}

func NewOrchestrator(deps Deps) Orchestrator {
	questionTimeout := deps.QuestionWaitTimeout
	if questionTimeout <= 0 {
		questionTimeout = defaultQuestionWaitTimeout
	}
	return &orchestrator{
		deps:            deps,
		runs:            map[string]*runState{},
		pending:         map[string]*pendingApproval{},
		questions:       map[string]*pendingQuestion{},
		questionTimeout: questionTimeout,
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
	runContext, contextCancel := context.WithCancel(context.Background())
	rs := &runState{denied: map[string]struct{}{}, runContext: runContext, contextCancel: contextCancel, preparing: true}
	o.runs[sessionID] = rs
	o.mu.Unlock()

	prepareErr := o.prepare(ctx, sessionID, rs)
	rs.resourceMu.Lock()
	rs.preparing = false
	ended := rs.ending
	rs.resourceMu.Unlock()
	if ended && prepareErr == nil {
		prepareErr = context.Canceled
	}
	if err := prepareErr; err != nil {
		log.Printf("runtime: prepare session %s: %v", sessionID, err)
		publicErr := publicPreparationError(err)
		contextCancel()
		o.finishResources(rs, "failed")
		if rs.turn > 0 {
			_, _ = o.commitRun(context.Background(), sessionID, rs, []session.EventInput{
				{Type: session.EventTurnEnd, Data: session.TurnEndData{Turn: rs.turn, Reason: "failed", Error: publicErr.Message, ErrorCode: publicErr.Code}},
			})
		}
		o.mu.Lock()
		if o.runs[sessionID] == rs {
			delete(o.runs, sessionID)
		}
		o.mu.Unlock()
		return publicErr
	}

	iterCtx := tools.WithContext(rs.runContext, rs.toolCtx)
	iterCtx = provider.WithMediaOrg(iterCtx, rs.toolCtx.OrgID)
	iterCtx = provider.WithUsageSink(iterCtx, rs.usageSink)
	cancelOpt, cancelFn := adk.WithCancel()
	o.mu.Lock()
	rs.cancel = cancelFn
	canceled := rs.cancelRequested
	o.mu.Unlock()
	if canceled {
		_, _ = cancelFn(adk.WithAgentCancelMode(adk.CancelImmediate))
	}
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
	rs.usageSink = provider.NewUsageSink()
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
	if err := o.refreshAgentEnvs(ctx, sess.OrgID, &snap); err != nil {
		return err
	}
	rs.toolCtx = o.buildToolContext(sess, &snap)
	parallel := o.deps.MaxParallelTasks
	if parallel <= 0 {
		parallel = 4
	}
	rs.taskSlots = make(chan struct{}, parallel)
	if err := o.prepareComputer(rs.runContext, sess, &snap, rs); err != nil {
		return err
	}
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
	offloaded := planImageOffload(derived)
	msgs := make([]*schema.Message, 0, len(derived))
	for i, m := range derived {
		switch m.Role {
		case session.RoleUser:
			if len(m.Attachments) > 0 {
				msg, err := o.buildUserMessage(ctx, m, offloaded[i])
				if err != nil {
					return nil, err
				}
				msgs = append(msgs, msg)
			} else {
				msgs = append(msgs, schema.UserMessage(m.Content))
			}
		case session.RoleAssistant:
			calls := make([]schema.ToolCall, 0, len(m.ToolCalls))
			for _, tc := range m.ToolCalls {
				calls = append(calls, schema.ToolCall{
					ID:       tc.ID,
					Type:     "function",
					Function: schema.FunctionCall{Name: tc.Name, Arguments: tc.Arguments},
				})
			}
			msg := schema.AssistantMessage(m.Content, calls)
			if m.ProviderReplay != "" {
				msg.Extra = map[string]any{provider.ReplayKey: m.ProviderReplay}
			}
			msgs = append(msgs, msg)
		case session.RoleTool:
			if len(m.Attachments) > 0 {
				msg, err := o.buildToolMessage(ctx, m, offloaded[i])
				if err != nil {
					return nil, err
				}
				msgs = append(msgs, msg)
			} else {
				msgs = append(msgs, schema.ToolMessage(m.Content, m.ToolCallID, schema.WithToolName(m.ToolName)))
			}
		}
	}
	return o.trimContext(ctx, sessionID, msgs)
}

// imageTokenEstimate 是 trimContext 对单张图片的保守 token 估值：
// 视觉模型按像素折算（DeepSeek ≤512、ark high 1280 tok/图），取最保守上沿。
const imageTokenEstimate = 1280

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
			for _, p := range m.UserInputMultiContent {
				total += len(p.Text)
				if p.Image != nil {
					// 视觉模型按像素折算 token（DeepSeek ≤512、ark high 1280 tok/图），
					// 与 base64 体积无关；取最保守值计入。卸载后的占位文本按文本计。
					total += imageTokenEstimate * 4
				}
			}
			for _, tc := range m.ToolCalls {
				total += len(tc.Function.Arguments)
			}
		}
		return total / 4
	}
	// system 恒保留（防御性：当前链路 system 走 Instruction 不在 msgs 内）。
	var head []*schema.Message
	if len(msgs) > 0 && msgs[0].Role == schema.System {
		head, msgs = msgs[:1], msgs[1:]
	}
	// 按 turn 整块丢弃：从最早的用户轮次开始整轮（user 消息 + 后续 assistant/tool
	// 消息）丢弃直到估算量达标。窗口起点恒落在轮次边界，不随每轮新增内容逐条漂移
	// （runtime.md《缓存命中优先设计原则》§7）。
	over := func() bool { return estimate(head)+estimate(msgs) > limit }
	for over() {
		drop := len(msgs)
		seenUser := false
		for i, m := range msgs {
			if m.Role == schema.User {
				if seenUser {
					drop = i
					break
				}
				seenUser = true
			}
		}
		if drop == len(msgs) {
			// 只剩一个轮次，无法继续按轮裁剪
			break
		}
		msgs = msgs[drop:]
	}
	if over() {
		return nil, errors.New("上下文超出模型窗口，建议开启新会话")
	}
	return append(head, msgs...), nil
}

// consume 消费事件迭代器：翻译 → 落库 → 广播；返回时 turn 已收尾或已暂停（审批中断）。
func (o *orchestrator) consume(sessionID string, rs *runState, iter *adk.AsyncIterator[*adk.AgentEvent]) {
	for {
		event, ok := iter.Next()
		rs.eventMu.Lock()
		retired := rs.retired
		rs.eventMu.Unlock()
		if retired {
			return
		}
		if !ok {
			break
		}
		if event.Err != nil {
			var cancelErr *adk.CancelError
			if errors.As(event.Err, &cancelErr) || errors.Is(event.Err, context.Canceled) {
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
	o.publishRun(sessionID, rs, AssistantStreamFrame(StreamStart{
		Type: "start", AttemptID: attempt.id, Turn: rs.turn, Step: rs.step, StartedAfterSeq: startedAfter,
	}))
	err := attempt.drainStream(stream, func(fc framedChunk) {
		o.publishRun(sessionID, rs, AssistantStreamFrame(StreamChunkFrame{
			Type: "chunk", AttemptID: attempt.id, Index: fc.index, Time: nowMs(), Chunk: fc.chunk,
		}))
	})
	// 取本 step 旁路抓取的归一化缓存用量（usage 在末帧，drain 完成后已入 sink）；
	// 失败路径同样取走丢弃，避免残留进下一 step。
	attempt.captured = rs.usageSink.TakeLatest()
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
		o.publishRun(sessionID, rs, AssistantStreamFrame(StreamChunkFrame{
			Type: "chunk", AttemptID: attempt.id, Index: fc.index, Time: nowMs(), Chunk: fc.chunk,
		}))
	}
	rs.attempt = nil
	seq := o.commitAssistant(sessionID, rs, attempt, false)
	o.publishRun(sessionID, rs, AssistantStreamFrame(StreamEnd{
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
	// 非流式响应 body 在 Generate 返回前已读尽，抓取结果已入 sink。
	attempt.captured = rs.usageSink.TakeLatest()
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
			Usage: eventUsage(attempt.usage, attempt.captured), Interrupted: interrupted,
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
	events, err := o.commitRun(context.Background(), sessionID, rs, o.assistantInputs(rs, attempt, interrupted))
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
		Content: toolMessageContent(msg),
	}
	if m := rs.toolCtx.TakeMeta(msg.ToolCallID); m != nil {
		data.Meta = &session.ToolResultMeta{
			ComputerID: m.ComputerID, RunID: m.RunID,
			DurationMS: m.DurationMS,
			Render:     m.Render,
			Truncated:  m.Truncated,
			SandboxID:  m.SandboxID,
			Diff:       m.Diff,
			Operation:  m.Operation,
		}
		data.Attachments = m.Attachments
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
	if _, err := o.commitRun(context.Background(), sessionID, rs, []session.EventInput{
		{Type: session.EventApprovalAsked, Data: session.ApprovalAskedData{
			Turn: rs.turn, Step: rs.step, CallID: ai.CallID,
			Name: ai.Name, Arguments: ai.Arguments, Risk: ai.Risk,
		}},
	}); err != nil {
		o.failTurn(sessionID, rs, err)
		return
	}
	o.registerPending(&pendingApproval{
		root:         rs,
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
	if attempt.captured == nil {
		// 中断时消费可能已抓到的 usage（多数情况流未读到末帧，返回 nil），避免残留。
		attempt.captured = rs.usageSink.TakeLatest()
	}
	for _, fc := range attempt.endChunks(true) {
		o.publishRun(sessionID, rs, AssistantStreamFrame(StreamChunkFrame{
			Type: "chunk", AttemptID: attempt.id, Index: fc.index, Time: nowMs(), Chunk: fc.chunk,
		}))
	}
	if len(attempt.chunks) == 0 {
		return
	}
	seq := o.commitAssistant(sessionID, rs, attempt, true)
	o.publishRun(sessionID, rs, AssistantStreamFrame(StreamEnd{
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
	events, err := o.commitRun(context.Background(), sessionID, rs, []session.EventInput{
		{Type: session.EventAssistantAttempt, Data: session.AssistantAttemptData{
			Turn: attempt.turn, Step: attempt.step, Chunks: chunks, Error: cause.Error(),
		}},
	})
	if err == nil && len(events) > 0 {
		o.publishRun(sessionID, rs, AssistantStreamFrame(StreamEnd{
			Type: "end", AttemptID: attempt.id,
			Outcome: StreamOutcome{Kind: "abandoned", EventType: session.EventAssistantAttempt, Seq: events[0].Seq},
		}))
	}
	o.endTurn(sessionID, rs, "failed", cause.Error())
}

// endTurn 任何路径的 turn 收尾（turn/end + 释放会话锁 + 清理 checkpoint）。
func (o *orchestrator) endTurn(sessionID string, rs *runState, reason, errMsg string) {
	rs.endOnce.Do(func() { o.completeTurn(sessionID, rs, reason, errMsg) })
}
func (o *orchestrator) completeTurn(sessionID string, rs *runState, reason, errMsg string) {
	if rs.contextCancel != nil {
		rs.contextCancel()
	}
	status := reason
	if reason == "interrupted" {
		status = "canceled"
	}
	o.finishResources(rs, status)
	_, _ = o.commitRun(context.Background(), sessionID, rs, []session.EventInput{
		{Type: session.EventTurnEnd, Data: session.TurnEndData{Turn: rs.turn, Reason: reason, Error: errMsg}},
	})
	rs.eventMu.Lock()
	rs.retired = true
	rs.eventMu.Unlock()
	if rs.checkpointID != "" {
		if d, ok := o.deps.Checkpoints(sessionID).(interface {
			Delete(context.Context, string) error
		}); ok {
			_ = d.Delete(context.Background(), rs.checkpointID)
		}
	}
	o.forgetRun(sessionID, rs)
}

func (o *orchestrator) forgetRun(sessionID string, rs *runState) {
	o.mu.Lock()
	if o.runs[sessionID] != rs {
		o.mu.Unlock()
		return
	}
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
	// 提问等待随 turn 收尾防御性清理（正常路径工具在 turn 结束前已返回并自注销）；
	// 未答的问题无 question/answered，投影上仍属 continued 可补答。
	for id, w := range o.questions {
		if w.sessionID == sessionID {
			delete(o.questions, id)
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
	contextCancel := rs.contextCancel
	paused := false
	for _, pending := range o.pending {
		if pending.sessionID == sessionID && pending.sub == nil {
			paused = true
			break
		}
	}
	o.mu.Unlock()
	if contextCancel != nil {
		contextCancel()
	}
	if cancel != nil {
		_, _ = cancel(adk.WithAgentCancelMode(adk.CancelAfterToolCalls), adk.WithAgentCancelTimeout(5*time.Second))
	}
	if paused {
		o.endTurn(sessionID, rs, "interrupted", "")
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
	if _, err := o.commitRun(context.Background(), sessionID, rs, inputs); err != nil {
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
		bus.PublishControl(sess.OrgID, ControlFrame{Type: "remove", Remove: sess.ID, UserID: sess.UserID})
		return
	}
	usage := session.Usage{}
	_ = json.Unmarshal(sess.UsageTotal, &usage)
	bus.PublishControl(sess.OrgID, ControlFrame{Type: "upsert", UserID: sess.UserID, Session: &ControlSession{
		SessionID:    sess.ID,
		Title:        sess.Title,
		Running:      sess.Running,
		AgentID:      sess.AgentID,
		UsageTotal:   usage,
		LastActiveAt: sess.LastActiveAt.UnixMilli(),
		Status:       sess.Status,
	}})
}
