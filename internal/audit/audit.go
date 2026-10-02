package audit

import (
	"encoding/json"
	"time"

	"gorm.io/gorm"

	"github.com/ravenmk2/wispjar/internal/id"
	"github.com/ravenmk2/wispjar/internal/store"
)

const (
	OrgCreate         = "tenant.org_create"
	OrgUpdate         = "tenant.org_update"
	UserCreate        = "tenant.user_create"
	UserUpdate        = "tenant.user_update"
	UserResetPassword = "tenant.user_reset_password"
	MemberAdd         = "member.add"
	MemberUpdateRole  = "member.update_role"
	MemberRemove      = "member.remove"
	ProviderCreate    = "provider.create"
	ProviderUpdate    = "provider.update"
	ProviderUpdateKey = "provider.update_key"
	ProviderDelete    = "provider.delete"
	ModelCreate       = "model.create"
	ModelUpdate       = "model.update"
	ModelDelete       = "model.delete"
	SettingsUpdate    = "settings.update"
)

type Entry struct {
	OrgID   string
	ActorID string
	Action  string
	Target  string
	Detail  any
}

func Log(db *gorm.DB, e Entry) error {
	var detail json.RawMessage
	if e.Detail != nil {
		b, err := json.Marshal(e.Detail)
		if err != nil {
			return err
		}
		detail = b
	}
	return db.Create(&store.AuditLog{
		ID:        id.New(),
		OrgID:     e.OrgID,
		ActorID:   e.ActorID,
		Action:    e.Action,
		Target:    e.Target,
		Detail:    detail,
		CreatedAt: time.Now(),
	}).Error
}
