package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/schema"

	"github.com/ravenmk2/wispjar/internal/session"
)

func TestOrchestratorFullFlow(t *testing.T) {
	usage2 := &schema.TokenUsage{PromptTokens: 20, CompletionTokens: 8}
	env := newTestEnv(t, session.ApprovalAuto, func(call int, input []*schema.Message) []*schema.Message {
		if call == 1 {
			return toolCallChunks("call_1", "exec", `{"command":"ls"}`)
		}
		return textChunks("你好，世界", usage2)
	})
	env.prompt(t, "列一下文件", "req-1")

	frames, cancel := env.bus.SubscribeSession(env.session.ID, 512)
	collected := []FollowFrame{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for f := range frames {
			collected = append(collected, f)
		}
	}()

	if err := env.orch.StartTurn(context.Background(), env.session.ID); err != nil {
		t.Fatalf("StartTurn: %v", err)
	}
	cancel()
	<-done

	wantTypes := []string{
		"user/message", "turn/start", "request/header",
		"step/start", "assistant/message", "tool/call", "step/end",
		"tool/result",
		"step/start", "assistant/message", "step/end",
		"turn/end",
	}
	got := env.es.types()
	if len(got) != len(wantTypes) {
		t.Fatalf("event types:\n got %v\nwant %v", got, wantTypes)
	}
	for i := range wantTypes {
		if got[i] != wantTypes[i] {
			t.Fatalf("event[%d] = %s, want %s\nall: %v", i, got[i], wantTypes[i], got)
		}
	}

	msgs := env.es.byType(session.EventAssistantMessage)
	if len(msgs) != 2 {
		t.Fatalf("want 2 assistant/message, got %d", len(msgs))
	}
	var m1 session.AssistantMessageData
	if err := json.Unmarshal(msgs[0].Data, &m1); err != nil {
		t.Fatal(err)
	}
	if len(m1.Message.Content) != 1 || m1.Message.Content[0].Type != "tool-call" {
		t.Fatalf("assistant/message#1 want single tool-call block, got %+v", m1.Message.Content)
	}
	tc := m1.Message.Content[0]
	if tc.ID != "call_1" || tc.Name != "exec" || tc.Arguments != `{"command":"ls"}` {
		t.Fatalf("tool-call block mismatch: %+v", tc)
	}
	if m1.Usage == nil || m1.Usage.InputTokens != 10 || m1.Usage.OutputTokens != 5 {
		t.Fatalf("assistant/message#1 usage mismatch: %+v", m1.Usage)
	}

	var m2 session.AssistantMessageData
	if err := json.Unmarshal(msgs[1].Data, &m2); err != nil {
		t.Fatal(err)
	}
	if len(m2.Message.Content) != 1 || m2.Message.Content[0].Type != "text" || m2.Message.Content[0].Text != "你好，世界" {
		t.Fatalf("assistant/message#2 mismatch: %+v", m2.Message.Content)
	}
	if m2.Usage == nil || m2.Usage.InputTokens != 20 || m2.Usage.OutputTokens != 8 {
		t.Fatalf("assistant/message#2 usage mismatch: %+v", m2.Usage)
	}

	results := env.es.byType(session.EventToolResult)
	if len(results) != 1 {
		t.Fatalf("want 1 tool/result, got %d", len(results))
	}
	var tr session.ToolResultData
	if err := json.Unmarshal(results[0].Data, &tr); err != nil {
		t.Fatal(err)
	}
	if tr.CallID != "call_1" || tr.Name != "exec" {
		t.Fatalf("tool/result mismatch: %+v", tr)
	}
	if tr.Meta == nil || tr.Meta.Render != "terminal" || tr.Meta.SandboxID != "fake-sandbox" {
		t.Fatalf("tool/result meta mismatch: %+v", tr.Meta)
	}
	var execOut struct {
		ExitCode int    `json:"exit_code"`
		Stdout   string `json:"stdout"`
	}
	if err := json.Unmarshal([]byte(tr.Content), &execOut); err != nil {
		t.Fatalf("tool/result content not JSON: %v", err)
	}
	if execOut.ExitCode != 0 || execOut.Stdout != "file.txt\n" {
		t.Fatalf("exec output mismatch: %+v", execOut)
	}

	ends := env.es.byType(session.EventTurnEnd)
	var te session.TurnEndData
	if err := json.Unmarshal(ends[0].Data, &te); err != nil {
		t.Fatal(err)
	}
	if te.Reason != "completed" || te.Turn != 1 {
		t.Fatalf("turn/end mismatch: %+v", te)
	}

	assertStreamFrames(t, collected, []string{"call_1"})
}

// assertStreamFrames 校验两个 attempt 的 assistant-stream 帧序列。
func assertStreamFrames(t *testing.T, frames []FollowFrame, _ []string) {
	t.Helper()
	type attemptFrames struct {
		started bool
		ended   bool
		chunks  []StreamChunkFrame
		lastIdx int
	}
	attempts := map[string]*attemptFrames{}
	order := []string{}
	for _, f := range frames {
		if f.Type != "assistant-stream" {
			continue
		}
		switch fr := f.Frame.(type) {
		case StreamStart:
			if _, ok := attempts[fr.AttemptID]; !ok {
				attempts[fr.AttemptID] = &attemptFrames{lastIdx: -1}
				order = append(order, fr.AttemptID)
			}
			attempts[fr.AttemptID].started = true
		case StreamChunkFrame:
			a := attempts[fr.AttemptID]
			if fr.Index != a.lastIdx+1 {
				t.Fatalf("attempt %s chunk index gap: got %d after %d", fr.AttemptID, fr.Index, a.lastIdx)
			}
			a.lastIdx = fr.Index
			a.chunks = append(a.chunks, fr)
		case StreamEnd:
			attempts[fr.AttemptID].ended = true
			if fr.Outcome.Kind != "committed" || fr.Outcome.EventType != session.EventAssistantMessage || fr.Outcome.Seq == 0 {
				t.Fatalf("attempt %s end outcome mismatch: %+v", fr.AttemptID, fr.Outcome)
			}
		}
	}
	if len(order) != 2 {
		t.Fatalf("want 2 attempts, got %d (frames=%d)", len(order), len(frames))
	}
	for _, id := range order {
		if !attempts[id].started || !attempts[id].ended {
			t.Fatalf("attempt %s missing start/end", id)
		}
	}

	a1 := attempts[order[0]].chunks
	wantSeq1 := []string{ChunkBlockStart, ChunkToolCallDelta, ChunkToolCallDelta, ChunkBlockEnd, ChunkUsage, ChunkFinish}
	assertChunkTypes(t, "attempt1", a1, wantSeq1)
	if a1[0].Chunk.BlockType != BlockToolCall {
		t.Fatalf("attempt1 block-start type = %s", a1[0].Chunk.BlockType)
	}
	if a1[1].Chunk.ID != "call_1" || a1[1].Chunk.Name != "exec" {
		t.Fatalf("attempt1 first tool-call-delta must carry id+name: %+v", a1[1].Chunk)
	}
	args := a1[1].Chunk.ArgumentsDelta + a1[2].Chunk.ArgumentsDelta
	if args != `{"command":"ls"}` {
		t.Fatalf("attempt1 arguments reassembly = %q", args)
	}
	blk, ok := a1[3].Chunk.Block.(session.ContentBlock)
	if !ok || blk.Type != "tool-call" || blk.ID != "call_1" || blk.Arguments != `{"command":"ls"}` {
		t.Fatalf("attempt1 block-end mismatch: %+v", a1[3].Chunk.Block)
	}
	if a1[5].Chunk.Reason != FinishToolCalls {
		t.Fatalf("attempt1 finish reason = %s", a1[5].Chunk.Reason)
	}

	a2 := attempts[order[1]].chunks
	wantSeq2 := []string{ChunkBlockStart, ChunkTextDelta, ChunkTextDelta, ChunkBlockEnd, ChunkUsage, ChunkFinish}
	assertChunkTypes(t, "attempt2", a2, wantSeq2)
	text := a2[1].Chunk.Text + a2[2].Chunk.Text
	if text != "你好，世界" {
		t.Fatalf("attempt2 text reassembly = %q", text)
	}
	if a2[4].Chunk.Usage == nil || a2[4].Chunk.Usage.InputTokens != 20 || a2[4].Chunk.Usage.OutputTokens != 8 {
		t.Fatalf("attempt2 usage chunk mismatch: %+v", a2[4].Chunk.Usage)
	}
	if a2[5].Chunk.Reason != FinishStop {
		t.Fatalf("attempt2 finish reason = %s", a2[5].Chunk.Reason)
	}
}

func assertChunkTypes(t *testing.T, label string, chunks []StreamChunkFrame, want []string) {
	t.Helper()
	if len(chunks) != len(want) {
		got := []string{}
		for _, c := range chunks {
			got = append(got, c.Chunk.Type)
		}
		t.Fatalf("%s chunk types: got %v want %v", label, got, want)
	}
	for i, w := range want {
		if chunks[i].Chunk.Type != w {
			t.Fatalf("%s chunk[%d] = %s, want %s", label, i, chunks[i].Chunk.Type, w)
		}
	}
}

func TestApprovalDenyFlow(t *testing.T) {
	var call2Input []*schema.Message
	env := newTestEnv(t, session.ApprovalDefault, func(call int, input []*schema.Message) []*schema.Message {
		if call == 1 {
			return toolCallChunks("call_1", "exec", `{"command":"rm -rf /tmp/x"}`)
		}
		call2Input = input
		return textChunks("好的，已取消该操作。", nil)
	})
	env.prompt(t, "删掉临时目录", "req-1")

	if err := env.orch.StartTurn(context.Background(), env.session.ID); err != nil {
		t.Fatalf("StartTurn: %v", err)
	}

	asked := env.es.byType(session.EventApprovalAsked)
	if len(asked) != 1 {
		t.Fatalf("want approval/asked, events: %v", env.es.types())
	}
	var ask session.ApprovalAskedData
	if err := json.Unmarshal(asked[0].Data, &ask); err != nil {
		t.Fatal(err)
	}
	if ask.CallID != "call_1" || ask.Name != "exec" || ask.Risk != RiskHigh {
		t.Fatalf("approval/asked mismatch: %+v", ask)
	}
	if n := len(env.es.byType(session.EventTurnEnd)); n != 0 {
		t.Fatal("turn must stay open while approval pending")
	}
	if err := env.orch.StartTurn(context.Background(), env.session.ID); !errors.Is(err, ErrSessionRunning) {
		t.Fatalf("concurrent StartTurn want ErrSessionRunning, got %v", err)
	}

	if err := env.orch.DecideApproval(env.session.ID, "call_1", false, "user-1"); err != nil {
		t.Fatalf("DecideApproval: %v", err)
	}
	end, err := env.es.waitFor(session.EventTurnEnd, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	var te session.TurnEndData
	if err := json.Unmarshal(end.Data, &te); err != nil {
		t.Fatal(err)
	}
	if te.Reason != "completed" {
		t.Fatalf("turn/end reason = %s", te.Reason)
	}

	decided := env.es.byType(session.EventApprovalDecided)
	if len(decided) != 1 {
		t.Fatalf("want 1 approval/decided, got %d", len(decided))
	}
	var dec session.ApprovalDecidedData
	if err := json.Unmarshal(decided[0].Data, &dec); err != nil {
		t.Fatal(err)
	}
	if dec.Decision != "deny" || dec.DecidedBy != "user-1" || dec.Auto {
		t.Fatalf("approval/decided mismatch: %+v", dec)
	}

	results := env.es.byType(session.EventToolResult)
	if len(results) != 1 {
		t.Fatalf("want 1 tool/result, got %d", len(results))
	}
	var tr session.ToolResultData
	if err := json.Unmarshal(results[0].Data, &tr); err != nil {
		t.Fatal(err)
	}
	if tr.Error != deniedResult {
		t.Fatalf("tool/result error = %q, want %q", tr.Error, deniedResult)
	}

	sawDenied := false
	for _, m := range call2Input {
		if m.Role == schema.Tool && m.ToolCallID == "call_1" && m.Content == deniedResult {
			sawDenied = true
		}
	}
	if !sawDenied {
		t.Fatalf("model call2 input missing denied tool message: %+v", call2Input)
	}

	if err := env.orch.DecideApproval(env.session.ID, "call_1", true, "user-1"); !errors.Is(err, ErrApprovalNotPending) {
		t.Fatalf("re-decide want ErrApprovalNotPending, got %v", err)
	}
}

func TestNeedsApproval(t *testing.T) {
	cases := []struct {
		mode, tool string
		want       bool
	}{
		{session.ApprovalStrict, "exec", true},
		{session.ApprovalStrict, "write", true},
		{session.ApprovalStrict, "edit", true},
		{session.ApprovalStrict, "read", false},
		{session.ApprovalStrict, "glob", false},
		{session.ApprovalStrict, "grep", false},
		{session.ApprovalStrict, "activate_skill", false},
		{session.ApprovalDefault, "exec", true},
		{session.ApprovalDefault, "write", false},
		{session.ApprovalDefault, "edit", false},
		{session.ApprovalDefault, "grep", false},
		{session.ApprovalAuto, "exec", false},
		{session.ApprovalAuto, "write", false},
	}
	for _, c := range cases {
		if got := needsApproval(c.mode, c.tool); got != c.want {
			t.Errorf("needsApproval(%s, %s) = %v, want %v", c.mode, c.tool, got, c.want)
		}
	}
}

func TestDecideApprovalNotPending(t *testing.T) {
	env := newTestEnv(t, session.ApprovalDefault, func(call int, input []*schema.Message) []*schema.Message {
		return textChunks("ok", nil)
	})
	err := env.orch.DecideApproval(env.session.ID, "call_x", true, "user-1")
	if !errors.Is(err, ErrApprovalNotPending) {
		t.Fatalf("want ErrApprovalNotPending, got %v", err)
	}
}

// 工具 infra 错误（exec 沙箱不可达）→ 结构化错误工具结果，turn 正常 completed。
func TestExecInfraErrorTurnCompletes(t *testing.T) {
	var call2Input []*schema.Message
	env := newTestEnv(t, session.ApprovalAuto, func(call int, input []*schema.Message) []*schema.Message {
		if call == 1 {
			return toolCallChunks("call_1", "exec", `{"command":"ls"}`)
		}
		call2Input = input
		return textChunks("命令执行环境出了点问题。", nil)
	})
	env.handle.runErr = errors.New("sandbox unreachable")
	env.prompt(t, "列一下文件", "req-1")

	if err := env.orch.StartTurn(context.Background(), env.session.ID); err != nil {
		t.Fatalf("StartTurn: %v", err)
	}

	ends := env.es.byType(session.EventTurnEnd)
	if len(ends) != 1 {
		t.Fatalf("want turn/end, events: %v", env.es.types())
	}
	var te session.TurnEndData
	if err := json.Unmarshal(ends[0].Data, &te); err != nil {
		t.Fatal(err)
	}
	if te.Reason != "completed" {
		t.Fatalf("turn/end reason = %s, want completed (error=%q)", te.Reason, te.Error)
	}

	results := env.es.byType(session.EventToolResult)
	if len(results) != 1 {
		t.Fatalf("want 1 tool/result, events: %v", env.es.types())
	}
	var tr session.ToolResultData
	if err := json.Unmarshal(results[0].Data, &tr); err != nil {
		t.Fatal(err)
	}
	var execOut struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal([]byte(tr.Content), &execOut); err != nil {
		t.Fatalf("exec infra error result must be structured JSON: %v (%s)", err, tr.Content)
	}
	if !strings.Contains(execOut.Error, "sandbox unreachable") {
		t.Fatalf("tool/result content = %s", tr.Content)
	}
	if tr.Meta == nil || tr.Meta.Render != "terminal" {
		t.Fatalf("tool/result meta = %+v, want render terminal", tr.Meta)
	}

	sawToolMsg := false
	for _, m := range call2Input {
		if m.Role == schema.Tool && m.ToolCallID == "call_1" && strings.Contains(m.Content, "sandbox unreachable") {
			sawToolMsg = true
		}
	}
	if !sawToolMsg {
		t.Fatal("model call2 input missing structured error tool message")
	}
}

// 工具域失败（read 文件不存在）→ 中间件归一化为错误文本工具结果，turn 正常 completed。
func TestToolDomainErrorTurnCompletes(t *testing.T) {
	env := newTestEnv(t, session.ApprovalAuto, func(call int, input []*schema.Message) []*schema.Message {
		if call == 1 {
			return toolCallChunks("call_1", "read", `{"path":"/workspace/nope.txt"}`)
		}
		return textChunks("文件不存在，换个路径试试。", nil)
	})
	env.prompt(t, "读一下配置", "req-1")

	if err := env.orch.StartTurn(context.Background(), env.session.ID); err != nil {
		t.Fatalf("StartTurn: %v", err)
	}

	ends := env.es.byType(session.EventTurnEnd)
	if len(ends) != 1 {
		t.Fatalf("want turn/end, events: %v", env.es.types())
	}
	var te session.TurnEndData
	if err := json.Unmarshal(ends[0].Data, &te); err != nil {
		t.Fatal(err)
	}
	if te.Reason != "completed" {
		t.Fatalf("turn/end reason = %s, want completed (error=%q)", te.Reason, te.Error)
	}

	results := env.es.byType(session.EventToolResult)
	if len(results) != 1 {
		t.Fatalf("want 1 tool/result, events: %v", env.es.types())
	}
	var tr session.ToolResultData
	if err := json.Unmarshal(results[0].Data, &tr); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(tr.Content, toolFailurePrefix) || !strings.Contains(tr.Content, "no such file") {
		t.Fatalf("tool/result content = %q, want %q 前缀的错误文本", tr.Content, toolFailurePrefix)
	}
	if tr.Meta == nil || tr.Meta.Render != "read" {
		t.Fatalf("tool/result meta = %+v, want render read", tr.Meta)
	}
}
