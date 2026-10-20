package computer

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/ravenmk2/wispjar/internal/store"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Repository interface {
	Member(context.Context, string, string) error
	Get(context.Context, string) (store.Computer, error)
	List(context.Context, string, string) ([]store.Computer, error)
	Template(context.Context, string, string) (Config, error)
	Bindings(context.Context, string, string) ([]store.UserAgentBinding, error)
	Bind(context.Context, store.UserAgentBinding) error
	Unbind(context.Context, string, string, string) error
	Intent(context.Context, *store.Computer, store.ResourceOperation) (store.ResourceOperation, error)
	Operation(context.Context, string) (store.ResourceOperation, error)
	Pending(context.Context) ([]store.ResourceOperation, error)
	Progress(context.Context, string, operationPlan) error
	Complete(context.Context, store.ResourceOperation, *string, string, error) error
	RecoverRuns(context.Context, store.ResourceOperation) error
	Lock(context.Context, string) (func() error, error)
	Runs(context.Context, string) ([]store.ComputerRun, error)
	RegisterRun(context.Context, store.ComputerRun, uint64) error
	FinishRun(context.Context, string, string, string) error
	Rename(context.Context, string, string, string, string) error
}

type GORMRepository struct{ db *gorm.DB }

func NewGORMRepository(db *gorm.DB) *GORMRepository { return &GORMRepository{db: db} }
func (r *GORMRepository) Member(ctx context.Context, org, user string) error {
	var n int64
	if err := r.db.WithContext(ctx).Model(&store.OrgMember{}).Where("org_id=? AND user_id=?", org, user).Count(&n).Error; err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
func (r *GORMRepository) Get(ctx context.Context, id string) (store.Computer, error) {
	var c store.Computer
	err := r.db.WithContext(ctx).First(&c, "id=?", id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		err = ErrNotFound
	}
	return c, err
}
func (r *GORMRepository) List(ctx context.Context, org, user string) ([]store.Computer, error) {
	cs := []store.Computer{}
	q := r.db.WithContext(ctx).Where("status <> ?", "deleted")
	if org != "" {
		q = q.Where("org_id=? AND user_id=?", org, user)
	}
	err := q.Order("created_at DESC").Find(&cs).Error
	return cs, err
}
func (r *GORMRepository) Template(ctx context.Context, org, agent string) (Config, error) {
	var a store.Agent
	var c Config
	err := r.db.WithContext(ctx).Where("id=? AND org_id=? AND status=?", agent, org, "published").First(&a).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return c, ErrNotFound
	}
	if err != nil {
		return c, err
	}
	if len(a.SandboxConfig) > 0 {
		err = json.Unmarshal(a.SandboxConfig, &c)
	}
	return c, err
}
func (r *GORMRepository) Bindings(ctx context.Context, org, user string) ([]store.UserAgentBinding, error) {
	rows := []store.UserAgentBinding{}
	err := r.db.WithContext(ctx).Where("org_id=? AND user_id=?", org, user).Find(&rows).Error
	return rows, err
}
func (r *GORMRepository) Bind(ctx context.Context, b store.UserAgentBinding) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var c store.Computer
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&c, "id=? AND org_id=? AND user_id=?", b.ComputerID, b.OrgID, b.UserID).Error; err != nil {
			return ErrNotFound
		}
		if c.Status == "deleted" || c.Status == "deleting" {
			return ErrBusy
		}
		return tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "org_id"}, {Name: "user_id"}, {Name: "agent_id"}},
			DoUpdates: clause.AssignmentColumns([]string{"computer_id", "updated_at"})}).Create(&b).Error
	})
}
func (r *GORMRepository) Unbind(ctx context.Context, org, user, agent string) error {
	return r.db.WithContext(ctx).Where("org_id=? AND user_id=? AND agent_id=?", org, user, agent).Delete(&store.UserAgentBinding{}).Error
}

func activeRuns(db *gorm.DB, id string) *gorm.DB {
	return db.Model(&store.ComputerRun{}).Where("computer_id=? AND status NOT IN ?", id, []string{"completed", "failed", "canceled"})
}

func planIntent(c *store.Computer, op *store.ResourceOperation, busy, inUse bool) error {
	if c.OrgID != op.OrgID || c.UserID != op.UserID || c.Status == "deleted" {
		return ErrNotFound
	}
	switch c.Status {
	case "starting", "pausing", "rebuilding", "deleting", "recovering":
		return ErrBusy
	}
	var recoverRequest struct {
		ReplaceMissing uint64     `json:"replace_missing"`
		IdleBefore     *time.Time `json:"idle_before,omitempty"`
	}
	_ = json.Unmarshal(op.Progress, &recoverRequest)
	if recoverRequest.IdleBefore != nil && (c.LastActiveAt == nil || c.LastActiveAt.After(*recoverRequest.IdleBefore)) {
		return ErrBusy
	}
	if recoverRequest.ReplaceMissing != 0 && recoverRequest.ReplaceMissing != c.Generation {
		return ErrBusy
	}
	if op.Action != "computer/recover" && (op.Action != "computer/start" || c.Status == "failed" || recoverRequest.ReplaceMissing != 0) && busy {
		return ErrBusy
	}
	if op.Action == "computer/delete" && inUse {
		return ErrInUse
	}
	p := operationPlan{Generation: c.Generation}
	switch op.Action {
	case "computer/create", "computer/start":
		if recoverRequest.ReplaceMissing != 0 {
			p.PreviousGeneration = c.Generation
			p.PreviousSandboxID = c.SandboxID
			c.Generation++
			p.Generation = c.Generation
			c.SandboxID = nil
			c.Status = "starting"
		} else if c.Status == "running" {
			op.Status = "completed"
		} else {
			c.Status = "starting"
		}
	case "computer/rebuild":
		p.PreviousGeneration = c.Generation
		p.PreviousSandboxID = c.SandboxID
		c.Generation++
		p.Generation = c.Generation
		c.SandboxID = nil
		c.Status = "rebuilding"
		// The request configuration was resolved before queuing.
		var req RebuildInput
		_ = json.Unmarshal(op.Progress, &req)
		if req.Config != nil {
			c.Config, _ = json.Marshal(req.Config)
		}
	case "computer/pause":
		c.Status = "pausing"
	case "computer/delete":
		c.Status = "deleting"
	case "computer/recover":
		// Keep the old generation until termination and all dependent cleanup
		// are confirmed. The recovering state rejects every new registration.
		p.PreviousGeneration = c.Generation
		p.PreviousSandboxID = c.SandboxID
		c.Status = "recovering"
	default:
		return ErrInvalid
	}
	c.LastError = ""
	op.Progress, _ = json.Marshal(p)
	return nil
}

func (r *GORMRepository) Intent(ctx context.Context, created *store.Computer, op store.ResourceOperation) (result store.ResourceOperation, err error) {
	// Serializes same-user idempotency keys even when they target different computers.
	unlock, err := store.AcquireAdvisoryLock(ctx, r.db, "computer-intent:"+op.OrgID, op.UserID)
	if err != nil {
		return op, err
	}
	defer func() { err = errors.Join(err, unlock()) }()
	err = r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if op.IdempotencyKey != nil {
			var old store.ResourceOperation
			err := tx.Where("org_id=? AND user_id=? AND action=? AND idempotency_key=?", op.OrgID, op.UserID, op.Action, *op.IdempotencyKey).First(&old).Error
			if err == nil {
				if old.RequestHash != op.RequestHash {
					return ErrConflict
				}
				op = old
				return nil
			}
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
		}
		var c store.Computer
		if created != nil {
			c = *created
		} else if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&c, "id=?", op.ResourceID).Error; err != nil {
			return ErrNotFound
		}
		var runs, bindings, sessions int64
		if err := activeRuns(tx, c.ID).Count(&runs).Error; err != nil {
			return err
		}
		if op.Action == "computer/delete" {
			if err := tx.Model(&store.Session{}).Where("computer_id=? AND status<>?", c.ID, "deleted").Count(&sessions).Error; err != nil {
				return err
			}
			if err := tx.Model(&store.UserAgentBinding{}).Where("computer_id=?", c.ID).Count(&bindings).Error; err != nil {
				return err
			}
		}
		var previous operationPlan
		if c.Status == "failed" {
			var last store.ResourceOperation
			e := tx.Where("resource_id=? AND resource_type=? AND status=?", c.ID, "computer", "failed").Order("created_at DESC").First(&last).Error
			if e != nil && !errors.Is(e, gorm.ErrRecordNotFound) {
				return e
			}
			if e == nil {
				if err := json.Unmarshal(last.Progress, &previous); err != nil {
					return err
				}
			}
		}
		if err := planIntent(&c, &op, runs > 0, sessions+bindings > 0); err != nil {
			return err
		}
		// A new start request must not erase evidence of an ambiguous create
		// or unfinished deletion from an earlier failed rebuild.
		if previous.Generation == c.Generation && op.Action == "computer/start" {
			op.Progress, _ = json.Marshal(previous)
		} else if previous.Generation != 0 {
			var current operationPlan
			_ = json.Unmarshal(op.Progress, &current)
			current.Cleanup = append(current.Cleanup, previous.Cleanup...)
			if previous.PreviousGeneration != 0 {
				current.Cleanup = append(current.Cleanup, retiredInstance{Generation: previous.PreviousGeneration, SandboxID: previous.PreviousSandboxID})
			}
			op.Progress, _ = json.Marshal(current)
		}
		if err := tx.Save(&c).Error; err != nil {
			return err
		}
		return tx.Create(&op).Error
	})
	return op, err
}
func (r *GORMRepository) Operation(ctx context.Context, id string) (store.ResourceOperation, error) {
	var op store.ResourceOperation
	err := r.db.WithContext(ctx).First(&op, "id=? AND resource_type=?", id, "computer").Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		err = ErrNotFound
	}
	return op, err
}
func (r *GORMRepository) Pending(ctx context.Context) ([]store.ResourceOperation, error) {
	rows := []store.ResourceOperation{}
	err := r.db.WithContext(ctx).Where("resource_type=? AND status IN ?", "computer", []string{"pending", "running"}).Order("created_at").Limit(100).Find(&rows).Error
	return rows, err
}
func (r *GORMRepository) Progress(ctx context.Context, id string, p operationPlan) error {
	raw, _ := json.Marshal(p)
	until := time.Now().Add(5 * time.Minute)
	return r.db.WithContext(ctx).Model(&store.ResourceOperation{}).Where("id=? AND status IN ?", id, []string{"pending", "running"}).
		Updates(map[string]any{"status": "running", "progress": raw, "lease_until": until}).Error
}
func (r *GORMRepository) Complete(ctx context.Context, op store.ResourceOperation, sandboxID *string, status string, cause error) error {
	var p operationPlan
	if err := json.Unmarshal(op.Progress, &p); err != nil {
		return err
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var current store.Computer
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&current, "id=?", op.ResourceID).Error; err != nil {
			return err
		}
		var currentOp store.ResourceOperation
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&currentOp, "id=?", op.ID).Error; err != nil {
			return err
		}
		if current.Generation != p.Generation || (currentOp.Status != "pending" && currentOp.Status != "running") {
			return ErrConflict
		}
		fields := map[string]any{"status": status, "last_error": ""}
		opStatus := "completed"
		msg := ""
		if cause != nil {
			fields["status"] = "failed"
			msg = operationError(cause, op.ID)
			fields["last_error"] = msg
			opStatus = "failed"
			if op.Action == "computer/recover" {
				fields["status"] = "recovering"
				opStatus = "running"
			}
		}
		if cause == nil {
			fields["sandbox_id"] = sandboxID
			fields["last_active_at"] = time.Now()
			if op.Action == "computer/recover" {
				if current.Status != "recovering" {
					return ErrConflict
				}
				var saved operationPlan
				if err := json.Unmarshal(currentOp.Progress, &saved); err != nil {
					return err
				}
				if !saved.RunsRecovered {
					return ErrConflict
				}
				fields["generation"] = p.Generation + 1
			}
		}
		result := tx.Model(&store.Computer{}).Where("id=? AND generation=?", op.ResourceID, p.Generation).Updates(fields)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrConflict
		}
		return tx.Model(&store.ResourceOperation{}).Where("id=? AND status IN ?", op.ID, []string{"pending", "running"}).
			Updates(map[string]any{"status": opStatus, "error": msg, "lease_until": nil}).Error
	})
}
func (r *GORMRepository) Lock(ctx context.Context, id string) (func() error, error) {
	return store.AcquireAdvisoryLock(ctx, r.db, "computer-worker", id)
}
func (r *GORMRepository) Runs(ctx context.Context, id string) ([]store.ComputerRun, error) {
	rows := []store.ComputerRun{}
	err := activeRuns(r.db.WithContext(ctx), id).Find(&rows).Error
	return rows, err
}
func (r *GORMRepository) RegisterRun(ctx context.Context, run store.ComputerRun, generation uint64) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var c store.Computer
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&c, "id=?", run.ComputerID).Error; err != nil {
			return ErrNotFound
		}
		if c.OrgID != run.OrgID || c.UserID != run.UserID {
			return ErrNotFound
		}
		if c.Status != "running" || c.Generation != generation {
			return ErrBusy
		}
		if run.SessionID != "" {
			var session store.Session
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&session, "id=? AND org_id=? AND user_id=? AND computer_id=? AND status<>?", run.SessionID, run.OrgID, run.UserID, c.ID, "deleted").Error; err != nil {
				return ErrNotFound
			}
			var workspace struct {
				State string `json:"state"`
			}
			_ = json.Unmarshal(session.Workspace, &workspace)
			if workspace.State == "migrating" {
				return ErrBusy
			}
			if run.ParentRunID == nil && !strings.HasPrefix(run.Owner, "runtime/files:") {
				var n int64
				if err := activeRuns(tx, c.ID).Where("session_id=? AND parent_run_id IS NULL AND owner NOT LIKE ?", run.SessionID, "runtime/files:%").Count(&n).Error; err != nil {
					return err
				}
				if n > 0 {
					return ErrBusy
				}
			}
		}
		if run.ParentRunID != nil {
			var parent store.ComputerRun
			if err := tx.First(&parent, "id=? AND computer_id=? AND session_id=?", *run.ParentRunID, c.ID, run.SessionID).Error; err != nil {
				return ErrNotFound
			}
			if TerminalRun(parent.Status) {
				return ErrBusy
			}
		}
		var execution map[string]any
		if len(run.Execution) > 0 {
			if err := json.Unmarshal(run.Execution, &execution); err != nil {
				return ErrInvalid
			}
		}
		if execution == nil {
			execution = map[string]any{}
		}
		execution["generation"] = generation
		run.Execution, _ = json.Marshal(execution)
		var manifest map[string]any
		if len(run.Manifest) > 0 {
			if err := json.Unmarshal(run.Manifest, &manifest); err != nil {
				return ErrInvalid
			}
		}
		if manifest == nil {
			manifest = map[string]any{}
		}
		manifest["generation"] = generation
		run.Manifest, _ = json.Marshal(manifest)
		run.Status = "running"
		if err := tx.Create(&run).Error; err != nil {
			return err
		}
		return tx.Model(&c).Update("last_active_at", time.Now()).Error
	})
}
func (r *GORMRepository) FinishRun(ctx context.Context, runID, user, status string) error {
	if !TerminalRun(status) {
		return ErrInvalid
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var run store.ComputerRun
		if err := tx.First(&run, "id=? AND user_id=?", runID, user).Error; err != nil {
			return ErrNotFound
		}
		var c store.Computer
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&c, "id=?", run.ComputerID).Error; err != nil {
			return err
		}
		// A recovery may have terminalized this run while we waited for the
		// computer lock. Late runtime callbacks must preserve that outcome.
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&run, "id=? AND user_id=?", runID, user).Error; err != nil {
			return err
		}
		if TerminalRun(run.Status) {
			return nil
		}
		var n int64
		if err := activeRuns(tx, c.ID).Where("parent_run_id=?", runID).Count(&n).Error; err != nil {
			return err
		}
		if n > 0 {
			return ErrBusy
		}
		if err := tx.Model(&store.ComputerRun{}).Where("id=? AND user_id=?", runID, user).Updates(map[string]any{"status": status, "lease_until": nil}).Error; err != nil {
			return err
		}
		return tx.Model(&c).Update("last_active_at", time.Now()).Error
	})
}
func (r *GORMRepository) Rename(ctx context.Context, id, org, user, name string) error {
	return r.db.WithContext(ctx).Model(&store.Computer{}).Where("id=? AND org_id=? AND user_id=? AND status<>?", id, org, user, "deleted").Update("name", name).Error
}
