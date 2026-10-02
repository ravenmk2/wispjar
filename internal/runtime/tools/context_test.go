package tools

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/ravenmk2/wispjar/internal/agent"
	"github.com/ravenmk2/wispjar/internal/sandbox"
)

func skillContext(dataDir string, h *memHandle) *Context {
	tc := NewContext("sess-1", "org-1")
	tc.Sandbox = memProvider{h: h}
	tc.Settings = sandbox.DefaultSettings()
	tc.DataDir = dataDir
	tc.Skills = []agent.SnapshotSkill{{ID: "skill-1", Name: "pdf-tools", Description: "Handle PDFs"}}
	return tc
}

func writeSkillPackage(t *testing.T, dataDir string) {
	t.Helper()
	dir := filepath.Join(dataDir, "skills", "org-1", "skill-1")
	if err := os.MkdirAll(filepath.Join(dir, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"SKILL.md":       "---\nname: pdf-tools\ndescription: Handle PDFs\n---\n",
		"scripts/run.py": "print('hi')\n",
		"_source.zip":    "zip-bytes",
	}
	for rel, content := range files {
		if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(rel)), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSandboxHandleUploadsSkills(t *testing.T) {
	dataDir := t.TempDir()
	writeSkillPackage(t, dataDir)
	h := newMemHandle()
	tc := skillContext(dataDir, h)

	if _, _, err := tc.sandboxHandle(context.Background()); err != nil {
		t.Fatalf("sandboxHandle: %v", err)
	}
	if got := string(h.files["/skills/pdf-tools/SKILL.md"]); got == "" {
		t.Fatalf("SKILL.md not uploaded; files = %v", keysOf(h.files))
	}
	if got := string(h.files["/skills/pdf-tools/scripts/run.py"]); got != "print('hi')\n" {
		t.Fatalf("scripts/run.py = %q", got)
	}
	if _, ok := h.files["/skills/pdf-tools/_source.zip"]; ok {
		t.Fatal("_source.zip must not be uploaded")
	}
}

func TestSandboxHandleSkillUploadFailureNotBlocking(t *testing.T) {
	dataDir := t.TempDir()
	h := newMemHandle()
	tc := skillContext(dataDir, h)
	failures := []string{}
	tc.OnSkillUploadFailed = func(name, sandboxID string, err error) {
		failures = append(failures, name+"@"+sandboxID+": "+err.Error())
	}

	if _, sid, err := tc.sandboxHandle(context.Background()); err != nil {
		t.Fatalf("sandboxHandle should not fail: %v", err)
	} else if sid != "mem-sandbox" {
		t.Fatalf("sid = %q", sid)
	}
	if len(failures) != 1 {
		t.Fatalf("expected 1 failure report, got %v", failures)
	}
}

func keysOf(m map[string][]byte) []string {
	out := []string{}
	for k := range m {
		out = append(out, k)
	}
	return out
}
