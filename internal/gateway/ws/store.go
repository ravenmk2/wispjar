package ws

import (
	"context"
	"encoding/json"

	"gorm.io/gorm"

	"github.com/ravenmk2/wispjar/internal/computer"
	"github.com/ravenmk2/wispjar/internal/runtime"
	"github.com/ravenmk2/wispjar/internal/session"
	"github.com/ravenmk2/wispjar/internal/store"
)

type Store interface {
	ActiveUser(ctx context.Context, userID string) (bool, error)
	IsMember(ctx context.Context, orgID, userID string) (bool, error)
	OwnsSession(ctx context.Context, orgID, userID, sessionID string) (bool, error)
	SessionProjections(ctx context.Context, orgID, userID string) ([]runtime.ControlSession, error)
}

type gormStore struct {
	db *gorm.DB
}

func NewGormStore(db *gorm.DB) Store {
	return &gormStore{db: db}
}

func (s *gormStore) ActiveUser(ctx context.Context, userID string) (bool, error) {
	var n int64
	err := s.db.WithContext(ctx).Model(&store.User{}).
		Where("id = ? AND status = ?", userID, "active").
		Count(&n).Error
	return n > 0, err
}

func (s *gormStore) IsMember(ctx context.Context, orgID, userID string) (bool, error) {
	var n int64
	err := s.db.WithContext(ctx).Model(&store.OrgMember{}).
		Where("org_id = ? AND user_id = ?", orgID, userID).
		Count(&n).Error
	return n > 0, err
}

func (s *gormStore) OwnsSession(ctx context.Context, orgID, userID, sessionID string) (bool, error) {
	var n int64
	err := s.db.WithContext(ctx).Model(&store.Session{}).
		Where("id = ? AND org_id = ? AND user_id = ? AND status <> ?", sessionID, orgID, userID, session.StatusDeleted).
		Count(&n).Error
	return n > 0, err
}

func (s *gormStore) SessionProjections(ctx context.Context, orgID, userID string) ([]runtime.ControlSession, error) {
	rows := []store.Session{}
	if err := s.db.WithContext(ctx).
		Where("org_id = ? AND user_id = ? AND status <> ?", orgID, userID, session.StatusDeleted).
		Order("last_active_at DESC").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]runtime.ControlSession, 0, len(rows))
	for _, r := range rows {
		usage := session.Usage{}
		_ = json.Unmarshal(r.UsageTotal, &usage)
		out = append(out, runtime.ControlSession{
			SessionID:    r.ID,
			Title:        r.Title,
			Running:      r.Running,
			AgentID:      r.AgentID,
			UsageTotal:   usage,
			LastActiveAt: r.LastActiveAt.UnixMilli(),
			Status:       r.Status,
		})
	}
	return out, nil
}

// Optional capability keeps transport tests and other Store implementations
// independent of computer persistence while production always provides it.
func (s *gormStore) ComputerProjections(ctx context.Context, orgID, userID string) ([]any, error) {
	rows := []store.Computer{}
	if err := s.db.WithContext(ctx).Where("org_id=? AND user_id=? AND status<>?", orgID, userID, "deleted").
		Order("created_at DESC").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]any, 0, len(rows))
	for _, row := range rows {
		out = append(out, computer.View(row))
	}
	return out, nil
}
