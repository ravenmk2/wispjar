package runtime

import (
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
