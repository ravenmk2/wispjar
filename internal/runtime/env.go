package runtime

import (
	"context"
	"encoding/json"

	"github.com/ravenmk2/wispjar/internal/agent"
)

func (o *orchestrator) refreshAgentEnvs(ctx context.Context, org string, snap *agent.Snapshot) error {
	if o.deps.AgentEnvs == nil {
		return nil
	}
	ids := []string{snap.AgentID}
	for _, sub := range snap.SubAgents {
		ids = append(ids, sub.ID)
	}
	envs, err := o.deps.AgentEnvs(ctx, org, ids)
	if err != nil {
		return err
	}
	set := func(raw json.RawMessage, id string) json.RawMessage {
		config := parseSandboxConfig(raw)
		config.Env = envs[id]
		updated, _ := json.Marshal(config)
		return updated
	}
	snap.SandboxConfig = set(snap.SandboxConfig, snap.AgentID)
	for i := range snap.SubAgents {
		sub := &snap.SubAgents[i]
		sub.SandboxConfig = set(sub.SandboxConfig, sub.ID)
	}
	return nil
}
