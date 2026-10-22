package store

import (
	"os"
	"testing"
)

// Explicit opt-in: DSN must point to an empty disposable schema. The resulting
// schema is left intact for inspection; the test never drops a database.
func TestMigrationsMySQLIntegration(t *testing.T) {
	dsn := os.Getenv("WISPJAR_STORE_MIGRATION_TEST_DSN")
	if dsn == "" {
		t.Skip("set WISPJAR_STORE_MIGRATION_TEST_DSN to an empty disposable MySQL schema")
	}
	db, err := Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := sqlDB.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	})
	var count int64
	if err := db.Raw("SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = DATABASE()").Scan(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("migration integration test requires an empty schema; refusing to modify existing tables")
	}
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"computer", "user_volume", "user_agent_binding", "user_skill_state", "computer_run", "run_skill_lease", "resource_operation", "attachment_access", "provider_media_file"} {
		if !db.Migrator().HasTable(table) {
			t.Errorf("missing table %s", table)
		}
	}
	for _, field := range []struct{ table, column string }{
		{"session", "computer_id"}, {"session", "workspace"}, {"skill", "revision"}, {"skill", "content_hash"}, {"user_volume", "usage"},
	} {
		if !db.Migrator().HasColumn(field.table, field.column) {
			t.Errorf("missing %s.%s", field.table, field.column)
		}
	}
	if err := Migrate(db); err != nil {
		t.Fatalf("migration rerun: %v", err)
	}
}
