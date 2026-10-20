package volume

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/ravenmk2/wispjar/internal/id"
	"github.com/ravenmk2/wispjar/internal/store"
)

// Run only against an explicitly provided, already-migrated disposable DB:
// WISPJAR_VOLUME_TEST_DSN=... go test ./internal/volume -run TestGORMRepositoryIntegration
// This test never connects to config.yaml or creates/deletes Docker resources.
func TestGORMRepositoryIntegration(t *testing.T) {
	dsn := os.Getenv("WISPJAR_VOLUME_TEST_DSN")
	if dsn == "" {
		t.Skip("set WISPJAR_VOLUME_TEST_DSN to an isolated, migrated test database")
	}
	db, err := store.Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := sqlDB.Close(); err != nil {
			t.Error(err)
		}
	})
	repo := NewGORMRepository(db)
	ctx := context.Background()
	first, err := repo.InstallationID(ctx)
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewGORMRepository(db).InstallationID(ctx)
	if err != nil || first != second {
		t.Fatalf("identity changed: %v", err)
	}
	org, user := id.New(), id.New()
	t.Cleanup(func() {
		if err := db.Where("org_id = ? AND user_id = ?", org, user).Delete(&store.UserVolume{}).Error; err != nil {
			t.Error(err)
		}
	})
	provider := newProvider()
	s := service(t, repo, provider)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Go(func() {
			if _, err := s.Ensure(ctx, org, user); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	rows, err := repo.List(ctx, org, user)
	if err != nil || len(rows) != 3 || provider.creates != 3 {
		t.Fatalf("concurrent ensure: %d rows / %d creates / %v", len(rows), provider.creates, err)
	}
	for _, row := range rows {
		if err := repo.SetState(ctx, row.ID, "ready", ""); err != nil {
			t.Fatal(err)
		}
	}
	unlock, err := repo.Lock(ctx, org, user)
	if err != nil {
		t.Fatal(err)
	}
	timeout, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
	_, err = repo.Lock(timeout, org, user)
	cancel()
	if err == nil {
		t.Error("contended lock did not honor cancellation")
	}
	if err := unlock(); err != nil {
		t.Fatal(err)
	}
	// Verify the cancellation did not leak a lock through pooled connections.
	unlock, err = repo.Lock(ctx, org, user)
	if err != nil {
		t.Fatal(err)
	}
	if err := unlock(); err != nil {
		t.Fatal(err)
	}
}
