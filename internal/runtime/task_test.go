package runtime

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"github.com/ravenmk2/wispjar/internal/agent"
	"github.com/ravenmk2/wispjar/internal/runtime/tools"
	"github.com/ravenmk2/wispjar/internal/session"
	"github.com/ravenmk2/wispjar/internal/store"
)

// ---------- 主/子模型分流工厂 ----------

type scriptedModelFactory struct {
	models map[string]*fakeModel
}

func (f scriptedModelFactory) NewChatModel(_ context.Context, modelID string, _ map[string]any) (model.ToolCallingChatModel, error) {
	return f.models[modelID], nil
}

// ---------- task 测试装配 ----------

type taskTestEnv struct {
	*testEnv
	main *fakeModel
	sub  *fakeModel
}

func newTaskTestEnv(t *testing.T, approvalMode string, mainScript, subScript func(call int, input []*schema.Message) []*schema.Message) *taskTestEnv {
	t.Helper()
	snap := agent.Snapshot{
		AgentID:       "agent-1",
		Name:          "主编排智能体",
		SystemPrompt:  "你是主编排助手。",
		MaxIterations: 10,
		ToolsEnabled:  json.RawMessage(`{"exec":true,"read":true,"write":true,"edit":true,"glob":true,"grep":true}`),
		Model:         agent.SnapshotModel{ID: "model-main", ModelCode: "fake-main", ProviderID: "prov-1"},
		SubAgents: []agent.SnapshotSubAgent{{
			ID:            "sub-1",
			Name:          "data-analyst",
			Description:   "数据分析与可视化，擅长 pandas/matplotlib",
			SystemPrompt:  "你是数据分析子智能体。",
			MaxIterations: 5,
			ToolsEnabled:  json.RawMessage(`{"exec":true,"read":true}`),
			Model:         agent.SnapshotModel{ID: "model-sub", ModelCode: "fake-sub", ProviderID: "prov-1"},
		}},
	}
	snapRaw, err := json.Marshal(snap)
	if err != nil {
		t.Fatal(err)
	}
	sess := &store.Session{
		ID:            "sess-1",
		OrgID:         "org-1",
		UserID:        "user-1",
		AgentID:       "agent-1",
		AgentSnapshot: snapRaw,
		ComputerID:    ptrComputerID("computer-test"),
		ApprovalMode:  approvalMode,
		ModelID:       "model-main",
		Status:        session.StatusActive,
	}
	es := &fakeEventStore{}
	repo := &fakeRepo{sess: sess, es: es}
	main := &fakeModel{script: mainScript}
	sub := &fakeModel{script: subScript}
	handle := newFakeHandle()
	bus := NewBus()
	cp := newFakeCheckpointStore()

	orch := NewOrchestrator(Deps{
		Events:      es,
		Sessions:    repo,
		Models:      scriptedModelFactory{models: map[string]*fakeModel{"model-main": main, "model-sub": sub}},
		Checkpoints: func(sessionID string) adk.CheckPointStore { return cp },
		Bus:         bus,
		ToolContextFactory: func(sess *store.Session, snap *agent.Snapshot) *tools.Context {
			tc := tools.NewContext(sess.ID, sess.OrgID)
			tc.BoundHandle, tc.BoundSandboxID = handle, "fake-sandbox"
			return tc
		},
	})
	return &taskTestEnv{
		testEnv: &testEnv{orch: orch, es: es, repo: repo, model: main, handle: handle, bus: bus, session: sess},
		main:    main,
		sub:     sub,
	}
}

func TestWriteOperationPersistsInRootAndNestedResults(t *testing.T) {
	for _, nested := range []bool{false, true} {
		name := "root"
		if nested {
			name = "nested"
		}
		t.Run(name, func(t *testing.T) {
			writeScript := func(call int, _ []*schema.Message) []*schema.Message {
				if call == 1 {
					return toolCallChunks("write-file", "write", `{"path":"/workspace/file.txt","content":"new\n"}`)
				}
				return textChunks("done", nil)
			}
			var env *testEnv
			if nested {
				taskEnv := newTaskTestEnv(t, session.ApprovalAuto,
					func(call int, _ []*schema.Message) []*schema.Message {
						if call == 1 {
							return taskCallChunks("task-write", "data-analyst", "write file")
						}
						return textChunks("done", nil)
					}, writeScript)
				env = taskEnv.testEnv
				var snap agent.Snapshot
				if err := json.Unmarshal(env.session.AgentSnapshot, &snap); err != nil {
					t.Fatal(err)
				}
				snap.SubAgents[0].ToolsEnabled = json.RawMessage(`{"write":true}`)
				raw, err := json.Marshal(snap)
				if err != nil {
					t.Fatal(err)
				}
				env.session.AgentSnapshot = raw
			} else {
				env = newTestEnv(t, session.ApprovalAuto, writeScript)
			}
			env.handle.files["/workspace/file.txt"] = []byte("old\n")
			env.prompt(t, "write file", "req-write")
			if err := env.orch.StartTurn(context.Background(), env.session.ID); err != nil {
				t.Fatal(err)
			}
			for _, ev := range env.es.byType(session.EventToolResult) {
				var result session.ToolResultData
				if err := json.Unmarshal(ev.Data, &result); err != nil {
					t.Fatal(err)
				}
				if result.CallID != "write-file" {
					continue
				}
				if result.Meta == nil || result.Meta.Operation != "update" || !strings.Contains(result.Meta.Diff, "+new") {
					t.Fatalf("write presentation metadata not persisted: %+v", result.Meta)
				}
				return
			}
			t.Fatal("write result missing")
		})
	}
}

func taskCallChunks(callID, subagent, prompt string) []*schema.Message {
	args, _ := json.Marshal(map[string]string{"subagent": subagent, "prompt": prompt})
	return toolCallChunks(callID, "task", string(args))
}

// 两个并发 task tool-call（同一条 assistant 消息）。
func twoTaskCallChunks(args1, args2 string) []*schema.Message {
	idx0, idx1 := 0, 1
	return []*schema.Message{
		{Role: schema.Assistant, ToolCalls: []schema.ToolCall{
			{Index: &idx0, ID: "call_t1", Type: "function", Function: schema.FunctionCall{Name: "task", Arguments: args1}},
			{Index: &idx1, ID: "call_t2", Type: "function", Function: schema.FunctionCall{Name: "task", Arguments: args2}},
		}, ResponseMeta: &schema.ResponseMeta{FinishReason: "tool_calls"}},
	}
}

func taskResultOf(t *testing.T, es *fakeEventStore, callID string) session.ToolResultData {
	t.Helper()
	for _, ev := range es.byType(session.EventToolResult) {
		var d session.ToolResultData
		if json.Unmarshal(ev.Data, &d) == nil && d.CallID == callID {
			return d
		}
	}
	t.Fatalf("tool/result for %s not found; events: %v", callID, es.types())
	return session.ToolResultData{}
}

// ---------- 全流程：委派 → 嵌套事件 → 结果合成 → derive 过滤 ----------

func TestTaskDelegateFullFlow(t *testing.T) {
	var mainCall2Input []*schema.Message
	env := newTaskTestEnv(t, session.ApprovalAuto,
		func(call int, input []*schema.Message) []*schema.Message {
			if call == 1 {
				return taskCallChunks("call_task1", "Data-Analyst", "分析 /workspace 下的数据")
			}
			mainCall2Input = input
			return textChunks("子智能体结论已收到。", nil)
		},
		func(call int, input []*schema.Message) []*schema.Message {
			return textChunks("分析结论：42", &schema.TokenUsage{PromptTokens: 7, CompletionTokens: 3})
		})
	env.prompt(t, "帮我做个数据分析", "req-1")

	if err := env.orch.StartTurn(context.Background(), env.session.ID); err != nil {
		t.Fatalf("StartTurn: %v", err)
	}

	// 主 task 调用/结果
	calls := env.es.byType(session.EventToolCall)
	var taskCall *session.ToolCallData
	for _, ev := range calls {
		var d session.ToolCallData
		if json.Unmarshal(ev.Data, &d) == nil && d.Name == "task" && d.ParentCallID == "" {
			d2 := d
			taskCall = &d2
		}
	}
	if taskCall == nil {
		t.Fatalf("main task tool/call not found; events: %v", env.es.types())
	}
	if taskCall.CallID != "call_task1" {
		t.Fatalf("task call_id = %s", taskCall.CallID)
	}

	tr := taskResultOf(t, env.es, "call_task1")
	var out struct {
		Output string `json:"output"`
	}
	if err := json.Unmarshal([]byte(tr.Content), &out); err != nil || out.Output != "分析结论：42" {
		t.Fatalf("task result content = %q (err %v)", tr.Content, err)
	}
	if tr.Meta == nil || tr.Meta.Render != "generic" {
		t.Fatalf("task result meta = %+v", tr.Meta)
	}

	// 嵌套事件：parent_call_id 正确 + sub_step 序列
	type nestedProbe struct {
		ParentCallID string `json:"parent_call_id"`
		SubTurn      int    `json:"sub_turn"`
		SubStep      int    `json:"sub_step"`
	}
	nested := []struct {
		typ string
		p   nestedProbe
		raw json.RawMessage
	}{}
	env.es.mu.Lock()
	for _, ev := range env.es.events {
		var p nestedProbe
		if json.Unmarshal(ev.Data, &p) == nil && p.ParentCallID != "" {
			nested = append(nested, struct {
				typ string
				p   nestedProbe
				raw json.RawMessage
			}{ev.Type, p, ev.Data})
		}
	}
	env.es.mu.Unlock()
	if len(nested) != 3 {
		t.Fatalf("want 3 nested events (step/start, assistant/message, step/end), got %d: %v", len(nested), env.es.types())
	}
	wantTypes := []string{session.EventStepStart, session.EventAssistantMessage, session.EventStepEnd}
	for i, n := range nested {
		if n.typ != wantTypes[i] {
			t.Fatalf("nested[%d] type = %s, want %s", i, n.typ, wantTypes[i])
		}
		if n.p.ParentCallID != "call_task1" {
			t.Fatalf("nested[%d] parent_call_id = %s", i, n.p.ParentCallID)
		}
		if n.p.SubTurn != 1 || n.p.SubStep != 1 {
			t.Fatalf("nested[%d] sub_turn/sub_step = %d/%d", i, n.p.SubTurn, n.p.SubStep)
		}
	}
	var subMsg nestedAssistantMessageData
	if err := json.Unmarshal(nested[1].raw, &subMsg); err != nil {
		t.Fatal(err)
	}
	if len(subMsg.Message.Content) != 1 || subMsg.Message.Content[0].Text != "分析结论：42" {
		t.Fatalf("nested assistant/message = %+v", subMsg.Message)
	}
	if subMsg.Usage == nil || subMsg.Usage.InputTokens != 7 {
		t.Fatalf("nested usage = %+v", subMsg.Usage)
	}

	// 主模型第二轮收到了 task 结果
	sawResult := false
	for _, m := range mainCall2Input {
		if m.Role == schema.Tool && m.ToolCallID == "call_task1" && strings.Contains(m.Content, "分析结论：42") {
			sawResult = true
		}
	}
	if !sawResult {
		t.Fatalf("main model call2 input missing task result: %+v", mainCall2Input)
	}

	// derive：主历史只含主 task call/result，不含任何子事件
	env.prompt(t, "第二轮", "req-2")
	events, err := env.es.Events(context.Background(), env.session.ID, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	msgs := session.DeriveMessages(events)
	if len(msgs) != 5 {
		t.Fatalf("derived %d messages, want 5: %+v", len(msgs), msgs)
	}
	if msgs[0].Role != session.RoleUser || msgs[0].Content != "帮我做个数据分析" {
		t.Fatalf("msgs[0] = %+v", msgs[0])
	}
	if msgs[1].Role != session.RoleAssistant || len(msgs[1].ToolCalls) != 1 || msgs[1].ToolCalls[0].Name != "task" {
		t.Fatalf("msgs[1] = %+v", msgs[1])
	}
	if msgs[2].Role != session.RoleTool || msgs[2].ToolCallID != "call_task1" || msgs[2].ToolName != "task" {
		t.Fatalf("msgs[2] = %+v", msgs[2])
	}
	if !strings.Contains(msgs[2].Content, "分析结论：42") {
		t.Fatalf("task result content = %q", msgs[2].Content)
	}
	if msgs[3].Role != session.RoleAssistant || msgs[3].Content != "子智能体结论已收到。" || len(msgs[3].ToolCalls) != 0 {
		t.Fatalf("msgs[3] = %+v", msgs[3])
	}
	if msgs[4].Role != session.RoleUser || msgs[4].Content != "第二轮" {
		t.Fatalf("msgs[4] = %+v", msgs[4])
	}

	ends := env.es.byType(session.EventTurnEnd)
	var te session.TurnEndData
	if err := json.Unmarshal(ends[0].Data, &te); err != nil {
		t.Fatal(err)
	}
	if te.Reason != "completed" {
		t.Fatalf("turn/end reason = %s", te.Reason)
	}
}

// ---------- 失败路径：子模型 error → 主 Agent 收到含 Partial output 的 error ----------

func TestTaskSubAgentError(t *testing.T) {
	var mainCall2Input []*schema.Message
	env := newTaskTestEnv(t, session.ApprovalAuto,
		func(call int, input []*schema.Message) []*schema.Message {
			if call == 1 {
				return taskCallChunks("call_task1", "data-analyst", "跑个会失败的任务")
			}
			mainCall2Input = input
			return textChunks("收到失败信息。", nil)
		},
		func(call int, input []*schema.Message) []*schema.Message {
			if call == 1 {
				idx := 0
				return []*schema.Message{
					{Role: schema.Assistant, Content: "部分结论：已清洗 3 行", ToolCalls: []schema.ToolCall{
						{Index: &idx, ID: "call_sub1", Type: "function", Function: schema.FunctionCall{Name: "exec", Arguments: `{"command":"wc -l x.csv"}`}},
					}, ResponseMeta: &schema.ResponseMeta{FinishReason: "tool_calls"}},
				}
			}
			return nil // 子模型第二次调用报错
		})
	env.prompt(t, "委派任务", "req-1")

	if err := env.orch.StartTurn(context.Background(), env.session.ID); err != nil {
		t.Fatalf("StartTurn: %v", err)
	}

	tr := taskResultOf(t, env.es, "call_task1")
	if !strings.Contains(tr.Content, "reason: error") {
		t.Fatalf("task result missing reason: %q", tr.Content)
	}
	if !strings.Contains(tr.Content, "Diagnostic: ") {
		t.Fatalf("task result missing Diagnostic: %q", tr.Content)
	}
	if !strings.Contains(tr.Content, "Partial output before the run ended:\n部分结论：已清洗 3 行") {
		t.Fatalf("task result missing partial output: %q", tr.Content)
	}

	// 主 turn 不因子 Agent 失败而失败
	ends := env.es.byType(session.EventTurnEnd)
	var te session.TurnEndData
	if err := json.Unmarshal(ends[0].Data, &te); err != nil {
		t.Fatal(err)
	}
	if te.Reason != "completed" {
		t.Fatalf("turn/end reason = %s, want completed", te.Reason)
	}

	// 主模型第二轮看到错误文本（部分输出仍回喂）
	sawErr := false
	for _, m := range mainCall2Input {
		if m.Role == schema.Tool && m.ToolCallID == "call_task1" && strings.Contains(m.Content, "Partial output") {
			sawErr = true
		}
	}
	if !sawErr {
		t.Fatalf("main model call2 input missing error text: %+v", mainCall2Input)
	}

	// 子 exec 调用产生嵌套 tool/call + tool/result（子 exec 成功执行过）
	nestedResults := 0
	for _, ev := range env.es.byType(session.EventToolResult) {
		var p struct {
			ParentCallID string `json:"parent_call_id"`
		}
		if json.Unmarshal(ev.Data, &p) == nil && p.ParentCallID == "call_task1" {
			nestedResults++
		}
	}
	if nestedResults != 1 {
		t.Fatalf("want 1 nested tool/result, got %d", nestedResults)
	}
}

// ---------- 子 Agent 名称匹配失败 ----------

func TestTaskUnknownSubAgent(t *testing.T) {
	env := newTaskTestEnv(t, session.ApprovalAuto,
		func(call int, input []*schema.Message) []*schema.Message {
			if call == 1 {
				return taskCallChunks("call_task1", "nobody", "do something")
			}
			return textChunks("好的，没有这个子智能体。", nil)
		},
		func(call int, input []*schema.Message) []*schema.Message {
			t.Fatalf("sub model must not be called")
			return nil
		})
	env.prompt(t, "委派给不存在的子智能体", "req-1")

	if err := env.orch.StartTurn(context.Background(), env.session.ID); err != nil {
		t.Fatalf("StartTurn: %v", err)
	}

	tr := taskResultOf(t, env.es, "call_task1")
	if !strings.Contains(tr.Content, `subagent "nobody" not found`) {
		t.Fatalf("task result = %q", tr.Content)
	}
	if !strings.Contains(tr.Content, "data-analyst") {
		t.Fatalf("task result should list available subagents: %q", tr.Content)
	}
	if env.sub.calls != 0 {
		t.Fatalf("sub model called %d times", env.sub.calls)
	}
	ends := env.es.byType(session.EventTurnEnd)
	var te session.TurnEndData
	if err := json.Unmarshal(ends[0].Data, &te); err != nil {
		t.Fatal(err)
	}
	if te.Reason != "completed" {
		t.Fatalf("turn/end reason = %s", te.Reason)
	}
}

// ---------- 同一 turn 两个 task 并行且结果按调用身份归属 ----------

func TestTaskParallelExecution(t *testing.T) {
	var mu sync.Mutex
	inFlight := 0
	maxInFlight := 0
	subCalls := 0

	args1, _ := json.Marshal(map[string]string{"subagent": "data-analyst", "prompt": "任务一"})
	args2, _ := json.Marshal(map[string]string{"subagent": "data-analyst", "prompt": "任务二"})

	env := newTaskTestEnv(t, session.ApprovalAuto,
		func(call int, input []*schema.Message) []*schema.Message {
			if call == 1 {
				return twoTaskCallChunks(string(args1), string(args2))
			}
			return textChunks("两个任务都完成了。", nil)
		},
		func(call int, input []*schema.Message) []*schema.Message {
			mu.Lock()
			subCalls++
			inFlight++
			if inFlight > maxInFlight {
				maxInFlight = inFlight
			}
			mu.Unlock()
			time.Sleep(50 * time.Millisecond)
			mu.Lock()
			inFlight--
			mu.Unlock()
			return textChunks("子任务结果", nil)
		})
	env.prompt(t, "并行委派两个任务", "req-1")

	if err := env.orch.StartTurn(context.Background(), env.session.ID); err != nil {
		t.Fatalf("StartTurn: %v", err)
	}

	if subCalls != 2 {
		t.Fatalf("sub model calls = %d, want 2", subCalls)
	}
	if maxInFlight != 2 {
		t.Fatalf("sub runs did not overlap (maxInFlight = %d)", maxInFlight)
	}
	for _, id := range []string{"call_t1", "call_t2"} {
		tr := taskResultOf(t, env.es, id)
		if !strings.Contains(tr.Content, "子任务结果") {
			t.Fatalf("task result %s = %q", id, tr.Content)
		}
	}
}

// ---------- 嵌套审批：子 Agent exec 触发审批，DecideApproval 恢复子运行 ----------

func TestTaskNestedApproval(t *testing.T) {
	env := newTaskTestEnv(t, session.ApprovalDefault,
		func(call int, input []*schema.Message) []*schema.Message {
			if call == 1 {
				return taskCallChunks("call_task1", "data-analyst", "统计文件行数")
			}
			return textChunks("统计完成。", nil)
		},
		func(call int, input []*schema.Message) []*schema.Message {
			if call == 1 {
				return toolCallChunks("call_sub_exec", "exec", `{"command":"wc -l x.csv"}`)
			}
			return textChunks("共 100 行。", nil)
		})
	env.prompt(t, "委派统计任务", "req-1")

	done := make(chan error, 1)
	go func() { done <- env.orch.StartTurn(context.Background(), env.session.ID) }()

	asked, err := env.es.waitFor(session.EventApprovalAsked, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	var ask nestedApprovalAskedData
	if err := json.Unmarshal(asked.Data, &ask); err != nil {
		t.Fatal(err)
	}
	if ask.CallID != "call_sub_exec" || ask.Name != "exec" || ask.Risk != RiskHigh {
		t.Fatalf("nested approval/asked = %+v", ask)
	}
	if ask.ParentCallID != "call_task1" {
		t.Fatalf("nested approval/asked parent_call_id = %q", ask.ParentCallID)
	}

	if err := env.orch.DecideApproval(env.session.ID, "call_sub_exec", true, "user-1"); err != nil {
		t.Fatalf("DecideApproval: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatalf("StartTurn: %v", err)
	}

	// decided 带 parent_call_id
	var dec nestedApprovalDecidedData
	found := false
	for _, ev := range env.es.byType(session.EventApprovalDecided) {
		if json.Unmarshal(ev.Data, &dec) == nil && dec.CallID == "call_sub_exec" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("approval/decided for call_sub_exec not found")
	}
	if dec.Decision != "allow" || dec.DecidedBy != "user-1" || dec.ParentCallID != "call_task1" {
		t.Fatalf("nested approval/decided = %+v", dec)
	}

	// 子运行恢复并完成：task 结果含最终文本
	tr := taskResultOf(t, env.es, "call_task1")
	if !strings.Contains(tr.Content, "共 100 行。") {
		t.Fatalf("task result = %q", tr.Content)
	}
	ends := env.es.byType(session.EventTurnEnd)
	var te session.TurnEndData
	if err := json.Unmarshal(ends[0].Data, &te); err != nil {
		t.Fatal(err)
	}
	if te.Reason != "completed" {
		t.Fatalf("turn/end reason = %s", te.Reason)
	}
}

// ---------- 工具描述与匹配 ----------

func TestTaskDescriptionAndMatch(t *testing.T) {
	subs := []agent.SnapshotSubAgent{
		{Name: "data-analyst", Description: "数据分析与可视化，擅长 pandas/matplotlib"},
		{Name: "web-researcher", Description: "联网检索与资料综述"},
	}
	desc := taskDescription(subs)
	for _, want := range []string{
		"it does NOT see this conversation",
		"shares your filesystem",
		"- data-analyst: 数据分析与可视化，擅长 pandas/matplotlib",
		"- web-researcher: 联网检索与资料综述",
	} {
		if !strings.Contains(desc, want) {
			t.Fatalf("task description missing %q:\n%s", want, desc)
		}
	}
	if matchSubAgent(subs, "DATA-Analyst") == nil {
		t.Fatal("case-insensitive match failed")
	}
	if matchSubAgent(subs, "nope") != nil {
		t.Fatal("unexpected match")
	}
}
