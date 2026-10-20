package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"database/sql/driver"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
)

var ErrLockBusy = errors.New("store/lock_busy")

// AcquireAdvisoryLock serializes a resource operation without a long SQL
// transaction. A separate short-lived connection is essential: waiting for
// named locks must not exhaust the application pool that lock holders need
// to persist their results. scope/key are hashed with the database identity.
func AcquireAdvisoryLock(ctx context.Context, db *gorm.DB, scope, key string) (func() error, error) {
	dialect, ok := db.Dialector.(*gormmysql.Dialector)
	if !ok || dialect.DSN == "" {
		return nil, errors.New("advisory locks require a configured MySQL connection")
	}
	lockDB, err := sql.Open("mysql", dialect.DSN)
	if err != nil {
		return nil, err
	}
	lockDB.SetMaxOpenConns(1)
	lockDB.SetMaxIdleConns(0)
	conn, err := lockDB.Conn(ctx)
	if err != nil {
		return nil, errors.Join(err, lockDB.Close())
	}
	closeConnection := func() error {
		return errors.Join(conn.Close(), lockDB.Close())
	}
	var databaseName string
	if err := conn.QueryRowContext(ctx, "SELECT DATABASE()").Scan(&databaseName); err != nil {
		return nil, errors.Join(err, closeConnection())
	}
	sum := sha256.Sum256([]byte(databaseName + "\x00" + scope + "\x00" + key))
	name := "wj:" + hex.EncodeToString(sum[:28])
	var locked int
	if err = conn.QueryRowContext(ctx, "SELECT GET_LOCK(?, 10)", name).Scan(&locked); err != nil || locked != 1 {
		if err != nil {
			// Cancellation can hide successful acquisition. Do not reuse
			// this physical connection with its session-scoped lock.
			_ = conn.Raw(func(any) error { return driver.ErrBadConn })
		} else {
			err = ErrLockBusy
		}
		return nil, errors.Join(err, closeConnection())
	}
	var once sync.Once
	var releaseErr error
	return func() error {
		once.Do(func() {
			releaseCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			var released int
			releaseErr = conn.QueryRowContext(releaseCtx, "SELECT RELEASE_LOCK(?)", name).Scan(&released)
			if releaseErr == nil && released != 1 {
				releaseErr = fmt.Errorf("advisory lock was not held")
			}
			if releaseErr != nil {
				_ = conn.Raw(func(any) error { return driver.ErrBadConn })
			}
			releaseErr = errors.Join(releaseErr, closeConnection())
		})
		return releaseErr
	}, nil
}
