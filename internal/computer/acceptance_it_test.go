package computer

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/ravenmk2/wispjar/internal/id"
	"github.com/ravenmk2/wispjar/internal/store"
)

// Explicitly opt in with an already migrated disposable MySQL schema.
// No sandbox or volume backend is contacted by this test.
func TestRepositoryConcurrencyIntegration(t *testing.T) {
	dsn := os.Getenv("WISPJAR_VOLUME_TEST_DSN")
	if dsn == "" {
		t.Skip("set WISPJAR_VOLUME_TEST_DSN to an isolated migrated database")
	}
	db, err := store.Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	org, user := id.New(), id.New()
	providerID, modelID, agentID := id.New(), id.New(), id.New()
	t.Cleanup(func() {
		for _, row := range []struct {
			model any
			key   string
		}{
			{&store.Agent{}, agentID}, {&store.Model{}, modelID}, {&store.ModelProvider{}, providerID},
			{&store.User{}, user}, {&store.Organization{}, org},
		} {
			if err := db.Where("id=?", row.key).Delete(row.model).Error; err != nil {
				t.Error(err)
			}
		}
	})
	t.Cleanup(func() {
		for _, model := range []any{&store.ComputerRun{}, &store.ResourceOperation{}, &store.UserAgentBinding{}, &store.Session{}, &store.Computer{}} {
			if err := db.Where("org_id=? AND user_id=?", org, user).Delete(model).Error; err != nil {
				t.Error(err)
			}
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	for _, fixture := range []any{
		&store.Organization{ID: org, Name: "computer-test", Status: "active"},
		&store.User{ID: user, Username: user, PasswordHash: "unused", Status: "active"},
		&store.ModelProvider{ID: providerID, Name: "computer-test", APIKeyEnc: []byte{}, CreatedBy: user, Status: "active"},
		&store.Model{ID: modelID, ProviderID: providerID, ModelCode: "test", Capabilities: json.RawMessage(`{}`), Status: "active"},
		&store.Agent{ID: agentID, OrgID: org, Name: "computer-test", ModelID: modelID, ToolsEnabled: json.RawMessage(`[]`), CreatedBy: user, Status: "published"},
	} {
		if err := db.Create(fixture).Error; err != nil {
			t.Fatal(err)
		}
	}
	repo := NewGORMRepository(db)
	key := id.New()
	ops := make(chan store.ResourceOperation, 8)
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			c := store.Computer{ID: id.New(), OrgID: org, UserID: user, Name: "integration", BackendID: "test", Generation: 1, Status: "stopped"}
			op, err := newOperation(org, user, c.ID, "create", key, "identical")
			if err == nil {
				op, err = repo.Intent(ctx, &c, op)
			}
			if err != nil {
				t.Error(err)
				return
			}
			ops <- op
		})
	}
	wg.Wait()
	close(ops)
	var op store.ResourceOperation
	for candidate := range ops {
		if op.ID != "" && op.ID != candidate.ID {
			t.Fatal("same idempotency key created multiple operations")
		}
		op = candidate
	}
	if op.ID == "" {
		t.Fatal("no create operation")
	}
	var count int64
	if err := db.Model(&store.Computer{}).Where("org_id=? AND user_id=?", org, user).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("duplicate computers: %d / %v", count, err)
	}
	conflicting, _ := newOperation(org, user, op.ResourceID, "create", key, "different")
	if _, err := repo.Intent(ctx, nil, conflicting); !errors.Is(err, ErrConflict) {
		t.Fatalf("idempotency payload mismatch: %v", err)
	}
	sandboxID := "test-instance"
	if err := repo.Complete(ctx, op, &sandboxID, "running", nil); err != nil {
		t.Fatal(err)
	}
	if err := repo.Complete(ctx, op, nil, "failed", errors.New("late completion")); !errors.Is(err, ErrConflict) {
		t.Fatalf("late worker overwrote completed intent: %v", err)
	}
	computerID := op.ResourceID
	session := store.Session{ID: id.New(), OrgID: org, UserID: user, AgentID: agentID, ComputerID: &computerID,
		AgentSnapshot: json.RawMessage(`{}`), ModelParams: json.RawMessage(`{}`), Sandbox: json.RawMessage(`{}`),
		UsageTotal: json.RawMessage(`{}`), Status: "active", LastActiveAt: time.Now()}
	if err := db.Create(&session).Error; err != nil {
		t.Fatal(err)
	}
	run := func(sid string) store.ComputerRun {
		return store.ComputerRun{ID: id.New(), OrgID: org, UserID: user, ComputerID: computerID, SessionID: sid, AgentID: session.AgentID}
	}
	winners := make(chan store.ComputerRun, 8)
	for range 8 {
		wg.Go(func() {
			candidate := run(session.ID)
			err := repo.RegisterRun(ctx, candidate, 1)
			if err == nil {
				winners <- candidate
			} else if !errors.Is(err, ErrBusy) {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	close(winners)
	var root store.ComputerRun
	n := 0
	for candidate := range winners {
		root = candidate
		n++
	}
	if n != 1 {
		t.Fatalf("same-session roots: got %d, want 1", n)
	}
	secondSession := session
	secondSession.ID = id.New()
	if err := db.Create(&secondSession).Error; err != nil {
		t.Fatal(err)
	}
	secondRoot := run(secondSession.ID)
	if err := repo.RegisterRun(ctx, secondRoot, 1); err != nil {
		t.Fatalf("independent session blocked on same computer: %v", err)
	}
	if err := repo.FinishRun(ctx, secondRoot.ID, user, "completed"); err != nil {
		t.Fatal(err)
	}
	if err := repo.RegisterRun(ctx, run(""), 0); !errors.Is(err, ErrBusy) {
		t.Fatalf("stale generation registered: %v", err)
	}
	foreign := run("")
	foreign.UserID = id.New()
	if err := repo.RegisterRun(ctx, foreign, 1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign owner registered: %v", err)
	}
	child := run(session.ID)
	child.ParentRunID = &root.ID
	if err := repo.RegisterRun(ctx, child, 1); err != nil {
		t.Fatal(err)
	}
	pause, _ := newOperation(org, user, computerID, "pause", "", nil)
	if _, err := repo.Intent(ctx, nil, pause); !errors.Is(err, ErrBusy) {
		t.Fatalf("maintenance passed active runs: %v", err)
	}
	if err := repo.FinishRun(ctx, root.ID, user, "completed"); !errors.Is(err, ErrBusy) {
		t.Fatalf("parent finished before child: %v", err)
	}
	if err := repo.FinishRun(ctx, child.ID, user, "completed"); err != nil {
		t.Fatal(err)
	}
	if err := repo.FinishRun(ctx, root.ID, user, "completed"); err != nil {
		t.Fatal(err)
	}
	orphan := run(session.ID)
	orphan.ParentRunID = &root.ID
	if err := repo.RegisterRun(ctx, orphan, 1); !errors.Is(err, ErrBusy) {
		t.Fatalf("child registered after parent finished: %v", err)
	}
	deletion, _ := newOperation(org, user, computerID, "delete", "", nil)
	if _, err := repo.Intent(ctx, nil, deletion); !errors.Is(err, ErrInUse) {
		t.Fatalf("delete ignored session reference: %v", err)
	}
	pause, _ = newOperation(org, user, computerID, "pause", "", nil)
	pause, err = repo.Intent(ctx, nil, pause)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.RegisterRun(ctx, run(""), 1); !errors.Is(err, ErrBusy) {
		t.Fatalf("new run bypassed maintenance intent: %v", err)
	}
	if err := repo.Complete(ctx, pause, &sandboxID, "paused", nil); err != nil {
		t.Fatal(err)
	}
}
