package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"github.com/ravenmk2/wispjar/internal/agent"
	"github.com/ravenmk2/wispjar/internal/runtime/tools"
	"github.com/ravenmk2/wispjar/internal/sandbox"
	"github.com/ravenmk2/wispjar/internal/session"
	"github.com/ravenmk2/wispjar/internal/store"
)

// ---------- fake EventStore ----------

type fakeEventStore struct {
	mu     sync.Mutex
	events []store.SessionEvent
}

func (s *fakeEventStore) AppendBatch(_ context.Context, sessionID string, inputs []session.EventInput) ([]store.SessionEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]store.SessionEvent, 0, len(inputs))
	for _, in := range inputs {
		raw, err := json.Marshal(in.Data)
		if err != nil {
			return nil, err
		}
		ev := store.SessionEvent{
			SessionID: sessionID,
			Seq:       int64(len(s.events)) + 1,
			Type:      in.Type,
			TimeMs:    time.Now().UnixMilli(),
			Data:      raw,
		}
		s.events = append(s.events, ev)
		out = append(out, ev)
	}
	return out, nil
}

func (s *fakeEventStore) Events(_ context.Context, sessionID string, afterSeq int64, limit int) ([]store.SessionEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []store.SessionEvent{}
	for _, ev := range s.events {
		if ev.Seq > afterSeq {
			out = append(out, ev)
		}
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (s *fakeEventStore) TailEvents(_ context.Context, sessionID string, n int) ([]store.SessionEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.events) <= n {
		return append([]store.SessionEvent{}, s.events...), nil
	}
	return append([]store.SessionEvent{}, s.events[len(s.events)-n:]...), nil
}

func (s *fakeEventStore) FindUserMessageByRequestID(_ context.Context, sessionID, requestID string) (*store.SessionEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, ev := range s.events {
		if ev.Type != session.EventUserMessage {
			continue
		}
		var d session.UserMessageData
		if json.Unmarshal(ev.Data, &d) == nil && d.RequestID == requestID {
			e := ev
			return &e, nil
		}
	}
	return nil, nil
}

func (s *fakeEventStore) types() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.events))
	for _, ev := range s.events {
		out = append(out, ev.Type)
	}
	return out
}

func (s *fakeEventStore) byType(t string) []store.SessionEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []store.SessionEvent{}
	for _, ev := range s.events {
		if ev.Type == t {
			out = append(out, ev)
		}
	}
	return out
}

func (s *fakeEventStore) waitFor(t string, timeout time.Duration) (store.SessionEvent, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if evs := s.byType(t); len(evs) > 0 {
			return evs[len(evs)-1], nil
		}
		time.Sleep(5 * time.Millisecond)
	}
	return store.SessionEvent{}, fmt.Errorf("timeout waiting for event %s", t)
}

// ---------- fake SessionRepo ----------

type fakeRepo struct {
	sess *store.Session
	es   *fakeEventStore
}

func (r *fakeRepo) SessionByID(_ context.Context, sessionID string) (*store.Session, error) {
	return r.sess, nil
}

func (r *fakeRepo) TurnCount(_ context.Context, sessionID string) (int, error) {
	return len(r.es.byType(session.EventTurnStart)), nil
}

func (r *fakeRepo) SetTitleIfUnpinned(_ context.Context, sessionID, title string) (bool, error) {
	if r.sess.TitlePinned {
		return false, nil
	}
	r.sess.Title = title
	return true, nil
}

func (r *fakeRepo) SetSandboxStatus(_ context.Context, sessionID, sandboxID, status string) error {
	raw, _ := json.Marshal(map[string]string{"id": sandboxID, "status": status})
	r.sess.Sandbox = raw
	return nil
}

func (r *fakeRepo) ApprovalDecision(_ context.Context, sessionID, callID string) (bool, bool, error) {
	for _, ev := range r.es.byType(session.EventApprovalDecided) {
		var d session.ApprovalDecidedData
		if json.Unmarshal(ev.Data, &d) == nil && d.CallID == callID {
			return true, d.Auto, nil
		}
	}
	return false, false, nil
}

func (r *fakeRepo) QuestionAsked(_ context.Context, sessionID, callID string) (*session.QuestionAskedData, error) {
	for _, ev := range r.es.byType(session.EventQuestionAsked) {
		var d session.QuestionAskedData
		if json.Unmarshal(ev.Data, &d) == nil && d.CallID == callID {
			out := d
			return &out, nil
		}
	}
	return nil, nil
}

func (r *fakeRepo) QuestionAnswered(_ context.Context, sessionID, callID string) (bool, error) {
	for _, ev := range r.es.byType(session.EventQuestionAnswered) {
		var d session.QuestionAnsweredData
		if json.Unmarshal(ev.Data, &d) == nil && d.CallID == callID {
			return true, nil
		}
	}
	return false, nil
}

func (r *fakeRepo) RunningSessions(_ context.Context) ([]store.Session, error) {
	return []store.Session{*r.sess}, nil
}

func (r *fakeRepo) UndecidedApprovals(_ context.Context, sessionID string) ([]session.ApprovalAskedData, error) {
	decided := map[string]bool{}
	for _, ev := range r.es.byType(session.EventApprovalDecided) {
		var d session.ApprovalDecidedData
		if json.Unmarshal(ev.Data, &d) == nil {
			decided[d.CallID] = true
		}
	}
	out := []session.ApprovalAskedData{}
	for _, ev := range r.es.byType(session.EventApprovalAsked) {
		var d session.ApprovalAskedData
		if json.Unmarshal(ev.Data, &d) == nil && !decided[d.CallID] {
			out = append(out, d)
		}
	}
	return out, nil
}

// ---------- fake ChatModel ----------

type fakeModel struct {
	mu     sync.Mutex
	calls  int
	script func(call int, input []*schema.Message) []*schema.Message
}

func (m *fakeModel) Generate(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	chunks, err := m.stream(ctx, input)
	if err != nil {
		return nil, err
	}
	return chunks[len(chunks)-1], nil
}

func (m *fakeModel) Stream(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	chunks, err := m.stream(ctx, input)
	if err != nil {
		return nil, err
	}
	return schema.StreamReaderFromArray(chunks), nil
}

func (m *fakeModel) stream(_ context.Context, input []*schema.Message) ([]*schema.Message, error) {
	m.mu.Lock()
	m.calls++
	call := m.calls
	m.mu.Unlock()
	chunks := m.script(call, input)
	if len(chunks) == 0 {
		return nil, errors.New("fake model: empty script")
	}
	return chunks, nil
}

func (m *fakeModel) WithTools(tools []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return m, nil
}

type fakeModelFactory struct{ m *fakeModel }

func (f fakeModelFactory) NewChatModel(_ context.Context, modelID string, params map[string]any) (model.ToolCallingChatModel, error) {
	return f.m, nil
}

// ---------- fake CheckPointStore ----------

type fakeCheckpointStore struct {
	mu   sync.Mutex
	data map[string][]byte
}

func newFakeCheckpointStore() *fakeCheckpointStore {
	return &fakeCheckpointStore{data: map[string][]byte{}}
}

func (s *fakeCheckpointStore) Get(_ context.Context, id string) ([]byte, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.data[id]
	return b, ok, nil
}

func (s *fakeCheckpointStore) Set(_ context.Context, id string, b []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[id] = append([]byte{}, b...)
	return nil
}

func (s *fakeCheckpointStore) Delete(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.data, id)
	return nil
}

// ---------- fake sandbox Handle（内存文件系统） ----------

type fakeHandle struct {
	mu     sync.Mutex
	files  map[string][]byte
	execFn func(cmd string) (int, string, string)
	runErr error
}

func newFakeHandle() *fakeHandle {
	return &fakeHandle{
		files: map[string][]byte{},
		execFn: func(cmd string) (int, string, string) {
			if strings.HasPrefix(cmd, "mkdir -p ") {
				return 0, "", ""
			}
			return 0, "file.txt\n", ""
		},
	}
}

func (h *fakeHandle) RunCommand(_ context.Context, cmd, cwd string, env map[string]string, timeoutMs int64, onEvent func(stream, chunk string)) (*sandbox.CommandResult, error) {
	if h.runErr != nil {
		return nil, h.runErr
	}
	code, stdout, stderr := h.execFn(cmd)
	if onEvent != nil && stdout != "" {
		onEvent("stdout", stdout)
	}
	return &sandbox.CommandResult{ExitCode: code, Stdout: stdout, Stderr: stderr}, nil
}

func (h *fakeHandle) UploadFile(_ context.Context, remotePath string, content []byte) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.files[remotePath] = append([]byte{}, content...)
	return nil
}

func (h *fakeHandle) DownloadFile(_ context.Context, remotePath string) ([]byte, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	data, ok := h.files[remotePath]
	if !ok {
		return nil, fmt.Errorf("no such file: %s", remotePath)
	}
	return append([]byte{}, data...), nil
}

func (h *fakeHandle) ReplaceInFile(_ context.Context, remotePath, old, new string, replaceAll bool) (int, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	data, ok := h.files[remotePath]
	if !ok {
		return 0, fmt.Errorf("no such file: %s", remotePath)
	}
	content := string(data)
	n := strings.Count(content, old)
	if n == 0 {
		return 0, nil
	}
	if replaceAll {
		content = strings.ReplaceAll(content, old, new)
	} else {
		content = strings.Replace(content, old, new, 1)
	}
	h.files[remotePath] = []byte(content)
	return n, nil
}

func (h *fakeHandle) SearchFiles(_ context.Context, dir, pattern string) ([]sandbox.FileInfo, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := []sandbox.FileInfo{}
	for p, data := range h.files {
		if strings.HasPrefix(p, dir) {
			out = append(out, sandbox.FileInfo{Path: p, Type: "file", Size: int64(len(data)), ModifiedAt: time.Now()})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

func (h *fakeHandle) ListDir(_ context.Context, remotePath string) ([]sandbox.FileInfo, error) {
	return h.SearchFiles(context.Background(), remotePath, "*")
}

func (h *fakeHandle) FileInfo(_ context.Context, remotePath string) (sandbox.FileInfo, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	data, ok := h.files[remotePath]
	if !ok {
		return sandbox.FileInfo{}, fmt.Errorf("no such file: %s", remotePath)
	}
	return sandbox.FileInfo{Path: remotePath, Type: "file", Size: int64(len(data)), ModifiedAt: time.Now()}, nil
}

type fakeSandboxProvider struct{ h *fakeHandle }

func (p fakeSandboxProvider) Get(_ context.Context, sessionID string, opts sandbox.Options) (tools.Handle, string, error) {
	return p.h, "fake-sandbox", nil
}

// ---------- 测试装配 ----------

type testEnv struct {
	orch    Orchestrator
	es      *fakeEventStore
	repo    *fakeRepo
	model   *fakeModel
	handle  *fakeHandle
	bus     *Bus
	session *store.Session
}

func newTestEnv(t *testing.T, approvalMode string, script func(call int, input []*schema.Message) []*schema.Message) *testEnv {
	t.Helper()
	snap := agent.Snapshot{
		AgentID:       "agent-1",
		Name:          "测试智能体",
		SystemPrompt:  "你是测试助手。",
		MaxIterations: 10,
		ToolsEnabled:  json.RawMessage(`{"exec":true,"read":true,"write":true,"edit":true,"glob":true,"grep":true}`),
		Model:         agent.SnapshotModel{ID: "model-1", ModelCode: "fake-v1", ProviderID: "prov-1"},
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
		ApprovalMode:  approvalMode,
		ModelID:       "model-1",
		Status:        session.StatusActive,
		Sandbox:       json.RawMessage(`{"id":"","status":"none"}`),
	}
	es := &fakeEventStore{}
	repo := &fakeRepo{sess: sess, es: es}
	m := &fakeModel{script: script}
	handle := newFakeHandle()
	bus := NewBus()
	cp := newFakeCheckpointStore()

	orch := NewOrchestrator(Deps{
		Events:      es,
		Sessions:    repo,
		Models:      fakeModelFactory{m: m},
		Checkpoints: func(sessionID string) adk.CheckPointStore { return cp },
		Bus:         bus,
		ToolContextFactory: func(sess *store.Session, snap *agent.Snapshot) *tools.Context {
			tc := tools.NewContext(sess.ID, sess.OrgID)
			tc.Sandbox = fakeSandboxProvider{h: handle}
			return tc
		},
	})
	return &testEnv{orch: orch, es: es, repo: repo, model: m, handle: handle, bus: bus, session: sess}
}

// prompt 模拟 REST 层追加 user/message + turn/start + request/header。
func (e *testEnv) prompt(t *testing.T, content, requestID string) {
	t.Helper()
	turn := len(e.es.byType(session.EventTurnStart)) + 1
	_, err := e.es.AppendBatch(context.Background(), e.session.ID, []session.EventInput{
		{Type: session.EventUserMessage, Data: session.UserMessageData{Turn: turn, Content: content, RequestID: requestID}},
		{Type: session.EventTurnStart, Data: session.TurnStartData{Turn: turn, RequestID: requestID}},
		{Type: session.EventRequestHeader, Data: session.RequestHeaderData{Turn: turn, ProviderID: "prov-1", ModelID: "model-1", ModelCode: "fake-v1", Tools: []string{"exec"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
}

func toolCallChunks(callID, name, args string) []*schema.Message {
	idx := 0
	return []*schema.Message{
		{Role: schema.Assistant, ToolCalls: []schema.ToolCall{
			{Index: &idx, ID: callID, Type: "function", Function: schema.FunctionCall{Name: name, Arguments: args[:len(args)/2]}},
		}},
		{Role: schema.Assistant, ToolCalls: []schema.ToolCall{
			{Index: &idx, Function: schema.FunctionCall{Arguments: args[len(args)/2:]}},
		}, ResponseMeta: &schema.ResponseMeta{
			FinishReason: "tool_calls",
			Usage:        &schema.TokenUsage{PromptTokens: 10, CompletionTokens: 5},
		}},
	}
}

func textChunks(text string, usage *schema.TokenUsage) []*schema.Message {
	mid := len(text) / 2
	return []*schema.Message{
		{Role: schema.Assistant, Content: text[:mid]},
		{Role: schema.Assistant, Content: text[mid:], ResponseMeta: &schema.ResponseMeta{
			FinishReason: "stop",
			Usage:        usage,
		}},
	}
}
