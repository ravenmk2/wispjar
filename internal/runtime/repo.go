package runtime

import (
	"context"
	"encoding/json"
	"errors"

	"gorm.io/gorm"

	"github.com/ravenmk2/wispjar/internal/session"
	"github.com/ravenmk2/wispjar/internal/store"
)

// EventStore 是 session.Store 的子集接口，便于测试替换。
type EventStore interface {
	AppendBatch(ctx context.Context, sessionID string, inputs []session.EventInput) ([]store.SessionEvent, error)
	Events(ctx context.Context, sessionID string, afterSeq int64, limit int) ([]store.SessionEvent, error)
	TailEvents(ctx context.Context, sessionID string, n int) ([]store.SessionEvent, error)
	FindUserMessageByRequestID(ctx context.Context, sessionID, requestID string) (*store.SessionEvent, error)
}

// SessionRepo 是 runtime 对 session 行的窄接口（gorm 实现 / 测试 fake）。
type SessionRepo interface {
	SessionByID(ctx context.Context, sessionID string) (*store.Session, error)
	TurnCount(ctx context.Context, sessionID string) (int, error)
	SetTitleIfUnpinned(ctx context.Context, sessionID, title string) (bool, error)
	SetSandboxStatus(ctx context.Context, sessionID, sandboxID, status string) error
	ApprovalDecision(ctx context.Context, sessionID, callID string) (decided bool, auto bool, err error)
	QuestionAsked(ctx context.Context, sessionID, callID string) (*session.QuestionAskedData, error)
	QuestionAnswered(ctx context.Context, sessionID, callID string) (bool, error)
	RunningSessions(ctx context.Context) ([]store.Session, error)
	UndecidedApprovals(ctx context.Context, sessionID string) ([]session.ApprovalAskedData, error)
}

type gormRepo struct {
	db *gorm.DB
}

func NewSessionRepo(db *gorm.DB) SessionRepo {
	return &gormRepo{db: db}
}

func (r *gormRepo) SessionByID(ctx context.Context, sessionID string) (*store.Session, error) {
	var sess store.Session
	if err := r.db.WithContext(ctx).First(&sess, "id = ?", sessionID).Error; err != nil {
		return nil, err
	}
	return &sess, nil
}

func (r *gormRepo) TurnCount(ctx context.Context, sessionID string) (int, error) {
	var n int64
	err := r.db.WithContext(ctx).Model(&store.SessionEvent{}).
		Where("session_id = ? AND type = ?", sessionID, session.EventTurnStart).
		Count(&n).Error
	return int(n), err
}

func (r *gormRepo) SetTitleIfUnpinned(ctx context.Context, sessionID, title string) (bool, error) {
	res := r.db.WithContext(ctx).Model(&store.Session{}).
		Where("id = ? AND title_pinned = 0", sessionID).
		Update("title", title)
	return res.RowsAffected > 0, res.Error
}

func (r *gormRepo) SetSandboxStatus(ctx context.Context, sessionID, sandboxID, status string) error {
	raw, _ := json.Marshal(map[string]string{"id": sandboxID, "status": status})
	return r.db.WithContext(ctx).Model(&store.Session{}).
		Where("id = ?", sessionID).
		Update("sandbox", raw).Error
}

func (r *gormRepo) ApprovalDecision(ctx context.Context, sessionID, callID string) (bool, bool, error) {
	var ev store.SessionEvent
	err := r.db.WithContext(ctx).
		Where("session_id = ? AND type = ? AND JSON_UNQUOTE(JSON_EXTRACT(data, '$.call_id')) = ?",
			sessionID, session.EventApprovalDecided, callID).
		First(&ev).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, false, nil
		}
		return false, false, err
	}
	var d session.ApprovalDecidedData
	_ = json.Unmarshal(ev.Data, &d)
	return true, d.Auto, nil
}

// QuestionAsked 取某次提问的 asked 事件（无则 nil）；continued 判定 = asked 存在且无 answered。
func (r *gormRepo) QuestionAsked(ctx context.Context, sessionID, callID string) (*session.QuestionAskedData, error) {
	var ev store.SessionEvent
	err := r.db.WithContext(ctx).
		Where("session_id = ? AND type = ? AND JSON_UNQUOTE(JSON_EXTRACT(data, '$.call_id')) = ?",
			sessionID, session.EventQuestionAsked, callID).
		First(&ev).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	var d session.QuestionAskedData
	if err := json.Unmarshal(ev.Data, &d); err != nil {
		return nil, err
	}
	return &d, nil
}

func (r *gormRepo) QuestionAnswered(ctx context.Context, sessionID, callID string) (bool, error) {
	var n int64
	err := r.db.WithContext(ctx).Model(&store.SessionEvent{}).
		Where("session_id = ? AND type = ? AND JSON_UNQUOTE(JSON_EXTRACT(data, '$.call_id')) = ?",
			sessionID, session.EventQuestionAnswered, callID).
		Count(&n).Error
	return n > 0, err
}

func (r *gormRepo) RunningSessions(ctx context.Context) ([]store.Session, error) {
	sessions := []store.Session{}
	err := r.db.WithContext(ctx).
		Where("running = 1 AND status <> ?", session.StatusDeleted).
		Find(&sessions).Error
	return sessions, err
}

func (r *gormRepo) UndecidedApprovals(ctx context.Context, sessionID string) ([]session.ApprovalAskedData, error) {
	rows := []store.SessionEvent{}
	err := r.db.WithContext(ctx).
		Where(`session_id = ? AND type = ? AND NOT EXISTS (
			SELECT 1 FROM session_event d
			WHERE d.session_id = session_event.session_id AND d.type = ?
			  AND JSON_UNQUOTE(JSON_EXTRACT(d.data, '$.call_id')) = JSON_UNQUOTE(JSON_EXTRACT(session_event.data, '$.call_id'))
		)`, sessionID, session.EventApprovalAsked, session.EventApprovalDecided).
		Order("seq").
		Find(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make([]session.ApprovalAskedData, 0, len(rows))
	for _, row := range rows {
		var d session.ApprovalAskedData
		if json.Unmarshal(row.Data, &d) == nil {
			out = append(out, d)
		}
	}
	return out, nil
}
