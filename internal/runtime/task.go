package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"

	"github.com/ravenmk2/wispjar/internal/agent"
	"github.com/ravenmk2/wispjar/internal/id"
	"github.com/ravenmk2/wispjar/internal/provider"
	"github.com/ravenmk2/wispjar/internal/runtime/tools"
	"github.com/ravenmk2/wispjar/internal/session"
	"github.com/ravenmk2/wispjar/internal/store"
)

// task 委派（runtime.md §4.8/§6）：子智能体独立上下文运行、共享主会话沙箱句柄，
// 全部子事件带 parent_call_id 落库+广播，前端按 parent_call_id 聚合。

type taskArgs struct {
	Subagent string `json:"subagent" jsonschema:"description=name of the subagent to delegate to (see the tool description for available names)"`
	Prompt   string `json:"prompt" jsonschema:"description=complete self-contained task description; the subagent cannot see this conversation, so include everything it needs"`
}

type taskTool struct {
	o    *orchestrator
	sess *store.Session
	snap *agent.Snapshot
	tc   *tools.Context
	rs   *runState
}

func taskDescription(subs []agent.SnapshotSubAgent) string {
	var b strings.Builder
	b.WriteString("Delegate a self-contained task to a subagent. The subagent works in its own context (it does NOT see this conversation) and shares your filesystem. It returns only its final result. Available subagents:\n")
	for _, sa := range subs {
		fmt.Fprintf(&b, "- %s: %s\n", sa.Name, sa.Description)
	}
	b.WriteString("Use this when a subtask is focused and independent; include everything the subagent needs in `prompt`.")
	return b.String()
}

func matchSubAgent(subs []agent.SnapshotSubAgent, name string) *agent.SnapshotSubAgent {
	for i := range subs {
		if strings.EqualFold(subs[i].Name, name) {
			return &subs[i]
		}
	}
	return nil
}

// newTaskTool 仅在快照含子智能体时注册（builder 调用前提）。
func (o *orchestrator) newTaskTool(sess *store.Session, snap *agent.Snapshot, tc *tools.Context, rs *runState) (tool.BaseTool, error) {
	tt := &taskTool{o: o, sess: sess, snap: snap, tc: tc, rs: rs}
	return utils.InferTool("task", taskDescription(snap.SubAgents), tt.invoke)
}

// invoke 执行一次委派。子运行失败/中断不抛 Go error（eino ToolsNode 会把工具错误
// 升级为整个 run 失败），统一合成为结果文本回喂主 Agent（runtime.md §4.8 返回约定）。
func (t *taskTool) invoke(ctx context.Context, in taskArgs) (string, error) {
	if t.rs.taskSlots != nil {
		select {
		case t.rs.taskSlots <- struct{}{}:
			defer func() { <-t.rs.taskSlots }()
		case <-ctx.Done():
			return "error: task canceled before starting", nil
		}
	}

	meta := t.tc.BeginMeta(ctx, tools.RenderGeneric)
	start := time.Now()
	defer func() { meta.DurationMS = time.Since(start).Milliseconds() }()

	sub := matchSubAgent(t.snap.SubAgents, in.Subagent)
	if sub == nil {
		names := make([]string, 0, len(t.snap.SubAgents))
		for _, sa := range t.snap.SubAgents {
			names = append(names, sa.Name)
		}
		return fmt.Sprintf("error: subagent %q not found. Available subagents: %s", in.Subagent, strings.Join(names, ", ")), nil
	}
	if strings.TrimSpace(in.Prompt) == "" {
		return "error: prompt must not be empty", nil
	}

	childContext := t.tc.Fork(sub.ID, id.New(), sub.Skills)
	childContext.Env = parseSandboxConfig(sub.SandboxConfig).Env
	childSink := provider.NewUsageSink()
	ctx = tools.WithContext(ctx, childContext)
	ctx = provider.WithUsageSink(ctx, childSink)
	childStatus := "failed"
	if t.rs.registered {
		err := t.o.registerChildRun(ctx, t.rs, childContext)
		if err != nil {
			return "error: child run could not start", nil
		}
		tracker := tools.NewExecutionHandle(t.rs.execution.Handle)
		childContext.BoundHandle = tracker
		finish := t.o.childFinalizer(t.rs, childContext, tracker)
		defer func() {
			if ctx.Err() != nil {
				childStatus = "canceled"
			}
			finish(childStatus)
		}()
	}
	subAgent, err := t.o.buildSubAgent(ctx, t.sess, sub)
	if err != nil {
		return fmt.Sprintf("error: build subagent %q: %v", sub.Name, err), nil
	}

	sr := &subRun{
		root:         t.rs,
		o:            t.o,
		sessionID:    t.sess.ID,
		tc:           childContext,
		parentCallID: compose.GetToolCallID(ctx),
		turn:         t.rs.turn,
		step:         t.rs.currentStep(),
		denied:       newDenySet(),
		usageSink:    childSink,
	}
	checkpointID := id.New()
	sr.checkpointID = checkpointID
	runner := adk.NewRunner(ctx, adk.RunnerConfig{
		Agent:           subAgent,
		EnableStreaming: true,
		CheckPointStore: t.o.deps.Checkpoints(t.sess.ID),
	})
	defer func() {
		if d, ok := t.o.deps.Checkpoints(t.sess.ID).(interface {
			Delete(context.Context, string) error
		}); ok {
			_ = d.Delete(context.Background(), checkpointID)
		}
	}()

	iter := runner.Run(ctx, []*schema.Message{schema.UserMessage(in.Prompt)}, adk.WithCheckPointID(checkpointID))
	for {
		reason, intr, cause := sr.consume(iter)
		if intr == nil {
			switch reason {
			case "completed":
				childStatus = "completed"
			case "interrupted":
				childStatus = "canceled"
			}
			return sr.synthesize(sub.Name, reason, cause), nil
		}
		gate, err := sr.askApproval(intr)
		if err != nil {
			return sr.synthesize(sub.Name, "error", err), nil
		}
		allow, err := gate.wait(ctx)
		if err != nil {
			return sr.synthesize(sub.Name, "interrupted", err), nil
		}
		iter, err = runner.ResumeWithParams(ctx, checkpointID,
			&adk.ResumeParams{Targets: map[string]any{intr.interruptID: allow}},
			adk.WithCheckPointID(checkpointID))
		if err != nil {
			return sr.synthesize(sub.Name, "error", fmt.Errorf("resume: %w", err)), nil
		}
	}
}

// buildSubAgent 按子快照构建子 Agent（depth=1：不注册 task 工具；审批沿用会话 approval_mode）。
func (o *orchestrator) buildSubAgent(ctx context.Context, sess *store.Session, sub *agent.SnapshotSubAgent) (adk.Agent, error) {
	params := map[string]any{}
	if len(sub.ModelParams) > 0 {
		_ = json.Unmarshal(sub.ModelParams, &params)
	}
	// 子 Agent 未显式指定 effort 时继承父会话锁定的档位（runtime.md §6）。
	if _, ok := params["reasoning_effort"]; !ok {
		if effort, ok := sessionEffort(sess.ModelParams); ok {
			params["reasoning_effort"] = effort
		}
	}
	chatModel, err := o.deps.Models.NewChatModel(ctx, sub.Model.ID, params)
	if err != nil {
		return nil, err
	}
	subSnap := &agent.Snapshot{
		SearchProviderID: sub.SearchProviderID,
		AgentID:          sub.ID,
		Name:             sub.Name,
		Description:      sub.Description,
		Avatar:           sub.Avatar,
		SystemPrompt:     sub.SystemPrompt,
		MaxIterations:    sub.MaxIterations,
		ModelParams:      sub.ModelParams,
		ToolsEnabled:     sub.ToolsEnabled,
		SandboxConfig:    sub.SandboxConfig,
		Model:            sub.Model,
		Skills:           sub.Skills,
	}
	toolList, _, err := buildTools(nil, subSnap, sess.ApprovalMode, o.visionOf(ctx, sub.Model.ID))
	if err != nil {
		return nil, err
	}
	// depth=1：question 注册拒绝桩（子 Agent 不能提问，错误提示把未决问题
	// 写进最终结果由父 Agent 上浮；runtime.md §4.9）。
	question, err := o.newQuestionTool(sess, nil, 1)
	if err != nil {
		return nil, err
	}
	toolList = append(toolList, question)
	maxIter := sub.MaxIterations
	if maxIter <= 0 {
		maxIter = 20
	}
	return adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
		Name:        sub.ID,
		Description: sub.Description,
		Instruction: renderInstruction(subSnap, sess),
		Model:       chatModel,
		ToolsConfig: adk.ToolsConfig{
			ToolsNodeConfig: compose.ToolsNodeConfig{
				Tools:               toolList,
				ToolCallMiddlewares: []compose.ToolMiddleware{approvalMiddleware(sess.ApprovalMode), errorResultMiddleware()},
			},
		},
		MaxIterations: maxIter,
	})
}

// ---------- 嵌套事件 data（parent_call_id + sub_turn/sub_step，复用主事件词表） ----------

type nestedStepData struct {
	Turn         int    `json:"turn"`
	Step         int    `json:"step"`
	SubTurn      int    `json:"sub_turn"`
	SubStep      int    `json:"sub_step"`
	ParentCallID string `json:"parent_call_id"`
}

type nestedAssistantMessageData struct {
	Turn         int                      `json:"turn"`
	Step         int                      `json:"step"`
	SubTurn      int                      `json:"sub_turn"`
	SubStep      int                      `json:"sub_step"`
	ParentCallID string                   `json:"parent_call_id"`
	Message      session.AssistantMessage `json:"message"`
	Usage        *session.Usage           `json:"usage,omitempty"`
}

type nestedToolCallData struct {
	Turn         int    `json:"turn"`
	Step         int    `json:"step"`
	SubTurn      int    `json:"sub_turn"`
	SubStep      int    `json:"sub_step"`
	CallID       string `json:"call_id"`
	Name         string `json:"name"`
	Arguments    string `json:"arguments"`
	ParentCallID string `json:"parent_call_id"`
}

type nestedToolResultData struct {
	Turn         int                     `json:"turn"`
	Step         int                     `json:"step"`
	SubTurn      int                     `json:"sub_turn"`
	SubStep      int                     `json:"sub_step"`
	CallID       string                  `json:"call_id"`
	Name         string                  `json:"name"`
	Content      string                  `json:"content"`
	Error        string                  `json:"error,omitempty"`
	Meta         *session.ToolResultMeta `json:"meta,omitempty"`
	Attachments  []session.AttachmentRef `json:"attachments,omitempty"`
	ParentCallID string                  `json:"parent_call_id"`
}

type nestedApprovalAskedData struct {
	Turn         int    `json:"turn"`
	Step         int    `json:"step"`
	SubTurn      int    `json:"sub_turn"`
	SubStep      int    `json:"sub_step"`
	CallID       string `json:"call_id"`
	Name         string `json:"name"`
	Arguments    string `json:"arguments"`
	Risk         string `json:"risk"`
	ParentCallID string `json:"parent_call_id"`
}

type nestedApprovalDecidedData struct {
	session.ApprovalDecidedData
	ParentCallID string `json:"parent_call_id,omitempty"`
}

// ---------- 子运行状态 ----------

type denySet struct {
	mu sync.Mutex
	m  map[string]struct{}
}

func newDenySet() *denySet { return &denySet{m: map[string]struct{}{}} }

func (d *denySet) add(id string) {
	d.mu.Lock()
	d.m[id] = struct{}{}
	d.mu.Unlock()
}

func (d *denySet) take(id string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	_, ok := d.m[id]
	if ok {
		delete(d.m, id)
	}
	return ok
}

// subApproval 是子 Agent 审批的决议通道（挂在 pendingApproval 上，decide 投递结果）。
type subApproval struct {
	ch     chan bool
	denied *denySet
}

func (g *subApproval) wait(ctx context.Context) (bool, error) {
	select {
	case allow := <-g.ch:
		return allow, nil
	case <-ctx.Done():
		return false, ctx.Err()
	}
}

type subInterrupt struct {
	info        ApprovalInterruptInfo
	interruptID string
}

type subRun struct {
	root         *runState
	o            *orchestrator
	sessionID    string
	tc           *tools.Context
	parentCallID string
	checkpointID string
	turn         int // 主 turn
	step         int // 主 step（task 调用所在 step）
	subStep      int
	denied       *denySet
	// Each child owns its usage sink; concurrent responses cannot consume one another.
	usageSink *provider.UsageSink

	lastText  string // 最近一条 assistant 消息文本（失败时的部分输出）
	finalText string // 最近一条无 tool-call 的 assistant 消息文本（completed 时的 output）
}

func (sr *subRun) stepData() nestedStepData {
	return nestedStepData{
		Turn: sr.turn, Step: sr.step,
		SubTurn: 1, SubStep: sr.subStep,
		ParentCallID: sr.parentCallID,
	}
}

func (sr *subRun) commit(inputs []session.EventInput) error {
	_, err := sr.o.commitRun(context.Background(), sr.sessionID, sr.root, inputs)
	return err
}

// consume 消费子 AgentEvent 流直到结束/出错/审批中断（runtime.md §6.4）。
// 不产出嵌套 turn/start（derive 按最后一个 turn/start 截断历史，嵌套边界事件会污染主历史）。
func (sr *subRun) consume(iter *adk.AsyncIterator[*adk.AgentEvent]) (string, *subInterrupt, error) {
	for {
		event, ok := iter.Next()
		if !ok {
			return "completed", nil, nil
		}
		if event.Err != nil {
			var cancelErr *adk.CancelError
			switch {
			case errors.As(event.Err, &cancelErr),
				errors.Is(event.Err, context.Canceled),
				errors.Is(event.Err, context.DeadlineExceeded):
				return "interrupted", nil, event.Err
			case errors.Is(event.Err, adk.ErrExceedMaxIterations):
				return "max_iterations", nil, event.Err
			default:
				return "error", nil, event.Err
			}
		}
		if event.Action != nil && event.Action.Interrupted != nil {
			ai, rootID, ok := approvalFromInterrupt(event.Action.Interrupted)
			if !ok {
				return "error", nil, errors.New("unknown interrupt (not an approval request)")
			}
			return "", &subInterrupt{info: ai, interruptID: rootID}, nil
		}
		mv := event.Output
		if mv == nil || mv.MessageOutput == nil {
			continue
		}
		out := mv.MessageOutput
		var err error
		switch {
		case out.Role == schema.Assistant && out.IsStreaming:
			err = sr.stepStream(out.MessageStream)
		case out.Role == schema.Assistant && out.Message != nil:
			err = sr.stepMessage(out.Message)
		case out.Role == schema.Tool && out.Message != nil:
			err = sr.toolResult(out.Message)
		}
		if err != nil {
			return "error", nil, err
		}
	}
}

// stepStream 消费一次子模型流式调用（一个 sub_step；瞬态帧 v1 不向主会话广播）。
func (sr *subRun) stepStream(stream *schema.StreamReader[*schema.Message]) error {
	sr.subStep++
	attempt := newAttempt(sr.turn, sr.subStep)
	if err := sr.commit([]session.EventInput{{Type: session.EventStepStart, Data: sr.stepData()}}); err != nil {
		return err
	}
	err := attempt.drainStream(stream, func(framedChunk) {})
	attempt.captured = sr.usageSink.TakeLatest()
	if err != nil {
		if attempt.text != nil && attempt.text.text != "" {
			sr.lastText = attempt.text.text
		}
		return err
	}
	return sr.commitAssistant(attempt)
}

func (sr *subRun) stepMessage(msg *schema.Message) error {
	sr.subStep++
	attempt := newAttempt(sr.turn, sr.subStep)
	attempt.feed(msg)
	attempt.captured = sr.usageSink.TakeLatest()
	if err := sr.commit([]session.EventInput{{Type: session.EventStepStart, Data: sr.stepData()}}); err != nil {
		return err
	}
	return sr.commitAssistant(attempt)
}

func (sr *subRun) commitAssistant(attempt *attemptState) error {
	asst := attempt.assistantMessage()
	text := ""
	hasToolCalls := false
	for _, b := range asst.Content {
		switch b.Type {
		case "text":
			text += b.Text
		case "tool-call":
			hasToolCalls = true
		}
	}
	if text != "" {
		sr.lastText = text
	}
	if !hasToolCalls {
		sr.finalText = text
	}
	inputs := []session.EventInput{
		{Type: session.EventAssistantMessage, Data: nestedAssistantMessageData{
			Turn: sr.turn, Step: sr.step,
			SubTurn: 1, SubStep: sr.subStep,
			ParentCallID: sr.parentCallID,
			Message:      asst,
			Usage:        eventUsage(attempt.usage, attempt.captured),
		}},
	}
	for _, b := range asst.Content {
		if b.Type != "tool-call" {
			continue
		}
		inputs = append(inputs, session.EventInput{Type: session.EventToolCall, Data: nestedToolCallData{
			Turn: sr.turn, Step: sr.step,
			SubTurn: 1, SubStep: sr.subStep,
			CallID: b.ID, Name: b.Name, Arguments: b.Arguments,
			ParentCallID: sr.parentCallID,
		}})
	}
	inputs = append(inputs, session.EventInput{Type: session.EventStepEnd, Data: sr.stepData()})
	return sr.commit(inputs)
}

func (sr *subRun) toolResult(msg *schema.Message) error {
	data := nestedToolResultData{
		Turn: sr.turn, Step: sr.step,
		SubTurn: 1, SubStep: sr.subStep,
		CallID: msg.ToolCallID, Name: msg.ToolName, Content: toolMessageContent(msg),
		ParentCallID: sr.parentCallID,
	}
	if m := sr.tc.TakeMeta(msg.ToolCallID); m != nil {
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
	if sr.denied.take(msg.ToolCallID) {
		data.Error = deniedResult
		data.Content = deniedResult
	}
	return sr.commit([]session.EventInput{{Type: session.EventToolResult, Data: data}})
}

// askApproval 落库嵌套 approval/asked 并把决议通道登记进主审批管线。
func (sr *subRun) askApproval(intr *subInterrupt) (*subApproval, error) {
	if err := sr.commit([]session.EventInput{{Type: session.EventApprovalAsked, Data: nestedApprovalAskedData{
		Turn: sr.turn, Step: sr.step,
		SubTurn: 1, SubStep: sr.subStep,
		CallID: intr.info.CallID, Name: intr.info.Name,
		Arguments: intr.info.Arguments, Risk: intr.info.Risk,
		ParentCallID: sr.parentCallID,
	}}}); err != nil {
		return nil, err
	}
	gate := &subApproval{ch: make(chan bool, 1), denied: sr.denied}
	sr.o.registerPending(&pendingApproval{
		root:         sr.root,
		sessionID:    sr.sessionID,
		checkpointID: sr.checkpointID,
		interruptID:  intr.interruptID,
		callID:       intr.info.CallID,
		parentCallID: sr.parentCallID,
		sub:          gate,
	})
	return gate, nil
}

// synthesize 结果合成（runtime.md §4.8）：completed → {"output": ...}；
// 其余 stopReason → "<原因>\nDiagnostic: ...\nPartial output before the run ended:\n<部分文本>"。
func (sr *subRun) synthesize(subName, reason string, cause error) string {
	if reason == "completed" {
		out, err := json.Marshal(map[string]any{"output": sr.finalText})
		if err != nil {
			return fmt.Sprintf("error: marshal subagent output: %v", err)
		}
		return string(out)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "subagent %q did not complete the task (reason: %s)", subName, reason)
	b.WriteString("\nDiagnostic: ")
	if cause != nil {
		b.WriteString(cause.Error())
	} else {
		b.WriteString("no diagnostic available")
	}
	b.WriteString("\nPartial output before the run ended:\n")
	if sr.lastText != "" {
		b.WriteString(sr.lastText)
	} else {
		b.WriteString("(no output)")
	}
	return b.String()
}
