package agent

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/ravenmk2/wispjar/internal/audit"
	"github.com/ravenmk2/wispjar/internal/gateway/respond"
	"github.com/ravenmk2/wispjar/internal/id"
	"github.com/ravenmk2/wispjar/internal/store"
)

const (
	StatusDraft     = "draft"
	StatusPublished = "published"
	StatusArchived  = "archived"
)

type Error struct {
	Status  int
	Code    string
	Message string
	Details any
}

func (e *Error) Error() string { return e.Message }

func errInvalid(msg string, details any) *Error {
	return &Error{Status: http.StatusBadRequest, Code: respond.CodeInvalid, Message: msg, Details: details}
}

func errNotFound(msg string) *Error {
	return &Error{Status: http.StatusNotFound, Code: respond.CodeNotFound, Message: msg}
}

func errConflict(msg string, details any) *Error {
	return &Error{Status: http.StatusConflict, Code: respond.CodeConflict, Message: msg, Details: details}
}

type Service struct {
	db *gorm.DB
}

func NewService(db *gorm.DB) *Service {
	return &Service{db: db}
}

type ToolsEnabled struct {
	Exec  bool `json:"exec"`
	Read  bool `json:"read"`
	Write bool `json:"write"`
	Edit  bool `json:"edit"`
	Glob  bool `json:"glob"`
	Grep  bool `json:"grep"`
}

func defaultToolsEnabled() json.RawMessage {
	b, _ := json.Marshal(ToolsEnabled{Exec: true, Read: true, Write: true, Edit: true, Glob: true, Grep: true})
	return b
}

func nullAsEmpty(raw json.RawMessage) json.RawMessage {
	if strings.TrimSpace(string(raw)) == "null" {
		return nil
	}
	return raw
}

func parseModelParams(raw json.RawMessage) (json.RawMessage, error) {
	raw = nullAsEmpty(raw)
	if len(raw) == 0 {
		return nil, nil
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, errInvalid("model_params 必须是 JSON 对象", map[string]any{"field": "model_params"})
	}
	return raw, nil
}

func parseToolsEnabled(raw json.RawMessage) (json.RawMessage, error) {
	raw = nullAsEmpty(raw)
	if len(raw) == 0 {
		return defaultToolsEnabled(), nil
	}
	tools := ToolsEnabled{}
	if err := json.Unmarshal(raw, &tools); err != nil {
		return nil, errInvalid("tools_enabled 必须是 {exec,read,write,edit,glob,grep} 布尔对象", map[string]any{"field": "tools_enabled"})
	}
	b, err := json.Marshal(tools)
	if err != nil {
		return nil, err
	}
	return b, nil
}

type SandboxResources struct {
	CPU    string `json:"cpu,omitempty"`
	Memory string `json:"memory,omitempty"`
}

type SandboxConfig struct {
	Image     string            `json:"image"`
	Resources *SandboxResources `json:"resources"`
}

func parseSandboxConfig(raw json.RawMessage) (json.RawMessage, error) {
	raw = nullAsEmpty(raw)
	if len(raw) == 0 {
		return nil, nil
	}
	cfg := SandboxConfig{}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, errInvalid("sandbox_config 必须是 {image, resources:{cpu,memory}} 对象", map[string]any{"field": "sandbox_config"})
	}
	b, err := json.Marshal(cfg)
	if err != nil {
		return nil, err
	}
	return b, nil
}

type AgentInput struct {
	Name          string
	Description   string
	Avatar        string
	SystemPrompt  string
	ModelID       string
	ModelParams   json.RawMessage
	MaxIterations int
	ToolsEnabled  json.RawMessage
	SkillIDs      []string
	SubAgentIDs   []string
	SandboxConfig json.RawMessage
}

func dedupe(ids []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(ids))
	for _, v := range ids {
		v = strings.TrimSpace(v)
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

type validatedInput struct {
	name          string
	description   string
	avatar        string
	systemPrompt  string
	modelID       string
	modelParams   json.RawMessage
	maxIterations int
	toolsEnabled  json.RawMessage
	skillIDs      []string
	subAgentIDs   []string
	sandboxConfig json.RawMessage
}

func (s *Service) validateInput(orgID, agentID string, in AgentInput) (*validatedInput, error) {
	v := &validatedInput{
		name:         strings.TrimSpace(in.Name),
		description:  strings.TrimSpace(in.Description),
		avatar:       strings.TrimSpace(in.Avatar),
		systemPrompt: in.SystemPrompt,
		modelID:      strings.TrimSpace(in.ModelID),
	}
	if v.name == "" || len(v.name) > 128 {
		return nil, errInvalid("name 必填且不超过 128 字符", map[string]any{"field": "name"})
	}
	if len(v.description) > 1024 {
		return nil, errInvalid("description 不超过 1024 字符", map[string]any{"field": "description"})
	}
	if len(v.avatar) > 512 {
		return nil, errInvalid("avatar 不超过 512 字符", map[string]any{"field": "avatar"})
	}
	if v.modelID == "" {
		return nil, errInvalid("model_id 必填", map[string]any{"field": "model_id"})
	}
	var modelCount int64
	if err := s.db.Model(&store.Model{}).
		Where("id = ? AND status = ?", v.modelID, "active").
		Count(&modelCount).Error; err != nil {
		return nil, err
	}
	if modelCount == 0 {
		return nil, errInvalid("model_id 对应的模型不存在或未上架（active）", map[string]any{"field": "model_id"})
	}
	v.maxIterations = in.MaxIterations
	if v.maxIterations == 0 {
		v.maxIterations = 20
	}
	if v.maxIterations < 1 || v.maxIterations > 200 {
		return nil, errInvalid("max_iterations 必须是 1..200", map[string]any{"field": "max_iterations"})
	}
	var err error
	if v.modelParams, err = parseModelParams(in.ModelParams); err != nil {
		return nil, err
	}
	if v.toolsEnabled, err = parseToolsEnabled(in.ToolsEnabled); err != nil {
		return nil, err
	}
	if v.sandboxConfig, err = parseSandboxConfig(in.SandboxConfig); err != nil {
		return nil, err
	}
	v.skillIDs = dedupe(in.SkillIDs)
	v.subAgentIDs = dedupe(in.SubAgentIDs)
	if len(v.skillIDs) > 0 {
		var skillCount int64
		if err := s.db.Model(&store.Skill{}).
			Where("org_id = ? AND id IN ?", orgID, v.skillIDs).
			Count(&skillCount).Error; err != nil {
			return nil, err
		}
		if int(skillCount) != len(v.skillIDs) {
			return nil, errInvalid("部分 skill 不存在或不属于本机构", map[string]any{"field": "skill_ids"})
		}
	}
	if len(v.subAgentIDs) > 0 {
		subs, err := s.subAgentsByIDs(orgID, v.subAgentIDs)
		if err != nil {
			return nil, err
		}
		for _, sub := range subs {
			if sub.Status != StatusPublished {
				return nil, errInvalid("仅 published 状态的智能体可被委派为子智能体", map[string]any{"sub_agent_id": sub.ID, "status": sub.Status})
			}
		}
		edges, err := s.subagentEdges(orgID, agentID)
		if err != nil {
			return nil, err
		}
		if dagErr := validateSubagentDAG(agentID, v.subAgentIDs, edges); dagErr != nil {
			return nil, dagErr
		}
	}
	return v, nil
}

func (s *Service) subAgentsByIDs(orgID string, ids []string) ([]store.Agent, error) {
	subs := []store.Agent{}
	if err := s.db.Where("org_id = ? AND id IN ?", orgID, ids).Find(&subs).Error; err != nil {
		return nil, err
	}
	if len(subs) != len(ids) {
		return nil, errInvalid("部分子智能体不存在或不属于本机构", map[string]any{"field": "sub_agent_ids"})
	}
	return subs, nil
}

func (s *Service) subagentEdges(orgID, excludeAgentID string) (map[string][]string, error) {
	rows := []store.AgentSubagent{}
	err := s.db.Model(&store.AgentSubagent{}).
		Select("agent_subagent.*").
		Joins("JOIN agent ON agent.id = agent_subagent.agent_id").
		Where("agent.org_id = ? AND agent_subagent.agent_id <> ?", orgID, excludeAgentID).
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	edges := make(map[string][]string, len(rows))
	for _, r := range rows {
		edges[r.AgentID] = append(edges[r.AgentID], r.SubAgentID)
	}
	return edges, nil
}

func replaceRelations(tx *gorm.DB, agentID string, skillIDs, subAgentIDs []string) error {
	if err := tx.Where("agent_id = ?", agentID).Delete(&store.AgentSkill{}).Error; err != nil {
		return err
	}
	if err := tx.Where("agent_id = ?", agentID).Delete(&store.AgentSubagent{}).Error; err != nil {
		return err
	}
	for i, skillID := range skillIDs {
		if err := tx.Create(&store.AgentSkill{AgentID: agentID, SkillID: skillID, Sort: i}).Error; err != nil {
			return err
		}
	}
	for i, subID := range subAgentIDs {
		if err := tx.Create(&store.AgentSubagent{AgentID: agentID, SubAgentID: subID, Sort: i}).Error; err != nil {
			return err
		}
	}
	return nil
}

type ModelRef struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
	ModelCode   string `json:"model_code"`
	Status      string `json:"status"`
}

type SkillRef struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

type SubAgentRef struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Status      string `json:"status"`
}

type Detail struct {
	store.Agent
	Model               *ModelRef     `json:"model"`
	Skills              []SkillRef    `json:"skills"`
	SubAgents           []SubAgentRef `json:"sub_agents"`
	TransitionedToDraft bool          `json:"transitioned_to_draft"`
}

func (s *Service) agentOf(orgID, agentID string) (*store.Agent, error) {
	var a store.Agent
	if err := s.db.Where("id = ? AND org_id = ?", agentID, orgID).First(&a).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errNotFound("智能体不存在")
		}
		return nil, err
	}
	return &a, nil
}

func (s *Service) detailOf(a *store.Agent) (*Detail, error) {
	d := &Detail{Agent: *a, Skills: []SkillRef{}, SubAgents: []SubAgentRef{}}
	var m store.Model
	if err := s.db.First(&m, "id = ?", a.ModelID).Error; err == nil {
		d.Model = &ModelRef{ID: m.ID, DisplayName: m.DisplayName, ModelCode: m.ModelCode, Status: m.Status}
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	skillRows := []store.Skill{}
	if err := s.db.Model(&store.Skill{}).
		Select("skill.*").
		Joins("JOIN agent_skill ON agent_skill.skill_id = skill.id").
		Where("agent_skill.agent_id = ?", a.ID).
		Order("agent_skill.sort").
		Scan(&skillRows).Error; err != nil {
		return nil, err
	}
	for _, r := range skillRows {
		d.Skills = append(d.Skills, SkillRef{ID: r.ID, Name: r.Name, Description: r.Description})
	}
	subRows := []store.Agent{}
	if err := s.db.Model(&store.Agent{}).
		Select("agent.*").
		Joins("JOIN agent_subagent ON agent_subagent.sub_agent_id = agent.id").
		Where("agent_subagent.agent_id = ?", a.ID).
		Order("agent_subagent.sort").
		Scan(&subRows).Error; err != nil {
		return nil, err
	}
	for _, r := range subRows {
		d.SubAgents = append(d.SubAgents, SubAgentRef{ID: r.ID, Name: r.Name, Description: r.Description, Status: r.Status})
	}
	return d, nil
}

type ListItem struct {
	ID               string    `json:"id"`
	Name             string    `json:"name"`
	Description      string    `json:"description"`
	Avatar           string    `json:"avatar"`
	Status           string    `json:"status"`
	ModelID          string    `json:"model_id"`
	ModelDisplayName string    `json:"model_display_name"`
	MaxIterations    int       `json:"max_iterations"`
	SkillCount       int       `json:"skill_count"`
	SubagentCount    int       `json:"subagent_count"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

func (s *Service) List(orgID, cursor string, limit int) ([]ListItem, string, error) {
	type row struct {
		store.Agent
		ModelDisplayName string `gorm:"column:model_display_name"`
	}
	q := s.db.Model(&store.Agent{}).
		Select("agent.*, model.display_name AS model_display_name").
		Joins("JOIN model ON model.id = agent.model_id").
		Where("agent.org_id = ?", orgID).
		Order("agent.id DESC").
		Limit(limit + 1)
	if cursor != "" {
		q = q.Where("agent.id < ?", cursor)
	}
	rows := []row{}
	if err := q.Scan(&rows).Error; err != nil {
		return nil, "", err
	}
	next := ""
	if len(rows) > limit {
		next = rows[limit-1].ID
		rows = rows[:limit]
	}
	ids := make([]string, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.ID)
	}
	skillCounts, err := s.relationCounts(&store.AgentSkill{}, ids)
	if err != nil {
		return nil, "", err
	}
	subCounts, err := s.relationCounts(&store.AgentSubagent{}, ids)
	if err != nil {
		return nil, "", err
	}
	items := make([]ListItem, 0, len(rows))
	for _, r := range rows {
		items = append(items, ListItem{
			ID:               r.ID,
			Name:             r.Name,
			Description:      r.Description,
			Avatar:           r.Avatar,
			Status:           r.Status,
			ModelID:          r.ModelID,
			ModelDisplayName: r.ModelDisplayName,
			MaxIterations:    r.MaxIterations,
			SkillCount:       skillCounts[r.ID],
			SubagentCount:    subCounts[r.ID],
			CreatedAt:        r.CreatedAt,
			UpdatedAt:        r.UpdatedAt,
		})
	}
	return items, next, nil
}

func (s *Service) relationCounts(model any, agentIDs []string) (map[string]int, error) {
	counts := map[string]int{}
	if len(agentIDs) == 0 {
		return counts, nil
	}
	type countRow struct {
		AgentID string
		Cnt     int
	}
	rows := []countRow{}
	err := s.db.Model(model).
		Select("agent_id, COUNT(*) AS cnt").
		Where("agent_id IN ?", agentIDs).
		Group("agent_id").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		counts[r.AgentID] = r.Cnt
	}
	return counts, nil
}

type WorkspaceAgent struct {
	ID               string    `json:"id"`
	Name             string    `json:"name"`
	Description      string    `json:"description"`
	Avatar           string    `json:"avatar"`
	ModelID          string    `json:"model_id"`
	ModelDisplayName string    `json:"model_display_name"`
	CreatedAt        time.Time `json:"created_at"`
}

func (s *Service) ListPublished(orgID string) ([]WorkspaceAgent, error) {
	rows := []WorkspaceAgent{}
	err := s.db.Model(&store.Agent{}).
		Select("agent.id, agent.name, agent.description, agent.avatar, agent.model_id, model.display_name AS model_display_name, agent.created_at").
		Joins("JOIN model ON model.id = agent.model_id").
		Where("agent.org_id = ? AND agent.status = ?", orgID, StatusPublished).
		Order("agent.created_at DESC").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	return rows, nil
}

func (s *Service) Get(orgID, agentID string) (*Detail, error) {
	a, err := s.agentOf(orgID, agentID)
	if err != nil {
		return nil, err
	}
	return s.detailOf(a)
}

func (s *Service) Create(actorID, orgID string, in AgentInput) (*Detail, error) {
	v, err := s.validateInput(orgID, "", in)
	if err != nil {
		return nil, err
	}
	a := store.Agent{
		ID:            id.New(),
		OrgID:         orgID,
		Name:          v.name,
		Description:   v.description,
		Avatar:        v.avatar,
		SystemPrompt:  v.systemPrompt,
		ModelID:       v.modelID,
		ModelParams:   v.modelParams,
		MaxIterations: v.maxIterations,
		ToolsEnabled:  v.toolsEnabled,
		SandboxConfig: v.sandboxConfig,
		Status:        StatusDraft,
		CreatedBy:     actorID,
	}
	err = s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&a).Error; err != nil {
			return err
		}
		if err := replaceRelations(tx, a.ID, v.skillIDs, v.subAgentIDs); err != nil {
			return err
		}
		return audit.Log(tx, audit.Entry{
			OrgID:   orgID,
			ActorID: actorID,
			Action:  audit.AgentCreate,
			Target:  a.ID,
			Detail:  map[string]any{"name": a.Name, "model_id": a.ModelID},
		})
	})
	if err != nil {
		return nil, err
	}
	return s.detailOf(&a)
}

func (s *Service) Update(actorID, orgID, agentID string, in AgentInput) (*Detail, error) {
	a, err := s.agentOf(orgID, agentID)
	if err != nil {
		return nil, err
	}
	v, err := s.validateInput(orgID, agentID, in)
	if err != nil {
		return nil, err
	}
	transitioned := a.Status == StatusPublished
	updates := map[string]any{
		"name":           v.name,
		"description":    v.description,
		"avatar":         v.avatar,
		"system_prompt":  v.systemPrompt,
		"model_id":       v.modelID,
		"model_params":   v.modelParams,
		"max_iterations": v.maxIterations,
		"tools_enabled":  v.toolsEnabled,
		"sandbox_config": v.sandboxConfig,
	}
	if transitioned {
		updates["status"] = StatusDraft
	}
	err = s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&store.Agent{}).Where("id = ?", a.ID).Updates(updates).Error; err != nil {
			return err
		}
		if err := replaceRelations(tx, a.ID, v.skillIDs, v.subAgentIDs); err != nil {
			return err
		}
		return audit.Log(tx, audit.Entry{
			OrgID:   orgID,
			ActorID: actorID,
			Action:  audit.AgentUpdate,
			Target:  a.ID,
			Detail:  map[string]any{"name": v.name, "transitioned_to_draft": transitioned},
		})
	})
	if err != nil {
		return nil, err
	}
	if err := s.db.First(a, "id = ?", a.ID).Error; err != nil {
		return nil, err
	}
	d, err := s.detailOf(a)
	if err != nil {
		return nil, err
	}
	d.TransitionedToDraft = transitioned
	return d, nil
}

func (s *Service) Publish(actorID, orgID, agentID string) (*Detail, error) {
	a, err := s.agentOf(orgID, agentID)
	if err != nil {
		return nil, err
	}
	modelActive := false
	var m store.Model
	if err := s.db.First(&m, "id = ?", a.ModelID).Error; err == nil {
		modelActive = m.Status == "active"
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	subs := []store.Agent{}
	if err := s.db.Model(&store.Agent{}).
		Select("agent.*").
		Joins("JOIN agent_subagent ON agent_subagent.sub_agent_id = agent.id").
		Where("agent_subagent.agent_id = ?", a.ID).
		Scan(&subs).Error; err != nil {
		return nil, err
	}
	subIDs := make([]string, 0, len(subs))
	for _, sub := range subs {
		subIDs = append(subIDs, sub.ID)
	}
	edges, err := s.subagentEdges(orgID, a.ID)
	if err != nil {
		return nil, err
	}
	if dagErr := validateSubagentDAG(a.ID, subIDs, edges); dagErr != nil {
		return nil, dagErr
	}
	if pubErr := validatePublishable(a, modelActive, subs); pubErr != nil {
		return nil, pubErr
	}
	if a.Status != StatusPublished {
		err = s.db.Transaction(func(tx *gorm.DB) error {
			if err := tx.Model(&store.Agent{}).Where("id = ?", a.ID).Update("status", StatusPublished).Error; err != nil {
				return err
			}
			return audit.Log(tx, audit.Entry{
				OrgID:   orgID,
				ActorID: actorID,
				Action:  audit.AgentPublish,
				Target:  a.ID,
				Detail:  map[string]any{"name": a.Name},
			})
		})
		if err != nil {
			return nil, err
		}
		a.Status = StatusPublished
	}
	return s.detailOf(a)
}

func (s *Service) Archive(actorID, orgID, agentID string) (*Detail, error) {
	a, err := s.agentOf(orgID, agentID)
	if err != nil {
		return nil, err
	}
	if a.Status != StatusArchived {
		from := a.Status
		err = s.db.Transaction(func(tx *gorm.DB) error {
			if err := tx.Model(&store.Agent{}).Where("id = ?", a.ID).Update("status", StatusArchived).Error; err != nil {
				return err
			}
			return audit.Log(tx, audit.Entry{
				OrgID:   orgID,
				ActorID: actorID,
				Action:  audit.AgentArchive,
				Target:  a.ID,
				Detail:  map[string]any{"name": a.Name, "from": from},
			})
		})
		if err != nil {
			return nil, err
		}
		a.Status = StatusArchived
	}
	return s.detailOf(a)
}

func (s *Service) Delete(actorID, orgID, agentID string) error {
	a, err := s.agentOf(orgID, agentID)
	if err != nil {
		return err
	}
	if a.Status == StatusPublished {
		return errConflict("published 状态的智能体不可删除，请先下线（archive）", nil)
	}
	var sessionCount int64
	if err := s.db.Model(&store.Session{}).Where("agent_id = ?", a.ID).Count(&sessionCount).Error; err != nil {
		return err
	}
	if sessionCount > 0 {
		return errConflict("该智能体已被会话引用，不可删除，只能下线（archive）", map[string]any{"sessions": sessionCount})
	}
	return s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("sub_agent_id = ?", a.ID).Delete(&store.AgentSubagent{}).Error; err != nil {
			return err
		}
		if err := tx.Delete(&store.Agent{}, "id = ?", a.ID).Error; err != nil {
			return err
		}
		return audit.Log(tx, audit.Entry{
			OrgID:   orgID,
			ActorID: actorID,
			Action:  audit.AgentDelete,
			Target:  a.ID,
			Detail:  map[string]any{"name": a.Name, "status": a.Status},
		})
	})
}
