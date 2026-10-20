package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/ravenmk2/wispjar/internal/audit"
	"github.com/ravenmk2/wispjar/internal/sandbox"
	"github.com/ravenmk2/wispjar/internal/store"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// LoadEnvs loads one consistent version for all agents in a root run.
func (s *Service) LoadEnvs(ctx context.Context, org string, ids []string) (map[string]map[string]string, error) {
	var rows []store.Agent
	if err := s.db.WithContext(ctx).Select("id", "sandbox_config").Where("org_id=? AND id IN ?", org, ids).Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make(map[string]map[string]string, len(rows))
	for _, row := range rows {
		var config SandboxConfig
		if len(row.SandboxConfig) > 0 {
			if err := json.Unmarshal(row.SandboxConfig, &config); err != nil {
				return nil, err
			}
		}
		if err := sandbox.ValidateEnv(config.Env); err != nil {
			return nil, err
		}
		out[row.ID] = config.Env
	}
	for _, id := range ids {
		if _, ok := out[id]; !ok {
			return nil, fmt.Errorf("agent environment unavailable: %s", id)
		}
	}
	return out, nil
}

// PatchEnv changes only runtime variables, without changing publication status.
func (s *Service) PatchEnv(ctx context.Context, actor, org, id string, patch sandbox.EnvPatch) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var row store.Agent
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id=? AND org_id=?", id, org).Take(&row).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return errNotFound("智能体不存在")
			}
			return err
		}
		var config SandboxConfig
		if len(row.SandboxConfig) > 0 {
			if err := json.Unmarshal(row.SandboxConfig, &config); err != nil {
				return err
			}
		}
		var err error
		config.Env, err = patch.Apply(config.Env)
		if err != nil {
			return errInvalid(err.Error(), nil)
		}
		raw, err := json.Marshal(config)
		if err != nil {
			return err
		}
		if err := tx.Model(&row).Update("sandbox_config", raw).Error; err != nil {
			return err
		}
		return audit.Log(tx, audit.Entry{OrgID: org, ActorID: actor, Action: "agent.env_update", Target: id})
	})
}
