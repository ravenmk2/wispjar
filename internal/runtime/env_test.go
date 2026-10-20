package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/ravenmk2/wispjar/internal/agent"
)

func TestRefreshAgentEnvsReplacesSnapshotsAndFreezesEachTurn(t *testing.T) {
	stored := &agent.Snapshot{AgentID: "main", SandboxConfig: json.RawMessage(`{"image":"image","env":{"OLD":"old"}}`), SubAgents: []agent.SnapshotSubAgent{{ID: "child", SandboxConfig: json.RawMessage(`{"env":{"OLD":"old"}}`)}}}
	before, _ := json.Marshal(stored)
	version := "v1"
	o := &orchestrator{deps: Deps{AgentEnvs: func(_ context.Context, org string, ids []string) (map[string]map[string]string, error) {
		if org != "org" || !reflect.DeepEqual(ids, []string{"main", "child"}) {
			t.Fatal("wrong environment scope")
		}
		return map[string]map[string]string{"main": {"MAIN": version}, "child": {"CHILD": version}}, nil
	}}}
	var first agent.Snapshot
	_ = json.Unmarshal(before, &first)
	if err := o.refreshAgentEnvs(context.Background(), "org", &first); err != nil {
		t.Fatal(err)
	}
	version = "v2"
	var second agent.Snapshot
	_ = json.Unmarshal(before, &second)
	if err := o.refreshAgentEnvs(context.Background(), "org", &second); err != nil {
		t.Fatal(err)
	}
	if parseSandboxConfig(first.SandboxConfig).Env["MAIN"] != "v1" || parseSandboxConfig(second.SandboxConfig).Env["MAIN"] != "v2" {
		t.Fatal("turn environments not independent")
	}
	if cfg := parseSandboxConfig(first.SandboxConfig); cfg.Image != "image" || len(cfg.Env) != 1 {
		t.Fatal("did not preserve image or remove old env")
	}
	if cfg := parseSandboxConfig(first.SubAgents[0].SandboxConfig); len(cfg.Env) != 1 || cfg.Env["CHILD"] != "v1" {
		t.Fatal("wrong child environment")
	}
	after, _ := json.Marshal(stored)
	if string(after) != string(before) {
		t.Fatal("rewrote persisted snapshot")
	}
	cause := errors.New("environment read failed")
	o.deps.AgentEnvs = func(context.Context, string, []string) (map[string]map[string]string, error) { return nil, cause }
	if err := o.refreshAgentEnvs(context.Background(), "org", &second); !errors.Is(err, cause) {
		t.Fatal("silently used stale environment")
	}
}
