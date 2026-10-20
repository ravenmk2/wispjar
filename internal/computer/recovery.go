package computer

import (
	"context"
	"encoding/json"

	"github.com/ravenmk2/wispjar/internal/audit"
	"github.com/ravenmk2/wispjar/internal/store"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// RecoverRuns is called only after every recorded instance was proved absent.
// It leaves the computer fenced until runtime and skill reconciliation succeed.
func (r *GORMRepository) RecoverRuns(ctx context.Context, op store.ResourceOperation) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var c store.Computer
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&c, "id=?", op.ResourceID).Error; err != nil {
			return err
		}
		var current store.ResourceOperation
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&current, "id=?", op.ID).Error; err != nil {
			return err
		}
		var plan operationPlan
		if err := json.Unmarshal(current.Progress, &plan); err != nil {
			return err
		}
		if current.Action != "computer/recover" || current.ResourceID != c.ID || current.OrgID != c.OrgID || current.UserID != c.UserID ||
			c.Status != "recovering" || c.Generation != plan.Generation || (current.Status != "pending" && current.Status != "running") {
			return ErrConflict
		}
		if plan.RunsRecovered {
			return nil
		}
		var runs []store.ComputerRun
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("computer_id=? AND org_id=? AND user_id=?", c.ID, c.OrgID, c.UserID).Find(&runs).Error; err != nil {
			return err
		}
		for _, run := range runs {
			fields := map[string]any{}
			if !TerminalRun(run.Status) {
				fields["status"] = "failed"
				fields["lease_until"] = nil
			}
			if run.ParentRunID == nil {
				var manifest map[string]any
				if len(run.Manifest) > 0 {
					if err := json.Unmarshal(run.Manifest, &manifest); err != nil {
						return err
					}
				}
				if manifest == nil {
					manifest = map[string]any{}
				}
				manifest["skill_leases_released"] = true
				manifest["recovery_operation_id"] = op.ID
				encoded, err := json.Marshal(manifest)
				if err != nil {
					return err
				}
				fields["manifest"] = encoded
				if err := tx.Where("run_id=? AND org_id=? AND user_id=?", run.ID, c.OrgID, c.UserID).Delete(&store.RunSkillLease{}).Error; err != nil {
					return err
				}
			}
			if len(fields) > 0 {
				if err := tx.Model(&store.ComputerRun{}).Where("id=?", run.ID).Updates(fields).Error; err != nil {
					return err
				}
			}
		}
		plan.RunsRecovered = true
		progress, _ := json.Marshal(plan)
		if err := tx.Model(&current).Update("progress", progress).Error; err != nil {
			return err
		}
		return audit.Log(tx, audit.Entry{OrgID: c.OrgID, ActorID: c.UserID, Action: "computer.recover_runs", Target: c.ID,
			Detail: map[string]any{"operation_id": op.ID, "generation": c.Generation, "instance_absent": true}})
	})
}
