package ws

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"

	"github.com/ravenmk2/wispjar/internal/auth"
	"github.com/ravenmk2/wispjar/internal/runtime"
	"github.com/ravenmk2/wispjar/internal/session"
)

var errTest = errors.New("boom")

var testSecret = []byte("test-secret")

type fakeStore struct {
	activeUsers map[string]bool
	members     map[string]bool
	owned       map[string]bool
	projections []runtime.ControlSession
	computers   []any
	err         error
}

func keyOf(parts ...string) string { return strings.Join(parts, "/") }

func (f *fakeStore) ActiveUser(_ context.Context, userID string) (bool, error) {
	return f.activeUsers[userID], f.err
}

func (f *fakeStore) IsMember(_ context.Context, orgID, userID string) (bool, error) {
	return f.members[keyOf(orgID, userID)], f.err
}

func (f *fakeStore) OwnsSession(_ context.Context, orgID, userID, sessionID string) (bool, error) {
	return f.owned[keyOf(orgID, userID, sessionID)], f.err
}

func (f *fakeStore) SessionProjections(_ context.Context, orgID, userID string) ([]runtime.ControlSession, error) {
	return f.projections, f.err
}

func (f *fakeStore) ComputerProjections(_ context.Context, orgID, userID string) ([]any, error) {
	return f.computers, f.err
}

type decideCall struct {
	sessionID string
	callID    string
	allow     bool
	by        string
}

type fakeOrchestrator struct {
	snapshot  *runtime.Snapshot
	frames    chan runtime.FollowFrame
	followErr error
	decideErr error

	mu          sync.Mutex
	afterSeqs   []int64
	decideCalls []decideCall
	unsubCount  int
}

func (f *fakeOrchestrator) StartTurn(context.Context, string) error       { return nil }
func (f *fakeOrchestrator) Cancel(string) error                           { return nil }
func (f *fakeOrchestrator) RecoverPendingApprovals(context.Context) error { return nil }

func (f *fakeOrchestrator) AnswerQuestion(string, string, []session.QuestionAnswer, string) (bool, error) {
	return false, nil
}

func (f *fakeOrchestrator) DecideApproval(sessionID, callID string, allow bool, by string) error {
	f.mu.Lock()
	f.decideCalls = append(f.decideCalls, decideCall{sessionID, callID, allow, by})
	f.mu.Unlock()
	return f.decideErr
}

func (f *fakeOrchestrator) Follow(_ string, afterSeq int64) (*runtime.Snapshot, <-chan runtime.FollowFrame, func(), error) {
	f.mu.Lock()
	f.afterSeqs = append(f.afterSeqs, afterSeq)
	f.mu.Unlock()
	if f.followErr != nil {
		return nil, nil, nil, f.followErr
	}
	return f.snapshot, f.frames, func() {
		f.mu.Lock()
		f.unsubCount++
		f.mu.Unlock()
	}, nil
}

func (f *fakeOrchestrator) decideCallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.decideCalls)
}

func (f *fakeOrchestrator) lastDecide() decideCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.decideCalls[len(f.decideCalls)-1]
}

func (f *fakeOrchestrator) unsubs() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.unsubCount
}

func (f *fakeOrchestrator) lastAfterSeq() int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.afterSeqs[len(f.afterSeqs)-1]
}

type testEnv struct {
	server *Server
	orch   *fakeOrchestrator
	store  *fakeStore
	bus    *runtime.Bus
	http   *httptest.Server
	wsURL  string
}

func newTestEnv(t *testing.T, mutate func(*Deps)) *testEnv {
	t.Helper()
	orch := &fakeOrchestrator{
		snapshot: &runtime.Snapshot{Header: runtime.SnapshotHeader{SessionID: "sess1", LastSeq: 2}},
		frames:   make(chan runtime.FollowFrame, 16),
	}
	st := &fakeStore{
		activeUsers: map[string]bool{"u1": true},
		members:     map[string]bool{keyOf("o1", "u1"): true},
		owned:       map[string]bool{keyOf("o1", "u1", "sess1"): true},
	}
	bus := runtime.NewBus()
	deps := Deps{JWTSecret: testSecret, Store: st, Orchestrator: orch, Bus: bus}
	if mutate != nil {
		mutate(&deps)
	}
	srv := NewServer(deps)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/api/ws", srv.Handle)
	ts := httptest.NewServer(r)
	t.Cleanup(ts.Close)
	return &testEnv{
		server: srv, orch: orch, store: st, bus: bus, http: ts,
		wsURL: "ws" + strings.TrimPrefix(ts.URL, "http") + "/api/ws",
	}
}

func (e *testEnv) token(t *testing.T, uid string) string {
	t.Helper()
	tok, err := auth.SignToken(testSecret, uid, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

func (e *testEnv) dial(t *testing.T, uid string, header http.Header) *websocket.Conn {
	t.Helper()
	url := e.wsURL + "?token=" + e.token(t, uid)
	c, resp, err := websocket.DefaultDialer.Dial(url, header)
	if err != nil {
		status := 0
		if resp != nil {
			status = resp.StatusCode
		}
		t.Fatalf("dial: %v (status %d)", err, status)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

type testFrame struct {
	Type     string          `json:"type"`
	StreamID string          `json:"streamId"`
	Value    json.RawMessage `json:"value"`
	Error    *frameError     `json:"error"`
}

func readFrame(t *testing.T, c *websocket.Conn) testFrame {
	t.Helper()
	if err := c.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	_, data, err := c.ReadMessage()
	if err != nil {
		t.Fatalf("read frame: %v", err)
	}
	var f testFrame
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatalf("decode frame %s: %v", data, err)
	}
	return f
}

func writeClient(t *testing.T, c *websocket.Conn, msg string) {
	t.Helper()
	if err := c.WriteMessage(websocket.TextMessage, []byte(msg)); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for %s", what)
}
