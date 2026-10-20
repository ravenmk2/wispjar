package skill

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/ravenmk2/wispjar/internal/audit"
	"github.com/ravenmk2/wispjar/internal/id"
	"github.com/ravenmk2/wispjar/internal/store"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type AcquireRequest struct {
	OrgID     string
	UserID    string
	RootRunID string
	// Include the main Agent and every reachable subagent before starting.
	AgentIDs []string
}
type ResolvedSkill struct {
	ID          string `json:"id"`
	AgentID     string `json:"agent_id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Revision    uint64 `json:"revision"`
	ContentHash string `json:"content_hash"`
	Directory   string `json:"directory"`
}
type Manifest struct {
	AgentIDs []string        `json:"agent_ids"`
	Skills   []ResolvedSkill `json:"skills"`
}
type InstallPlan struct {
	OrgID           string   `json:"org_id"`
	UserID          string   `json:"user_id"`
	SkillID         string   `json:"skill_id"`
	RootRunID       string   `json:"root_run_id,omitempty"`
	ComputerID      string   `json:"computer_id,omitempty"`
	Generation      uint64   `json:"generation,omitempty"`
	Name            string   `json:"name"`
	Revision        uint64   `json:"revision"`
	ContentHash     string   `json:"content_hash"`
	Fence           uint64   `json:"fence"`
	Owner           string   `json:"owner"`
	Targets         []string `json:"targets"`
	PreviousTargets []string `json:"previous_targets"`
	Files           []File   `json:"files,omitempty"`
}
type Installer interface {
	Install(context.Context, InstallPlan) error
}

// StoppedInstallError certifies that no remote command can still mutate files.
// All other installer failures are treated as uncertain and require Recover.
type StoppedInstallError struct{ Err error }

func (e *StoppedInstallError) Error() string { return e.Err.Error() }
func (e *StoppedInstallError) Unwrap() error { return e.Err }

type Coordinator struct {
	db      *gorm.DB
	sources *Service
}

func NewCoordinator(db *gorm.DB, sources *Service) *Coordinator {
	return &Coordinator{db: db, sources: sources}
}
func syncError(code, msg string) *Error {
	return &Error{Status: http.StatusConflict, Code: code, Message: msg}
}

func uniqueSorted(values []string) []string {
	set := map[string]bool{}
	for _, v := range values {
		set[v] = true
	}
	result := make([]string, 0, len(set))
	for v := range set {
		result = append(result, v)
	}
	sort.Strings(result)
	return result
}
func validID(value string) bool {
	if len(value) != 32 {
		return false
	}
	for _, c := range value {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
func validateAcquire(r AcquireRequest) error {
	if !validID(r.OrgID) || !validID(r.UserID) || !validID(r.RootRunID) || len(r.AgentIDs) == 0 {
		return errInvalid("无效的技能运行上下文", nil)
	}
	for _, v := range r.AgentIDs {
		if !validID(v) {
			return errInvalid("无效的智能体 ID", nil)
		}
	}
	return nil
}
func (c *Coordinator) run(ctx context.Context, r AcquireRequest) (store.ComputerRun, error) {
	var run store.ComputerRun
	err := c.db.WithContext(ctx).Where("id = ? AND org_id = ? AND user_id = ? AND parent_run_id IS NULL", r.RootRunID, r.OrgID, r.UserID).Take(&run).Error
	return run, err
}
func storedManifest(run store.ComputerRun) (Manifest, bool, bool, error) {
	var raw map[string]json.RawMessage
	if len(run.Manifest) > 0 {
		if err := json.Unmarshal(run.Manifest, &raw); err != nil {
			return Manifest{}, false, false, err
		}
	}
	_, released := raw["skill_leases_released"]
	value, ok := raw["skill_manifest"]
	if !ok {
		return Manifest{}, false, released, nil
	}
	var manifest Manifest
	err := json.Unmarshal(value, &manifest)
	return manifest, true, released, err
}

// Acquire is idempotent for a root run, including approval resume. It returns
// that run's original manifest and leases rather than resolving new revisions.
func (c *Coordinator) Acquire(ctx context.Context, r AcquireRequest, installer Installer) (manifest Manifest, err error) {
	if err = validateAcquire(r); err != nil {
		return
	}
	if installer == nil {
		return manifest, fmt.Errorf("skill installer unavailable")
	}
	r.AgentIDs = uniqueSorted(r.AgentIDs)
	err = withDBLock(ctx, c.db, "user:"+r.OrgID+":"+r.UserID, func() error {
		run, err := c.run(ctx, r)
		if err != nil {
			return err
		}
		if terminalRun(run) {
			return syncError("skill/run_finished", "运行已经结束")
		}
		if _, err := runGeneration(run); err != nil {
			return err
		}
		saved, exists, released, err := storedManifest(run)
		if err != nil {
			return err
		}
		if released {
			return syncError("skill/lease_released", "该运行的技能使用权已释放")
		}
		if exists {
			if !reflect.DeepEqual(saved.AgentIDs, r.AgentIDs) {
				return syncError("skill/manifest_conflict", "运行技能依赖不可在恢复时改变")
			}
			var leases []store.RunSkillLease
			if err := c.db.WithContext(ctx).Where("run_id = ?", r.RootRunID).Find(&leases).Error; err != nil {
				return err
			}
			expected := map[string]uint64{}
			for _, sk := range saved.Skills {
				expected[sk.ID] = sk.Revision
			}
			if len(leases) != len(expected) {
				return syncError("skill/lease_missing", "运行技能使用权缺失，需要人工核查")
			}
			for _, lease := range leases {
				if expected[lease.SkillID] != lease.Revision {
					return syncError("skill/lease_missing", "运行技能使用权不一致")
				}
			}
			manifest = saved
			return nil
		}
		links, err := c.resolveLinks(ctx, r.OrgID, r.AgentIDs)
		if err != nil {
			return err
		}
		ids := []string{}
		for _, link := range links {
			ids = append(ids, link.SkillID)
		}
		ids = uniqueSorted(ids)
		workIDs, err := c.installDependencies(ctx, r, ids)
		if err != nil {
			return err
		}
		rows := map[string]store.Skill{}
		for _, skillID := range workIDs {
			row, files, err := c.sources.sourceSnapshot(ctx, r.OrgID, skillID)
			if err != nil {
				return err
			}
			targets, err := c.targets(ctx, r.OrgID, skillID, row.Name)
			if err != nil {
				return err
			}
			if err = c.ensureInstalled(ctx, r, run, row, files, targets, installer); err != nil {
				return err
			}
			rows[skillID] = row
		}
		manifest = Manifest{AgentIDs: r.AgentIDs, Skills: []ResolvedSkill{}}
		for _, link := range links {
			row := rows[link.SkillID]
			manifest.Skills = append(manifest.Skills, ResolvedSkill{
				ID: row.ID, AgentID: link.AgentID, Name: row.Name, Description: row.Description, Revision: row.Revision, ContentHash: row.ContentHash, Directory: "/agents/" + link.AgentID + "/skills/" + row.Name,
			})
		}
		return c.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			current, err := activeRunLocked(tx, run)
			if err != nil {
				return err
			}
			// Every dependency is checked before the first lease is inserted.
			// Source publication takes the same skill row lock.
			for _, skillID := range ids {
				var row store.Skill
				if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND org_id = ?", skillID, r.OrgID).Take(&row).Error; err != nil {
					return err
				}
				if row.Revision != rows[skillID].Revision {
					return syncError("skill/update_pending", "技能源包已变化，请重试")
				}
				var state store.UserSkillState
				if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("org_id = ? AND user_id = ? AND skill_id = ?", r.OrgID, r.UserID, skillID).Take(&state).Error; err != nil {
					return err
				}
				if state.Status != "ready" || state.AppliedRevision != row.Revision || state.DesiredRevision != row.Revision {
					return syncError("skill/update_pending", "技能等待同步")
				}
			}
			nowLinks, err := resolveLinksDB(tx, r.OrgID, r.AgentIDs)
			if err != nil {
				return err
			}
			if !reflect.DeepEqual(links, nowLinks) {
				return syncError("skill/update_pending", "智能体技能关联已变化，请重试")
			}
			for _, skillID := range ids {
				if err := tx.Create(&store.RunSkillLease{RunID: r.RootRunID, OrgID: r.OrgID, UserID: r.UserID, SkillID: skillID, Revision: rows[skillID].Revision}).Error; err != nil {
					return err
				}
			}
			raw := map[string]any{}
			if len(current.Manifest) > 0 {
				if err := json.Unmarshal(current.Manifest, &raw); err != nil {
					return err
				}
			}
			raw["skill_manifest"] = manifest
			encoded, _ := json.Marshal(raw)
			return tx.Model(&store.ComputerRun{}).Where("id = ?", r.RootRunID).Update("manifest", encoded).Error
		})
	})
	if err != nil {
		return Manifest{}, err
	}
	return
}

// Include previously installed skills for these Agents, so removing an
// association retires its directory at the same safe task boundary.
func (c *Coordinator) installDependencies(ctx context.Context, r AcquireRequest, current []string) ([]string, error) {
	var operations []store.ResourceOperation
	if err := c.db.WithContext(ctx).Where("org_id = ? AND user_id = ? AND action = ?", r.OrgID, r.UserID, "skill/sync").Order("created_at DESC, id DESC").Find(&operations).Error; err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	result := append([]string(nil), current...)
	for _, op := range operations {
		if seen[op.ResourceID] {
			continue
		}
		seen[op.ResourceID] = true
		var plan InstallPlan
		if err := json.Unmarshal(op.Progress, &plan); err != nil {
			return nil, err
		}
		for _, target := range append(plan.Targets, plan.PreviousTargets...) {
			for _, agentID := range r.AgentIDs {
				if strings.HasPrefix(target, "/agents/"+agentID+"/skills/") {
					// A fully retired skill can subsequently have its source deleted.
					var count int64
					if err := c.db.WithContext(ctx).Model(&store.Skill{}).Where("id = ? AND org_id = ?", op.ResourceID, r.OrgID).Count(&count).Error; err != nil {
						return nil, err
					}
					if count > 0 {
						result = append(result, op.ResourceID)
					}
					break
				}
			}
		}
	}
	return uniqueSorted(result), nil
}

func (c *Coordinator) resolveLinks(ctx context.Context, orgID string, agents []string) ([]store.AgentSkill, error) {
	return resolveLinksDB(c.db.WithContext(ctx), orgID, agents)
}
func resolveLinksDB(db *gorm.DB, orgID string, agents []string) ([]store.AgentSkill, error) {
	var count int64
	if err := db.Model(&store.Agent{}).Where("org_id = ? AND id IN ?", orgID, agents).Count(&count).Error; err != nil {
		return nil, err
	}
	if count != int64(len(agents)) {
		return nil, errNotFound("智能体不存在")
	}
	links := []store.AgentSkill{}
	err := db.Where("agent_id IN ?", agents).Order("agent_id, skill_id").Find(&links).Error
	return links, err
}
func (c *Coordinator) targets(ctx context.Context, orgID, skillID, name string) ([]string, error) {
	var agents []string
	err := c.db.WithContext(ctx).Model(&store.AgentSkill{}).Select("agent_skill.agent_id").
		Joins("JOIN agent ON agent.id = agent_skill.agent_id").Where("agent.org_id = ? AND agent_skill.skill_id = ?", orgID, skillID).Pluck("agent_skill.agent_id", &agents).Error
	if err != nil {
		return nil, err
	}
	result := []string{}
	for _, agentID := range uniqueSorted(agents) {
		result = append(result, "/agents/"+agentID+"/skills/"+name)
	}
	return result, nil
}

func (c *Coordinator) previousPlan(ctx context.Context, r AcquireRequest, skillID string) (InstallPlan, error) {
	var op store.ResourceOperation
	err := c.db.WithContext(ctx).Where("org_id = ? AND user_id = ? AND resource_id = ? AND action = ?", r.OrgID, r.UserID, skillID, "skill/sync").Order("created_at DESC, id DESC").Take(&op).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return InstallPlan{}, nil
	}
	if err != nil {
		return InstallPlan{}, err
	}
	var plan InstallPlan
	err = json.Unmarshal(op.Progress, &plan)
	return plan, err
}
func (c *Coordinator) ensureInstalled(ctx context.Context, r AcquireRequest, run store.ComputerRun, row store.Skill, files []File, targets []string, installer Installer) error {
	state := store.UserSkillState{OrgID: r.OrgID, UserID: r.UserID, SkillID: row.ID, DesiredRevision: row.Revision, Status: "pending"}
	if err := c.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&state).Error; err != nil {
		return err
	}
	if err := c.db.WithContext(ctx).Where("org_id = ? AND user_id = ? AND skill_id = ?", r.OrgID, r.UserID, row.ID).Take(&state).Error; err != nil {
		return err
	}
	previous, err := c.previousPlan(ctx, r, row.ID)
	if err != nil {
		return err
	}
	if state.Status == "syncing" || state.Status == "blocked" {
		return syncError("skill/sync_blocked", "上次同步远端状态未知，确认命令停止后显式恢复")
	}
	if state.Status == "ready" && state.AppliedRevision == row.Revision && state.DesiredRevision == row.Revision && reflect.DeepEqual(previous.Targets, targets) {
		return nil
	}
	var active int64
	if err := c.db.WithContext(ctx).Model(&store.RunSkillLease{}).Where("org_id = ? AND user_id = ? AND skill_id = ?", r.OrgID, r.UserID, row.ID).Count(&active).Error; err != nil {
		return err
	}
	if active > 0 {
		if err := c.db.WithContext(ctx).Model(&store.UserSkillState{}).Where("org_id = ? AND user_id = ? AND skill_id = ?", r.OrgID, r.UserID, row.ID).Updates(map[string]any{"desired_revision": row.Revision, "status": "pending"}).Error; err != nil {
			return err
		}
		return syncError("skill/update_pending", "技能更新等待已有任务（含审批等待）结束")
	}
	plan := InstallPlan{OrgID: r.OrgID, UserID: r.UserID, SkillID: row.ID, Name: row.Name, Revision: row.Revision, ContentHash: row.ContentHash,
		RootRunID: run.ID, ComputerID: run.ComputerID,
		Fence: state.Fence + 1, Owner: id.New(), Targets: targets, PreviousTargets: uniqueSorted(append(previous.PreviousTargets, previous.Targets...))}
	plan.Generation, err = runGeneration(run)
	if err != nil {
		return err
	}
	progress, _ := json.Marshal(plan)
	op := store.ResourceOperation{ID: id.New(), OrgID: r.OrgID, UserID: r.UserID, ResourceType: "skill", ResourceID: row.ID, Action: "skill/sync", Status: "running", Progress: progress}
	err = c.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, err := activeRunLocked(tx, run); err != nil {
			return err
		}
		result := tx.Model(&store.UserSkillState{}).Where("org_id = ? AND user_id = ? AND skill_id = ? AND fence = ?", r.OrgID, r.UserID, row.ID, state.Fence).
			Updates(map[string]any{"desired_revision": row.Revision, "status": "syncing", "fence": plan.Fence, "lock_owner": plan.Owner, "lock_until": nil, "last_error": ""})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return syncError("skill/sync_blocked", "同步 fencing 已变化")
		}
		return tx.Create(&op).Error
	})
	if err != nil {
		return err
	}
	plan.Files = files
	installErr := installer.Install(ctx, plan)
	// A canceled caller must not prevent persisting the conservative outcome.
	finishCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	status, opStatus, reason := "ready", "completed", ""
	if installErr != nil {
		status, opStatus, reason = "blocked", "blocked", "远端同步结果未知，需确认停止后恢复"
		var stopped *StoppedInstallError
		if errors.As(installErr, &stopped) {
			status, opStatus, reason = "error", "failed", "技能同步失败，可安全重试"
		}
	}
	finishErr := c.db.WithContext(finishCtx).Transaction(func(tx *gorm.DB) error {
		if _, err := activeRunLocked(tx, run); err != nil {
			return err
		}
		updates := map[string]any{"status": status, "last_error": reason, "lock_owner": "", "lock_until": nil}
		if installErr == nil {
			updates["applied_revision"] = row.Revision
		}
		result := tx.Model(&store.UserSkillState{}).Where("org_id = ? AND user_id = ? AND skill_id = ? AND fence = ? AND lock_owner = ?", r.OrgID, r.UserID, row.ID, plan.Fence, plan.Owner).Updates(updates)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return syncError("skill/sync_blocked", "同步 fencing 不匹配")
		}
		return tx.Model(&store.ResourceOperation{}).Where("id = ?", op.ID).Updates(map[string]any{"status": opStatus, "error": reason}).Error
	})
	if finishErr != nil {
		return finishErr
	}
	if installErr != nil {
		return syncError("skill/sync_failed", reason)
	}
	return nil
}

// Release must only be called after the caller has positively confirmed all
// main/subagent/background commands have stopped. Lease expiry is not proof.
func (c *Coordinator) Release(ctx context.Context, orgID, userID, rootRunID string, commandsStopped bool) error {
	if !commandsStopped {
		return syncError("skill/sync_blocked", "未确认所有远端命令停止，不能释放技能使用权")
	}
	return withDBLock(ctx, c.db, "user:"+orgID+":"+userID, func() error {
		return c.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			var run store.ComputerRun
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND org_id = ? AND user_id = ? AND parent_run_id IS NULL", rootRunID, orgID, userID).Take(&run).Error; err != nil {
				return err
			}
			if err := tx.Where("run_id = ? AND org_id = ? AND user_id = ?", rootRunID, orgID, userID).Delete(&store.RunSkillLease{}).Error; err != nil {
				return err
			}
			raw := map[string]any{}
			if len(run.Manifest) > 0 {
				if err := json.Unmarshal(run.Manifest, &raw); err != nil {
					return err
				}
			}
			raw["skill_leases_released"] = true
			encoded, _ := json.Marshal(raw)
			return tx.Model(&store.ComputerRun{}).Where("id = ?", rootRunID).Update("manifest", encoded).Error
		})
	})
}

type RecoveryRequest struct {
	OrgID, UserID, SkillID, ActorID string
	CommandsStopped                 bool
}

func (c *Coordinator) Recover(ctx context.Context, r RecoveryRequest) error {
	if !r.CommandsStopped {
		return syncError("skill/sync_blocked", "恢复前必须确认远端同步命令停止")
	}
	return withDBLock(ctx, c.db, "user:"+r.OrgID+":"+r.UserID, func() error {
		return c.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			var active int64
			if err := tx.Model(&store.RunSkillLease{}).Where("org_id = ? AND user_id = ? AND skill_id = ?", r.OrgID, r.UserID, r.SkillID).Count(&active).Error; err != nil {
				return err
			}
			if active > 0 {
				return syncError("skill/update_pending", "仍有技能使用者，不能恢复同步")
			}
			if err := tx.Model(&store.UserSkillState{}).Where("org_id = ? AND user_id = ? AND skill_id = ?", r.OrgID, r.UserID, r.SkillID).
				Updates(map[string]any{"status": "pending", "lock_owner": "", "lock_until": nil, "fence": gorm.Expr("fence + 1"), "last_error": ""}).Error; err != nil {
				return err
			}
			return audit.Log(tx, audit.Entry{OrgID: r.OrgID, ActorID: r.ActorID, Action: "skill.sync_recover", Target: r.SkillID, Detail: map[string]any{"user_id": r.UserID, "commands_stopped": true}})
		})
	})
}
