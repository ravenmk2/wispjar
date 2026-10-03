package runtime

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"

	"github.com/ravenmk2/wispjar/internal/agent"
	"github.com/ravenmk2/wispjar/internal/attachment"
	"github.com/ravenmk2/wispjar/internal/runtime/tools"
	"github.com/ravenmk2/wispjar/internal/session"
	"github.com/ravenmk2/wispjar/internal/store"
)

// recordingImageSaver 内存版 ImageSaver + AttachmentReader：按 sha256 去重并留存字节，
// 供 read_media 入库与 history 回放组装共用。
type recordingImageSaver struct {
	mu    sync.Mutex
	calls int
	refs  map[string]*session.AttachmentRef
	bytes map[string][]byte
}

func newRecordingImageSaver() *recordingImageSaver {
	return &recordingImageSaver{refs: map[string]*session.AttachmentRef{}, bytes: map[string][]byte{}}
}

func (s *recordingImageSaver) Upload(_ context.Context, orgID, actorID, name string, data []byte) (*session.AttachmentRef, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	sum := sha256.Sum256(data)
	hexSum := hex.EncodeToString(sum[:])
	id := "sha256:" + hexSum
	if ref, ok := s.refs[id]; ok {
		return ref, nil
	}
	mime, w, h, err := attachment.ValidateImage(data)
	if err != nil {
		return nil, err
	}
	ref := &session.AttachmentRef{ID: id, MIME: mime, Bytes: int64(len(data)), Width: w, Height: h, Name: name}
	s.refs[id] = ref
	s.bytes[id] = append([]byte{}, data...)
	return ref, nil
}

func (s *recordingImageSaver) ReadBytes(_ context.Context, id string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]byte{}, s.bytes[id]...), nil
}

func (s *recordingImageSaver) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

// readMediaEnv 装配启用 read_media 的测试环境（沙箱内预置图片；vision 可开关）。
type readMediaEnv struct {
	*testEnv
	saver *recordingImageSaver
}

func newReadMediaEnv(t *testing.T, vision bool, files map[string][]byte,
	script func(call int, input []*schema.Message) []*schema.Message) *readMediaEnv {
	t.Helper()
	snap := agent.Snapshot{
		AgentID:       "agent-1",
		Name:          "测试智能体",
		SystemPrompt:  "你是测试助手。",
		MaxIterations: 10,
		ToolsEnabled:  json.RawMessage(`{"exec":true,"read":true,"read_media":true}`),
		Model:         agent.SnapshotModel{ID: "model-1", ModelCode: "fake-v1", ProviderID: "prov-1"},
	}
	snapRaw, err := json.Marshal(snap)
	if err != nil {
		t.Fatal(err)
	}
	sess := &store.Session{
		ID: "sess-1", OrgID: "org-1", UserID: "user-1", AgentID: "agent-1",
		AgentSnapshot: snapRaw, ApprovalMode: session.ApprovalAuto,
		ModelID: "model-1", Status: session.StatusActive,
		Sandbox: json.RawMessage(`{"id":"","status":"none"}`),
	}
	es := &fakeEventStore{}
	repo := &fakeRepo{sess: sess, es: es}
	m := &fakeModel{script: script}
	handle := newFakeHandle()
	for p, data := range files {
		handle.files[p] = data
	}
	bus := NewBus()
	cp := newFakeCheckpointStore()
	saver := newRecordingImageSaver()

	orch := NewOrchestrator(Deps{
		Events:      es,
		Sessions:    repo,
		Models:      fakeModelFactory{m: m},
		Checkpoints: func(sessionID string) adk.CheckPointStore { return cp },
		Bus:         bus,
		Attachments: saver,
		ImageSaver:  saver,
		Vision:      func(_ context.Context, _ string) bool { return vision },
		ToolContextFactory: func(sess *store.Session, snap *agent.Snapshot) *tools.Context {
			tc := tools.NewContext(sess.ID, sess.OrgID)
			tc.Sandbox = fakeSandboxProvider{h: handle}
			tc.Images = saver
			return tc
		},
	})
	return &readMediaEnv{
		testEnv: &testEnv{orch: orch, es: es, repo: repo, model: m, handle: handle, bus: bus, session: sess},
		saver:   saver,
	}
}

func toolCallOne(callID, name, args string) []*schema.Message {
	idx := 0
	return []*schema.Message{
		{Role: schema.Assistant, ToolCalls: []schema.ToolCall{
			{Index: &idx, ID: callID, Type: "function", Function: schema.FunctionCall{Name: name, Arguments: args}},
		}, ResponseMeta: &schema.ResponseMeta{
			FinishReason: "tool_calls",
			Usage:        &schema.TokenUsage{PromptTokens: 10, CompletionTokens: 5},
		}},
	}
}

// findToolMessage 在模型输入中按 callID 找 tool 消息。
func findToolMessage(input []*schema.Message, callID string) *schema.Message {
	for _, m := range input {
		if m.Role == schema.Tool && m.ToolCallID == callID {
			return m
		}
	}
	return nil
}

func TestReadMediaEndToEnd(t *testing.T) {
	pngBytes := encodePNGForRuntime(t)
	var call2Input []*schema.Message
	env := newReadMediaEnv(t, true, map[string][]byte{"/workspace/a.png": pngBytes},
		func(call int, input []*schema.Message) []*schema.Message {
			if call == 1 {
				return toolCallOne("call_1", "read_media", `{"path":"/workspace/a.png"}`)
			}
			call2Input = input
			return textChunks("看到了，是一张图", nil)
		})
	env.prompt(t, "看看 /workspace/a.png", "req-1")
	if err := env.orch.StartTurn(context.Background(), env.session.ID); err != nil {
		t.Fatalf("StartTurn: %v", err)
	}

	// tool/result 事件：文本信封 + 附件引用
	results := env.es.byType(session.EventToolResult)
	if len(results) != 1 {
		t.Fatalf("want 1 tool/result, events: %v", env.es.types())
	}
	var tr session.ToolResultData
	if err := json.Unmarshal(results[0].Data, &tr); err != nil {
		t.Fatal(err)
	}
	if tr.Name != "read_media" || tr.Error != "" {
		t.Fatalf("tool/result mismatch: %+v", tr)
	}
	if len(tr.Attachments) != 1 || !strings.HasPrefix(tr.Attachments[0].ID, "sha256:") ||
		tr.Attachments[0].MIME != "image/png" || tr.Attachments[0].Width != 4 {
		t.Fatalf("tool/result attachments mismatch: %+v", tr.Attachments)
	}
	var handle struct {
		Path   string `json:"path"`
		Sha256 string `json:"sha256"`
	}
	if err := json.Unmarshal([]byte(tr.Content), &handle); err != nil {
		t.Fatalf("content must be the JSON handle: %v", err)
	}
	if handle.Path != "/workspace/a.png" || len(handle.Sha256) != 8 {
		t.Fatalf("handle mismatch: %+v", handle)
	}
	if tr.Meta == nil || tr.Meta.Render != "read" {
		t.Fatalf("meta mismatch: %+v", tr.Meta)
	}

	// 模型第二次调用输入：tool 消息直接带图（text 信封 + image part，顺序一致）
	tm := findToolMessage(call2Input, "call_1")
	if tm == nil {
		t.Fatalf("call2 input missing tool message: %+v", call2Input)
	}
	parts := tm.UserInputMultiContent
	if len(parts) != 2 || parts[0].Type != schema.ChatMessagePartTypeText ||
		parts[1].Type != schema.ChatMessagePartTypeImageURL {
		t.Fatalf("in-run tool message must be [text, image]: %+v", parts)
	}
	decoded, err := base64.StdEncoding.DecodeString(*parts[1].Image.Base64Data)
	if err != nil || !reflect.DeepEqual(decoded, pngBytes) {
		t.Fatalf("in-run image bytes mismatch (err=%v)", err)
	}

	// 回放：history 从事件 + 附件存储重组多模态 tool 消息，与 in-run 字节一致。
	// （derive 按最后一个 turn/start 截断历史，先补第二个 prompt 模拟下一轮开始）
	env.prompt(t, "继续", "req-2")
	o, ok := env.orch.(*orchestrator)
	if !ok {
		t.Fatal("orch must be *orchestrator")
	}
	msgs, err := o.history(context.Background(), env.session.ID)
	if err != nil {
		t.Fatal(err)
	}
	replayed := findToolMessage(msgs, "call_1")
	if replayed == nil {
		t.Fatalf("history missing tool message: %+v", msgs)
	}
	rp := replayed.UserInputMultiContent
	if len(rp) != 2 || rp[0].Type != schema.ChatMessagePartTypeText || rp[0].Text != tr.Content {
		t.Fatalf("replayed text part mismatch: %+v", rp)
	}
	if rp[1].Type != schema.ChatMessagePartTypeImageURL ||
		*rp[1].Image.Base64Data != *parts[1].Image.Base64Data {
		t.Fatal("replayed image must be byte-identical to in-run image")
	}
	if replayed.ToolName != "read_media" {
		t.Fatalf("replayed tool name = %q", replayed.ToolName)
	}
}

func TestReadMediaVisionGateEndToEnd(t *testing.T) {
	pngBytes := encodePNGForRuntime(t)
	env := newReadMediaEnv(t, false, map[string][]byte{"/workspace/a.png": pngBytes},
		func(call int, input []*schema.Message) []*schema.Message {
			if call == 1 {
				return toolCallOne("call_1", "read_media", `{"path":"/workspace/a.png"}`)
			}
			return textChunks("模型不支持图片，作罢", nil)
		})
	env.prompt(t, "看看 /workspace/a.png", "req-1")
	if err := env.orch.StartTurn(context.Background(), env.session.ID); err != nil {
		t.Fatalf("StartTurn: %v", err)
	}

	results := env.es.byType(session.EventToolResult)
	if len(results) != 1 {
		t.Fatalf("want 1 tool/result, events: %v", env.es.types())
	}
	var tr session.ToolResultData
	if err := json.Unmarshal(results[0].Data, &tr); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(tr.Content, "不支持图片输入") {
		t.Fatalf("vision gate content: %q", tr.Content)
	}
	if len(tr.Attachments) != 0 {
		t.Fatalf("vision gate must not record attachments: %+v", tr.Attachments)
	}
	if env.saver.callCount() != 0 {
		t.Fatal("vision gate must reject before attachment work")
	}
	ends := env.es.byType(session.EventTurnEnd)
	var te session.TurnEndData
	if err := json.Unmarshal(ends[0].Data, &te); err != nil {
		t.Fatal(err)
	}
	if te.Reason != "completed" {
		t.Fatalf("vision-gated read_media must not fail the turn: %+v", te)
	}
}

func TestBuildToolMessageOffload(t *testing.T) {
	ref := session.AttachmentRef{
		ID:   "sha256:" + strings.Repeat("c", 64),
		MIME: "image/png", Bytes: 15, Width: 4, Height: 2, Name: "a.png",
	}
	o := &orchestrator{deps: Deps{
		Attachments: fakeAttachmentReader{data: map[string][]byte{ref.ID: []byte("fake-png-bytes")}},
	}}
	m := session.Message{
		Role: session.RoleTool, ToolCallID: "call_1", ToolName: "read_media",
		Content: `{"path":"/workspace/a.png"}`, Attachments: []session.AttachmentRef{ref},
	}

	msg, err := o.buildToolMessage(context.Background(), m, nil)
	if err != nil {
		t.Fatal(err)
	}
	if msg.Role != schema.Tool || msg.ToolCallID != "call_1" || msg.ToolName != "read_media" {
		t.Fatalf("tool message identity mismatch: %+v", msg)
	}
	parts := msg.UserInputMultiContent
	if len(parts) != 2 || parts[0].Type != schema.ChatMessagePartTypeText ||
		parts[1].Type != schema.ChatMessagePartTypeImageURL {
		t.Fatalf("want [text, image], got %+v", parts)
	}

	// 卸载位：占位文本含文件名 + sha256 前 8 位 + read_media 读回提示，且确定性
	msg, err = o.buildToolMessage(context.Background(), m, []bool{true})
	if err != nil {
		t.Fatal(err)
	}
	parts = msg.UserInputMultiContent
	if len(parts) != 2 || parts[1].Type != schema.ChatMessagePartTypeText {
		t.Fatalf("offloaded occurrence must be placeholder text: %+v", parts)
	}
	ph := parts[1].Text
	if !strings.Contains(ph, "a.png") || !strings.Contains(ph, "sha256:cccccccc") ||
		!strings.Contains(ph, "read_media") {
		t.Fatalf("placeholder must carry name + sha256[:8] + re-read hint: %q", ph)
	}
	if ph != offloadedToolImageText(ref) {
		t.Fatal("placeholder must be deterministic")
	}
}

func TestPlanImageOffloadCoversToolImages(t *testing.T) {
	// 最旧两张在 tool 消息（read_media 读入），其余在 user 消息：卸载必须覆盖
	// tool 消息图片出现位（共用同一预算与最旧优先量子化规则）。
	toolMsg := func(id string) session.Message {
		return session.Message{
			Role: session.RoleTool, ToolCallID: "call_" + id, ToolName: "read_media",
			Content:     "handle",
			Attachments: []session.AttachmentRef{att(id, 10)},
		}
	}
	msgs := []session.Message{
		toolMsg(strings.Repeat("1", 8)),
		toolMsg(strings.Repeat("2", 8)),
	}
	msgs = append(msgs, imageMsgs(make([]int64, 7)...)...) // 合计 9 张：卸载最旧 2 张
	marks := offloadCount(planImageOffload(msgs))
	want := []bool{true, true, false, false, false, false, false, false, false}
	if !reflect.DeepEqual(marks, want) {
		t.Fatalf("tool images must join the offload budget: want %v, got %v", want, marks)
	}
	if !reflect.DeepEqual(planImageOffload(msgs), planImageOffload(msgs)) {
		t.Fatal("offload plan must be deterministic")
	}
}

// encodePNGForRuntime 生成 4x2 合法 PNG（runtime 测试用，避免跨包引用 attachment 测试helper）。
func encodePNGForRuntime(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 4, 2))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
