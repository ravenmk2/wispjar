package workspacemigration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/ravenmk2/wispjar/internal/id"
	"github.com/ravenmk2/wispjar/internal/store"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const maxArchiveBytes int64 = 1200 * 1024 * 1024

type Service struct {
	db      *gorm.DB
	legacy  Legacy
	targets Targets
	tempDir string
}

func NewService(db *gorm.DB, legacy Legacy, targets Targets, tempDir string) *Service {
	return &Service{db: db, legacy: legacy, targets: targets, tempDir: tempDir}
}
func validID(v string) bool {
	if len(v) != 32 {
		return false
	}
	for _, c := range v {
		if !(c >= '0' && c <= '9') && !(c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
func sourceID(sess store.Session) (string, error) {
	var state struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(sess.Sandbox, &state); err != nil {
		return "", err
	}
	if state.ID == "" {
		return "", fmt.Errorf("%w: no legacy sandbox", ErrConflict)
	}
	return state.ID, nil
}
func verifyLegacy(sess store.Session, info LegacyInfo) error {
	sid, err := sourceID(sess)
	if err != nil {
		return err
	}
	if info.SandboxID != sid || info.Metadata["wispjar"] != "1" || info.Metadata["wispjar-session"] != sess.ID || info.Metadata["wispjar-createdby"] != "wispjar-manager" {
		return fmt.Errorf("%w: legacy identity mismatch", ErrConflict)
	}
	return nil
}
func noRuns(db *gorm.DB, sess store.Session) error {
	if sess.Running {
		return ErrBusy
	}
	var count int64
	if err := db.Model(&store.ComputerRun{}).Where("session_id = ? AND status NOT IN ?", sess.ID, []string{"completed", "failed", "canceled"}).Count(&count).Error; err != nil {
		return err
	}
	if count != 0 {
		return ErrBusy
	}
	return nil
}
func runJSON(ctx context.Context, remote Remote, script string, args any, out any) error {
	result, err := remote.Run(ctx, command(script, args))
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUncertain, err)
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("migration command exited %d: %s", result.ExitCode, result.Output)
	}
	if out != nil {
		return json.Unmarshal([]byte(result.Output), out)
	}
	return nil
}

func (s *Service) Inventory(ctx context.Context, sessionIDs []string) ([]InventoryItem, error) {
	if len(sessionIDs) == 0 {
		return nil, fmt.Errorf("explicit session IDs are required")
	}
	result := make([]InventoryItem, 0, len(sessionIDs))
	for _, sessionID := range sessionIDs {
		if !validID(sessionID) {
			return nil, fmt.Errorf("invalid session ID")
		}
		var sess store.Session
		if err := s.db.WithContext(ctx).Where("id = ?", sessionID).Take(&sess).Error; err != nil {
			return nil, err
		}
		item := InventoryItem{SessionID: sess.ID, OrgID: sess.OrgID, UserID: sess.UserID, ComputerID: sess.ComputerID,
			EnvironmentNote: "Only /workspace is copied. Image and session sandbox configuration are preserved; original system-layer installs and historical platform defaults remain in the old instance. File ownership maps to task UID/GID 65532; rwx modes and timestamps are preserved. External symlinks are rejected."}
		check := func() error {
			if sess.ComputerID != nil {
				return fmt.Errorf("already bound to a computer")
			}
			if sess.Status == "deleted" {
				return fmt.Errorf("session deleted")
			}
			if err := noRuns(s.db.WithContext(ctx), sess); err != nil {
				return err
			}
			sandboxID, err := sourceID(sess)
			if err != nil {
				return err
			}
			info, err := s.legacy.Inspect(ctx, sandboxID)
			if err != nil {
				return err
			}
			item.Legacy = &info
			if err = verifyLegacy(sess, info); err != nil {
				return err
			}
			if info.State != "running" {
				return fmt.Errorf("source is %s; inventory does not resume it", info.State)
			}
			remote, err := s.legacy.Open(ctx, sandboxID, false)
			if err != nil {
				return err
			}
			var inventory json.RawMessage
			if err = runJSON(ctx, remote, inventoryPython, map[string]any{}, &inventory); err != nil {
				return err
			}
			item.Workspace = inventory
			return nil
		}
		err := check()
		item.Eligible = err == nil
		if err != nil {
			item.Reason = err.Error()
		}
		result = append(result, item)
	}
	return result, nil
}
func (s *Service) operation(ctx context.Context, sessionID string) (store.ResourceOperation, error) {
	var op store.ResourceOperation
	err := s.db.WithContext(ctx).Where("resource_type = ? AND resource_id = ? AND action = ?", "session", sessionID, "workspace/migrate").Order("created_at DESC").Take(&op).Error
	return op, err
}
func (s *Service) save(ctx context.Context, op *store.ResourceOperation, progress Progress, status, reason string) error {
	raw, err := json.Marshal(progress)
	if err != nil {
		return err
	}
	err = s.db.WithContext(ctx).Model(&store.ResourceOperation{}).Where("id = ?", op.ID).Updates(map[string]any{"progress": raw, "status": status, "error": reason}).Error
	if err == nil {
		op.Progress = raw
		op.Status = status
		op.Error = reason
	}
	return err
}
func (s *Service) begin(ctx context.Context, sessionID string) (sess store.Session, op store.ResourceOperation, progress Progress, err error) {
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", sessionID).Take(&sess).Error; err != nil {
			return err
		}
		if sess.Status == "deleted" {
			return ErrConflict
		}
		var old store.ResourceOperation
		opErr := tx.Where("resource_type = ? AND resource_id = ? AND action = ?", "session", sessionID, "workspace/migrate").Order("created_at DESC").Take(&old).Error
		if opErr == nil {
			op = old
			if err := json.Unmarshal(op.Progress, &progress); err != nil {
				return err
			}
			if op.Status == "completed" {
				if sess.ComputerID != nil && *sess.ComputerID == progress.Target.ComputerID {
					return nil
				}
				return ErrConflict
			}
		} else if !errors.Is(opErr, gorm.ErrRecordNotFound) {
			return opErr
		}
		if err := noRuns(tx, sess); err != nil {
			return err
		}
		if sess.ComputerID != nil {
			return ErrConflict
		}
		sandboxID, err := sourceID(sess)
		if err != nil {
			return err
		}
		if op.ID == "" {
			progress = Progress{SessionID: sessionID, SourceSandboxID: sandboxID, Phase: "frozen"}
			key := sessionID
			raw, _ := json.Marshal(progress)
			op = store.ResourceOperation{ID: id.New(), OrgID: sess.OrgID, UserID: sess.UserID, ResourceType: "session", ResourceID: sessionID, Action: "workspace/migrate", Status: "running", IdempotencyKey: &key, Progress: raw}
			if err := tx.Create(&op).Error; err != nil {
				return err
			}
		} else if progress.SourceSandboxID != sandboxID {
			return ErrConflict
		}
		workspace, _ := json.Marshal(map[string]any{"state": "migrating", "migration_operation_id": op.ID})
		return tx.Model(&store.Session{}).Where("id = ? AND computer_id IS NULL", sessionID).Update("workspace", workspace).Error
	})
	return
}

func (s *Service) Migrate(ctx context.Context, request Request) (op store.ResourceOperation, err error) {
	if !validID(request.SessionID) {
		return op, fmt.Errorf("invalid session ID")
	}
	if !request.ConfirmCommandsStopped {
		return op, ErrConfirmationRequired
	}
	if s.targets == nil {
		return op, fmt.Errorf("migration target service unavailable")
	}
	release, err := store.AcquireAdvisoryLock(ctx, s.db, "workspace-migration", request.SessionID)
	if err != nil {
		return op, err
	}
	defer func() { err = errors.Join(err, release()) }()
	sess, op, progress, err := s.begin(ctx, request.SessionID)
	if err != nil {
		return op, err
	}
	if op.Status == "completed" {
		if progress.MaintenanceRunID != "" {
			err = s.targets.Release(ctx, sess, progress.MaintenanceRunID)
		}
		return op, err
	}
	// Failure leaves both original data and the migration gate intact. An
	// explicit confirmed retry may finish a previous maintenance occupant.
	defer func() {
		if err == nil || op.Status == "completed" {
			return
		}
		recoveryCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		// A failed COMMIT response may hide a successful atomic binding.
		// Resolve it before recording failure; if SQL is still unavailable,
		// leave the durable intent untouched for the next explicit retry.
		persisted, readErr := s.operation(recoveryCtx, sess.ID)
		if readErr != nil {
			err = errors.Join(err, readErr)
			return
		}
		if persisted.Status == "completed" {
			op = persisted
			return
		}
		status := "failed"
		if errors.Is(err, ErrUncertain) {
			status = "blocked"
		}
		saveErr := s.save(recoveryCtx, &op, progress, status, "Migration "+progress.Phase+" failed; original workspace retained. Review maintenance diagnostics before retry.")
		workspace, _ := json.Marshal(map[string]any{"state": "migration_failed", "migration_operation_id": op.ID})
		gateErr := s.db.WithContext(recoveryCtx).Model(&store.Session{}).Where("id = ? AND computer_id IS NULL", sess.ID).Update("workspace", workspace).Error
		err = errors.Join(err, saveErr, gateErr)
	}()
	info, err := s.legacy.Inspect(ctx, progress.SourceSandboxID)
	if err != nil {
		return op, err
	}
	if err = verifyLegacy(sess, info); err != nil {
		return op, err
	}
	source, err := s.legacy.Open(ctx, info.SandboxID, true)
	if err != nil {
		return op, err
	}
	var exported struct {
		Archive      string `json:"archive"`
		SourceHash   string `json:"source_hash"`
		ManifestHash string `json:"manifest_hash"`
		ArchiveHash  string `json:"archive_hash"`
		Entries      int    `json:"entries"`
		Bytes        int64  `json:"bytes"`
	}
	progress.Phase = "exporting"
	if err = s.save(ctx, &op, progress, "running", ""); err != nil {
		return op, err
	}
	if err = runJSON(ctx, source, exportPython, map[string]string{"operation_id": op.ID}, &exported); err != nil {
		return op, err
	}
	progress.SourceHash = exported.SourceHash
	progress.ManifestHash = exported.ManifestHash
	progress.ArchiveHash = exported.ArchiveHash
	progress.Entries = exported.Entries
	progress.Bytes = exported.Bytes
	progress.Phase = "exported"
	if err = s.save(ctx, &op, progress, "running", ""); err != nil {
		return op, err
	}
	file, err := os.CreateTemp(s.tempDir, "wispjar-migration-*.tar")
	if err != nil {
		return op, err
	}
	defer func() { err = errors.Join(err, file.Close(), os.Remove(file.Name())) }()
	reader, err := source.Download(ctx, exported.Archive)
	if err != nil {
		return op, err
	}
	hash := sha256.New()
	n, copyErr := io.Copy(io.MultiWriter(file, hash), io.LimitReader(reader, maxArchiveBytes+1))
	err = errors.Join(copyErr, reader.Close())
	if err != nil {
		return op, err
	}
	if n > maxArchiveBytes || hex.EncodeToString(hash.Sum(nil)) != exported.ArchiveHash {
		return op, fmt.Errorf("downloaded archive limit/hash mismatch")
	}
	if progress.MaintenanceRunID != "" {
		if err = s.targets.Release(ctx, sess, progress.MaintenanceRunID); err != nil {
			return op, err
		}
		progress.MaintenanceRunID = ""
	}
	target, targetErr := s.targets.Ensure(ctx, sess, info, op.ID, progress.Target.ComputerID)
	if target.ComputerID != "" {
		progress.Target = target
	}
	if err = s.save(ctx, &op, progress, "running", ""); err != nil {
		return op, errors.Join(err, targetErr)
	}
	if targetErr != nil {
		return op, targetErr
	}
	progress.MaintenanceRunID, err = s.targets.Hold(ctx, sess, target, op.ID)
	if err != nil {
		return op, err
	}
	if err = s.save(ctx, &op, progress, "running", ""); err != nil {
		return op, err
	}
	destination, err := s.targets.Open(ctx, target)
	if err != nil {
		return op, err
	}
	args := map[string]string{"operation_id": op.ID, "session_id": sess.ID, "archive_hash": progress.ArchiveHash, "manifest_hash": progress.ManifestHash}
	if err = runJSON(ctx, destination, prepareTargetPython, args, nil); err != nil {
		return op, err
	}
	if _, err = file.Seek(0, io.SeekStart); err != nil {
		return op, err
	}
	progress.Phase = "copying"
	if err = s.save(ctx, &op, progress, "running", ""); err != nil {
		return op, err
	}
	if err = destination.Upload(ctx, "/sessions/.wispjar/migrations/"+op.ID+"/workspace.tar", file); err != nil {
		return op, err
	}
	var verified struct {
		Verified  bool `json:"verified"`
		Published bool `json:"published"`
	}
	if err = runJSON(ctx, destination, importPython, args, &verified); err != nil {
		return op, err
	}
	if !verified.Verified || !verified.Published {
		return op, fmt.Errorf("destination did not verify publication")
	}
	if err = runJSON(ctx, source, verifySourcePython, map[string]string{"source_hash": progress.SourceHash}, nil); err != nil {
		return op, err
	}
	progress.Phase = "verified"
	if err = s.save(ctx, &op, progress, "running", ""); err != nil {
		return op, err
	}
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var current store.Session
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", sess.ID).Take(&current).Error; err != nil {
			return err
		}
		if current.Status == "deleted" {
			return ErrConflict
		}
		if err := noRuns(tx, current); err != nil {
			return err
		}
		currentSource, err := sourceID(current)
		if err != nil {
			return err
		}
		if current.ComputerID != nil || currentSource != progress.SourceSandboxID {
			return ErrConflict
		}
		var gate struct {
			State     string `json:"state"`
			Operation string `json:"migration_operation_id"`
		}
		if err := json.Unmarshal(current.Workspace, &gate); err != nil {
			return err
		}
		if gate.State != "migrating" || gate.Operation != op.ID {
			return ErrConflict
		}
		workspace, _ := json.Marshal(map[string]any{"state": "ready", "layout_version": 1, "migration_operation_id": op.ID, "source_hash": progress.SourceHash, "owner_uid": 65532})
		result := tx.Model(&store.Session{}).Where("id = ? AND computer_id IS NULL", sess.ID).Updates(map[string]any{"computer_id": target.ComputerID, "workspace": workspace})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrConflict
		}
		progress.Phase = "completed"
		raw, _ := json.Marshal(progress)
		return tx.Model(&store.ResourceOperation{}).Where("id = ?", op.ID).Updates(map[string]any{"status": "completed", "progress": raw, "error": ""}).Error
	})
	if err != nil {
		return op, err
	}
	op.Status = "completed"
	op.Progress, _ = json.Marshal(progress)
	// Successful remote exit + verification is positive evidence that the
	// migration commands ended; only now release the maintenance occupant.
	if err = s.targets.Release(ctx, sess, progress.MaintenanceRunID); err != nil {
		return op, err
	}
	return op, nil
}
