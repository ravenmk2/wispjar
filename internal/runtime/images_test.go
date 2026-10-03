package runtime

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"

	"github.com/ravenmk2/wispjar/internal/session"
	"github.com/ravenmk2/wispjar/internal/store"
)

func att(id string, bytes int64) session.AttachmentRef {
	return session.AttachmentRef{ID: id, MIME: "image/png", Bytes: bytes, Width: 4, Height: 4, Name: id + ".png"}
}

func imageMsgs(sizes ...int64) []session.Message {
	msgs := make([]session.Message, 0, len(sizes))
	for _, sz := range sizes {
		msgs = append(msgs, session.Message{
			Role:        session.RoleUser,
			Content:     "看图",
			Attachments: []session.AttachmentRef{att(strings.Repeat("a", 8), sz)},
		})
	}
	return msgs
}

func offloadCount(marks [][]bool) []bool {
	flat := []bool{}
	for _, m := range marks {
		if m == nil {
			flat = append(flat, false)
			continue
		}
		flat = append(flat, m...)
	}
	return flat
}

func TestPlanImageOffloadUnderBudget(t *testing.T) {
	marks := planImageOffload(imageMsgs(100, 200, 300))
	for _, m := range marks {
		for _, b := range m {
			if b {
				t.Fatal("under budget: nothing should be offloaded")
			}
		}
	}
}

func TestPlanImageOffloadCountQuantum(t *testing.T) {
	// 9 张：超出 1 张，按 2 张量子取整 → 卸载最旧 2 张
	marks := offloadCount(planImageOffload(imageMsgs(make([]int64, 9)...)))
	want := []bool{true, true, false, false, false, false, false, false, false}
	if !reflect.DeepEqual(marks, want) {
		t.Fatalf("9 images: want %v, got %v", want, marks)
	}
	// 8 张：不超预算
	for _, b := range offloadCount(planImageOffload(imageMsgs(make([]int64, 8)...))) {
		if b {
			t.Fatal("8 images: nothing should be offloaded")
		}
	}
	// 11 张：超出 3 张，取整到 4 张
	marks = offloadCount(planImageOffload(imageMsgs(make([]int64, 11)...)))
	want = []bool{true, true, true, true, false, false, false, false, false, false, false}
	if !reflect.DeepEqual(marks, want) {
		t.Fatalf("11 images: want %v, got %v", want, marks)
	}
}

func TestPlanImageOffloadByteQuantum(t *testing.T) {
	// 4 张 x 5MB：base64 合计约 26.7MB > 16MB。
	// 超出约 10.7MB，目标取整到 12MB；最旧 2 张 base64 约 14MB >= 12MB → 卸载 2 张。
	mb := int64(5 << 20)
	marks := offloadCount(planImageOffload(imageMsgs(mb, mb, mb, mb)))
	want := []bool{true, true, false, false}
	if !reflect.DeepEqual(marks, want) {
		t.Fatalf("byte budget: want %v, got %v", want, marks)
	}
	// 保留部分必须重新落入字节预算内
	var kept int
	for i, b := range marks {
		if !b {
			kept += base64Len(imageMsgs(mb, mb, mb, mb)[i].Attachments[0].Bytes)
		}
	}
	if kept > offloadMaxBytes {
		t.Fatalf("kept bytes %d still over budget %d", kept, offloadMaxBytes)
	}
}

func TestPlanImageOffloadDeterministic(t *testing.T) {
	msgs := imageMsgs(make([]int64, 13)...)
	first := planImageOffload(msgs)
	second := planImageOffload(msgs)
	if !reflect.DeepEqual(first, second) {
		t.Fatal("offload plan must be deterministic for identical history")
	}
}

func TestOffloadedImageText(t *testing.T) {
	ref := session.AttachmentRef{
		ID:   "sha256:abcdef1234567890" + strings.Repeat("0", 48),
		Name: "截图.png",
	}
	text := offloadedImageText(ref)
	if !strings.Contains(text, "sha256:abcdef12") || !strings.Contains(text, "截图.png") {
		t.Fatalf("placeholder must carry identity: %q", text)
	}
	if text != offloadedImageText(ref) {
		t.Fatal("placeholder must be deterministic")
	}
}

// fakeAttachmentReader 内存附件字节源。
type fakeAttachmentReader struct {
	data map[string][]byte
	err  error
}

func (f fakeAttachmentReader) ReadBytes(_ context.Context, id string) ([]byte, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.data[id], nil
}

func TestBuildUserMessageOrder(t *testing.T) {
	o := &orchestrator{deps: Deps{
		Attachments: fakeAttachmentReader{data: map[string][]byte{
			"sha256:" + strings.Repeat("a", 64): []byte("fake-png-bytes"),
			"sha256:" + strings.Repeat("b", 64): []byte("other-png-bytes"),
		}},
	}}
	m := session.Message{
		Role:    session.RoleUser,
		Content: "描述这两张图",
		Attachments: []session.AttachmentRef{
			att("sha256:"+strings.Repeat("a", 64), 15),
			att("sha256:"+strings.Repeat("b", 64), 16),
		},
	}
	msg, err := o.buildUserMessage(context.Background(), m, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(msg.UserInputMultiContent) != 3 {
		t.Fatalf("want 3 parts (2 image + 1 text), got %d", len(msg.UserInputMultiContent))
	}
	parts := msg.UserInputMultiContent
	if parts[0].Type != schema.ChatMessagePartTypeImageURL || parts[1].Type != schema.ChatMessagePartTypeImageURL {
		t.Fatal("images must come before text")
	}
	if parts[2].Type != schema.ChatMessagePartTypeText || parts[2].Text != "描述这两张图" {
		t.Fatalf("text part must be last: %+v", parts[2])
	}
	if parts[0].Image == nil || parts[0].Image.Base64Data == nil || parts[0].Image.MIMEType != "image/png" {
		t.Fatalf("image part must carry Base64Data + MIME: %+v", parts[0].Image)
	}

	// 卸载位：最旧图替换为占位文本，顺序不变
	msg, err = o.buildUserMessage(context.Background(), m, []bool{true, false})
	if err != nil {
		t.Fatal(err)
	}
	parts = msg.UserInputMultiContent
	if parts[0].Type != schema.ChatMessagePartTypeText || !strings.Contains(parts[0].Text, "图片已卸载") {
		t.Fatalf("offloaded occurrence must be placeholder text: %+v", parts[0])
	}
	if parts[1].Type != schema.ChatMessagePartTypeImageURL {
		t.Fatal("retained image must stay inline")
	}
}

func TestTrimContextCountsImages(t *testing.T) {
	repo := &fakeRepo{sess: &store.Session{ModelID: "model-1"}, es: &fakeEventStore{}}
	o := &orchestrator{deps: Deps{
		Sessions:    repo,
		ContextSize: func(_ context.Context, _ string) int { return 1000 }, // limit = 800 tokens
	}}
	b64 := strings.Repeat("x", 4000) // 约 1000 tokens 的图片
	imagePart := schema.MessageInputPart{
		Type:  schema.ChatMessagePartTypeImageURL,
		Image: &schema.MessageInputImage{MessagePartCommon: schema.MessagePartCommon{Base64Data: &b64, MIMEType: "image/png"}},
	}
	mk := func() []*schema.Message {
		return []*schema.Message{
			{Role: schema.User, UserInputMultiContent: []schema.MessageInputPart{
				imagePart, {Type: schema.ChatMessagePartTypeText, Text: "看图"},
			}},
			schema.AssistantMessage("好的", nil),
			schema.UserMessage("第二轮"),
		}
	}
	trimmed, err := o.trimContext(context.Background(), "sess-1", mk())
	if err != nil {
		t.Fatal(err)
	}
	if len(trimmed) != 1 || trimmed[0].Role != schema.User || trimmed[0].Content != "第二轮" {
		t.Fatalf("image bytes must push turn 1 out of window, got %+v", trimmed)
	}

	// 对照：相同结构的纯文本消息远低于预算，不裁剪
	textOnly := []*schema.Message{
		schema.UserMessage("看图"),
		schema.AssistantMessage("好的", nil),
		schema.UserMessage("第二轮"),
	}
	trimmed, err = o.trimContext(context.Background(), "sess-1", textOnly)
	if err != nil {
		t.Fatal(err)
	}
	if len(trimmed) != 3 {
		t.Fatalf("text-only history must not be trimmed, got %d messages", len(trimmed))
	}
}

// 大图回归：几 MB 的照片 base64 体积巨大，但视觉模型按像素折算 token，
// 不得把 base64 字符数计入窗口估算（否则普通照片即报"上下文超出模型窗口"）。
func TestTrimContextLargeImageNotBlownUp(t *testing.T) {
	repo := &fakeRepo{sess: &store.Session{ModelID: "model-1"}, es: &fakeEventStore{}}
	o := &orchestrator{deps: Deps{
		Sessions:    repo,
		ContextSize: func(_ context.Context, _ string) int { return 128000 },
	}}
	b64 := strings.Repeat("x", 7_000_000) // 约 5MB 照片的 base64
	msgs := []*schema.Message{
		{Role: schema.User, UserInputMultiContent: []schema.MessageInputPart{
			{Type: schema.ChatMessagePartTypeImageURL,
				Image: &schema.MessageInputImage{MessagePartCommon: schema.MessagePartCommon{Base64Data: &b64, MIMEType: "image/png"}}},
			{Type: schema.ChatMessagePartTypeText, Text: "看图"},
		}},
		schema.AssistantMessage("好的", nil),
		schema.UserMessage("第二轮"),
	}
	trimmed, err := o.trimContext(context.Background(), "sess-1", msgs)
	if err != nil {
		t.Fatal(err)
	}
	if len(trimmed) != 3 {
		t.Fatalf("large photo must count as vision tokens, not base64 bytes; got %d messages", len(trimmed))
	}
}
