package computer

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"sync"
	"testing"

	"github.com/ravenmk2/wispjar/internal/id"
	"github.com/ravenmk2/wispjar/internal/store"
)

// Explicit opt-in; the parent acceptance runner supplies an isolated schema.
func TestRecoveryRepositoryIntegration(t *testing.T) {
	dsn := os.Getenv("WISPJAR_VOLUME_TEST_DSN")
	if dsn == "" {
		t.Skip("requires isolated migrated MySQL")
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
	t.Cleanup(func() {
		for _, model := range []any{&store.AuditLog{}, &store.RunSkillLease{}, &store.ComputerRun{}, &store.ResourceOperation{}, &store.Computer{}} {
			if err := db.Where("org_id=?", org).Delete(model).Error; err != nil {
				t.Error(err)
			}
		}
		if err := db.Where("id=?", user).Delete(&store.User{}).Error; err != nil {
			t.Error(err)
		}
		if err := db.Where("id=?", org).Delete(&store.Organization{}).Error; err != nil {
			t.Error(err)
		}
	})
	for _, row := range []any{&store.Organization{ID: org, Name: "recovery-test", Status: "active"}, &store.User{ID: user, Username: user, PasswordHash: "unused", Status: "active"}} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	c := store.Computer{ID: id.New(), OrgID: org, UserID: user, Generation: 4, Name: "recover", BackendID: "test", Status: "running"}
	other := store.Computer{ID: id.New(), OrgID: org, UserID: user, Generation: 2, Name: "other", BackendID: "test", Status: "running"}
	for _, row := range []*store.Computer{&c, &other} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	repo := NewGORMRepository(db)
	ctx := context.Background()
	root := store.ComputerRun{ID: id.New(), OrgID: org, UserID: user, ComputerID: c.ID, Owner: "runtime"}
	otherRun := store.ComputerRun{ID: id.New(), OrgID: org, UserID: user, ComputerID: other.ID, Owner: "runtime"}
	for _, row := range []struct {
		run store.ComputerRun
		gen uint64
	}{{root, 4}, {otherRun, 2}} {
		if err := repo.RegisterRun(ctx, row.run, row.gen); err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&store.RunSkillLease{RunID: row.run.ID, OrgID: org, UserID: user, SkillID: id.New(), Revision: 1}).Error; err != nil {
			t.Fatal(err)
		}
	}
	op, _ := newOperation(org, user, c.ID, "recover", id.New(), RecoverInput{ConfirmStopAll: true})
	op, err = repo.Intent(ctx, nil, op)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := repo.RegisterRun(ctx, store.ComputerRun{ID: id.New(), OrgID: org, UserID: user, ComputerID: c.ID, Owner: "runtime"}, 4)
			if !errors.Is(err, ErrBusy) {
				t.Errorf("recovery admitted run: %v", err)
			}
		}()
	}
	wg.Wait()
	if err := repo.Complete(ctx, op, nil, "stopped", nil); !errors.Is(err, ErrConflict) {
		t.Fatalf("completed without durable cleanup: %v", err)
	}
	if err := repo.Complete(ctx, op, nil, "recovering", errors.New("transport")); err != nil {
		t.Fatal(err)
	}
	var count int64
	db.Model(&store.RunSkillLease{}).Where("run_id=?", root.ID).Count(&count)
	if count != 1 {
		t.Fatal("failure released lease")
	}
	if err := repo.RecoverRuns(ctx, op); err != nil {
		t.Fatal(err)
	}
	if err := repo.RecoverRuns(ctx, op); err != nil {
		t.Fatal(err)
	}
	if err := repo.Complete(ctx, op, nil, "stopped", nil); err != nil {
		t.Fatal(err)
	}
	replayed, err := repo.Intent(ctx, nil, op)
	if err != nil || replayed.ID != op.ID || replayed.Status != "completed" {
		t.Fatalf("completed idempotent recovery was not reused: %+v %v", replayed, err)
	}
	row, err := repo.Get(ctx, c.ID)
	if err != nil || row.Generation != 5 || row.Status != "stopped" {
		t.Fatalf("bad recovery: %+v %v", row, err)
	}
	if err := repo.FinishRun(ctx, root.ID, user, "completed"); err != nil {
		t.Fatal(err)
	}
	db.First(&root, "id=?", root.ID)
	var manifest map[string]any
	_ = json.Unmarshal(root.Manifest, &manifest)
	if root.Status != "failed" || manifest["skill_leases_released"] != true {
		t.Fatal("late callback changed recovery outcome")
	}
	db.Model(&store.RunSkillLease{}).Where("run_id=?", root.ID).Count(&count)
	if count != 0 {
		t.Fatal("recovery retained lease")
	}
	db.Model(&store.RunSkillLease{}).Where("run_id=?", otherRun.ID).Count(&count)
	if count != 1 {
		t.Fatal("recovery affected other computer")
	}
	db.First(&otherRun, "id=?", otherRun.ID)
	if otherRun.Status != "running" {
		t.Fatal("other computer stopped")
	}
}
