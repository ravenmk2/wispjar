package runtime

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"github.com/ravenmk2/wispjar/internal/agent"
	"github.com/ravenmk2/wispjar/internal/store"
)

// recordingFactory 记录最近一次 NewChatModel 收到的 params。
type recordingFactory struct {
	m      *fakeModel
	params map[string]any
	models []string
}

func (f *recordingFactory) NewChatModel(_ context.Context, modelID string, params map[string]any) (model.ToolCallingChatModel, error) {
	f.params = params
	f.models = append(f.models, modelID)
	return f.m, nil
}

func userMsg(s string) *schema.Message { return schema.UserMessage(s) }
func asstMsg(s string) *schema.Message { return schema.AssistantMessage(s, nil) }
func toolMsg(s string) *schema.Message { return schema.ToolMessage(s, "call-1") }
func sysMsg(s string) *schema.Message  { return &schema.Message{Role: schema.System, Content: s} }

func newTrimOrchestrator(contextSize int) *orchestrator {
	return &orchestrator{deps: Deps{
		Sessions: &fakeRepo{sess: &store.Session{ModelID: "model-1"}},
		ContextSize: func(_ context.Context, modelID string) int {
			return contextSize
		},
	}}
}

// 每个 turn：user(80 chars) + assistant(80 chars) → 40 tokens。
// contextSize=100 → limit=80 tokens → 3 个 turn（120 tokens）须裁掉 1 个。
func threeTurns() []*schema.Message {
	pad := strings.Repeat("x", 78)
	return []*schema.Message{
		userMsg("u1" + pad), asstMsg("a1" + pad),
		userMsg("u2" + pad), asstMsg("a2" + pad),
		userMsg("u3" + pad), asstMsg("a3" + pad),
	}
}

func TestTrimContextDropsWholeTurns(t *testing.T) {
	o := newTrimOrchestrator(100)
	out, err := o.trimContext(context.Background(), "sess-1", threeTurns())
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 4 {
		t.Fatalf("want 4 messages after dropping one turn, got %d", len(out))
	}
	// 裁剪边界落在轮次边界：首条必须是 u2 的 user 消息
	if out[0].Role != schema.User || !strings.HasPrefix(out[0].Content, "u2") {
		t.Fatalf("trim boundary must align to turn start, got %s %q", out[0].Role, out[0].Content[:8])
	}
	// 顺序稳定：u2,a2,u3,a3
	want := []string{"u2", "a2", "u3", "a3"}
	for i, w := range want {
		if !strings.HasPrefix(out[i].Content, w) {
			t.Fatalf("order not stable at %d: want prefix %q, got %q", i, w, out[i].Content[:8])
		}
	}
}

func TestTrimContextKeepsSystem(t *testing.T) {
	o := newTrimOrchestrator(110) // limit=88 tokens：system(3)+2 turns(80) 达标，3 turns(120) 超
	msgs := append([]*schema.Message{sysMsg("system-prompt")}, threeTurns()...)
	out, err := o.trimContext(context.Background(), "sess-1", msgs)
	if err != nil {
		t.Fatal(err)
	}
	if out[0].Role != schema.System || out[0].Content != "system-prompt" {
		t.Fatalf("system must be preserved at head, got %s %q", out[0].Role, out[0].Content)
	}
	if out[1].Role != schema.User || !strings.HasPrefix(out[1].Content, "u2") {
		t.Fatalf("first kept turn must start at u2, got %s %q", out[1].Role, out[1].Content[:8])
	}
}

func TestTrimContextDropsTrailingToolMessagesWithTurn(t *testing.T) {
	o := newTrimOrchestrator(100)
	pad := strings.Repeat("x", 78)
	msgs := []*schema.Message{
		userMsg("u1" + pad), asstMsg("a1" + pad), toolMsg("t1" + pad),
		userMsg("u2" + pad), asstMsg("a2" + pad),
	}
	// 总量 400 chars/4 = 100 tokens > 80 → 裁第一轮（含 tool 消息共 3 条）
	out, err := o.trimContext(context.Background(), "sess-1", msgs)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 || !strings.HasPrefix(out[0].Content, "u2") {
		t.Fatalf("first turn including its tool message must be dropped as a block, got %d msgs", len(out))
	}
}

func TestTrimContextSingleOversizedTurnFails(t *testing.T) {
	o := newTrimOrchestrator(100)
	msgs := []*schema.Message{userMsg(strings.Repeat("x", 4000))}
	if _, err := o.trimContext(context.Background(), "sess-1", msgs); err == nil {
		t.Fatal("want error when a single turn exceeds the window")
	}
}

func TestTrimContextUnderLimitUnchanged(t *testing.T) {
	o := newTrimOrchestrator(100000)
	msgs := threeTurns()
	out, err := o.trimContext(context.Background(), "sess-1", msgs)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != len(msgs) {
		t.Fatalf("under limit must not trim, got %d of %d", len(out), len(msgs))
	}
}

func TestRenderInstructionUsesSessionCreatedAt(t *testing.T) {
	snap := &agent.Snapshot{SystemPrompt: "sys"}
	sess := &store.Session{
		ApprovalMode: "auto",
		CreatedAt:    time.Date(2026, 3, 5, 10, 0, 0, 0, time.UTC),
	}
	got := renderInstruction(snap, sess)
	if !strings.Contains(got, "当前日期：2026-03-05") {
		t.Fatalf("instruction must use session creation date, got:\n%s", got)
	}
}

func TestBuildAgentInjectsPromptCacheKeyForArk(t *testing.T) {
	rf := &recordingFactory{m: &fakeModel{script: func(call int, input []*schema.Message) []*schema.Message {
		return textChunks("ok", nil)
	}}}
	o := &orchestrator{deps: Deps{
		Models:       rf,
		ProviderType: func(_ context.Context, modelID string) string { return "ark" },
	}}
	snap := &agent.Snapshot{
		AgentID: "agent-1", SystemPrompt: "sys",
		Model: agent.SnapshotModel{ID: "model-1", ProviderType: "ark"},
	}
	sess := &store.Session{ID: "sess-1", ModelID: "model-1", ApprovalMode: "auto"}
	if _, _, err := o.buildAgent(context.Background(), sess, snap, nil, nil); err != nil {
		t.Fatal(err)
	}
	if rf.params["prompt_cache_key"] != "sess-1" {
		t.Fatalf("ark must get prompt_cache_key=sessionID, got %v", rf.params)
	}
}

func TestBuildAgentNoPromptCacheKeyForOtherProviders(t *testing.T) {
	rf := &recordingFactory{m: &fakeModel{script: func(call int, input []*schema.Message) []*schema.Message {
		return textChunks("ok", nil)
	}}}
	o := &orchestrator{deps: Deps{Models: rf}} // ProviderType nil → 回退快照 provider_type
	snap := &agent.Snapshot{
		AgentID: "agent-1", SystemPrompt: "sys",
		Model: agent.SnapshotModel{ID: "model-1", ProviderType: "deepseek"},
	}
	sess := &store.Session{ID: "sess-1", ModelID: "model-1", ApprovalMode: "auto"}
	if _, _, err := o.buildAgent(context.Background(), sess, snap, nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, ok := rf.params["prompt_cache_key"]; ok {
		t.Fatalf("non-ark must not get prompt_cache_key, got %v", rf.params)
	}
}

func TestBuildAgentSessionParamsOverrideSnapshot(t *testing.T) {
	rf := &recordingFactory{m: &fakeModel{script: func(call int, input []*schema.Message) []*schema.Message {
		return textChunks("ok", nil)
	}}}
	o := &orchestrator{deps: Deps{Models: rf}}
	snap := &agent.Snapshot{
		AgentID: "agent-1", SystemPrompt: "sys",
		ModelParams: json.RawMessage(`{"temperature":0.7,"reasoning_effort":"low"}`),
		Model:       agent.SnapshotModel{ID: "model-1", ProviderType: "deepseek"},
	}
	sess := &store.Session{
		ID: "sess-1", ModelID: "model-1", ApprovalMode: "auto",
		ModelParams: json.RawMessage(`{"reasoning_effort":"high"}`),
	}
	if _, _, err := o.buildAgent(context.Background(), sess, snap, nil, nil); err != nil {
		t.Fatal(err)
	}
	if rf.params["reasoning_effort"] != "high" || rf.params["temperature"] != 0.7 {
		t.Fatalf("session params must override snapshot params, got %v", rf.params)
	}
}

func TestBuildSubAgentEffortInheritance(t *testing.T) {
	rf := &recordingFactory{m: &fakeModel{script: func(call int, input []*schema.Message) []*schema.Message {
		return textChunks("ok", nil)
	}}}
	o := &orchestrator{deps: Deps{Models: rf}}
	sub := &agent.SnapshotSubAgent{
		ID: "sub-1", Name: "sub", SystemPrompt: "sys",
		Model: agent.SnapshotModel{ID: "model-1", ProviderType: "deepseek"},
	}
	sess := &store.Session{
		ID: "sess-1", ModelID: "model-1", ApprovalMode: "auto",
		ModelParams: json.RawMessage(`{"reasoning_effort":"high"}`),
	}
	if _, err := o.buildSubAgent(context.Background(), sess, sub); err != nil {
		t.Fatal(err)
	}
	if rf.params["reasoning_effort"] != "high" {
		t.Fatalf("subagent must inherit session effort, got %v", rf.params)
	}

	// 子 Agent 显式指定时用自己的
	sub.ModelParams = json.RawMessage(`{"reasoning_effort":"low"}`)
	if _, err := o.buildSubAgent(context.Background(), sess, sub); err != nil {
		t.Fatal(err)
	}
	if rf.params["reasoning_effort"] != "low" {
		t.Fatalf("subagent explicit effort must win, got %v", rf.params)
	}
}
