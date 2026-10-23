package agent

import (
	"encoding/json"
	"errors"

	"gorm.io/gorm"

	"github.com/ravenmk2/wispjar/internal/store"
)

// Snapshot 是创建会话时写入 session.agent_snapshot 的智能体全量配置快照。
// 经 JSON 序列化存储；会话运行期间以快照为准，智能体后续变更不影响已有会话。
// 快照不含任何供应商密钥——密钥由 provider 工厂在运行时按 model_id 获取。
type Snapshot struct {
	SearchProviderID *string            `json:"search_provider_id,omitempty"`
	FetchProviderID  *string            `json:"fetch_provider_id,omitempty"`
	AgentID          string             `json:"agent_id"`
	Name             string             `json:"name"`
	Description      string             `json:"description"`
	Avatar           string             `json:"avatar"`
	SystemPrompt     string             `json:"system_prompt"`
	MaxIterations    int                `json:"max_iterations"`
	ModelParams      json.RawMessage    `json:"model_params,omitempty"`
	ToolsEnabled     json.RawMessage    `json:"tools_enabled"`
	SandboxConfig    json.RawMessage    `json:"sandbox_config,omitempty"`
	Model            SnapshotModel      `json:"model"`
	Skills           []SnapshotSkill    `json:"skills"`
	SubAgents        []SnapshotSubAgent `json:"sub_agents"`
}

// SnapshotModel 是快照中的模型解析结果：调用模型所需的全部非密钥信息。
type SnapshotModel struct {
	ID           string `json:"id"`
	ModelCode    string `json:"model_code"`
	DisplayName  string `json:"display_name"`
	ProviderID   string `json:"provider_id"`
	ProviderType string `json:"provider_type"`
	BaseURL      string `json:"base_url"`
}

// SnapshotSkill 是快照中的 skill 解析结果，meta 为完整 frontmatter 与文件清单。
type SnapshotSkill struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Meta        json.RawMessage `json:"meta"`
}

// SnapshotSubAgent 是快照中的子智能体解析结果：递归一层取其全量配置
// （子智能体层级限 1 层，故不再向下解析其子智能体）。
type SnapshotSubAgent struct {
	SearchProviderID *string         `json:"search_provider_id,omitempty"`
	FetchProviderID  *string         `json:"fetch_provider_id,omitempty"`
	ID               string          `json:"id"`
	Name             string          `json:"name"`
	Description      string          `json:"description"`
	Avatar           string          `json:"avatar"`
	SystemPrompt     string          `json:"system_prompt"`
	MaxIterations    int             `json:"max_iterations"`
	ModelParams      json.RawMessage `json:"model_params,omitempty"`
	ToolsEnabled     json.RawMessage `json:"tools_enabled"`
	SandboxConfig    json.RawMessage `json:"sandbox_config,omitempty"`
	Model            SnapshotModel   `json:"model"`
	Skills           []SnapshotSkill `json:"skills"`
}

// BuildSnapshot 构建指定智能体的会话快照（供 internal/session 创建会话时调用），
// 序列化为 JSON 后写入 session.agent_snapshot。
func (s *Service) BuildSnapshot(agentID string) (*Snapshot, error) {
	var a store.Agent
	if err := s.db.First(&a, "id = ?", agentID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errNotFound("智能体不存在")
		}
		return nil, err
	}
	model, err := s.snapshotModel(a.ModelID)
	if err != nil {
		return nil, err
	}
	skills, err := s.snapshotSkills(a.ID)
	if err != nil {
		return nil, err
	}
	subs, err := s.snapshotSubAgents(a.ID)
	if err != nil {
		return nil, err
	}
	return &Snapshot{
		SearchProviderID: a.SearchProviderID,
		FetchProviderID:  a.FetchProviderID,
		AgentID:          a.ID,
		Name:             a.Name,
		Description:      a.Description,
		Avatar:           a.Avatar,
		SystemPrompt:     a.SystemPrompt,
		MaxIterations:    a.MaxIterations,
		ModelParams:      a.ModelParams,
		ToolsEnabled:     a.ToolsEnabled,
		SandboxConfig:    a.SandboxConfig,
		Model:            *model,
		Skills:           skills,
		SubAgents:        subs,
	}, nil
}

func (s *Service) snapshotModel(modelID string) (*SnapshotModel, error) {
	var row struct {
		store.Model
		ProviderType string `gorm:"column:provider_type"`
		BaseURL      string `gorm:"column:base_url"`
	}
	err := s.db.Model(&store.Model{}).
		Select("model.*, model_provider.type AS provider_type, model_provider.base_url").
		Joins("JOIN model_provider ON model_provider.id = model.provider_id").
		Where("model.id = ?", modelID).
		Take(&row).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errNotFound("模型不存在")
		}
		return nil, err
	}
	return &SnapshotModel{
		ID:           row.ID,
		ModelCode:    row.ModelCode,
		DisplayName:  row.DisplayName,
		ProviderID:   row.ProviderID,
		ProviderType: row.ProviderType,
		BaseURL:      row.BaseURL,
	}, nil
}

func (s *Service) snapshotSkills(agentID string) ([]SnapshotSkill, error) {
	rows := []store.Skill{}
	err := s.db.Model(&store.Skill{}).
		Select("skill.*").
		Joins("JOIN agent_skill ON agent_skill.skill_id = skill.id").
		Where("agent_skill.agent_id = ?", agentID).
		Order("agent_skill.sort").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	skills := make([]SnapshotSkill, 0, len(rows))
	for _, r := range rows {
		skills = append(skills, SnapshotSkill{
			ID:          r.ID,
			Name:        r.Name,
			Description: r.Description,
			Meta:        r.Meta,
		})
	}
	return skills, nil
}

func (s *Service) snapshotSubAgents(agentID string) ([]SnapshotSubAgent, error) {
	rows := []store.Agent{}
	err := s.db.Model(&store.Agent{}).
		Select("agent.*").
		Joins("JOIN agent_subagent ON agent_subagent.sub_agent_id = agent.id").
		Where("agent_subagent.agent_id = ?", agentID).
		Order("agent_subagent.sort").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	subs := make([]SnapshotSubAgent, 0, len(rows))
	for _, r := range rows {
		model, err := s.snapshotModel(r.ModelID)
		if err != nil {
			return nil, err
		}
		skills, err := s.snapshotSkills(r.ID)
		if err != nil {
			return nil, err
		}
		subs = append(subs, SnapshotSubAgent{
			SearchProviderID: r.SearchProviderID,
			FetchProviderID:  r.FetchProviderID,
			ID:               r.ID,
			Name:             r.Name,
			Description:      r.Description,
			Avatar:           r.Avatar,
			SystemPrompt:     r.SystemPrompt,
			MaxIterations:    r.MaxIterations,
			ModelParams:      r.ModelParams,
			ToolsEnabled:     r.ToolsEnabled,
			SandboxConfig:    r.SandboxConfig,
			Model:            *model,
			Skills:           skills,
		})
	}
	return subs, nil
}
