package session

import (
	"context"
	"encoding/json"
	"strings"
	"time"
	"unicode/utf8"

	"gorm.io/gorm"

	"github.com/ravenmk2/wispjar/internal/id"
	"github.com/ravenmk2/wispjar/internal/store"
)

// Fork 从 at_seq 事件点分叉新会话（api.md §3）：复制 seq<=at_seq 的事件，
// 复制不触发 Store.AfterAppend（用量聚合）挂钩。
func (s *Service) Fork(ctx context.Context, uid, sessionID string, atSeq int64, title *string) (string, error) {
	sess, err := s.ownedSession(ctx, uid, sessionID)
	if err != nil {
		return "", err
	}
	events := []store.SessionEvent{}
	if err := s.db.WithContext(ctx).
		Where("session_id = ? AND seq <= ?", sess.ID, atSeq).
		Order("seq").
		Find(&events).Error; err != nil {
		return "", err
	}
	forked, copied, err := planFork(sess, events, atSeq, id.New(), time.Now(), title)
	if err != nil {
		return "", err
	}
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockSessionComputer(tx, forked); err != nil {
			return err
		}
		if err := tx.Create(forked).Error; err != nil {
			return err
		}
		if len(copied) > 0 {
			if err := tx.CreateInBatches(copied, 200).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if s.AfterEvents != nil {
		s.AfterEvents(forked.ID, nil)
	}
	return forked.ID, nil
}

func planFork(src *store.Session, events []store.SessionEvent, atSeq int64, newID string, now time.Time, title *string) (*store.Session, []store.SessionEvent, error) {
	if atSeq < 1 || atSeq > src.LastSeq {
		return nil, nil, errInvalid("at_seq 必须在 1..last_seq 范围内",
			map[string]any{"field": "at_seq", "last_seq": src.LastSeq})
	}
	copied := make([]store.SessionEvent, 0, len(events))
	for _, ev := range events {
		if ev.Seq > atSeq {
			continue
		}
		ev.SessionID = newID
		copied = append(copied, ev)
	}
	forked := &store.Session{
		ID:            newID,
		OrgID:         src.OrgID,
		UserID:        src.UserID,
		AgentID:       src.AgentID,
		AgentSnapshot: src.AgentSnapshot,
		Title:         forkTitle(src.Title),
		TitlePinned:   src.TitlePinned,
		ApprovalMode:  src.ApprovalMode,
		ModelID:       src.ModelID,
		ModelParams:   src.ModelParams,
		LastSeq:       atSeq,
		Running:       false,
		UsageTotal:    usageTotalFromEvents(copied),
		Status:        StatusActive,
		LastActiveAt:  now,
		ComputerID:    src.ComputerID,
	}
	if title != nil {
		customTitle := strings.TrimSpace(*title)
		if utf8.RuneCountInString(customTitle) > 256 {
			return nil, nil, errInvalid("title 不超过 256 字符", map[string]any{"field": "title"})
		}
		if customTitle != "" {
			forked.Title = customTitle
			forked.TitlePinned = true
		}
	}
	forked.Workspace = newWorkspace()
	return forked, copied, nil
}

func forkTitle(title string) string {
	const suffix = "（副本）"
	const maxRunes = 256
	if utf8.RuneCountInString(title)+utf8.RuneCountInString(suffix) <= maxRunes {
		return title + suffix
	}
	r := []rune(title)
	return string(r[:maxRunes-utf8.RuneCountInString(suffix)]) + suffix
}

func usageTotalFromEvents(events []store.SessionEvent) json.RawMessage {
	u := Usage{}
	for _, ev := range events {
		if ev.Type != EventAssistantMessage {
			continue
		}
		if d, ok := usageFromEventData(ev.Data); ok {
			u.InputTokens += d.InputTokens
			u.OutputTokens += d.OutputTokens
			u.CacheReadTokens += d.CacheReadTokens
			u.CacheWriteTokens += d.CacheWriteTokens
			u.ReasoningTokens += d.ReasoningTokens
		}
	}
	b, err := json.Marshal(u)
	if err != nil {
		return zeroUsage()
	}
	return b
}
