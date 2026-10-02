package audit

import (
	"context"
	"encoding/json"
	"time"

	"gorm.io/gorm"

	"github.com/ravenmk2/wispjar/internal/store"
)

type ListItem struct {
	ID        string          `json:"id"`
	OrgID     string          `json:"org_id"`
	ActorID   string          `json:"actor_id"`
	ActorName string          `json:"actor_name"`
	Action    string          `json:"action"`
	Target    string          `json:"target"`
	Detail    json.RawMessage `json:"detail"`
	CreatedAt time.Time       `json:"created_at"`
}

func List(ctx context.Context, db *gorm.DB, orgID, cursor string, limit int) ([]ListItem, string, error) {
	type row struct {
		store.AuditLog
		ActorName string `gorm:"column:actor_name"`
	}
	q := db.WithContext(ctx).Model(&store.AuditLog{}).
		Select("audit_log.*, COALESCE(NULLIF(user.display_name, ''), user.username, '') AS actor_name").
		Joins("LEFT JOIN user ON user.id = audit_log.actor_id").
		Order("audit_log.id DESC").
		Limit(limit + 1)
	if orgID != "" {
		q = q.Where("audit_log.org_id = ?", orgID)
	}
	if cursor != "" {
		q = q.Where("audit_log.id < ?", cursor)
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
	items := make([]ListItem, 0, len(rows))
	for _, r := range rows {
		items = append(items, ListItem{
			ID:        r.ID,
			OrgID:     r.OrgID,
			ActorID:   r.ActorID,
			ActorName: r.ActorName,
			Action:    r.Action,
			Target:    r.Target,
			Detail:    r.Detail,
			CreatedAt: r.CreatedAt,
		})
	}
	return items, next, nil
}
