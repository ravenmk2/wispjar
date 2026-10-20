package store

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestAdvisoryLockDoesNotStarvePoolIntegration(t *testing.T) {
	dsn := os.Getenv("WISPJAR_VOLUME_TEST_DSN")
	if dsn == "" {
		t.Skip("set WISPJAR_VOLUME_TEST_DSN to an isolated MySQL database")
	}
	db, err := Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	sqlDB.SetMaxOpenConns(1)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	key := time.Now().Format("150405.000000000")
	release, err := AcquireAdvisoryLock(ctx, db, "pool-test", key)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := release(); err != nil {
			t.Error(err)
		}
	}()
	// The sole application connection must remain available while a
	// resource lock is held, including with concurrent lock waiters.
	waitCtx, stopWaiting := context.WithTimeout(ctx, 25*time.Millisecond)
	defer stopWaiting()
	waitDone := make(chan error, 1)
	go func() {
		unlock, err := AcquireAdvisoryLock(waitCtx, db, "pool-test", key)
		if err == nil {
			_ = unlock()
		}
		waitDone <- err
	}()
	var one int
	if err := db.WithContext(ctx).Raw("SELECT 1").Scan(&one).Error; err != nil || one != 1 {
		t.Fatalf("lock starved application pool: %d, %v", one, err)
	}
	if err := <-waitDone; err == nil {
		t.Fatal("same-resource lock waiter unexpectedly acquired the lock")
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
	next, err := AcquireAdvisoryLock(ctx, db, "pool-test", key)
	if err != nil {
		t.Fatalf("canceled waiter leaked a lock: %v", err)
	}
	if err := next(); err != nil {
		t.Fatal(err)
	}
}
