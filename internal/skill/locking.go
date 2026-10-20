package skill

import (
	"context"
	"errors"
	"github.com/ravenmk2/wispjar/internal/store"
	"gorm.io/gorm"
)

func withDBLock(ctx context.Context, db *gorm.DB, key string, fn func() error) (err error) {
	release, err := store.AcquireAdvisoryLock(ctx, db, "skill", key)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, release()) }()
	return fn()
}
