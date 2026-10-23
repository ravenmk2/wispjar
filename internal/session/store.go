package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/ravenmk2/wispjar/internal/store"
)

type Store struct {
	db *gorm.DB

	// AfterAppend 在批量事件提交后调用（用量聚合挂钩；fork 复制不经此路径）。
	AfterAppend func(ctx context.Context, sessionID string, events []store.SessionEvent)
}

func NewStore(db *gorm.DB) *Store {
	return &Store{db: db}
}

type EventInput struct {
	// ExpectedLastSeq guards first-prompt configuration against concurrent PATCH requests.
	ExpectedLastSeq *int64
	// ExpectedTurn fences callbacks from a retired runtime generation.
	ExpectedTurn *int
	Type         string
	Data         any
}

func (s *Store) Append(ctx context.Context, sessionID, eventType string, data any) (*store.SessionEvent, error) {
	events, err := s.AppendBatch(ctx, sessionID, []EventInput{{Type: eventType, Data: data}})
	if err != nil {
		return nil, err
	}
	return &events[0], nil
}

func (s *Store) AppendBatch(ctx context.Context, sessionID string, inputs []EventInput) ([]store.SessionEvent, error) {
	if len(inputs) == 0 {
		return nil, nil
	}
	var out []store.SessionEvent
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		events, err := appendEventsTx(tx, sessionID, inputs)
		if err != nil {
			return err
		}
		out = events
		return nil
	})
	if err != nil {
		return nil, err
	}
	if s.AfterAppend != nil {
		s.AfterAppend(ctx, sessionID, out)
	}
	return out, nil
}

func appendEventsTx(tx *gorm.DB, sessionID string, inputs []EventInput) ([]store.SessionEvent, error) {
	var sess store.Session
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Select("id", "last_seq", "running", "usage_total", "workspace", "computer_id").
		First(&sess, "id = ?", sessionID).Error; err != nil {
		return nil, err
	}
	if err := validateEventSequence(sess.LastSeq, inputs); err != nil {
		return nil, err
	}
	// Read the lifecycle barrier before any other snapshot read in this
	// transaction. Recovery persists this state before deleting the instance;
	// an ensuing tool disconnect must not become a successful turn/end.
	computerStatus := ""
	if sess.ComputerID != nil {
		for _, in := range inputs {
			if in.Type == EventTurnStart || in.Type == EventTurnEnd {
				var computer store.Computer
				if err := tx.Select("status").First(&computer, "id = ?", *sess.ComputerID).Error; err != nil {
					return nil, err
				}
				computerStatus = computer.Status
				break
			}
		}
	}
	for _, in := range inputs {
		if in.ExpectedTurn != nil {
			var turn int64
			if err := tx.Model(&store.SessionEvent{}).Where("session_id = ? AND type = ?", sessionID, EventTurnStart).Count(&turn).Error; err != nil {
				return nil, err
			}
			if int64(*in.ExpectedTurn) != turn {
				return nil, &Error{Status: 409, Code: "session/turn_changed", Message: "会话已进入新的任务"}
			}
			break
		}
	}
	for _, in := range inputs {
		if in.Type == EventTurnStart {
			switch computerStatus {
			case "recovering", "deleting", "deleted":
				return nil, &Error{Status: 409, Code: "computer/busy", Message: "云电脑正在维护，暂时无法启动任务"}
			}
			if sess.Running {
				return nil, &Error{Status: 409, Code: "session/running", Message: "会话已有活动任务"}
			}
		}
	}
	seq := sess.LastSeq
	running := sess.Running
	usageTotal := sess.UsageTotal
	if len(usageTotal) == 0 {
		usageTotal = zeroUsage()
	}
	now := time.Now()
	timeMs := now.UnixMilli()
	out := make([]store.SessionEvent, 0, len(inputs))
	for _, in := range inputs {
		raw, err := marshalEventData(in.Data)
		if err != nil {
			return nil, err
		}
		if in.Type == EventTurnEnd && computerStatus == "recovering" {
			var end TurnEndData
			if err := json.Unmarshal(raw, &end); err != nil {
				return nil, err
			}
			end.Reason = "failed"
			end.ErrorCode = "computer/recovered"
			end.Error = "云电脑正在恢复，旧任务已终止"
			raw, err = marshalEventData(end)
			if err != nil {
				return nil, err
			}
		}
		seq++
		ev := store.SessionEvent{
			SessionID: sessionID,
			Seq:       seq,
			Type:      in.Type,
			TimeMs:    timeMs,
			Data:      raw,
		}
		if err := tx.Create(&ev).Error; err != nil {
			return nil, err
		}
		running = runningAfter(in.Type, running)
		if u, ok := usageFromEventData(raw); ok {
			usageTotal = addUsage(usageTotal, u)
		}
		out = append(out, ev)
	}
	updates := map[string]any{
		"last_seq":       seq,
		"running":        running,
		"usage_total":    usageTotal,
		"last_active_at": now,
	}
	for _, in := range inputs {
		if in.Type == "workspace/ready" {
			updates["workspace"] = json.RawMessage(`{"state":"ready","layout_version":1}`)
		}
	}
	err := tx.Model(&store.Session{}).Where("id = ?", sessionID).Updates(updates).Error
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Store) Events(ctx context.Context, sessionID string, afterSeq int64, limit int) ([]store.SessionEvent, error) {
	events := []store.SessionEvent{}
	q := s.db.WithContext(ctx).
		Where("session_id = ? AND seq > ?", sessionID, afterSeq).
		Order("seq")
	if limit > 0 {
		q = q.Limit(limit)
	}
	if err := q.Find(&events).Error; err != nil {
		return nil, err
	}
	return events, nil
}

func (s *Store) TailEvents(ctx context.Context, sessionID string, n int) ([]store.SessionEvent, error) {
	events := []store.SessionEvent{}
	if n <= 0 {
		return events, nil
	}
	if err := s.db.WithContext(ctx).
		Where("session_id = ?", sessionID).
		Order("seq DESC").
		Limit(n).
		Find(&events).Error; err != nil {
		return nil, err
	}
	for i, j := 0, len(events)-1; i < j; i, j = i+1, j-1 {
		events[i], events[j] = events[j], events[i]
	}
	return events, nil
}

func (s *Store) FindUserMessageByRequestID(ctx context.Context, sessionID, requestID string) (*store.SessionEvent, error) {
	if requestID == "" {
		return nil, nil
	}
	var ev store.SessionEvent
	err := s.db.WithContext(ctx).
		Where("session_id = ? AND type = ? AND JSON_UNQUOTE(JSON_EXTRACT(data, '$.request_id')) = ?",
			sessionID, EventUserMessage, requestID).
		Order("seq").
		First(&ev).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &ev, nil
}

func marshalEventData(data any) (json.RawMessage, error) {
	switch d := data.(type) {
	case nil:
		return json.RawMessage(`{}`), nil
	case json.RawMessage:
		if len(d) == 0 {
			return json.RawMessage(`{}`), nil
		}
		if !json.Valid(d) {
			return nil, fmt.Errorf("session: 事件 data 不是合法 JSON")
		}
		return d, nil
	default:
		b, err := json.Marshal(d)
		if err != nil {
			return nil, err
		}
		return b, nil
	}
}

func zeroUsage() json.RawMessage {
	b, _ := json.Marshal(Usage{})
	return b
}

func runningAfter(eventType string, running bool) bool {
	switch eventType {
	case EventTurnStart:
		return true
	case EventTurnEnd:
		return false
	default:
		return running
	}
}

func usageFromEventData(raw json.RawMessage) (Usage, bool) {
	var probe struct {
		Usage *Usage `json:"usage"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil || probe.Usage == nil {
		return Usage{}, false
	}
	return *probe.Usage, true
}

func addUsage(total json.RawMessage, delta Usage) json.RawMessage {
	u := Usage{}
	if len(total) > 0 {
		_ = json.Unmarshal(total, &u)
	}
	u.InputTokens += delta.InputTokens
	u.OutputTokens += delta.OutputTokens
	u.CacheReadTokens += delta.CacheReadTokens
	u.CacheWriteTokens += delta.CacheWriteTokens
	u.ReasoningTokens += delta.ReasoningTokens
	b, err := json.Marshal(u)
	if err != nil {
		return total
	}
	return b
}

func validateEventSequence(lastSeq int64, inputs []EventInput) error {
	for _, in := range inputs {
		if in.ExpectedLastSeq != nil && *in.ExpectedLastSeq != lastSeq {
			return &Error{Status: http.StatusConflict, Code: "session/config_changed", Message: "对话配置已更新，请重新发送消息"}
		}
	}
	return nil
}

func hasUserMessage(db *gorm.DB, sessionID string) (bool, error) {
	var events []store.SessionEvent
	err := db.Select("seq").Where("session_id = ? AND type = ?", sessionID, EventUserMessage).Limit(1).Find(&events).Error
	return len(events) > 0, err
}

// HasUserMessage examines the full history, including messages outside a follow snapshot window.
func (s *Store) HasUserMessage(ctx context.Context, sessionID string) (bool, error) {
	return hasUserMessage(s.db.WithContext(ctx), sessionID)
}
