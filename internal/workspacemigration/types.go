// Package workspacemigration performs explicit, resumable legacy workspace
// migrations. Inventory never provisions resources or changes database state.
package workspacemigration

import (
	"context"
	"encoding/json"
	"errors"
	"io"

	"github.com/ravenmk2/wispjar/internal/store"
)

var (
	ErrBusy                 = errors.New("migration/busy")
	ErrConflict             = errors.New("migration/conflict")
	ErrUncertain            = errors.New("migration/remote_uncertain")
	ErrConfirmationRequired = errors.New("migration/commands_not_confirmed_stopped")
)

type Result struct {
	ExitCode int
	Output   string
}
type Remote interface {
	Run(context.Context, string) (Result, error)
	Download(context.Context, string) (io.ReadCloser, error)
	Upload(context.Context, string, io.Reader) error
}
type LegacyInfo struct {
	SandboxID string            `json:"sandbox_id"`
	Image     string            `json:"image"`
	State     string            `json:"state"`
	Metadata  map[string]string `json:"-"`
}
type Legacy interface {
	Inspect(context.Context, string) (LegacyInfo, error)
	Open(context.Context, string, bool) (Remote, error) // resume only when apply=true
}
type Target struct {
	ComputerID string `json:"computer_id"`
	Generation uint64 `json:"generation"`
	SandboxID  string `json:"sandbox_id"`
}
type Targets interface {
	Ensure(context.Context, store.Session, LegacyInfo, string, string) (Target, error)
	Open(context.Context, Target) (Remote, error)
	Hold(context.Context, store.Session, Target, string) (string, error)
	Release(context.Context, store.Session, string) error
}
type InventoryItem struct {
	SessionID       string          `json:"session_id"`
	OrgID           string          `json:"org_id"`
	UserID          string          `json:"user_id"`
	ComputerID      *string         `json:"computer_id,omitempty"`
	Legacy          *LegacyInfo     `json:"legacy,omitempty"`
	Workspace       json.RawMessage `json:"workspace,omitempty"`
	Eligible        bool            `json:"eligible"`
	Reason          string          `json:"reason,omitempty"`
	EnvironmentNote string          `json:"environment_note"`
}
type Request struct {
	SessionID string
	// Required for an explicit legacy migration and every retry after unknown
	// remote execution. Runtime leases and source manifests are also checked.
	ConfirmCommandsStopped bool
}
type Progress struct {
	SessionID        string `json:"session_id"`
	SourceSandboxID  string `json:"source_sandbox_id"`
	Target           Target `json:"target"`
	MaintenanceRunID string `json:"maintenance_run_id,omitempty"`
	Phase            string `json:"phase"`
	SourceHash       string `json:"source_hash,omitempty"`
	ManifestHash     string `json:"manifest_hash,omitempty"`
	ArchiveHash      string `json:"archive_hash,omitempty"`
	Entries          int    `json:"entries,omitempty"`
	Bytes            int64  `json:"bytes,omitempty"`
}
