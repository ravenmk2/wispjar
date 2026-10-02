package store

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

func Open(dsn string) (*gorm.DB, error) {
	db, err := gorm.Open(mysql.Open(normalizeDSN(dsn)), &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Warn),
	})
	if err != nil {
		return nil, err
	}
	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}
	sqlDB.SetMaxOpenConns(20)
	sqlDB.SetMaxIdleConns(5)
	sqlDB.SetConnMaxLifetime(time.Hour)
	if err := sqlDB.Ping(); err != nil {
		return nil, fmt.Errorf("ping database: %w", err)
	}
	return db, nil
}

func normalizeDSN(dsn string) string {
	base, rawQuery, _ := strings.Cut(dsn, "?")
	q, err := url.ParseQuery(rawQuery)
	if err != nil {
		q = url.Values{}
	}
	setDefault := func(key, value string) {
		if q.Get(key) == "" {
			q.Set(key, value)
		}
	}
	setDefault("charset", "utf8mb4")
	setDefault("collation", "utf8mb4_unicode_ci")
	setDefault("parseTime", "true")
	setDefault("loc", "Local")
	setDefault("time_zone", "'+08:00'")
	setDefault("multiStatements", "true")
	return base + "?" + q.Encode()
}
