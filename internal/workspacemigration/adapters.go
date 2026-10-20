package workspacemigration

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path"
	"strings"
	"time"

	opensandbox "github.com/alibaba/OpenSandbox/sdks/sandbox/go"
	"github.com/ravenmk2/wispjar/internal/computer"
	"github.com/ravenmk2/wispjar/internal/store"
	"gorm.io/gorm"
)

// SDKLegacy is independent of the legacy session manager/reaper. It only
// connects to the explicit sandbox ID and never creates or deletes one.
type SDKLegacy struct {
	conn      opensandbox.ConnectionConfig
	lifecycle *opensandbox.LifecycleClient
}

func NewSDKLegacy(endpoint, key string, proxy bool) *SDKLegacy {
	base := strings.TrimSuffix(strings.TrimRight(endpoint, "/"), "/v1")
	return &SDKLegacy{conn: opensandbox.ConnectionConfig{Domain: base, APIKey: key, UseServerProxy: proxy, RequestTimeout: 120 * time.Second, DisableMetrics: true},
		lifecycle: opensandbox.NewLifecycleClient(base+"/v1", key)}
}
func (s *SDKLegacy) Inspect(ctx context.Context, id string) (LegacyInfo, error) {
	info, err := s.lifecycle.GetSandbox(ctx, id)
	if err != nil {
		return LegacyInfo{}, err
	}
	image := ""
	if info.Image != nil {
		image = info.Image.URI
	}
	state := strings.ToLower(string(info.Status.State))
	return LegacyInfo{SandboxID: info.ID, Image: image, State: state, Metadata: info.Metadata}, nil
}
func (s *SDKLegacy) Open(ctx context.Context, id string, apply bool) (Remote, error) {
	info, err := s.Inspect(ctx, id)
	if err != nil {
		return nil, err
	}
	if info.State == "paused" && apply {
		if err = s.lifecycle.ResumeSandbox(ctx, id); err != nil {
			return nil, err
		}
		for attempt := 0; attempt < 60; attempt++ {
			info, err = s.Inspect(ctx, id)
			if err != nil {
				return nil, err
			}
			if info.State == "running" {
				break
			}
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(500 * time.Millisecond):
			}
		}
	}
	if info.State != "running" {
		return nil, fmt.Errorf("source is %s", info.State)
	}
	sb, err := opensandbox.ConnectSandbox(ctx, s.conn, id)
	if err != nil {
		return nil, err
	}
	return &sdkRemote{sb: sb}, nil
}
func (s *SDKLegacy) connect(ctx context.Context, id string) (Remote, error) {
	sb, err := opensandbox.ConnectSandbox(ctx, s.conn, id)
	if err != nil {
		return nil, err
	}
	return &sdkRemote{sb: sb}, nil
}

type sdkRemote struct{ sb *opensandbox.Sandbox }

func (r *sdkRemote) Run(ctx context.Context, command string) (Result, error) {
	var output strings.Builder
	execution, err := r.sb.RunCommandWithOpts(ctx, opensandbox.RunCommandRequest{Command: command, Cwd: "/", Timeout: 120000},
		&opensandbox.ExecutionHandlers{
			OnStdout: func(m opensandbox.OutputMessage) error {
				output.WriteString(m.Text)
				output.WriteByte('\n')
				return nil
			},
			OnStderr: func(m opensandbox.OutputMessage) error {
				output.WriteString(m.Text)
				output.WriteByte('\n')
				return nil
			},
		})
	if err != nil {
		return Result{}, err
	}
	if execution == nil || execution.ExitCode == nil {
		return Result{}, ErrUncertain
	}
	return Result{ExitCode: *execution.ExitCode, Output: strings.TrimSpace(output.String())}, nil
}
func (r *sdkRemote) Download(ctx context.Context, file string) (io.ReadCloser, error) {
	return r.sb.DownloadFile(ctx, file, "")
}
func (r *sdkRemote) Upload(ctx context.Context, file string, reader io.Reader) error {
	return r.sb.UploadFile(ctx, reader, opensandbox.UploadFileOptions{FileName: path.Base(file), Metadata: opensandbox.FileMetadata{Path: file}})
}

type ComputerTargets struct {
	db        *gorm.DB
	computers *computer.Service
	sdk       *SDKLegacy
}

func NewComputerTargets(db *gorm.DB, computers *computer.Service, sdk *SDKLegacy) *ComputerTargets {
	return &ComputerTargets{db: db, computers: computers, sdk: sdk}
}
func (t *ComputerTargets) Ensure(ctx context.Context, sess store.Session, source LegacyInfo, operationID, existingID string) (Target, error) {
	target := Target{ComputerID: existingID}
	if existingID == "" {
		var snapshot struct {
			SandboxConfig json.RawMessage `json:"sandbox_config"`
		}
		if err := json.Unmarshal(sess.AgentSnapshot, &snapshot); err != nil {
			return target, err
		}
		var config computer.Config
		if len(snapshot.SandboxConfig) > 0 {
			if err := json.Unmarshal(snapshot.SandboxConfig, &config); err != nil {
				return target, err
			}
		}
		config.Image = source.Image
		// Network egress stays platform-managed, as for ordinary creation.
		config.NetworkPolicy = nil
		op, err := t.computers.Create(ctx, sess.OrgID, sess.UserID, "migration-"+operationID, computer.CreateInput{Name: "Migrated " + sess.ID, Config: config})
		if op.ResourceID != "" {
			target.ComputerID = op.ResourceID
		}
		if err != nil {
			return target, err
		}
		if err = t.computers.Process(ctx, op.ID); err != nil {
			return target, err
		}
	}
	var pending []store.ResourceOperation
	if err := t.db.WithContext(ctx).Where("resource_type = ? AND resource_id = ? AND status IN ?", "computer", target.ComputerID, []string{"pending", "running"}).Order("created_at").Find(&pending).Error; err != nil {
		return target, err
	}
	for _, operation := range pending {
		if err := t.computers.Process(ctx, operation.ID); err != nil {
			return target, err
		}
	}
	row, err := t.computers.EnsureRunning(ctx, sess.OrgID, sess.UserID, target.ComputerID)
	if err != nil {
		return target, err
	}
	target.Generation = row.Generation
	if row.SandboxID == nil {
		return target, fmt.Errorf("target computer has no sandbox")
	}
	target.SandboxID = *row.SandboxID
	return target, nil
}
func (t *ComputerTargets) Open(ctx context.Context, target Target) (Remote, error) {
	var row store.Computer
	if err := t.db.WithContext(ctx).Where("id = ?", target.ComputerID).Take(&row).Error; err != nil {
		return nil, err
	}
	if row.Generation != target.Generation || row.SandboxID == nil || *row.SandboxID != target.SandboxID {
		return nil, ErrConflict
	}
	// Handle validates the full installation/computer/generation identity.
	if _, err := t.computers.Handle(ctx, row.OrgID, row.UserID, row.ID); err != nil {
		return nil, err
	}
	return t.sdk.connect(ctx, target.SandboxID)
}
func (t *ComputerTargets) Hold(ctx context.Context, sess store.Session, target Target, operationID string) (string, error) {
	owner := "workspace-migration:" + operationID
	var runs []store.ComputerRun
	if err := t.db.WithContext(ctx).Where("computer_id = ? AND org_id = ? AND user_id = ? AND owner = ? AND status NOT IN ?", target.ComputerID, sess.OrgID, sess.UserID, owner, []string{"completed", "failed", "canceled"}).Find(&runs).Error; err != nil {
		return "", err
	}
	if len(runs) > 1 {
		return "", ErrConflict
	}
	if len(runs) == 1 {
		return runs[0].ID, nil
	}
	return t.computers.AcquireUse(ctx, sess.OrgID, sess.UserID, target.ComputerID, owner, target.Generation)
}
func (t *ComputerTargets) Release(ctx context.Context, sess store.Session, runID string) error {
	var run store.ComputerRun
	if err := t.db.WithContext(ctx).Where("id = ? AND org_id = ? AND user_id = ? AND owner LIKE ?", runID, sess.OrgID, sess.UserID, "workspace-migration:%").Take(&run).Error; err != nil {
		return err
	}
	return t.computers.FinishRun(ctx, runID, sess.UserID, "completed")
}
