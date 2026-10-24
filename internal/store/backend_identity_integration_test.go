package store

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/ravenmk2/wispjar/internal/id"
	"gorm.io/gorm"
)

// Only reconstructs historical fixtures in the explicitly disposable test DB.
// Production has no downgrade or legacy identity fallback.
func restoreBackendIdentityFixture(t *testing.T, db *gorm.DB) {
	t.Helper()
	for _, statement := range []string{
		"ALTER TABLE computer ADD COLUMN backend_id VARCHAR(64) NOT NULL DEFAULT 'fixture'",
		"ALTER TABLE user_volume ADD COLUMN backend_id VARCHAR(64) NOT NULL DEFAULT 'fixture', DROP INDEX uk_user_volume_backend_name, ADD UNIQUE KEY uk_user_volume_backend (backend_id, backend_name)",
	} {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
}

func testRemoveBackendIdentities(t *testing.T, db *gorm.DB, admin User) {
	t.Helper()
	for _, table := range []string{"computer", "user_volume"} {
		if db.Migrator().HasColumn(table, "backend_id") {
			t.Fatalf("fresh schema retains %s.backend_id", table)
		}
	}
	restoreBackendIdentityFixture(t, db)
	a := UserVolume{ID: id.New(), OrgID: id.PlatformOrgID, UserID: admin.ID, Kind: "storage", BackendName: "retained-volume", Status: "ready"}
	b := UserVolume{ID: id.New(), OrgID: id.PlatformOrgID, UserID: admin.ID, Kind: "agents", BackendName: a.BackendName, Status: "ready"}
	if err := db.Create(&a).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("ALTER TABLE user_volume ALTER COLUMN backend_id SET DEFAULT 'second'").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&b).Error; err != nil {
		t.Fatal(err)
	}
	up, err := migrationsFS.ReadFile("migrations/000012_remove_backend_ids.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	// Conflicting names must fail before either identity column is removed.
	if err := db.Exec(strings.Split(string(up), ";")[0]).Error; err == nil {
		t.Fatal("duplicate volume names accepted")
	}
	for _, table := range []string{"computer", "user_volume"} {
		if !db.Migrator().HasColumn(table, "backend_id") {
			t.Fatalf("failed migration changed %s", table)
		}
	}
	if err := db.Delete(&b).Error; err != nil {
		t.Fatal(err)
	}
	readRows := func() []byte {
		var computers []Computer
		var volumes []UserVolume
		if err := db.Order("id").Find(&computers).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Order("id").Find(&volumes).Error; err != nil {
			t.Fatal(err)
		}
		// Marshal model values including non-JSON exported fields via explicit volume names.
		names := make([]string, len(volumes))
		for i, v := range volumes {
			names[i] = v.BackendName
		}
		data, err := json.Marshal([]any{computers, volumes, names})
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	before := readRows()
	if err := db.Exec("UPDATE schema_migrations SET version=11, dirty=0").Error; err != nil {
		t.Fatal(err)
	}
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	if string(before) != string(readRows()) {
		t.Fatal("resource data changed during identity removal")
	}
	for _, table := range []string{"computer", "user_volume"} {
		if db.Migrator().HasColumn(table, "backend_id") {
			t.Fatalf("migration retains %s.backend_id", table)
		}
	}
	if err := db.Create(&b).Error; err == nil {
		t.Fatal("global volume name uniqueness not enforced")
	}
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
}
