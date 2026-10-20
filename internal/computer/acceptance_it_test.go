package computer

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/ravenmk2/wispjar/internal/agent"
	"github.com/ravenmk2/wispjar/internal/id"
	"github.com/ravenmk2/wispjar/internal/sandbox"
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
		if err := db.Where("org_id=?", org).Delete(&store.AuditLog{}).Error; err != nil {
			t.Error(err)
		}
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
	// Runtime variables update independently of Agent publication and computer generation.
	agentSvc := agent.NewService(db)
	if err := agentSvc.PatchEnv(ctx, user, org, agentID, sandbox.EnvPatch{Set: map[string]string{"AGENT_TOKEN": "fixture"}}); err != nil {
		t.Fatal(err)
	}
	envs, err := agentSvc.LoadEnvs(ctx, org, []string{agentID})
	if err != nil || envs[agentID]["AGENT_TOKEN"] != "fixture" {
		t.Fatalf("agent env: %v %v", envs, err)
	}
	var savedAgent store.Agent
	svc := NewService(repo, memoryVolumes{}, &memoryBackend{}, "test", "test", nil)
	template, err := svc.resolvedConfig(ctx, org, agentID, Config{Env: map[string]string{"COMPUTER": "computer"}})
	if err != nil || template.Env["AGENT_TOKEN"] != "" || template.Env["COMPUTER"] != "computer" {
		t.Fatal("Agent environment copied into computer")
	}
	if err := db.First(&savedAgent, "id=?", agentID).Error; err != nil || savedAgent.Status != "published" {
		t.Fatal("env edit changed publication status")
	}
	if _, err := agentSvc.LoadEnvs(ctx, "another-org", []string{agentID}); err == nil {
		t.Fatal("cross-org environment read")
	}
	if err := agentSvc.PatchEnv(ctx, user, org, agentID, sandbox.EnvPatch{Unset: []string{"AGENT_TOKEN"}}); err != nil {
		t.Fatal(err)
	}
	envs, err = agentSvc.LoadEnvs(ctx, org, []string{agentID})
	if err != nil || len(envs[agentID]) != 0 {
		t.Fatal("agent env deletion failed")
	}
	envComputerID := id.New()
	computer := store.Computer{ID: envComputerID, OrgID: org, UserID: user, Name: "env-test", BackendID: "test", Generation: 4, Status: "running", Config: json.RawMessage(`{"image":"python:3.12","ttl_seconds":3600,"env":{"SECRET":"keep","OLD":"remove"}}`)}
	if err := db.Create(&computer).Error; err != nil {
		t.Fatal(err)
	}
	if err := repo.Patch(ctx, envComputerID, org, user, PatchInput{Env: &sandbox.EnvPatch{Set: map[string]string{"NEW": "new"}, Unset: []string{"OLD"}}}); err != nil {
		t.Fatal(err)
	}
	savedComputer, err := repo.Get(ctx, envComputerID)
	var savedConfig Config
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(savedComputer.Config, &savedConfig); err != nil {
		t.Fatal(err)
	}
	if savedComputer.Generation != 4 || savedComputer.Status != "running" || savedConfig.Env["SECRET"] != "keep" || savedConfig.Env["NEW"] != "new" || len(savedConfig.Env) != 2 {
		t.Fatal("online computer environment patch failed")
	}
	if err := repo.Patch(ctx, envComputerID, org, "other", PatchInput{Env: &sandbox.EnvPatch{}}); !errors.Is(err, ErrNotFound) {
		t.Fatal("cross-user env write")
	}
	if err := db.Model(&computer).Update("status", "rebuilding").Error; err != nil {
		t.Fatal(err)
	}
	if err := repo.Patch(ctx, envComputerID, org, user, PatchInput{Env: &sandbox.EnvPatch{}}); !errors.Is(err, ErrBusy) {
		t.Fatal("env write during rebuild")
	}
	if err := db.Delete(&computer).Error; err != nil {
		t.Fatal(err)
	}
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
