package tools

import (
	"context"
	"errors"
	"testing"

	"github.com/ravenmk2/wispjar/internal/agent"
)

func TestExecutionUnknownCompletionRemainsOccupied(t *testing.T) {
	raw := newMemHandle()
	raw.runErr = errors.New("connection lost")
	h := NewExecutionHandle(raw)
	if _, err := h.RunCommand(context.Background(), "true", "/", nil, 1000, nil); err == nil {
		t.Fatal("expected transport error")
	}
	if h.CommandsStopped() {
		t.Fatal("lost connection must not certify process exit")
	}
}

func TestChildContextUsesOwnSkillsAndMetadata(t *testing.T) {
	parent := NewContext("session", "org")
	parent.AgentID, parent.RunID, parent.ComputerID = "root", "run-root", "computer"
	parent.WorkDir = "/sessions/session"
	parent.Skills = []agent.SnapshotSkill{{ID: "root-skill", Name: "root"}}
	parent.SkillsDir = "/agents/root/skills"
	parent.BoundHandle = newMemHandle()
	child := parent.Fork("child", "run-child", []agent.SnapshotSkill{{ID: "child-skill", Name: "child"}})
	if child.ComputerID != parent.ComputerID || child.BoundHandle != parent.BoundHandle || child.WorkDir != parent.WorkDir {
		t.Fatal("child lost inherited resource")
	}
	if child.SkillsDir != "/agents/child/skills" || child.Skills[0].Name != "child" || child.ParentRunID != parent.RunID {
		t.Fatal("child identity/skills mixed")
	}
	child.BeginMeta(context.Background(), "child-render")
	if parent.TakeMeta("") != nil {
		t.Fatal("child metadata leaked into parent")
	}
	if child.TakeMeta("").RunID != "run-child" {
		t.Fatal("child run metadata missing")
	}
	if got := child.ResolvePath("outputs/result.txt"); got != "/sessions/session/outputs/result.txt" {
		t.Fatalf("path=%s", got)
	}
}

func TestExecutionAdmissionClosesBeforeCleanup(t *testing.T) {
	raw := newMemHandle()
	called := false
	raw.exec = func(string) (int, string, string) { called = true; return 0, "", "" }
	h := NewExecutionHandle(raw)
	h.CloseAdmission()
	if _, err := h.RunCommand(context.Background(), "touch forbidden", "/", nil, 1000, nil); err == nil {
		t.Fatal("closed run admitted a command")
	}
	if called {
		t.Fatal("remote command started after resource release")
	}
	if !h.CommandsStopped() {
		t.Fatal("rejected command incorrectly occupies resources")
	}
}
