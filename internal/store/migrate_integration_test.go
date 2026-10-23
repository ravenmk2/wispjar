package store

import (
	"os"
	"strings"
	"testing"

	"github.com/ravenmk2/wispjar/internal/id"
	"gorm.io/gorm"
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
	var org Organization
	if err := db.First(&org, "id = ?", id.PlatformOrgID).Error; err != nil {
		t.Fatal(err)
	}
	if org.Name != "系统管理" {
		t.Fatalf("fresh organization name = %q", org.Name)
	}
	if err := BootstrapAdmin(db, "integration-test-only"); err != nil {
		t.Fatal(err)
	}
	var admin User
	if err := db.First(&admin, "username = ?", "admin").Error; err != nil {
		t.Fatal(err)
	}
	if admin.DisplayName != "系统管理员" {
		t.Fatalf("fresh admin display name = %q", admin.DisplayName)
	}
	testSystemDisplayNames(t, db, admin)
	testRetireSessionSandboxes(t, db, admin)
}

func testSystemDisplayNames(t *testing.T, db *gorm.DB, admin User) {
	t.Helper()
	sql, err := migrationsFS.ReadFile("migrations/000007_system_display_names.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, orgName, username, displayName, role, wantOrg, wantUser string
	}{
		{"legacy defaults", "平台管理机构", "admin", "平台管理员", "admin", "系统管理", "系统管理员"},
		{"custom names", "自定义组织", "admin", "自定义管理员", "admin", "自定义组织", "自定义管理员"},
		{"custom organization", "自定义组织", "admin", "平台管理员", "admin", "自定义组织", "系统管理员"},
		{"custom admin", "平台管理机构", "admin", "自定义管理员", "admin", "系统管理", "自定义管理员"},
		{"other account", "平台管理机构", "other", "平台管理员", "admin", "系统管理", "平台管理员"},
		{"non admin member", "平台管理机构", "admin", "平台管理员", "member", "系统管理", "平台管理员"},
		{"exact matching", "平台管理机构 ", "Admin", "平台管理员", "admin", "平台管理机构 ", "平台管理员"},
		{"custom trailing space", "平台管理机构", "admin", "平台管理员 ", "admin", "系统管理", "平台管理员 "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tx := db.Begin()
			if tx.Error != nil {
				t.Fatal(tx.Error)
			}
			defer tx.Rollback()
			for _, query := range []struct {
				sql  string
				args []any
			}{
				{"UPDATE organization SET name = ? WHERE id = ?", []any{tc.orgName, id.PlatformOrgID}},
				{"UPDATE user SET username = ?, display_name = ? WHERE id = ?", []any{tc.username, tc.displayName, admin.ID}},
				{"UPDATE org_member SET role = ? WHERE user_id = ? AND org_id = ?", []any{tc.role, admin.ID, id.PlatformOrgID}},
				{"INSERT INTO organization (id, name) VALUES (?, ?)", []any{id.New(), "平台管理机构"}},
			} {
				if err := tx.Exec(query.sql, query.args...).Error; err != nil {
					t.Fatal(err)
				}
			}
			// A second execution must preserve both migrated and custom values.
			for range 2 {
				for statement := range strings.SplitSeq(string(sql), ";") {
					if strings.TrimSpace(statement) != "" {
						if err := tx.Exec(statement).Error; err != nil {
							t.Fatal(err)
						}
					}
				}
			}
			var org Organization
			var user User
			if err := tx.First(&org, "id = ?", id.PlatformOrgID).Error; err != nil {
				t.Fatal(err)
			}
			if err := tx.First(&user, "id = ?", admin.ID).Error; err != nil {
				t.Fatal(err)
			}
			if org.Name != tc.wantOrg || user.DisplayName != tc.wantUser {
				t.Fatalf("names = %q / %q, want %q / %q", org.Name, user.DisplayName, tc.wantOrg, tc.wantUser)
			}
			if user.PasswordHash != admin.PasswordHash || user.MustChangePassword != admin.MustChangePassword || user.Status != admin.Status {
				t.Fatal("migration changed account security fields")
			}
			var unrelated int64
			if err := tx.Model(&Organization{}).Where("id <> ? AND BINARY name = BINARY ?", id.PlatformOrgID, "平台管理机构").Count(&unrelated).Error; err != nil || unrelated != 1 {
				t.Fatalf("unrelated organization changed: count=%d err=%v", unrelated, err)
			}
		})
	}
}
