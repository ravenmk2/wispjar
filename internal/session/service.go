package session

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/ravenmk2/wispjar/internal/agent"
	"github.com/ravenmk2/wispjar/internal/gateway/respond"
	"github.com/ravenmk2/wispjar/internal/id"
	"github.com/ravenmk2/wispjar/internal/store"
)

const (
	StatusActive   = "active"
	StatusArchived = "archived"
	StatusDeleted  = "deleted"
)

const (
	ApprovalStrict  = "strict"
	ApprovalDefault = "default"
	ApprovalAuto    = "auto"
)

const codeSessionNotFound = "session/not_found"

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
	return &Error{Status: http.StatusNotFound, Code: codeSessionNotFound, Message: msg}
}

type Service struct {
	db        *gorm.DB
	agent     *agent.Service
	store     *Store
	computers ComputerBindings

	// AfterEvents 在 PATCH 或 fork 事务提交后调用（WS 广播挂钩；生产接线，测试可为 nil）。
	// fork 只刷新 control 投影，events 为 nil，避免重新广播复制的历史事件。
	AfterEvents func(sessionID string, events []Event)
}

func NewService(db *gorm.DB, agentSvc *agent.Service) *Service {
	return &Service{db: db, agent: agentSvc, store: NewStore(db)}
}

func (s *Service) Store() *Store {
	return s.store
}

type AgentRef struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Avatar      string `json:"avatar"`
}

type Detail struct {
	store.Session
	Agent               AgentRef         `json:"agent"`
	EffortLocked        bool             `json:"effort_locked"`
	ModelSettingsLocked bool             `json:"model_settings_locked"`
	Computer            *ComputerSummary `json:"computer,omitempty"`
}

func detailOf(sess *store.Session, started bool) *Detail {
	d := &Detail{Session: *sess, EffortLocked: started, ModelSettingsLocked: started}
	var snap struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Avatar      string `json:"avatar"`
	}
	_ = json.Unmarshal(sess.AgentSnapshot, &snap)
	d.Agent = AgentRef{Name: snap.Name, Description: snap.Description, Avatar: snap.Avatar}
	return d
}

type CreateInput struct {
	ModelID         string
	AgentID         string
	Title           string
	ReasoningEffort string
	ComputerID      string
}

func (s *Service) Create(ctx context.Context, actorID, orgID string, in CreateInput) (*Detail, error) {
	agentID := strings.TrimSpace(in.AgentID)
	if agentID == "" {
		return nil, errInvalid("agent_id 必填", map[string]any{"field": "agent_id"})
	}
	title := strings.TrimSpace(in.Title)
	if len(title) > 256 {
		return nil, errInvalid("title 不超过 256 字符", map[string]any{"field": "title"})
	}
	var a store.Agent
	if err := s.db.WithContext(ctx).Where("id = ? AND org_id = ?", agentID, orgID).First(&a).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errNotFound("智能体不存在")
		}
		return nil, err
	}
	if a.Status != agent.StatusPublished {
		return nil, errInvalid("仅 published 状态的智能体可创建会话", map[string]any{"field": "agent_id", "status": a.Status})
	}
	snap, err := s.agent.BuildSnapshot(a.ID)
	if err != nil {
		return nil, err
	}
	snapRaw, err := json.Marshal(snap)
	if err != nil {
		return nil, err
	}
	modelID := a.ModelID
	if selected := strings.TrimSpace(in.ModelID); selected != "" {
		var m store.Model
		if err := s.db.WithContext(ctx).Where("id = ? AND status = ?", selected, StatusActive).First(&m).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, errInvalid("model_id 对应的模型不存在或未上架（active）", map[string]any{"field": "model_id"})
			}
			return nil, err
		}
		modelID = m.ID
	}
	modelParams, err := s.createModelParams(ctx, modelID, in.ReasoningEffort)
	if err != nil {
		return nil, err
	}
	sess := store.Session{
		ID:            id.New(),
		OrgID:         orgID,
		UserID:        actorID,
		AgentID:       a.ID,
		AgentSnapshot: snapRaw,
		Title:         title,
		TitlePinned:   title != "",
		ApprovalMode:  ApprovalAuto,
		ModelID:       modelID,
		ModelParams:   modelParams,
		UsageTotal:    zeroUsage(),
		Status:        StatusActive,
		LastActiveAt:  time.Now(),
	}
	if err := s.createComputer(ctx, &sess, in.ComputerID); err != nil {
		return nil, err
	}
	if err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockSessionComputer(tx, &sess); err != nil {
			return err
		}
		return tx.Create(&sess).Error
	}); err != nil {
		return nil, err
	}
	return s.detail(ctx, &sess, false)
}

// capabilities 是会话域关心的 model.capabilities 子集（思考强度档位声明、视觉能力）。
type capabilities struct {
	APIProtocol            string   `json:"api_protocol"`
	ToolImages             bool     `json:"tool_images"`
	Tools                  bool     `json:"tools"`
	Vision                 bool     `json:"vision"`
	ReasoningEfforts       []string `json:"reasoning_efforts"`
	ReasoningEffortDefault string   `json:"reasoning_effort_default"`
}

func (s *Service) modelCapabilities(ctx context.Context, modelID string) (capabilities, error) {
	var m store.Model
	if err := s.db.WithContext(ctx).Select("capabilities").First(&m, "id = ?", modelID).Error; err != nil {
		return capabilities{}, err
	}
	caps := capabilities{}
	_ = json.Unmarshal(m.Capabilities, &caps)
	return caps, nil
}

// createModelParams 计算新会话的 model_params：显式档位校验值域；
// 缺省回落模型默认档；无默认则不注入（NULL）。
func (s *Service) createModelParams(ctx context.Context, modelID, effort string) (json.RawMessage, error) {
	caps, err := s.modelCapabilities(ctx, modelID)
	if err != nil {
		return nil, err
	}
	effort = strings.TrimSpace(effort)
	if effort == "" {
		effort = caps.ReasoningEffortDefault
	} else if !effortAllowed(caps, effort) {
		return nil, errEffortInvalid(effort, caps)
	}
	if effort == "" {
		return nil, nil
	}
	return json.Marshal(map[string]any{"reasoning_effort": effort})
}

func effortAllowed(caps capabilities, effort string) bool {
	for _, e := range caps.ReasoningEfforts {
		if e == effort {
			return true
		}
	}
	return false
}

func errEffortInvalid(effort string, caps capabilities) *Error {
	return errInvalid("reasoning_effort 不在目标模型支持的档位内",
		map[string]any{"field": "reasoning_effort", "value": effort, "allowed": caps.ReasoningEfforts})
}

// currentEffort 从 model_params 取当前 reasoning_effort（无则空串）。
func currentEffort(raw json.RawMessage) string {
	m := map[string]any{}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &m)
	}
	s, _ := m["reasoning_effort"].(string)
	return s
}

// planEffortUpdate 校验目标模型档位并合并参数；锁定校验由事务内的更新入口完成。
func planEffortUpdate(sess *store.Session, caps capabilities, effort string) (json.RawMessage, error) {
	effort = strings.TrimSpace(effort)
	if effort == "" {
		effort = caps.ReasoningEffortDefault
	}
	if effort != "" && !effortAllowed(caps, effort) {
		return nil, errEffortInvalid(effort, caps)
	}
	m := map[string]any{}
	if len(sess.ModelParams) > 0 {
		_ = json.Unmarshal(sess.ModelParams, &m)
	}
	if effort == "" {
		delete(m, "reasoning_effort")
	} else {
		if m == nil {
			m = map[string]any{}
		}
		m["reasoning_effort"] = effort
	}
	if len(m) == 0 {
		return nil, nil
	}
	return json.Marshal(m)
}

type ListFilter struct {
	Q       string
	Status  string
	AgentID string
	Cursor  string
	Limit   int
}

type ListItem struct {
	ID           string          `json:"id"`
	AgentID      string          `json:"agent_id"`
	AgentName    string          `json:"agent_name"`
	Title        string          `json:"title"`
	Running      bool            `json:"running"`
	ApprovalMode string          `json:"approval_mode"`
	ModelID      string          `json:"model_id"`
	UsageTotal   json.RawMessage `json:"usage_total"`
	LastActiveAt time.Time       `json:"last_active_at"`
	CreatedAt    time.Time       `json:"created_at"`
}

func (s *Service) List(ctx context.Context, uid, orgID string, f ListFilter) ([]ListItem, string, error) {
	status := f.Status
	if status == "" {
		status = StatusActive
	}
	if status != StatusActive && status != StatusArchived {
		return nil, "", errInvalid("status 仅支持 active|archived", map[string]any{"status": f.Status})
	}
	type row struct {
		store.Session
		AgentName string `gorm:"column:agent_name"`
	}
	q := s.db.WithContext(ctx).Model(&store.Session{}).
		Select("session.*, agent.name AS agent_name").
		Joins("JOIN agent ON agent.id = session.agent_id").
		Where("session.org_id = ? AND session.user_id = ? AND session.status = ?", orgID, uid, status)
	if f.AgentID != "" {
		q = q.Where("session.agent_id = ?", f.AgentID)
	}
	if f.Q != "" {
		q = q.Where("session.title LIKE ?", likePattern(f.Q))
	}
	if f.Cursor != "" {
		t, cursorID, err := parseCursor(f.Cursor)
		if err != nil {
			return nil, "", err
		}
		q = q.Where("(session.last_active_at < ? OR (session.last_active_at = ? AND session.id < ?))", t, t, cursorID)
	}
	rows := []row{}
	if err := q.Order("session.last_active_at DESC, session.id DESC").
		Limit(f.Limit + 1).
		Scan(&rows).Error; err != nil {
		return nil, "", err
	}
	next := ""
	if len(rows) > f.Limit {
		next = formatCursor(rows[f.Limit-1].LastActiveAt, rows[f.Limit-1].ID)
		rows = rows[:f.Limit]
	}
	items := make([]ListItem, 0, len(rows))
	for _, r := range rows {
		items = append(items, ListItem{
			ID:           r.ID,
			AgentID:      r.AgentID,
			AgentName:    r.AgentName,
			Title:        r.Title,
			Running:      r.Running,
			ApprovalMode: r.ApprovalMode,
			ModelID:      r.ModelID,
			UsageTotal:   r.UsageTotal,
			LastActiveAt: r.LastActiveAt,
			CreatedAt:    r.CreatedAt,
		})
	}
	return items, next, nil
}

func (s *Service) ownedSession(ctx context.Context, uid, sessionID string) (*store.Session, error) {
	var sess store.Session
	if err := s.db.WithContext(ctx).First(&sess, "id = ?", sessionID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errNotFound("会话不存在或无权访问")
		}
		return nil, err
	}
	if sess.UserID != uid || sess.Status == StatusDeleted {
		return nil, errNotFound("会话不存在或无权访问")
	}
	return &sess, nil
}

func (s *Service) Get(ctx context.Context, uid, sessionID string) (*Detail, error) {
	sess, err := s.ownedSession(ctx, uid, sessionID)
	if err != nil {
		return nil, err
	}
	started, err := hasUserMessage(s.db.WithContext(ctx), sess.ID)
	if err != nil {
		return nil, err
	}
	return s.detail(ctx, sess, started)
}

type UpdateInput struct {
	Title           *string
	ApprovalMode    *string
	Status          *string
	ModelID         *string
	ReasoningEffort *string
	ComputerID      *string
}

func validateModelSettingsUpdate(sess *store.Session, started bool, in UpdateInput) error {
	if !started {
		return nil
	}
	if in.ModelID != nil {
		return &Error{Status: http.StatusConflict, Code: "session/model_locked", Message: "对话开始后不可更改模型，请新建对话", Details: map[string]any{"model_id": sess.ModelID}}
	}
	if in.ReasoningEffort != nil {
		return &Error{Status: http.StatusConflict, Code: "session/effort_locked", Message: "对话开始后不可更改推理等级，请新建对话", Details: map[string]any{"reasoning_effort": currentEffort(sess.ModelParams)}}
	}
	return nil
}

func (s *Service) Update(ctx context.Context, uid, sessionID string, in UpdateInput) (*Detail, error) {
	var committed []store.SessionEvent
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if in.ComputerID != nil {
			// Lifecycle operations lock computer before session. Hold the same
			// order so a concurrently deleted target cannot become a new binding.
			var target store.Computer
			err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND user_id = ? AND status NOT IN ?", *in.ComputerID, uid, []string{"deleting", "deleted"}).Take(&target).Error
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return &Error{Status: 404, Code: "computer/not_found", Message: "云电脑不存在或无权访问"}
			}
			if err != nil {
				return err
			}
		}
		var sess store.Session
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&sess, "id = ?", sessionID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return errNotFound("会话不存在或无权访问")
			}
			return err
		}
		if sess.UserID != uid || sess.Status == StatusDeleted {
			return errNotFound("会话不存在或无权访问")
		}
		started, err := hasUserMessage(tx, sessionID)
		if err != nil {
			return err
		}
		if err := validateModelSettingsUpdate(&sess, started, in); err != nil {
			return err
		}
		planner := *s
		planner.db = tx
		updates, events, err := planner.planUpdate(ctx, uid, &sess, in)
		if err != nil {
			return err
		}
		if len(updates) == 0 {
			return nil
		}
		if err := tx.Model(&store.Session{}).Where("id = ?", sess.ID).Updates(updates).Error; err != nil {
			return err
		}
		if len(events) > 0 {
			committed, err = appendEventsTx(tx, sess.ID, events)
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	if s.AfterEvents != nil && len(committed) > 0 {
		evs := make([]Event, 0, len(committed))
		for _, e := range committed {
			evs = append(evs, Event{Seq: e.Seq, Type: e.Type, Time: e.TimeMs, Data: e.Data})
		}
		s.AfterEvents(sessionID, evs)
	}
	return s.Get(ctx, uid, sessionID)
}

func (s *Service) planUpdate(ctx context.Context, uid string, sess *store.Session, in UpdateInput) (map[string]any, []EventInput, error) {
	updates := map[string]any{}
	events := []EventInput{}
	if in.ComputerID != nil {
		var err error
		updates, events, err = s.planComputerUpdate(ctx, sess, *in.ComputerID)
		if err != nil {
			return nil, nil, err
		}
	}
	if in.Title != nil {
		title := strings.TrimSpace(*in.Title)
		if len(title) > 256 {
			return nil, nil, errInvalid("title 不超过 256 字符", map[string]any{"field": "title"})
		}
		updates["title"] = title
		updates["title_pinned"] = true
		events = append(events, EventInput{Type: EventSessionTitle, Data: SessionTitleData{Title: title, Auto: false}})
	}
	if in.ApprovalMode != nil {
		mode := strings.TrimSpace(*in.ApprovalMode)
		if mode != ApprovalStrict && mode != ApprovalDefault && mode != ApprovalAuto {
			return nil, nil, errInvalid("approval_mode 仅支持 strict|default|auto", map[string]any{"field": "approval_mode"})
		}
		updates["approval_mode"] = mode
		events = append(events, EventInput{Type: EventSessionApprovalMode, Data: ApprovalModeData{Mode: mode, ChangedBy: uid}})
	}
	if in.Status != nil {
		status := strings.TrimSpace(*in.Status)
		if status != StatusActive && status != StatusArchived {
			return nil, nil, errInvalid("status 仅支持 active|archived", map[string]any{"field": "status"})
		}
		updates["status"] = status
	}
	if in.ModelID != nil {
		modelID := strings.TrimSpace(*in.ModelID)
		var m store.Model
		if err := s.db.WithContext(ctx).
			Where("id = ? AND status = ?", modelID, StatusActive).
			First(&m).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, nil, errInvalid("model_id 对应的模型不存在或未上架（active）", map[string]any{"field": "model_id"})
			}
			return nil, nil, err
		}
		caps := capabilities{}
		_ = json.Unmarshal(m.Capabilities, &caps)
		if !caps.Vision || !caps.Tools || !caps.ToolImages || caps.APIProtocol != "responses" {
			var count int64
			if err := s.db.WithContext(ctx).Model(&store.SessionEvent{}).Where("session_id = ? AND type = ? AND JSON_LENGTH(JSON_EXTRACT(data, '$.attachments')) > 0", sess.ID, EventToolResult).Count(&count).Error; err != nil {
				return nil, nil, err
			}
			if count > 0 {
				return nil, nil, &Error{Status: http.StatusConflict, Code: "session/media_locked", Message: "会话包含工具图片，目标模型须支持 Responses 工具图片", Details: map[string]any{"model_id": modelID}}
			}
		}
		if !caps.Vision {
			has, err := s.hasImageHistory(ctx, sess.ID)
			if err != nil {
				return nil, nil, err
			}
			if has {
				return nil, nil, &Error{Status: http.StatusConflict, Code: "session/vision_locked",
					Message: "会话历史包含图片，不能切换到不支持视觉的模型",
					Details: map[string]any{"model_id": modelID}}
			}
		}
		updates["model_id"] = modelID
		events = append(events, EventInput{Type: EventModelSelection, Data: ModelSelectionData{ModelID: modelID, ChangedBy: uid}})
	}
	if in.ReasoningEffort != nil || in.ModelID != nil {
		modelID := sess.ModelID
		if in.ModelID != nil {
			modelID = strings.TrimSpace(*in.ModelID)
		}
		caps, err := s.modelCapabilities(ctx, modelID)
		if err != nil {
			return nil, nil, err
		}
		effort := ""
		if in.ReasoningEffort != nil {
			effort = *in.ReasoningEffort
		}
		raw, err := planEffortUpdate(sess, caps, effort)
		if err != nil {
			return nil, nil, err
		}
		updates["model_params"] = raw
		events = append(events, EventInput{Type: EventSessionEffort, Data: SessionEffortData{Effort: currentEffort(raw), ChangedBy: uid}})
	}
	return updates, events, nil
}

// hasImageHistory 判断会话历史是否包含图片附件（PATCH 换模型的 vision 门控用）。
func (s *Service) hasImageHistory(ctx context.Context, sessionID string) (bool, error) {
	var n int64
	err := s.db.WithContext(ctx).Model(&store.SessionEvent{}).
		Where("session_id = ? AND type = ? AND JSON_LENGTH(JSON_EXTRACT(data, '$.attachments')) > 0",
			sessionID, EventUserMessage).
		Count(&n).Error
	return n > 0, err
}

func (s *Service) Delete(ctx context.Context, uid, sessionID string) error {
	sess, err := s.ownedSession(ctx, uid, sessionID)
	if err != nil {
		return err
	}
	return s.db.WithContext(ctx).Model(&store.Session{}).
		Where("id = ?", sess.ID).
		Update("status", StatusDeleted).Error
}

type Event struct {
	Seq  int64           `json:"seq"`
	Type string          `json:"type"`
	Time int64           `json:"time"`
	Data json.RawMessage `json:"data"`
}

func (s *Service) ListEvents(ctx context.Context, uid, sessionID string, afterSeq int64, limit int) ([]Event, int64, error) {
	sess, err := s.ownedSession(ctx, uid, sessionID)
	if err != nil {
		return nil, 0, err
	}
	rows, err := s.store.Events(ctx, sess.ID, afterSeq, limit)
	if err != nil {
		return nil, 0, err
	}
	items := make([]Event, 0, len(rows))
	for _, r := range rows {
		items = append(items, Event{Seq: r.Seq, Type: r.Type, Time: r.TimeMs, Data: r.Data})
	}
	return items, sess.LastSeq, nil
}

func parseCursor(cursor string) (time.Time, string, error) {
	msStr, cursorID, ok := strings.Cut(cursor, "_")
	ms, err := strconv.ParseInt(msStr, 10, 64)
	if !ok || cursorID == "" || err != nil {
		return time.Time{}, "", errInvalid("cursor 格式无效", map[string]any{"cursor": cursor})
	}
	return time.UnixMilli(ms), cursorID, nil
}

func formatCursor(t time.Time, id string) string {
	return strconv.FormatInt(t.UnixMilli(), 10) + "_" + id
}

func likePattern(q string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return "%" + r.Replace(q) + "%"
}
