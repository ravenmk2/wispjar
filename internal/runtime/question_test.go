package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/gin-gonic/gin"

	"github.com/ravenmk2/wispjar/internal/agent"
	"github.com/ravenmk2/wispjar/internal/session"
	"github.com/ravenmk2/wispjar/internal/store"
)

const askArgs = `{"questions":[{"id":"q1","question":"选哪个方案？","header":"Confirm","options":[{"label":"A"},{"label":"B","description":"方案 B"}]},{"id":"q2","question":"补充说明？"}]}`

var testAnswers = []session.QuestionAnswer{
	{ID: "q1", Selected: []string{"A"}},
	{ID: "q2", Custom: "随便"},
}

func invokeQuestionTool(t *testing.T, tt tool.BaseTool, ctx context.Context, args string) (string, error) {
	t.Helper()
	it, ok := tt.(tool.InvokableTool)
	if !ok {
		t.Fatal("tool is not invokable")
	}
	return it.InvokableRun(ctx, args)
}

// 工具 schema 与注册：名称/描述/参数结构；request/header 工具清单包含 ask_user_question；
// 任何 approval_mode 下不被审批拦截。
func TestQuestionToolSchema(t *testing.T) {
	qt, err := (&orchestrator{}).newQuestionTool(nil, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	info, err := qt.Info(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if info.Name != "ask_user_question" {
		t.Fatalf("tool name = %q", info.Name)
	}
	raw, err := json.Marshal(info)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"questions", "multi_select", "options", "label", "header"} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("tool schema missing %q: %s", want, raw)
		}
	}
	names := enabledToolNames(&agent.Snapshot{ToolsEnabled: json.RawMessage(`{"exec":true}`)})
	found := false
	for _, n := range names {
		if n == "ask_user_question" {
			found = true
		}
	}
	if !found {
		t.Fatalf("enabledToolNames missing ask_user_question: %v", names)
	}
	for _, mode := range []string{session.ApprovalStrict, session.ApprovalDefault, session.ApprovalAuto} {
		if needsApproval(mode, "ask_user_question") {
			t.Fatalf("ask_user_question must not need approval in mode %s", mode)
		}
	}
}

// 子 Agent（depth=1）调用直接拒绝，提示把未决问题写进最终结果由父 Agent 上浮。
func TestQuestionSubAgentRejected(t *testing.T) {
	qt, err := (&orchestrator{}).newQuestionTool(nil, nil, 1)
	if err != nil {
		t.Fatal(err)
	}
	out, err := invokeQuestionTool(t, qt, context.Background(), askArgs)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "only available to the root agent") || !strings.Contains(out, "final result") {
		t.Fatalf("delegated caller notice mismatch: %s", out)
	}
}

// 入参校验以工具结果文本回喂（模型可修正重试），不升级为 Go error。
func TestQuestionInvalidArgs(t *testing.T) {
	qt, err := (&orchestrator{}).newQuestionTool(nil, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	out, err := invokeQuestionTool(t, qt, context.Background(), `{"questions":[]}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "at least one question") {
		t.Fatalf("empty questions must be rejected, got: %s", out)
	}
	out, err = invokeQuestionTool(t, qt, context.Background(),
		`{"questions":[{"id":"q","question":"a?"},{"id":"q","question":"b?"}]}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "duplicated") {
		t.Fatalf("duplicate ids must be rejected, got: %s", out)
	}
}

// open 状态作答全流程：question/asked 落库 → AnswerQuestion 投递 → 答案作为工具结果 →
// question/answered 落库 → turn 正常完成（strict 模式下提问不经审批拦截）。
func TestQuestionOpenAnswerFlow(t *testing.T) {
	env := newTestEnv(t, session.ApprovalStrict, func(call int, input []*schema.Message) []*schema.Message {
		if call == 1 {
			return toolCallChunks("call_q1", "ask_user_question", askArgs)
		}
		return textChunks("好的，按 A 方案执行。", nil)
	})
	env.prompt(t, "需要时问我", "req-1")
	go func() { _ = env.orch.StartTurn(context.Background(), env.session.ID) }()

	askedEv, err := env.es.waitFor(session.EventQuestionAsked, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	var asked session.QuestionAskedData
	if err := json.Unmarshal(askedEv.Data, &asked); err != nil {
		t.Fatal(err)
	}
	if asked.CallID != "call_q1" || asked.Turn != 1 || asked.Step != 1 {
		t.Fatalf("question/asked mismatch: %+v", asked)
	}
	if len(asked.Questions) != 2 || asked.Questions[0].ID != "q1" || len(asked.Questions[0].Options) != 2 {
		t.Fatalf("question/asked questions mismatch: %+v", asked.Questions)
	}
	if n := len(env.es.byType(session.EventApprovalAsked)); n != 0 {
		t.Fatal("ask_user_question must not trigger approval")
	}

	delivered, err := env.orch.AnswerQuestion(env.session.ID, "call_q1", testAnswers, "user-1")
	if err != nil {
		t.Fatal(err)
	}
	if !delivered {
		t.Fatal("open wait must be delivered")
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

	answered := env.es.byType(session.EventQuestionAnswered)
	if len(answered) != 1 {
		t.Fatalf("want 1 question/answered, got %d", len(answered))
	}
	var ad session.QuestionAnsweredData
	if err := json.Unmarshal(answered[0].Data, &ad); err != nil {
		t.Fatal(err)
	}
	if ad.CallID != "call_q1" || ad.AnsweredBy != "user-1" || len(ad.Answers) != 2 || ad.Answers[0].Selected[0] != "A" {
		t.Fatalf("question/answered mismatch: %+v", ad)
	}
	if answered[0].Seq <= askedEv.Seq {
		t.Fatal("question/answered must come after question/asked")
	}

	results := env.es.byType(session.EventToolResult)
	if len(results) != 1 {
		t.Fatalf("want 1 tool/result, got %d", len(results))
	}
	var tr session.ToolResultData
	if err := json.Unmarshal(results[0].Data, &tr); err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Answers []session.QuestionAnswer `json:"answers"`
	}
	if err := json.Unmarshal([]byte(tr.Content), &payload); err != nil {
		t.Fatalf("tool result must be answers JSON: %v (%s)", err, tr.Content)
	}
	if len(payload.Answers) != 2 || payload.Answers[1].Custom != "随便" {
		t.Fatalf("tool result answers mismatch: %s", tr.Content)
	}
}

// 超时：工具结果为 {"pending":true,...}，turn 正常完成，问题保持可答（无 answered）。
func TestQuestionTimeoutPending(t *testing.T) {
	env := newTestEnv(t, session.ApprovalAuto, func(call int, input []*schema.Message) []*schema.Message {
		if call == 1 {
			return toolCallChunks("call_q1", "ask_user_question", askArgs)
		}
		return textChunks("先做别的。", nil)
	})
	env.orch.(*orchestrator).questionTimeout = 60 * time.Millisecond
	env.prompt(t, "需要时问我", "req-1")
	if err := env.orch.StartTurn(context.Background(), env.session.ID); err != nil {
		t.Fatalf("StartTurn: %v", err)
	}

	results := env.es.byType(session.EventToolResult)
	if len(results) != 1 {
		t.Fatalf("want 1 tool/result, got %d", len(results))
	}
	var tr session.ToolResultData
	if err := json.Unmarshal(results[0].Data, &tr); err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Pending bool   `json:"pending"`
		CallID  string `json:"call_id"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal([]byte(tr.Content), &payload); err != nil {
		t.Fatalf("timeout result must be JSON: %v (%s)", err, tr.Content)
	}
	if !payload.Pending || payload.CallID != "call_q1" || !strings.Contains(payload.Message, "PENDING, not skipped") {
		t.Fatalf("pending result mismatch: %s", tr.Content)
	}
	ends := env.es.byType(session.EventTurnEnd)
	var te session.TurnEndData
	if err := json.Unmarshal(ends[0].Data, &te); err != nil {
		t.Fatal(err)
	}
	if te.Reason != "completed" {
		t.Fatalf("timeout must not fail the turn, reason = %s", te.Reason)
	}
	if n := len(env.es.byType(session.EventQuestionAnswered)); n != 0 {
		t.Fatal("timed-out question must stay unanswered (continued)")
	}
	delivered, err := env.orch.AnswerQuestion(env.session.ID, "call_q1", testAnswers, "user-1")
	if err != nil {
		t.Fatal(err)
	}
	if delivered {
		t.Fatal("no in-memory wait after timeout; must fall to continued path")
	}
}

// turn 取消：等待随 ctx 结束，按现有中断语义收尾；问题仍可补答（continued）。
func TestQuestionCancelReleasesWait(t *testing.T) {
	env := newTestEnv(t, session.ApprovalAuto, func(call int, input []*schema.Message) []*schema.Message {
		return toolCallChunks("call_q1", "ask_user_question", askArgs)
	})
	env.prompt(t, "需要时问我", "req-1")
	go func() { _ = env.orch.StartTurn(context.Background(), env.session.ID) }()
	if _, err := env.es.waitFor(session.EventQuestionAsked, 5*time.Second); err != nil {
		t.Fatal(err)
	}
	if err := env.orch.Cancel(env.session.ID); err != nil {
		t.Fatal(err)
	}
	end, err := env.es.waitFor(session.EventTurnEnd, 15*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	var te session.TurnEndData
	if err := json.Unmarshal(end.Data, &te); err != nil {
		t.Fatal(err)
	}
	if te.Reason != "interrupted" {
		t.Fatalf("turn/end reason = %s, want interrupted", te.Reason)
	}
	if n := len(env.es.byType(session.EventQuestionAnswered)); n != 0 {
		t.Fatal("cancelled wait must not record an answer")
	}
	delivered, err := env.orch.AnswerQuestion(env.session.ID, "call_q1", testAnswers, "user-1")
	if err != nil {
		t.Fatal(err)
	}
	if delivered {
		t.Fatal("wait must be released with the cancelled turn")
	}
}

// ---------- REST 层（Handler.AnswerQuestion） ----------

type fakeAnswerOrchestrator struct {
	Orchestrator
	delivered bool
	answers   []session.QuestionAnswer
	starts    atomic.Int64
}

func (f *fakeAnswerOrchestrator) StartTurn(context.Context, string) error {
	f.starts.Add(1)
	return nil
}

func (f *fakeAnswerOrchestrator) AnswerQuestion(_, _ string, answers []session.QuestionAnswer, _ string) (bool, error) {
	f.answers = answers
	return f.delivered, nil
}

type questionHandlerEnv struct {
	handler *Handler
	es      *fakeEventStore
	orch    *fakeAnswerOrchestrator
}

func newQuestionHandlerEnv(t *testing.T) *questionHandlerEnv {
	t.Helper()
	snapRaw, err := json.Marshal(agent.Snapshot{AgentID: "agent-1", ToolsEnabled: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	detail := &session.Detail{Session: store.Session{
		ID: "sess-1", OrgID: "org-1", UserID: "user-1",
		AgentSnapshot: snapRaw, ModelID: "model-1", Status: session.StatusActive,
	}}
	es := &fakeEventStore{}
	// 前置历史：turn 1 已结束，遗留一次未答的 question/asked（continued）。
	if _, err := es.AppendBatch(context.Background(), "sess-1", []session.EventInput{
		{Type: session.EventTurnStart, Data: session.TurnStartData{Turn: 1, RequestID: "req-1"}},
		{Type: session.EventQuestionAsked, Data: session.QuestionAskedData{
			Turn: 1, Step: 1, CallID: "call_q1",
			Questions: []session.Question{
				{ID: "q1", Question: "选哪个方案？", Options: []session.QuestionOption{{Label: "A"}, {Label: "B"}}},
				{ID: "q2", Question: "补充说明？"},
			},
		}},
		{Type: session.EventTurnEnd, Data: session.TurnEndData{Turn: 1, Reason: "completed"}},
	}); err != nil {
		t.Fatal(err)
	}
	orch := &fakeAnswerOrchestrator{}
	h := NewHandler(orch, fakeSessionService{detail: detail}, es, &fakeRepo{sess: &detail.Session, es: es},
		nil, NewBus(),
		func(context.Context, string) (string, string, bool, error) { return "fake-v1", "prov-1", true, nil },
		"", nil)
	return &questionHandlerEnv{handler: h, es: es, orch: orch}
}

func (e *questionHandlerEnv) post(t *testing.T, callID string, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set("wispjar.uid", "user-1"); c.Next() })
	r.POST("/sessions/:id/questions/:callId/answer", e.handler.AnswerQuestion)
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/sessions/sess-1/questions/"+callID+"/answer", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// continued：无内存等待 → question/answered + 合成 user/message（source=question-reply）+
// 新 turn，复用 Prompt 管线。
func TestAnswerQuestionContinued(t *testing.T) {
	env := newQuestionHandlerEnv(t)
	rec := env.post(t, "call_q1", map[string]any{"answers": testAnswers})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("want 202, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Delivered string `json:"delivered"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Delivered != "continued" {
		t.Fatalf("delivered = %q, want continued", resp.Delivered)
	}
	deadline := time.Now().Add(2 * time.Second)
	for env.orch.starts.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if env.orch.starts.Load() != 1 {
		t.Fatalf("continued answer must start a new turn, starts = %d", env.orch.starts.Load())
	}
	if len(env.orch.answers) != 2 {
		t.Fatalf("orchestrator must receive the answers first: %+v", env.orch.answers)
	}

	answered := env.es.byType(session.EventQuestionAnswered)
	if len(answered) != 1 {
		t.Fatalf("want 1 question/answered, got %d", len(answered))
	}
	var ad session.QuestionAnsweredData
	if err := json.Unmarshal(answered[0].Data, &ad); err != nil {
		t.Fatal(err)
	}
	if ad.CallID != "call_q1" || ad.AnsweredBy != "user-1" {
		t.Fatalf("question/answered mismatch: %+v", ad)
	}

	msgs := env.es.byType(session.EventUserMessage)
	if len(msgs) != 1 {
		t.Fatalf("want 1 synthesized user/message, got %d", len(msgs))
	}
	var um session.UserMessageData
	if err := json.Unmarshal(msgs[0].Data, &um); err != nil {
		t.Fatal(err)
	}
	if um.Source == nil || um.Source.Kind != "question-reply" || um.Source.CallID != "call_q1" {
		t.Fatalf("user/message source mismatch: %+v", um.Source)
	}
	var body struct {
		Kind      string                   `json:"kind"`
		CallID    string                   `json:"call_id"`
		Questions []session.Question       `json:"questions"`
		Answers   []session.QuestionAnswer `json:"answers"`
	}
	if err := json.Unmarshal([]byte(um.Content), &body); err != nil {
		t.Fatalf("synthesized content must be JSON: %v (%s)", err, um.Content)
	}
	if body.Kind != "answer_to_pending_question" || body.CallID != "call_q1" || len(body.Questions) != 2 || len(body.Answers) != 2 {
		t.Fatalf("synthesized content mismatch: %s", um.Content)
	}
	if um.Turn != 2 {
		t.Fatalf("synthesized message must open turn 2, got turn %d", um.Turn)
	}
	if n := len(env.es.byType(session.EventTurnStart)); n != 2 {
		t.Fatalf("want 2 turn/start, got %d", n)
	}
	if n := len(env.es.byType(session.EventRequestHeader)); n != 1 {
		t.Fatalf("continued turn must carry request/header, got %d", n)
	}
}

// open：有内存等待 → 投递 channel 立即 200，不新增 turn。
func TestAnswerQuestionOpen(t *testing.T) {
	env := newQuestionHandlerEnv(t)
	env.orch.delivered = true
	rec := env.post(t, "call_q1", map[string]any{"answers": testAnswers})
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Delivered string `json:"delivered"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Delivered != "open" {
		t.Fatalf("delivered = %q, want open", resp.Delivered)
	}
	if env.orch.starts.Load() != 0 {
		t.Fatal("open delivery must not start a new turn")
	}
	if n := len(env.es.byType(session.EventUserMessage)); n != 0 {
		t.Fatal("open delivery must not synthesize user/message")
	}
}

// 校验：每题恰好一条答案；selected 必须是选项 label；单选题至多一个 selected。
func TestAnswerQuestionBadAnswer(t *testing.T) {
	cases := []struct {
		name    string
		answers []map[string]any
	}{
		{"missing one question", []map[string]any{{"id": "q1", "selected": []string{"A"}}}},
		{"unknown question id", []map[string]any{{"id": "q1", "selected": []string{"A"}}, {"id": "qX", "custom": "x"}}},
		{"duplicated id", []map[string]any{{"id": "q1", "selected": []string{"A"}}, {"id": "q1", "selected": []string{"B"}}}},
		{"label not in options", []map[string]any{{"id": "q1", "selected": []string{"C"}}, {"id": "q2", "custom": "x"}}},
		{"single choice multi selected", []map[string]any{{"id": "q1", "selected": []string{"A", "B"}}, {"id": "q2", "custom": "x"}}},
		{"free text with selected", []map[string]any{{"id": "q1", "selected": []string{"A"}}, {"id": "q2", "selected": []string{"A"}}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := newQuestionHandlerEnv(t)
			rec := env.post(t, "call_q1", map[string]any{"answers": tc.answers})
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("want 400, got %d: %s", rec.Code, rec.Body.String())
			}
			if code := errBody(t, rec); code != "question/bad_answer" {
				t.Fatalf("want question/bad_answer, got %q", code)
			}
			if len(env.es.byType(session.EventQuestionAnswered)) != 0 {
				t.Fatal("rejected answer must not produce events")
			}
		})
	}
}

func TestAnswerQuestionNotFound(t *testing.T) {
	env := newQuestionHandlerEnv(t)
	rec := env.post(t, "call_nope", map[string]any{"answers": testAnswers})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d: %s", rec.Code, rec.Body.String())
	}
	if code := errBody(t, rec); code != "question/not_found" {
		t.Fatalf("want question/not_found, got %q", code)
	}
}

func TestAnswerQuestionAlreadyAnswered(t *testing.T) {
	env := newQuestionHandlerEnv(t)
	if _, err := env.es.AppendBatch(context.Background(), "sess-1", []session.EventInput{
		{Type: session.EventQuestionAnswered, Data: session.QuestionAnsweredData{
			CallID: "call_q1", Answers: testAnswers, AnsweredBy: "user-1",
		}},
	}); err != nil {
		t.Fatal(err)
	}
	rec := env.post(t, "call_q1", map[string]any{"answers": testAnswers})
	if rec.Code != http.StatusConflict {
		t.Fatalf("want 409, got %d: %s", rec.Code, rec.Body.String())
	}
	if code := errBody(t, rec); code != "question/not_pending" {
		t.Fatalf("want question/not_pending, got %q", code)
	}
}
