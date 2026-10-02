package runtime

import (
	"context"
	"errors"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/ravenmk2/wispjar/internal/store"
)

// CheckpointStore 是 adk CheckPointStore 的 MySQL 实现（agent_checkpoint 表）。
type CheckpointStore struct {
	db        *gorm.DB
	sessionID string
}

func NewCheckpointStore(db *gorm.DB, sessionID string) *CheckpointStore {
	return &CheckpointStore{db: db, sessionID: sessionID}
}

func (s *CheckpointStore) Get(ctx context.Context, checkPointID string) ([]byte, bool, error) {
	var row store.AgentCheckpoint
	err := s.db.WithContext(ctx).First(&row, "id = ?", checkPointID).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, false, nil
		}
		return nil, false, err
	}
	return row.Data, true, nil
}

func (s *CheckpointStore) Set(ctx context.Context, checkPointID string, checkPoint []byte) error {
	row := store.AgentCheckpoint{ID: checkPointID, SessionID: s.sessionID, Data: checkPoint}
	return s.db.WithContext(ctx).
		Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "id"}}, DoUpdates: clause.AssignmentColumns([]string{"data"})}).
		Create(&row).Error
}

func (s *CheckpointStore) Delete(ctx context.Context, checkPointID string) error {
	return s.db.WithContext(ctx).Delete(&store.AgentCheckpoint{}, "id = ?", checkPointID).Error
}
