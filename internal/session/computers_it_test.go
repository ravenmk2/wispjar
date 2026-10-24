package session

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/ravenmk2/wispjar/internal/id"
	"github.com/ravenmk2/wispjar/internal/store"
	"gorm.io/gorm"
)

func TestComputerSessionGuardsIntegration(t *testing.T) {
	dsn := os.Getenv("WISPJAR_VOLUME_TEST_DSN")
	if dsn == "" {
		t.Skip("requires isolated migrated MySQL")
	}
	db, err := store.Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	conn, _ := db.DB()
	t.Cleanup(func() { _ = conn.Close() })
	org, user, provider, model, agentID, computerID, sid := id.New(), id.New(), id.New(), id.New(), id.New(), id.New(), id.New()
	fixtures := []any{
		&store.Organization{ID: org, Name: "session-guards", Status: "active"},
		&store.User{ID: user, Username: user, PasswordHash: "unused", Status: "active"},
		&store.ModelProvider{ID: provider, Name: "session-guards", APIKeyEnc: []byte{}, CreatedBy: user, Status: "active"},
		&store.Model{ID: model, ProviderID: provider, ModelCode: "test", Capabilities: json.RawMessage(`{}`), Status: "active"},
		&store.Agent{ID: agentID, OrgID: org, Name: "session-guards", ModelID: model, ToolsEnabled: json.RawMessage(`[]`), CreatedBy: user, Status: "published"},
		&store.Computer{ID: computerID, OrgID: org, UserID: user, Name: "session-guards", Status: "deleting", Generation: 1},
		&store.Session{ID: sid, OrgID: org, UserID: user, AgentID: agentID, ModelID: model, ComputerID: &computerID, AgentSnapshot: json.RawMessage(`{}`), ModelParams: json.RawMessage(`{}`), UsageTotal: json.RawMessage(`{}`), Workspace: json.RawMessage(`{"state":"ready"}`), Status: StatusActive, ApprovalMode: ApprovalAuto, LastActiveAt: time.Now()},
	}
	t.Cleanup(func() {
		_ = db.Where("session_id = ?", sid).Delete(&store.SessionEvent{}).Error
		for i := len(fixtures) - 1; i >= 0; i-- {
			_ = db.Delete(fixtures[i]).Error
		}
	})
	for _, fixture := range fixtures {
		if err := db.Create(fixture).Error; err != nil {
			t.Fatal(err)
		}
	}
	assertCode := func(err error, code string) {
		t.Helper()
		var e *Error
		if !errors.As(err, &e) || e.Code != code {
			t.Fatalf("expected %s, got %v", code, err)
		}
	}
	sess := fixtures[len(fixtures)-1].(*store.Session)
	assertCode(db.Transaction(func(tx *gorm.DB) error { return lockSessionComputer(tx, sess) }), "computer/not_found")
	_, err = NewService(db, nil).Update(context.Background(), user, sid, UpdateInput{ComputerID: &computerID})
	assertCode(err, "computer/not_found")
	events := NewStore(db)
	for _, status := range []string{"deleting", "recovering"} {
		if err := db.Model(&store.Computer{}).Where("id = ?", computerID).Update("status", status).Error; err != nil {
			t.Fatal(err)
		}
		_, err = events.Append(context.Background(), sid, EventTurnStart, TurnStartData{Turn: 1})
		assertCode(err, "computer/busy")
	}
	if err := db.Model(&store.Computer{}).Where("id = ?", computerID).Update("status", "stopped").Error; err != nil {
		t.Fatal(err)
	}
	_, err = events.Append(context.Background(), sid, EventTurnStart, TurnStartData{Turn: 1})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = events.Append(context.Background(), sid, EventTurnEnd, TurnEndData{Turn: 1})
	_, err = events.Append(context.Background(), sid, EventTurnStart, TurnStartData{Turn: 2})
	if err != nil {
		t.Fatal(err)
	}
	oldTurn := 1
	_, err = events.AppendBatch(context.Background(), sid, []EventInput{{ExpectedTurn: &oldTurn, Type: EventTurnEnd, Data: TurnEndData{Turn: 1}}})
	assertCode(err, "session/turn_changed")
	if err := db.First(sess, "id = ?", sid).Error; err != nil {
		t.Fatal(err)
	}
	if !sess.Running {
		t.Fatal("late old turn/end cleared the current running flag")
	}
	// Recovery's intent is committed before its backend Delete. A model may
	// nevertheless consume the disconnected tool result and say "completed"
	// before the post-delete runtime recovery hook has retired its memory.
	if err := db.Model(&store.Computer{}).Where("id = ?", computerID).Update("status", "recovering").Error; err != nil {
		t.Fatal(err)
	}
	currentTurn := 2
	ended, err := events.AppendBatch(context.Background(), sid, []EventInput{{ExpectedTurn: &currentTurn, Type: EventTurnEnd, Data: TurnEndData{Turn: 2, Reason: "completed"}}})
	if err != nil {
		t.Fatal(err)
	}
	var end TurnEndData
	if err := json.Unmarshal(ended[0].Data, &end); err != nil {
		t.Fatal(err)
	}
	if end.Reason != "failed" || end.ErrorCode != "computer/recovered" || end.Turn != 2 {
		t.Fatalf("recovery disconnect reported success: %+v", end)
	}
	var persisted store.SessionEvent
	if err := db.First(&persisted, "session_id = ? AND seq = ?", sid, ended[0].Seq).Error; err != nil {
		t.Fatal(err)
	}
	if string(persisted.Data) != string(ended[0].Data) {
		// MySQL JSON can normalize whitespace; compare parsed payloads.
		var persistedEnd TurnEndData
		if err := json.Unmarshal(persisted.Data, &persistedEnd); err != nil || persistedEnd != end {
			t.Fatalf("persisted recovery result differs: %+v / %v", persistedEnd, err)
		}
	}
}
