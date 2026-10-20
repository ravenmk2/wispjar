package runtime

import (
	"encoding/json"
	"testing"

	"github.com/ravenmk2/wispjar/internal/session"
	"github.com/ravenmk2/wispjar/internal/store"
)

func TestFileRootsAndRelativePaths(t *testing.T) {
	sess := &session.Detail{Session: store.Session{ID: "s", AgentID: "a"}}
	for scope, want := range map[string]string{"workspace": "/sessions/s", "storage": "/storage", "agent": "/agents/a/files"} {
		got, err := fileRoot(sess, scope)
		if err != nil || got != want {
			t.Fatalf("%s: %s %v", scope, got, err)
		}
	}
	for _, bad := range []string{"../secret", "a/../../secret", "/storage/secret", `..\secret`, "x\x00y"} {
		if _, err := relativeFilePath(bad); err == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
	if got, err := relativeFilePath("outputs/./result.txt"); err != nil || got != "outputs/result.txt" {
		t.Fatal(got, err)
	}
}
func TestMigrationWorkspaceBlocksBothLayouts(t *testing.T) {
	for _, state := range []string{"migrating", "migration_failed"} {
		raw, _ := json.Marshal(map[string]string{"state": state})
		if err := workspaceAvailable(raw); err == nil {
			t.Fatal("accepted " + state)
		}
	}
	for _, raw := range []json.RawMessage{nil, json.RawMessage(`{"state":"ready"}`), json.RawMessage(`{"state":"pending"}`)} {
		if err := workspaceAvailable(raw); err != nil {
			t.Fatal(err)
		}
	}
}
