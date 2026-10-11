package tools

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
	"strings"
	"sync"
	"testing"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"github.com/ravenmk2/wispjar/internal/attachment"
	"github.com/ravenmk2/wispjar/internal/session"
)

// memSaver 内存版 ImageSaver：按 sha256 去重（模拟内容寻址存储语义）。
type memSaver struct {
	mu    sync.Mutex
	calls int
	bySum map[string]*session.AttachmentRef
}

func newMemSaver() *memSaver { return &memSaver{bySum: map[string]*session.AttachmentRef{}} }

func (s *memSaver) Upload(_ context.Context, orgID, actorID, name string, data []byte) (*session.AttachmentRef, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	sum := sha256.Sum256(data)
	hexSum := hex.EncodeToString(sum[:])
	if ref, ok := s.bySum[hexSum]; ok {
		return ref, nil
	}
	mime, w, h, err := attachment.ValidateImage(data)
	if err != nil {
		return nil, err
	}
	ref := &session.AttachmentRef{
		ID: "sha256:" + hexSum, MIME: mime, Bytes: int64(len(data)),
		Width: w, Height: h, Name: name,
	}
	s.bySum[hexSum] = ref
	return ref, nil
}

func (s *memSaver) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func encodeTestPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// pngHeaderOnly 只含签名 + IHDR 的合法 PNG 头（CRC 有效），用于构造超大尺寸声明。
func pngHeaderOnly(w, h uint32) []byte {
	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:4], w)
	binary.BigEndian.PutUint32(ihdr[4:8], h)
	ihdr[8] = 8
	ihdr[9] = 2
	out := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}
	chunk := append([]byte("IHDR"), ihdr...)
	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], 13)
	out = append(out, lenBuf[:]...)
	out = append(out, chunk...)
	var crcBuf [4]byte
	binary.BigEndian.PutUint32(crcBuf[:], crc32.ChecksumIEEE(chunk))
	return append(out, crcBuf[:]...)
}

func invokeEnhanced(t *testing.T, tt tool.BaseTool, ctx context.Context, args string) *schema.ToolResult {
	t.Helper()
	et, ok := tt.(tool.EnhancedInvokableTool)
	if !ok {
		t.Fatal("read_media must be an EnhancedInvokableTool")
	}
	res, err := et.InvokableRun(ctx, &schema.ToolArgument{Text: args})
	if err != nil {
		t.Fatalf("InvokableRun: %v", err)
	}
	if res == nil || len(res.Parts) == 0 {
		t.Fatal("result must carry at least one part")
	}
	return res
}

func readMediaTestCtx(h *memHandle, saver *memSaver) context.Context {
	tc := NewContext("sess-1", "org-1")
	tc.Sandbox = memProvider{h: h}
	if saver != nil {
		tc.Images = saver
	}
	return WithContext(context.Background(), tc)
}

func singleText(t *testing.T, res *schema.ToolResult) string {
	t.Helper()
	if len(res.Parts) != 1 || res.Parts[0].Type != schema.ToolPartTypeText {
		t.Fatalf("want single text part, got %+v", res.Parts)
	}
	return res.Parts[0].Text
}

func TestReadMediaVisionGate(t *testing.T) {
	h := newMemHandle()
	saver := newMemSaver()
	// 沙箱内有合法图片，但模型非 vision：必须在任何 I/O 前拒绝
	h.files["/workspace/a.png"] = encodeTestPNG(t, 4, 2)
	ctx := readMediaTestCtx(h, saver)

	rm, err := newReadMediaTool(false)
	if err != nil {
		t.Fatal(err)
	}
	text := singleText(t, invokeEnhanced(t, rm, ctx, `{"path":"/workspace/a.png"}`))
	if !strings.Contains(text, "does not support image input") {
		t.Fatalf("vision gate text: %q", text)
	}
	if saver.callCount() != 0 {
		t.Fatal("vision gate must reject before attachment work")
	}
	tc := FromContext(ctx)
	if m := tc.TakeMeta(""); m != nil && len(m.Attachments) != 0 {
		t.Fatalf("vision gate must not record attachments: %+v", m.Attachments)
	}
}

func TestReadMediaNonImage(t *testing.T) {
	h := newMemHandle()
	saver := newMemSaver()
	h.files["/workspace/a.txt"] = []byte("hello, not an image")
	ctx := readMediaTestCtx(h, saver)

	rm, err := newReadMediaTool(true)
	if err != nil {
		t.Fatal(err)
	}
	text := singleText(t, invokeEnhanced(t, rm, ctx, `{"path":"/workspace/a.txt"}`))
	if !strings.Contains(text, "is not a supported image") {
		t.Fatalf("non-image text: %q", text)
	}
	if saver.callCount() != 0 {
		t.Fatal("non-image must not be persisted")
	}
}

func TestReadMediaTooLarge(t *testing.T) {
	h := newMemHandle()
	saver := newMemSaver()
	h.files["/workspace/big.png"] = make([]byte, attachment.MaxImageBytes+1)
	ctx := readMediaTestCtx(h, saver)

	rm, err := newReadMediaTool(true)
	if err != nil {
		t.Fatal(err)
	}
	text := singleText(t, invokeEnhanced(t, rm, ctx, `{"path":"/workspace/big.png"}`))
	if !strings.Contains(text, "exceeding the 8MB limit") || !strings.Contains(text, "downscale it with exec") {
		t.Fatalf("oversize text must carry limit + downscale advice: %q", text)
	}
	if saver.callCount() != 0 {
		t.Fatal("oversize image must not be persisted")
	}
}

func TestReadMediaTooManyPixels(t *testing.T) {
	h := newMemHandle()
	saver := newMemSaver()
	h.files["/workspace/huge.png"] = pngHeaderOnly(8193, 8193) // >64M 像素（仅头声明）
	ctx := readMediaTestCtx(h, saver)

	rm, err := newReadMediaTool(true)
	if err != nil {
		t.Fatal(err)
	}
	text := singleText(t, invokeEnhanced(t, rm, ctx, `{"path":"/workspace/huge.png"}`))
	if !strings.Contains(text, "exceeds the 64M pixel limit") || !strings.Contains(text, "downscale it with exec") {
		t.Fatalf("pixel-limit text must carry limit + downscale advice: %q", text)
	}
	if saver.callCount() != 0 {
		t.Fatal("oversize image must not be persisted")
	}
}

func TestReadMediaNoImageSaver(t *testing.T) {
	h := newMemHandle()
	h.files["/workspace/a.png"] = encodeTestPNG(t, 4, 2)
	ctx := readMediaTestCtx(h, nil)

	rm, err := newReadMediaTool(true)
	if err != nil {
		t.Fatal(err)
	}
	text := singleText(t, invokeEnhanced(t, rm, ctx, `{"path":"/workspace/a.png"}`))
	if !strings.Contains(text, "attachment service is not configured") {
		t.Fatalf("missing-saver text: %q", text)
	}
}

func TestReadMediaSuccess(t *testing.T) {
	h := newMemHandle()
	saver := newMemSaver()
	pngBytes := encodeTestPNG(t, 4, 2)
	h.files["/workspace/a.png"] = pngBytes
	ctx := readMediaTestCtx(h, saver)

	rm, err := newReadMediaTool(true)
	if err != nil {
		t.Fatal(err)
	}
	res := invokeEnhanced(t, rm, ctx, `{"path":"/workspace/a.png"}`)
	if len(res.Parts) != 2 {
		t.Fatalf("want text+image parts, got %+v", res.Parts)
	}
	if res.Parts[0].Type != schema.ToolPartTypeText || res.Parts[1].Type != schema.ToolPartTypeImage {
		t.Fatalf("parts order must be [text, image]: %+v", res.Parts)
	}
	var handle struct {
		Path         string `json:"path"`
		MIME         string `json:"mime"`
		Width        int    `json:"width"`
		Height       int    `json:"height"`
		Bytes        int    `json:"bytes"`
		Sha256       string `json:"sha256"`
		AttachmentID string `json:"attachment_id"`
	}
	if err := json.Unmarshal([]byte(res.Parts[0].Text), &handle); err != nil {
		t.Fatalf("handle text must be JSON: %v", err)
	}
	sum := sha256.Sum256(pngBytes)
	hexSum := hex.EncodeToString(sum[:])
	if handle.Path != "/workspace/a.png" || handle.MIME != "image/png" ||
		handle.Width != 4 || handle.Height != 2 || handle.Bytes != len(pngBytes) {
		t.Fatalf("handle mismatch: %+v", handle)
	}
	if handle.Sha256 != hexSum[:8] || handle.AttachmentID != "sha256:"+hexSum {
		t.Fatalf("handle identity mismatch: %+v", handle)
	}
	img := res.Parts[1].Image
	if img == nil || img.Base64Data == nil || img.MIMEType != "image/png" {
		t.Fatalf("image part must carry Base64Data + MIME: %+v", img)
	}
	decoded, err := base64.StdEncoding.DecodeString(*img.Base64Data)
	if err != nil || !bytes.Equal(decoded, pngBytes) {
		t.Fatalf("image part bytes mismatch (err=%v)", err)
	}

	// meta 记录附件引用（tool/result 事件与回放用）
	tc := FromContext(ctx)
	m := tc.TakeMeta("")
	if m == nil || len(m.Attachments) != 1 || m.Attachments[0].ID != "sha256:"+hexSum {
		t.Fatalf("meta attachments mismatch: %+v", m)
	}
	if m.Render != RenderRead {
		t.Fatalf("render = %q, want read", m.Render)
	}

	// 同字节重读命中同一引用（内容寻址去重，跨轮字节一致）
	ctx2 := readMediaTestCtx(h, saver)
	res2 := invokeEnhanced(t, rm, ctx2, `{"path":"/workspace/a.png"}`)
	var handle2 struct {
		AttachmentID string `json:"attachment_id"`
	}
	if err := json.Unmarshal([]byte(res2.Parts[0].Text), &handle2); err != nil {
		t.Fatal(err)
	}
	if handle2.AttachmentID != handle.AttachmentID {
		t.Fatalf("re-read must hit the same ref: %q vs %q", handle2.AttachmentID, handle.AttachmentID)
	}
	if *res2.Parts[1].Image.Base64Data != *res.Parts[1].Image.Base64Data {
		t.Fatal("re-read must produce byte-identical image part")
	}
}
