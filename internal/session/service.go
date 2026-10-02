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

var defaultSandbox = json.RawMessage(`{"id":"","status":"none"}`)

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
	db    *gorm.DB
	agent *agent.Service
	store *Store

	// AfterEvents 在 PATCH 附带的事件随事务提交后调用（WS 广播挂钩；生产接线，测试可为 nil）。
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
	Agent AgentRef `json:"agent"`
}

func detailOf(sess *store.Session) *Detail {
	d := &Detail{Session: *sess}
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
	AgentID string
	Title   string
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
	sess := store.Session{
		ID:            id.New(),
		OrgID:         orgID,
		UserID:        actorID,
		AgentID:       a.ID,
		AgentSnapshot: snapRaw,
		Title:         title,
		TitlePinned:   title != "",
		ApprovalMode:  ApprovalDefault,
		ModelID:       a.ModelID,
		Sandbox:       defaultSandbox,
		UsageTotal:    zeroUsage(),
		Status:        StatusActive,
		LastActiveAt:  time.Now(),
	}
	if err := s.db.WithContext(ctx).Create(&sess).Error; err != nil {
		return nil, err
	}
	return detailOf(&sess), nil
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
	return detailOf(sess), nil
}

type UpdateInput struct {
	Title        *string
	ApprovalMode *string
	Status       *string
	ModelID      *string
}

func (s *Service) Update(ctx context.Context, uid, sessionID string, in UpdateInput) (*Detail, error) {
	sess, err := s.ownedSession(ctx, uid, sessionID)
	if err != nil {
		return nil, err
	}
	updates := map[string]any{}
	events := []EventInput{}
	if in.Title != nil {
		title := strings.TrimSpace(*in.Title)
		if len(title) > 256 {
			return nil, errInvalid("title 不超过 256 字符", map[string]any{"field": "title"})
		}
		updates["title"] = title
		updates["title_pinned"] = true
		events = append(events, EventInput{Type: EventSessionTitle, Data: SessionTitleData{Title: title, Auto: false}})
	}
	if in.ApprovalMode != nil {
		mode := strings.TrimSpace(*in.ApprovalMode)
		if mode != ApprovalStrict && mode != ApprovalDefault && mode != ApprovalAuto {
			return nil, errInvalid("approval_mode 仅支持 strict|default|auto", map[string]any{"field": "approval_mode"})
		}
		updates["approval_mode"] = mode
		events = append(events, EventInput{Type: EventSessionApprovalMode, Data: ApprovalModeData{Mode: mode, ChangedBy: uid}})
	}
	if in.Status != nil {
		status := strings.TrimSpace(*in.Status)
		if status != StatusActive && status != StatusArchived {
			return nil, errInvalid("status 仅支持 active|archived", map[string]any{"field": "status"})
		}
		updates["status"] = status
	}
	if in.ModelID != nil {
		modelID := strings.TrimSpace(*in.ModelID)
		var count int64
		if err := s.db.WithContext(ctx).Model(&store.Model{}).
			Where("id = ? AND status = ?", modelID, StatusActive).
			Count(&count).Error; err != nil {
			return nil, err
		}
		if count == 0 {
			return nil, errInvalid("model_id 对应的模型不存在或未上架（active）", map[string]any{"field": "model_id"})
		}
		updates["model_id"] = modelID
		events = append(events, EventInput{Type: EventModelSelection, Data: ModelSelectionData{ModelID: modelID, ChangedBy: uid}})
	}
	if len(updates) == 0 {
		return detailOf(sess), nil
	}
	var committed []store.SessionEvent
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&store.Session{}).Where("id = ?", sess.ID).Updates(updates).Error; err != nil {
			return err
		}
		if len(events) > 0 {
			evs, err := appendEventsTx(tx, sess.ID, events)
			if err != nil {
				return err
			}
			committed = evs
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if s.AfterEvents != nil {
		evs := make([]Event, 0, len(committed))
		for _, e := range committed {
			evs = append(evs, Event{Seq: e.Seq, Type: e.Type, Time: e.TimeMs, Data: e.Data})
		}
		s.AfterEvents(sess.ID, evs)
	}
	return s.Get(ctx, uid, sessionID)
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
