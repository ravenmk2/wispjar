package tools

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ravenmk2/wispjar/internal/agent"
)

func TestComputerContextRequiresBoundHandle(t *testing.T) {
	tc := NewContext("session", "org")
	if _, _, err := tc.sandboxHandle(context.Background()); !errors.Is(err, errSandboxUnavailable) {
		t.Fatalf("missing computer handle was accepted: %v", err)
	}
	if got := tc.ResolvePath("outputs/result.txt"); got != "/sessions/session/outputs/result.txt" {
		t.Fatal(got)
	}
	h := newMemHandle()
	tc.BoundHandle, tc.BoundSandboxID = h, "computer-instance"
	got, sid, err := tc.sandboxHandle(context.Background())
	if err != nil || got != h || sid != "computer-instance" || len(h.files) != 0 {
		t.Fatalf("bound handle changed or uploaded files: %v %s", err, sid)
	}
}

func TestSkillReadsInstalledComputerRevision(t *testing.T) {
	h := newMemHandle()
	h.files["/agents/agent/skills/pdf/SKILL.md"] = []byte("installed revision")
	tc := NewContext("session", "org")
	tc.BoundHandle, tc.SkillsDir = h, "/agents/agent/skills"
	tc.Skills = []agent.SnapshotSkill{{ID: "skill", Name: "pdf"}}
	tt, err := newActivateSkillTool()
	if err != nil {
		t.Fatal(err)
	}
	out, err := invoke(t, tt, WithContext(context.Background(), tc), `{"name":"pdf"}`)
	if err != nil || !strings.Contains(out, "installed revision") || !strings.Contains(out, tc.SkillsDir) {
		t.Fatalf("skill did not use installed package: %s %v", out, err)
	}
}
