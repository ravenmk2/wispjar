package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/ravenmk2/wispjar/internal/sandbox"
)

type envHandle struct {
	Handle
	received map[string]string
	calls    int
}

func (h *envHandle) RunCommand(_ context.Context, _, _ string, env map[string]string, _ int64, _ func(string, string)) (*sandbox.CommandResult, error) {
	h.calls++
	h.received = env
	return &sandbox.CommandResult{}, nil
}

func TestExecEnvironmentPrecedenceAndChildScope(t *testing.T) {
	h := &envHandle{}
	parent := NewContext("session", "org")
	parent.BoundHandle = h
	parent.ComputerEnv = map[string]string{"BASE": "base", "OVERRIDE": "computer"}
	parent.Env = map[string]string{"PARENT_SECRET": "parent", "OVERRIDE": "parent"}
	execTool, err := newExecTool()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := invoke(t, execTool, WithContext(context.Background(), parent), `{"command":"true","env":{"OVERRIDE":"command"}}`); err != nil {
		t.Fatal(err)
	}
	if h.received["OVERRIDE"] != "command" || h.received["BASE"] != "base" || h.received["WISPJAR"] != "1" {
		t.Fatal("wrong environment precedence")
	}
	child := parent.Fork("child", "run", nil)
	child.Env = map[string]string{"OVERRIDE": "child"}
	if _, err := invoke(t, execTool, WithContext(context.Background(), child), `{"command":"true"}`); err != nil {
		t.Fatal(err)
	}
	if h.received["OVERRIDE"] != "child" || h.received["BASE"] != "base" || h.received["PARENT_SECRET"] != "" {
		t.Fatal("child inherited parent environment")
	}
	child.ComputerEnv["BASE"] = "changed"
	if parent.ComputerEnv["BASE"] != "base" || parent.Env["OVERRIDE"] != "parent" {
		t.Fatal("mutated parent environment")
	}
	calls := h.calls
	out, err := invoke(t, execTool, WithContext(context.Background(), child), `{"command":"true","env":{"EXECD_ACCESS_TOKEN":"bad"}}`)
	if err != nil || h.calls != calls || !strings.Contains(out, "保留") {
		t.Fatalf("reserved env reached executor: %v %s", err, out)
	}
}
