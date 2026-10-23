package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/ravenmk2/wispjar/internal/agent"
	"github.com/ravenmk2/wispjar/internal/attachment"
	"github.com/ravenmk2/wispjar/internal/session"
	"github.com/ravenmk2/wispjar/internal/store"
)

type fakeSessionService struct {
	detail *session.Detail
	err    error
}

func (f fakeSessionService) Get(_ context.Context, _, _ string) (*session.Detail, error) {
	return f.detail, f.err
}

func (f fakeSessionService) Delete(_ context.Context, _, _ string) error { return nil }

type fakeResolver struct {
	refs []session.AttachmentRef
	err  error

	gotOrg string
	gotIDs []string
}

func (f *fakeResolver) Resolve(_ context.Context, orgID string, ids []string) ([]session.AttachmentRef, error) {
	f.gotOrg, f.gotIDs = orgID, ids
	return f.refs, f.err
}

type fakePromptOrchestrator struct{ Orchestrator }

func (fakePromptOrchestrator) StartTurn(context.Context, string) error { return nil }

type promptTestEnv struct {
	handler  *Handler
	es       *fakeEventStore
	resolver *fakeResolver
	vision   bool
}

func newPromptTestEnv(t *testing.T) *promptTestEnv {
	t.Helper()
	snapRaw, err := json.Marshal(agent.Snapshot{AgentID: "agent-1", ToolsEnabled: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	env := &promptTestEnv{es: &fakeEventStore{}, resolver: &fakeResolver{}, vision: true}
	sessions := fakeSessionService{detail: &session.Detail{Session: store.Session{
		ID: "sess-1", OrgID: "org-1", UserID: "user-1",
		AgentSnapshot: snapRaw, ModelID: "model-1", Status: session.StatusActive,
	}}}
	repo := &fakeRepo{sess: &sessions.detail.Session, es: env.es}
	env.handler = NewHandler(fakePromptOrchestrator{}, sessions, env.es, repo, NewBus(),
		func(context.Context, string) (string, string, bool, error) {
			return "fake-v1", "prov-1", env.vision, nil
		}, "", env.resolver)
	return env
}

func (e *promptTestEnv) post(t *testing.T, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set("wispjar.uid", "user-1"); c.Next() })
	r.POST("/sessions/:id/prompts", e.handler.Prompt)
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/sessions/sess-1/prompts", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func errBody(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body: %v (%s)", err, rec.Body.String())
	}
	return body.Error.Code
}

func validIDs(n int) []string {
	ids := make([]string, 0, n)
	for i := 0; i < n; i++ {
		ids = append(ids, "sha256:"+strings.Repeat(string(rune('a'+i)), 64))
	}
	return ids
}

func TestPromptAttachmentsAccepted(t *testing.T) {
	env := newPromptTestEnv(t)
	refs := []session.AttachmentRef{
		{ID: validIDs(1)[0], MIME: "image/png", Bytes: 100, Width: 4, Height: 4, Name: "a.png"},
	}
	env.resolver.refs = refs
	rec := env.post(t, map[string]any{
		"content": "看这张图", "request_id": "r1", "attachments": validIDs(1),
	})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("want 202, got %d: %s", rec.Code, rec.Body.String())
	}
	if env.resolver.gotOrg != "org-1" {
		t.Fatalf("resolver must be scoped to session org, got %q", env.resolver.gotOrg)
	}
	// user/message 携带附件引用；request/header 记录卸载策略版本
	var um session.UserMessageData
	if err := json.Unmarshal(env.es.byType(session.EventUserMessage)[0].Data, &um); err != nil {
		t.Fatal(err)
	}
	if len(um.Attachments) != 1 || um.Attachments[0].Name != "a.png" {
		t.Fatalf("user/message attachments mismatch: %+v", um.Attachments)
	}
	var hdr session.RequestHeaderData
	if err := json.Unmarshal(env.es.byType(session.EventRequestHeader)[0].Data, &hdr); err != nil {
		t.Fatal(err)
	}
	if hdr.ImageOffload != imageOffloadVersion {
		t.Fatalf("request/header must record offload version, got %q", hdr.ImageOffload)
	}
}

func TestPromptWithoutAttachments(t *testing.T) {
	env := newPromptTestEnv(t)
	rec := env.post(t, map[string]any{"content": "你好", "request_id": "r1"})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("want 202, got %d: %s", rec.Code, rec.Body.String())
	}
	var raw map[string]any
	if err := json.Unmarshal(env.es.byType(session.EventUserMessage)[0].Data, &raw); err != nil {
		t.Fatal(err)
	}
	if _, ok := raw["attachments"]; ok {
		t.Fatal("text-only prompt must not emit attachments field")
	}
}

func TestPromptAttachmentsResolveFailure(t *testing.T) {
	env := newPromptTestEnv(t)
	env.resolver.err = &attachment.Error{
		Status: http.StatusBadRequest, Code: "request/invalid", Message: "附件不存在或无权访问",
	}
	rec := env.post(t, map[string]any{
		"content": "看图", "request_id": "r1", "attachments": validIDs(1),
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d: %s", rec.Code, rec.Body.String())
	}
	if code := errBody(t, rec); code != "request/invalid" {
		t.Fatalf("want request/invalid, got %q", code)
	}
	if len(env.es.byType(session.EventUserMessage)) != 0 {
		t.Fatal("rejected prompt must not produce events")
	}
}

func TestPromptVisionGate(t *testing.T) {
	env := newPromptTestEnv(t)
	env.vision = false
	env.resolver.refs = []session.AttachmentRef{
		{ID: validIDs(1)[0], MIME: "image/png", Bytes: 100, Width: 4, Height: 4, Name: "a.png"},
	}
	rec := env.post(t, map[string]any{
		"content": "看图", "request_id": "r1", "attachments": validIDs(1),
	})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("want 422, got %d: %s", rec.Code, rec.Body.String())
	}
	if code := errBody(t, rec); code != "model/not_vision" {
		t.Fatalf("want model/not_vision, got %q", code)
	}
	if len(env.es.byType(session.EventUserMessage)) != 0 {
		t.Fatal("vision-gated prompt must not produce events")
	}
}
