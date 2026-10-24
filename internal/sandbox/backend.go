package sandbox

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"time"

	"github.com/ravenmk2/wispjar/internal/volume"
)

// Backend owns provider operations, not the durable computer state machine.
type Backend interface {
	Ensure(context.Context, ComputerSpec, EnsurePolicy) (Instance, error)
	Inspect(context.Context, ComputerSpec) (string, error)
	Delete(context.Context, ComputerSpec) error
}

type EnsurePolicy struct{ AllowCreate, AllowResume bool }
type Pausable interface {
	Pause(context.Context, ComputerSpec) error
}
type Renewable interface {
	Renew(context.Context, ComputerSpec) error
}

// Instance exposes workload access only. It carries no privileged operations.
type Instance interface {
	Ref() InstanceRef
	Files() (FileReader, error)
}

type InstanceRef struct {
	InstanceID, ComputerID string
	Generation             uint64
}

type ComputerSpec struct {
	Installation, ComputerID, OrgID, UserID string
	Generation                              uint64
	SandboxID                               *string
	Options                                 Options
	Mounts                                  []volume.Mount
}

type Options struct {
	Image          string
	TTLSeconds     int
	ResourceLimits map[string]string
	NetworkPolicy  json.RawMessage
	Env            map[string]string
}

type CommandResult struct {
	ExitCode               int
	Stdout, Stderr, Output string
}

// FileInfo is a project-owned DTO for existing supervised tool operations.
type FileInfo struct {
	Path       string    `json:"path"`
	Type       string    `json:"type,omitempty"`
	Size       int64     `json:"size"`
	ModifiedAt time.Time `json:"modified_at"`
	CreatedAt  time.Time `json:"created_at"`
	Owner      string    `json:"owner"`
	Group      string    `json:"group"`
	Mode       int       `json:"mode"`
}

// Operations is the existing synchronous protocol. Runtime's ExecutionHandle
// supervises workload execution and confirms remote stop before releasing leases.
// An asynchronous Commands API can replace this without changing Instance identity.
type Operations interface {
	RunCommand(context.Context, string, string, map[string]string, int64, func(string, string)) (*CommandResult, error)
	UploadFile(context.Context, string, []byte) error
	DownloadFile(context.Context, string) ([]byte, error)
	ReplaceInFile(context.Context, string, string, string, bool) (int, error)
	SearchFiles(context.Context, string, string) ([]FileInfo, error)
	ListDir(context.Context, string) ([]FileInfo, error)
	FileInfo(context.Context, string) (FileInfo, error)
}

// Maintainer is injected only into trusted preparation/export services. Never
// hand it directly to a model tool; use the existing supervised workload wrapper.
type Maintainer interface {
	Operations
	DownloadFileStream(context.Context, string) (io.ReadCloser, error)
}

// MaintenanceProvider grants trusted access to an already validated connection,
// avoiding another lifecycle lookup. Adapters reject instances from other providers.
type MaintenanceProvider interface {
	Maintainer(Instance) (Maintainer, error)
}

var (
	ErrComputerIdentity  = errors.New("sandbox/computer_identity")
	ErrComputerMissing   = errors.New("sandbox/computer_missing")
	ErrComputerAmbiguous = errors.New("sandbox/computer_ambiguous")
	ErrComputerInsecure  = errors.New("computer/rebuild_required")
	ErrUnsupported       = errors.New("sandbox/unsupported")
	ErrBackendAuth       = errors.New("sandbox/backend_auth")
	ErrImageUnavailable  = errors.New("sandbox/image_unavailable")
	ErrUnavailable       = errors.New("sandbox/unavailable")
	ErrConflict          = errors.New("sandbox/conflict")
)
