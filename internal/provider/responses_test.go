package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cloudwego/eino/schema"
	"github.com/ravenmk2/wispjar/internal/attachment"
	"github.com/ravenmk2/wispjar/internal/session"
	"github.com/ravenmk2/wispjar/internal/store"
)

type testMediaReader struct {
	ref   session.AttachmentRef
	data  []byte
	reads atomic.Int32
}

func (r *testMediaReader) Resolve(_ context.Context, org string, ids []string) ([]session.AttachmentRef, error) {
	if org != "tenant-a" && org != "tenant-b" {
		return nil, fmt.Errorf("tenant denied")
	}
	if len(ids) != 1 || ids[0] != r.ref.ID {
		return nil, fmt.Errorf("attachment missing")
	}
	return []session.AttachmentRef{r.ref}, nil
}
func (r *testMediaReader) ReadForOrg(ctx context.Context, org, id string) ([]byte, string, error) {
	if _, err := r.Resolve(ctx, org, []string{id}); err != nil {
		return nil, "", err
	}
	r.reads.Add(1)
	return r.data, r.ref.MIME, nil
}

func testMedia(t *testing.T) *testMediaReader {
	t.Helper()
	picture := image.NewRGBA(image.Rect(0, 0, 64, 64))
	for row := 0; row < 64; row++ {
		for column := 0; column < 64; column++ {
			picture.Set(column, row, color.RGBA{R: 255, A: 255})
		}
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, picture); err != nil {
		t.Fatal(err)
	}
	blobs := attachment.NewBlobStore(t.TempDir())
	hash, err := blobs.Put(encoded.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	return &testMediaReader{ref: session.AttachmentRef{ID: "sha256:" + hash, MIME: "image/png", Bytes: int64(encoded.Len()), Width: 64, Height: 64, Name: "sample.png"}, data: encoded.Bytes()}
}

func testResponses(t *testing.T, handler http.HandlerFunc) (*responsesModel, *testMediaReader) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if _, exists := request.Header[http.CanonicalHeaderKey("X-Wispjar-Media-Policy")]; exists {
			t.Error("local media policy must not be sent to providers")
		}
		handler(writer, request)
	}))
	t.Cleanup(server.Close)
	reader := testMedia(t)
	return &responsesModel{baseURL: server.URL, apiKey: "test-key", providerID: "provider-a", providerType: "deepseek", modelCode: "test-model", caps: Capabilities{Vision: true, Tools: true, ToolImages: true}, media: &mediaStore{reader: reader}, client: mediaHTTPClient()}, reader
}

func toolImage(ref session.AttachmentRef) []*schema.Message {
	return []*schema.Message{schema.UserMessage("Describe the image returned by read_media."), schema.AssistantMessage("", []schema.ToolCall{{ID: "call_1", Type: "function", Function: schema.FunctionCall{Name: "read_media", Arguments: `{"path":"sample.png"}`}}}), {Role: schema.Tool, ToolCallID: "call_1", UserInputMultiContent: []schema.MessageInputPart{{Type: schema.ChatMessagePartTypeText, Text: "stored image"}, {Type: schema.ChatMessagePartTypeImageURL, Image: &schema.MessageInputImage{MessagePartCommon: attachment.ImageReference(ref)}}}}}
}

const completedResponse = `{"status":"completed","output":[{"type":"message","id":"msg_1","role":"assistant","content":[{"type":"output_text","text":"red"}]}],"usage":{"input_tokens":10,"output_tokens":2,"total_tokens":12,"input_tokens_details":{"cached_tokens":3}}}`

func TestResponsesFileRecoveryAndReplay(t *testing.T) {
	var uploads, requests atomic.Int32
	var payloads []map[string]any
	model, reader := testResponses(t, func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		if request.URL.Path == "/files" {
			if err := request.ParseMultipartForm(10 << 20); err != nil {
				t.Error(err)
				return
			}
			defer func() {
				if err := request.MultipartForm.RemoveAll(); err != nil {
					t.Error(err)
				}
			}()
			if request.FormValue("purpose") != "user_data" || request.FormValue("expires_after[seconds]") != "86400" {
				t.Error("missing upload policy")
			}
			_, _ = fmt.Fprintf(writer, `{"id":"file-%d","expires_at":%d}`, uploads.Add(1), time.Now().Add(time.Hour).Unix())
			return
		}
		var payload map[string]any
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		payloads = append(payloads, payload)
		if requests.Add(1) == 1 {
			writer.WriteHeader(400)
			_, _ = fmt.Fprint(writer, `{"error":{"code":"file_expired","message":"file-1 expired"}}`)
			return
		}
		_, _ = fmt.Fprint(writer, completedResponse)
	})
	ctx := WithMediaOrg(context.Background(), "tenant-a")
	sink := NewUsageSink()
	ctx = WithUsageSink(ctx, sink)
	message, err := model.Generate(ctx, toolImage(reader.ref))
	if err != nil {
		t.Fatal(err)
	}
	if message.Content != "red" || message.ResponseMeta.Usage.TotalTokens != 12 {
		t.Fatalf("message=%+v", message)
	}
	if usage := sink.TakeLatest(); usage == nil || usage.CacheRead != 3 {
		t.Fatalf("usage=%+v", usage)
	}
	if uploads.Load() != 2 || requests.Load() != 2 {
		t.Fatalf("upload=%d requests=%d", uploads.Load(), requests.Load())
	}
	output := payloads[1]["input"].([]any)[2].(map[string]any)
	if output["type"] != "function_call_output" || output["call_id"] != "call_1" {
		t.Fatalf("output=%v", output)
	}
	imagePart := output["output"].([]any)[1].(map[string]any)
	if imagePart["file_id"] != "file-2" || imagePart["image_url"] != nil {
		t.Fatalf("image=%v", imagePart)
	}
	encoded, _ := json.Marshal(payloads)
	if bytes.Contains(encoded, []byte("base64")) || bytes.Contains(encoded, []byte("wispjar-attachment")) {
		t.Fatal("local reference or image bytes leaked")
	}
	if _, err = model.Generate(ctx, toolImage(reader.ref)); err != nil {
		t.Fatal(err)
	}
	if uploads.Load() != 2 {
		t.Fatal("cached file not reused")
	}
	input, _, err := model.input(ctx, []*schema.Message{message, schema.UserMessage("again")})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(input)
	if !bytes.Contains(raw, []byte("msg_1")) {
		t.Fatal("native replay lost")
	}
	copy := *model
	copy.apiKey = "rotated"
	input, _, err = copy.input(ctx, []*schema.Message{message})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(input)
	if bytes.Contains(raw, []byte("msg_1")) {
		t.Fatal("native replay crossed credential scope")
	}
}

func TestResponsesUploadScopeAndConcurrency(t *testing.T) {
	var uploads atomic.Int32
	model, reader := testResponses(t, func(writer http.ResponseWriter, request *http.Request) {
		_, _ = fmt.Fprintf(writer, `{"id":"file-%d","expires_at":%d}`, uploads.Add(1), time.Now().Add(time.Hour).Unix())
	})
	ctx := WithMediaOrg(context.Background(), "tenant-a")
	var group sync.WaitGroup
	for index := 0; index < 12; index++ {
		group.Add(1)
		go func() {
			defer group.Done()
			if _, err := model.fileID(ctx, reader.ref.ID); err != nil {
				t.Error(err)
			}
		}()
	}
	group.Wait()
	if uploads.Load() != 1 {
		t.Fatalf("uploads=%d", uploads.Load())
	}
	if _, err := model.fileID(WithMediaOrg(ctx, "tenant-b"), reader.ref.ID); err != nil {
		t.Fatal(err)
	}
	copy := *model
	copy.apiKey = "other-key"
	if _, err := copy.fileID(ctx, reader.ref.ID); err != nil {
		t.Fatal(err)
	}
	if uploads.Load() != 3 {
		t.Fatal("files crossed tenant or credential scope")
	}
	model.media.memory.Store(model.scope(ctx)+reader.ref.ID, store.ProviderMediaFile{FileID: "expired", ExpiresAt: 1})
	if _, err := model.fileID(ctx, reader.ref.ID); err != nil {
		t.Fatal(err)
	}
	if uploads.Load() != 4 {
		t.Fatal("expired file reused")
	}
}

func TestResponsesBudgetAndStrictGate(t *testing.T) {
	model, reader := testResponses(t, func(http.ResponseWriter, *http.Request) { t.Error("unexpected network request") })
	ctx := WithMediaOrg(context.Background(), "tenant-a")
	messages := toolImage(reader.ref)
	for index := 0; index < 8; index++ {
		messages = append(messages, messages[2])
	}
	projected, err := model.media.project(ctx, messages, true)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, message := range projected {
		for _, part := range message.UserInputMultiContent {
			if part.Image != nil {
				count++
			}
		}
	}
	if count != 7 || messages[2].UserInputMultiContent[1].Image == nil {
		t.Fatalf("budget mutated checkpoint input or failed: %d", count)
	}
	if _, err = model.media.project(ctx, messages, false); err == nil {
		t.Fatal("text tool protocol accepted image")
	}
	if _, err = model.media.project(WithMediaOrg(ctx, "tenant-denied"), messages, true); err == nil {
		t.Fatal("unauthorized reference accepted")
	}
	if reader.reads.Load() != 0 {
		t.Fatal("projection read blobs")
	}
	reader.ref.Bytes = 8 << 20
	projected, err = model.media.project(ctx, toolImage(reader.ref), true)
	if err != nil || projected[2].UserInputMultiContent[1].Image == nil {
		t.Fatal("one valid image dropped")
	}
	messages = append(toolImage(reader.ref), toolImage(reader.ref)[2])
	projected, err = model.media.project(ctx, messages, true)
	if err != nil || projected[2].UserInputMultiContent[1].Image != nil {
		t.Fatal("byte budget not applied")
	}
}

func TestResponsesNoRetryForAuthOrRepeatedExpiry(t *testing.T) {
	for _, status := range []int{401, 429, 400} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			var uploads, requests atomic.Int32
			model, reader := testResponses(t, func(writer http.ResponseWriter, request *http.Request) {
				if request.URL.Path == "/files" {
					_, _ = fmt.Fprintf(writer, `{"id":"file-%d"}`, uploads.Add(1))
					return
				}
				requests.Add(1)
				writer.WriteHeader(status)
				_, _ = fmt.Fprint(writer, `{"error":{"code":"file_expired","message":"expired"}}`)
			})
			_, err := model.Generate(WithMediaOrg(context.Background(), "tenant-a"), toolImage(reader.ref))
			if err == nil {
				t.Fatal("expected failure")
			}
			want := int32(1)
			if status == 400 {
				want = 2
			}
			if uploads.Load() != want || requests.Load() != want {
				t.Fatalf("retry count upload=%d request=%d", uploads.Load(), requests.Load())
			}
		})
	}
}

func TestResponsesSSEAndTruncation(t *testing.T) {
	model, _ := testResponses(t, func(http.ResponseWriter, *http.Request) {})
	data := `data: {"type":"response.output_text.delta","delta":"r"}` + "\n\n" + `data: {"type":"response.completed","response":` + completedResponse + "}\n\n"
	var chunks []*schema.Message
	err := model.readStream(context.Background(), strings.NewReader(data), func(message *schema.Message) bool { chunks = append(chunks, message); return false })
	if err != nil {
		t.Fatal(err)
	}
	combined, err := schema.ConcatMessages(chunks)
	if err != nil || combined.Content != "red" || combined.Extra[ReplayKey] == nil {
		t.Fatalf("combined=%+v err=%v", combined, err)
	}
	if err = model.readStream(context.Background(), strings.NewReader("data: {\"type\":\"response.output_text.delta\",\"delta\":\"hi\"}\n\n"), func(*schema.Message) bool { return false }); err != io.ErrUnexpectedEOF {
		t.Fatalf("truncated stream=%v", err)
	}
	toolStream := `data: {"type":"response.output_item.added","output_index":1,"item":{"type":"function_call","call_id":"call_1","name":"read_media","arguments":""}}` + "\n\n" + `data: {"type":"response.function_call_arguments.delta","output_index":1,"delta":"{}"}` + "\n\n" + `data: {"type":"response.completed","response":{"status":"completed","output":[{"type":"reasoning","content":[{"type":"reasoning_text","text":"think"}]},{"type":"function_call","call_id":"call_1","name":"read_media","arguments":"{}"}]}}` + "\n\n"
	chunks = nil
	if err = model.readStream(context.Background(), strings.NewReader(toolStream), func(message *schema.Message) bool { chunks = append(chunks, message); return false }); err != nil {
		t.Fatal(err)
	}
	combined, err = schema.ConcatMessages(chunks)
	if err != nil || len(combined.ToolCalls) != 1 || combined.ToolCalls[0].ID != "call_1" || combined.ToolCalls[0].Function.Arguments != "{}" || combined.ReasoningContent != "think" {
		t.Fatalf("tool concat=%+v err=%v", combined, err)
	}
}

func TestArkProcessingAndCancel(t *testing.T) {
	var polls atomic.Int32
	model, reader := testResponses(t, func(writer http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodPost {
			_, _ = fmt.Fprint(writer, `{"id":"file-ark","status":"processing","expires_at":2000000000}`)
			return
		}
		polls.Add(1)
		_, _ = fmt.Fprint(writer, `{"id":"file-ark","status":"active","expires_at":2000000000}`)
	})
	model.providerType = "ark"
	ctx := WithMediaOrg(context.Background(), "tenant-a")
	if _, err := model.fileID(ctx, reader.ref.ID); err != nil {
		t.Fatal(err)
	}
	if polls.Load() != 1 {
		t.Fatal("did not wait for active")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	model.apiKey = "changed"
	if _, err := model.fileID(canceled, reader.ref.ID); err == nil {
		t.Fatal("upload ignored cancellation")
	}
}

func TestDeepSeekResponsesLive(t *testing.T) {
	key := os.Getenv("WISPJAR_TEST_DEEPSEEK_KEY")
	if key == "" {
		t.Skip("explicit live-provider opt-in required")
	}
	reader := testMedia(t)
	client := &responsesModel{baseURL: "https://api.deepseek.com", apiKey: key, providerID: "live-test", providerType: "deepseek", modelCode: "deepseek-flash", params: map[string]any{"reasoning_effort": "low", "max_tokens": 2048}, caps: Capabilities{Vision: true, Tools: true, ToolImages: true}, media: &mediaStore{reader: reader}, client: mediaHTTPClient()}
	ctx, cancel := context.WithTimeout(WithMediaOrg(context.Background(), "tenant-a"), 2*time.Minute)
	defer cancel()
	bound, err := client.WithTools([]*schema.ToolInfo{{Name: "read_media", Desc: "Read the image", ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{"path": {Type: schema.String, Required: true}})}})
	if err != nil {
		t.Fatal(err)
	}
	first, err := bound.Generate(ctx, []*schema.Message{schema.UserMessage("Call read_media with path sample.png. After receiving its image reply only with the main color in English.")})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.ToolCalls) != 1 {
		t.Fatalf("expected tool call, got %+v", first)
	}
	messages := toolImage(reader.ref)
	messages[1] = first
	messages[2].ToolCallID = first.ToolCalls[0].ID
	stream, err := bound.Stream(ctx, messages)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	var answer strings.Builder
	for {
		chunk, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		answer.WriteString(chunk.Content)
	}
	if !strings.Contains(strings.ToLower(answer.String()), "red") {
		t.Fatalf("unexpected image answer: %s", answer.String())
	}
	t.Logf("native tool image recognized: %s; local image reads=%d", answer.String(), reader.reads.Load())
}
