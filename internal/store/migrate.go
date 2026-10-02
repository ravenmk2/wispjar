package store

import (
	"embed"
	"errors"
	"fmt"

	"github.com/golang-migrate/migrate/v4"
	mysqlmigrate "github.com/golang-migrate/migrate/v4/database/mysql"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"gorm.io/gorm"
)

//go:embed migrations
var migrationsFS embed.FS

func Migrate(db *gorm.DB) error {
	sqlDB, err := db.DB()
	if err != nil {
		return err
	}
	src, err := iofs.New(migrationsFS, "migrations")
	if err != nil {
		return err
	}
	drv, err := mysqlmigrate.WithInstance(sqlDB, &mysqlmigrate.Config{})
	if err != nil {
		return err
	}
	m, err := migrate.NewWithInstance("iofs", src, "mysql", drv)
	if err != nil {
		return err
	}
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("run migrations: %w", err)
	}
	return nil
}
