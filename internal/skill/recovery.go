package skill

import (
	"context"
	"encoding/json"

	"github.com/ravenmk2/wispjar/internal/audit"
	"github.com/ravenmk2/wispjar/internal/store"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func terminalRun(run store.ComputerRun) bool {
	return run.Status == "completed" || run.Status == "failed" || run.Status == "canceled"
}

func runGeneration(run store.ComputerRun) (uint64, error) {
	var manifest struct {
		Generation uint64 `json:"generation"`
	}
	if len(run.Manifest) > 0 {
		if err := json.Unmarshal(run.Manifest, &manifest); err != nil {
			return 0, err
		}
	}
	var execution struct {
		Generation uint64 `json:"generation"`
	}
	if len(run.Execution) > 0 {
		if err := json.Unmarshal(run.Execution, &execution); err != nil {
			return 0, err
		}
	}
	if manifest.Generation != 0 && execution.Generation != 0 && manifest.Generation != execution.Generation {
		return 0, syncError("skill/sync_blocked", "运行电脑代次不一致，不能同步技能")
	}
	if manifest.Generation == 0 {
		manifest.Generation = execution.Generation
	}
	if manifest.Generation == 0 || !validID(run.ComputerID) {
		return 0, syncError("skill/sync_blocked", "运行缺少可信电脑代次，不能同步技能")
	}
	return manifest.Generation, nil
}

// The run row is always locked before skill/state rows. A computer recovery may
// finish the run while an installer is waiting on remote IO; neither its late
// completion nor the final lease transaction may resurrect that run.
func activeRunLocked(tx *gorm.DB, expected store.ComputerRun) (store.ComputerRun, error) {
	var current store.ComputerRun
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("id = ? AND org_id = ? AND user_id = ? AND parent_run_id IS NULL", expected.ID, expected.OrgID, expected.UserID).Take(&current).Error; err != nil {
		return current, err
	}
	if terminalRun(current) {
		return current, syncError("skill/run_finished", "运行已经结束")
	}
	_, _, released, err := storedManifest(current)
	if err != nil {
		return current, err
	}
	if released {
		return current, syncError("skill/lease_released", "该运行的技能使用权已释放")
	}
	before, err := runGeneration(expected)
	if err != nil {
		return current, err
	}
	now, err := runGeneration(current)
	if err != nil {
		return current, err
	}
	if current.ComputerID != expected.ComputerID || now != before {
		return current, syncError("skill/sync_blocked", "运行电脑代次已改变")
	}
	return current, nil
}

// RecoverComputer is an internal lifecycle hook, not a user assertion API.
// The caller must first Delete the old instance, positively Inspect it missing,
// and persist terminal statuses for its runs while keeping the computer in
// recovering state. Hold the computer advisory lock, but no SQL transaction or
// run row locks, when invoking this hook. This method only takes the user skill
// advisory lock and never calls back into the computer service.
//
// Old plans without root/computer/generation provenance remain blocked. They
// require separately audited user-wide confirmation that all installers stopped.
func (c *Coordinator) RecoverComputer(ctx context.Context, orgID, userID, computerID string, oldGeneration uint64) error {
	if !validID(orgID) || !validID(userID) || !validID(computerID) || oldGeneration == 0 {
		return errInvalid("无效的电脑恢复上下文", nil)
	}
	return withDBLock(ctx, c.db, "user:"+orgID+":"+userID, func() error {
		return c.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			var runs []store.ComputerRun
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
				Where("org_id = ? AND user_id = ? AND computer_id = ? AND parent_run_id IS NULL", orgID, userID, computerID).
				Order("id").Find(&runs).Error; err != nil {
				return err
			}
			recovered := map[string]bool{}
			for _, run := range runs {
				generation, err := runGeneration(run)
				if err != nil || generation != oldGeneration {
					// Unknown provenance must not be attributed to this instance.
					continue
				}
				if !terminalRun(run) {
					return syncError("skill/sync_blocked", "电脑恢复尚未终结旧代次运行")
				}
				if err := tx.Where("run_id = ? AND org_id = ? AND user_id = ?", run.ID, orgID, userID).Delete(&store.RunSkillLease{}).Error; err != nil {
					return err
				}
				var manifest map[string]any
				if err := json.Unmarshal(run.Manifest, &manifest); err != nil {
					return err
				}
				manifest["skill_leases_released"] = true
				raw, _ := json.Marshal(manifest)
				if err := tx.Model(&store.ComputerRun{}).Where("id = ?", run.ID).Update("manifest", raw).Error; err != nil {
					return err
				}
				recovered[run.ID] = true
			}
			var states []store.UserSkillState
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
				Where("org_id = ? AND user_id = ? AND status IN ?", orgID, userID, []string{"syncing", "blocked"}).
				Order("skill_id").Find(&states).Error; err != nil {
				return err
			}
			count := 0
			for _, state := range states {
				var operations []store.ResourceOperation
				if err := tx.Where("org_id = ? AND user_id = ? AND resource_type = ? AND resource_id = ? AND action = ?",
					orgID, userID, "skill", state.SkillID, "skill/sync").Order("created_at DESC, id DESC").Limit(1).Find(&operations).Error; err != nil {
					return err
				}
				if len(operations) == 0 {
					continue
				}
				op := operations[0]
				var plan InstallPlan
				if err := json.Unmarshal(op.Progress, &plan); err != nil {
					return err
				}
				if plan.OrgID != orgID || plan.UserID != userID || plan.SkillID != state.SkillID ||
					plan.ComputerID != computerID || plan.Generation != oldGeneration || !recovered[plan.RootRunID] || plan.Fence != state.Fence {
					continue
				}
				if state.LockOwner != "" && state.LockOwner != plan.Owner {
					continue
				}
				result := tx.Model(&store.UserSkillState{}).
					Where("org_id = ? AND user_id = ? AND skill_id = ? AND fence = ? AND status IN ?", orgID, userID, state.SkillID, plan.Fence, []string{"syncing", "blocked"}).
					Updates(map[string]any{"status": "pending", "fence": state.Fence + 1, "lock_owner": "", "lock_until": nil, "last_error": ""})
				if result.Error != nil {
					return result.Error
				}
				if result.RowsAffected != 1 {
					return syncError("skill/sync_blocked", "恢复时技能 fencing 已变化")
				}
				if err := tx.Model(&store.ResourceOperation{}).Where("id = ?", op.ID).
					Updates(map[string]any{"status": "failed", "error": "Installer instance confirmed terminated; synchronization must retry."}).Error; err != nil {
					return err
				}
				count++
			}
			return audit.Log(tx, audit.Entry{OrgID: orgID, ActorID: userID, Action: "skill.computer_recover", Target: computerID,
				Detail: map[string]any{"generation": oldGeneration, "released_runs": len(recovered), "retry_skills": count}})
		})
	})
}
