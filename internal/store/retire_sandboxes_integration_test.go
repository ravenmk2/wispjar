package store

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/ravenmk2/wispjar/internal/id"
	"gorm.io/gorm"
)

// Called only from the opt-in empty-schema integration test. Recreates version
// 7 and exercises the real startup migration with both old and current data.
func testRetireSessionSandboxes(t *testing.T, db *gorm.DB, admin User) {
	t.Helper()
	// Existing databases can use a different default from the application's
	// explicit table collation. Exercise that upgrade under the same mismatch.
	if err := db.Exec("ALTER DATABASE CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci").Error; err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	conn, err := sqlDB.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	down, err := migrationsFS.ReadFile("migrations/000008_remove_session_sandboxes.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	for statement := range strings.SplitSeq(string(down), ";") {
		if strings.TrimSpace(statement) != "" {
			if _, err := conn.ExecContext(context.Background(), statement); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("UPDATE schema_migrations SET version=7, dirty=0").Error; err != nil {
		t.Fatal(err)
	}
	provider, model, agentID, computerID := id.New(), id.New(), id.New(), id.New()
	old, migrated, copied, current := id.New(), id.New(), id.New(), id.New()
	runOld, runKeep, maintenance := id.New(), id.New(), id.New()
	for _, fixture := range []any{
		&ModelProvider{ID: provider, Name: "fixture", Type: "openai", BaseURL: "http://localhost", APIKeyEnc: []byte{}, CreatedBy: admin.ID, Status: "active"},
		&Model{ID: model, ProviderID: provider, ModelCode: "fixture", DisplayName: "fixture", Status: "active", Capabilities: json.RawMessage(`{}`), DefaultParams: json.RawMessage(`{}`)},
		&Agent{ID: agentID, OrgID: id.PlatformOrgID, Name: "fixture", Description: "", SystemPrompt: "", ModelID: model, CreatedBy: admin.ID, Status: "published", ModelParams: json.RawMessage(`{}`), ToolsEnabled: json.RawMessage(`{}`)},
		&Computer{ID: computerID, OrgID: id.PlatformOrgID, UserID: admin.ID, Name: "retained", Status: "stopped", BackendID: "test"},
	} {
		if err := db.Create(fixture).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, sid := range []string{old, migrated, copied, current} {
		var computer any = computerID
		sandbox := `{"id":"","status":"none"}`
		if sid == old {
			computer = nil
		}
		if sid == migrated {
			sandbox = `{"id":"retired-instance","status":"running"}`
		}
		if err := db.Exec(`INSERT INTO session
			(id,org_id,user_id,agent_id,agent_snapshot,model_id,sandbox,usage_total,computer_id,last_seq)
			VALUES (?,?,?,?,'{}',?,?,'{}',?,2)`, sid, id.PlatformOrgID, admin.ID, agentID, model, sandbox, computer).Error; err != nil {
			t.Fatal(err)
		}
		for seq := 1; seq <= 2; seq++ {
			typ := "user/message"
			if sid == copied && seq == 1 {
				typ = "session/sandbox"
			}
			if err := db.Create(&SessionEvent{SessionID: sid, Seq: int64(seq), Type: typ, TimeMs: 123, Data: json.RawMessage(`{"fixture":true}`)}).Error; err != nil {
				t.Fatal(err)
			}
		}
		if err := db.Create(&AgentCheckpoint{ID: id.New(), SessionID: sid, Data: []byte("checkpoint"), CreatedAt: time.Now()}).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, run := range []ComputerRun{
		{ID: runOld, SessionID: old}, {ID: runKeep, SessionID: current}, {ID: maintenance},
	} {
		run.OrgID, run.UserID, run.ComputerID, run.AgentID, run.Status = id.PlatformOrgID, admin.ID, computerID, agentID, "completed"
		if err := db.Create(&run).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&RunSkillLease{RunID: run.ID, OrgID: id.PlatformOrgID, UserID: admin.ID, SkillID: "fixture"}).Error; err != nil {
			t.Fatal(err)
		}
	}
	progress, _ := json.Marshal(map[string]string{"maintenance_run_id": maintenance})
	for _, op := range []ResourceOperation{
		{ID: id.New(), OrgID: id.PlatformOrgID, UserID: admin.ID, ResourceType: "workspace", ResourceID: old, Action: "workspace/migrate", Status: "failed", Progress: progress},
		{ID: id.New(), OrgID: id.PlatformOrgID, UserID: admin.ID, ResourceType: "computer", ResourceID: computerID, Action: "computer/start", Status: "completed"},
	} {
		if err := db.Create(&op).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	for table, want := range map[string]int64{"session": 1, "session_event": 2, "agent_checkpoint": 1, "computer": 1, "computer_run": 1, "run_skill_lease": 1, "resource_operation": 1} {
		var got int64
		if err := db.Table(table).Count(&got).Error; err != nil || got != want {
			t.Fatalf("%s: got %d want %d: %v", table, got, want, err)
		}
	}
	var kept Session
	if err := db.First(&kept, "id=?", current).Error; err != nil || kept.ComputerID == nil || *kept.ComputerID != computerID || kept.LastSeq != 2 {
		t.Fatalf("current session changed: %+v %v", kept, err)
	}
	if db.Migrator().HasColumn("session", "sandbox") {
		t.Fatal("retired sandbox column remains")
	}
	var nullable string
	if err := db.Raw("SELECT IS_NULLABLE FROM information_schema.columns WHERE table_schema=DATABASE() AND table_name='session' AND column_name='computer_id'").Scan(&nullable).Error; err != nil || nullable != "NO" {
		t.Fatalf("computer_id permits NULL: %s %v", nullable, err)
	}
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
}
