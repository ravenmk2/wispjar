package agent

import (
	"strings"

	"github.com/ravenmk2/wispjar/internal/store"
)

// validateSubagentDAG 校验子智能体指派关系：不得包含自身、不得成环、深度限 1 层
// （被指定为子智能体者自身不得再有下级）。childOf 为组织内既有的
// agent_id -> sub_agent_ids 边集（不含被编辑智能体自身的行）。
func validateSubagentDAG(agentID string, subIDs []string, childOf map[string][]string) *Error {
	seen := map[string]bool{}
	for _, id := range subIDs {
		if id == agentID {
			return errInvalid("子智能体不能包含自身", map[string]any{"sub_agent_id": id})
		}
		if seen[id] {
			return errInvalid("子智能体重复", map[string]any{"sub_agent_id": id})
		}
		seen[id] = true
	}
	visiting := map[string]bool{}
	var reaches func(cur string) bool
	reaches = func(cur string) bool {
		if cur == agentID {
			return true
		}
		if visiting[cur] {
			return false
		}
		visiting[cur] = true
		for _, next := range childOf[cur] {
			if reaches(next) {
				return true
			}
		}
		return false
	}
	for _, id := range subIDs {
		if reaches(id) {
			return errInvalid("子智能体指派存在循环依赖", map[string]any{"sub_agent_id": id})
		}
	}
	for _, id := range subIDs {
		if len(childOf[id]) > 0 {
			return errInvalid("子智能体层级仅限 1 层：被指定的子智能体自身不得再有下级", map[string]any{"sub_agent_id": id})
		}
	}
	return nil
}

// validatePublishable 校验发布完整性：prompt 非空、模型可用、全部子智能体均为 published。
func validatePublishable(agent *store.Agent, modelActive bool, subAgents []store.Agent) *Error {
	if strings.TrimSpace(agent.SystemPrompt) == "" {
		return errInvalid("发布前必须填写 system_prompt", map[string]any{"field": "system_prompt"})
	}
	if agent.ModelID == "" || !modelActive {
		return errInvalid("发布前必须绑定一个 active 状态的模型", map[string]any{"field": "model_id"})
	}
	for _, sub := range subAgents {
		if sub.Status != StatusPublished {
			return errInvalid("子智能体必须全部为 published 状态", map[string]any{"sub_agent_id": sub.ID, "status": sub.Status})
		}
	}
	return nil
}
