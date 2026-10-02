package usage

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/ravenmk2/wispjar/internal/session"
	"github.com/ravenmk2/wispjar/internal/store"
)

var reportTZ = time.FixedZone("UTC+8", 8*60*60)

type Recorder struct {
	db *gorm.DB
}

func NewRecorder(db *gorm.DB) *Recorder {
	return &Recorder{db: db}
}

// OnEvents 挂 session.Store.AfterAppend：提交后同步聚合，失败只记日志不阻断主流程。
func (r *Recorder) OnEvents(ctx context.Context, sessionID string, events []store.SessionEvent) {
	deltas := make([]eventDelta, 0, len(events))
	for _, ev := range events {
		if d, ok := deltaFromEvent(ev); ok {
			deltas = append(deltas, d)
		}
	}
	if len(deltas) == 0 {
		return
	}
	var sess store.Session
	if err := r.db.WithContext(ctx).
		Select("org_id", "user_id", "agent_id", "model_id").
		First(&sess, "id = ?", sessionID).Error; err != nil {
		log.Printf("usage: load session %s: %v", sessionID, err)
		return
	}
	for _, d := range deltas {
		if err := r.upsert(ctx, &sess, d); err != nil {
			log.Printf("usage: upsert session %s date %s: %v", sessionID, d.date.Format("2006-01-02"), err)
		}
	}
}

type eventDelta struct {
	date  time.Time
	usage session.Usage
}

func deltaFromEvent(ev store.SessionEvent) (eventDelta, bool) {
	if ev.Type != session.EventAssistantMessage {
		return eventDelta{}, false
	}
	var probe struct {
		Usage *session.Usage `json:"usage"`
	}
	if err := json.Unmarshal(ev.Data, &probe); err != nil || probe.Usage == nil {
		return eventDelta{}, false
	}
	t := time.UnixMilli(ev.TimeMs).In(reportTZ)
	date := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, reportTZ)
	return eventDelta{date: date, usage: *probe.Usage}, true
}

func (r *Recorder) upsert(ctx context.Context, sess *store.Session, d eventDelta) error {
	row := store.UsageDaily{
		Date:             d.date,
		OrgID:            sess.OrgID,
		UserID:           sess.UserID,
		AgentID:          sess.AgentID,
		ModelID:          sess.ModelID,
		Requests:         1,
		InputTokens:      d.usage.InputTokens,
		OutputTokens:     d.usage.OutputTokens,
		CacheReadTokens:  d.usage.CacheReadTokens,
		CacheWriteTokens: d.usage.CacheWriteTokens,
		ReasoningTokens:  d.usage.ReasoningTokens,
	}
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "date"}, {Name: "org_id"}, {Name: "user_id"}, {Name: "agent_id"}, {Name: "model_id"},
		},
		DoUpdates: clause.Assignments(map[string]any{
			"requests":           gorm.Expr("requests + VALUES(requests)"),
			"input_tokens":       gorm.Expr("input_tokens + VALUES(input_tokens)"),
			"output_tokens":      gorm.Expr("output_tokens + VALUES(output_tokens)"),
			"cache_read_tokens":  gorm.Expr("cache_read_tokens + VALUES(cache_read_tokens)"),
			"cache_write_tokens": gorm.Expr("cache_write_tokens + VALUES(cache_write_tokens)"),
			"reasoning_tokens":   gorm.Expr("reasoning_tokens + VALUES(reasoning_tokens)"),
		}),
	}).Create(&row).Error
}
