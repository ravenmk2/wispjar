package volume

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/ravenmk2/wispjar/internal/id"
	"github.com/ravenmk2/wispjar/internal/store"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Repository separates durable intent from backend calls. Lock must serialize
// a user's Ensure/Inspect across processes without keeping a SQL transaction
// open. The MySQL implementation uses a dedicated advisory-lock connection.
type Repository interface {
	InstallationID(context.Context) (string, error)
	Lock(context.Context, string, string) (func() error, error)
	GetOrCreate(context.Context, store.UserVolume) (store.UserVolume, error)
	List(context.Context, string, string) ([]store.UserVolume, error)
	SetState(context.Context, string, string, string) error
}

type GORMRepository struct{ db *gorm.DB }

func NewGORMRepository(db *gorm.DB) *GORMRepository { return &GORMRepository{db: db} }

func (r *GORMRepository) InstallationID(ctx context.Context) (string, error) {
	candidate, _ := json.Marshal(id.New())
	row := store.PlatformSetting{Key: "volume.installation_id", Value: candidate, UpdatedBy: "system"}
	if err := r.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error; err != nil {
		return "", err
	}
	if err := r.db.WithContext(ctx).Where("`key` = ?", "volume.installation_id").Take(&row).Error; err != nil {
		return "", err
	}
	var value string
	if err := json.Unmarshal(row.Value, &value); err != nil {
		return "", fmt.Errorf("invalid installation identity: %w", err)
	}
	if len(value) != 32 {
		return "", fmt.Errorf("invalid installation identity length")
	}
	if _, err := hex.DecodeString(value); err != nil {
		return "", fmt.Errorf("invalid installation identity: %w", err)
	}
	return value, nil
}

func (r *GORMRepository) Lock(ctx context.Context, orgID, userID string) (func() error, error) {
	release, err := store.AcquireAdvisoryLock(ctx, r.db, "volume:"+orgID, userID)
	if errors.Is(err, store.ErrLockBusy) {
		return nil, fmt.Errorf("%w: volume operation busy", ErrUnavailable)
	}
	return release, err
}

func (r *GORMRepository) GetOrCreate(ctx context.Context, row store.UserVolume) (store.UserVolume, error) {
	if err := r.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error; err != nil {
		return store.UserVolume{}, err
	}
	var saved store.UserVolume
	err := r.db.WithContext(ctx).Where("org_id = ? AND user_id = ? AND kind = ?", row.OrgID, row.UserID, row.Kind).Take(&saved).Error
	return saved, err
}
func (r *GORMRepository) List(ctx context.Context, orgID, userID string) ([]store.UserVolume, error) {
	rows := []store.UserVolume{}
	err := r.db.WithContext(ctx).Where("org_id = ? AND user_id = ?", orgID, userID).Order("kind").Find(&rows).Error
	return rows, err
}
func (r *GORMRepository) SetState(ctx context.Context, volumeID, state, reason string) error {
	result := r.db.WithContext(ctx).Model(&store.UserVolume{}).Where("id = ?", volumeID).Updates(map[string]any{"status": state, "last_error": reason})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		// MySQL may report zero changed rows for an idempotent update.
		var count int64
		if err := r.db.WithContext(ctx).Model(&store.UserVolume{}).Where("id = ?", volumeID).Count(&count).Error; err != nil {
			return err
		}
		if count != 1 {
			return fmt.Errorf("volume state update: missing intent %s", volumeID)
		}
	}
	return nil
}
