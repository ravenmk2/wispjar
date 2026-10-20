package workspacemigration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/ravenmk2/wispjar/internal/id"
	"github.com/ravenmk2/wispjar/internal/store"
	"gorm.io/gorm"
)

type localRemote struct {
	source, destination, temp string
	loseImport                bool
	afterImport               func()
}

func (r *localRemote) Run(ctx context.Context, command string) (Result, error) {
	// Test-only sandbox roots: execute the actual production Python while
	// mapping its fixed mounts into dedicated temp directories.
	command = strings.ReplaceAll(command, `SOURCE_ROOT = "/workspace"`, `SOURCE_ROOT = "`+r.source+`"`)
	command = strings.ReplaceAll(command, `TARGET_ROOT = "/sessions"`, `TARGET_ROOT = "`+r.destination+`"`)
	command = strings.ReplaceAll(command, `EXPORT_ROOT = "/tmp"`, `EXPORT_ROOT = "`+r.temp+`"`)
	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	output, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return Result{ExitCode: exit.ExitCode(), Output: string(output)}, nil
	}
	if err != nil {
		return Result{}, err
	}
	if strings.Contains(command, "extracted metadata/content differs") {
		if r.afterImport != nil {
			r.afterImport()
		}
		if r.loseImport {
			r.loseImport = false
			return Result{}, fmt.Errorf("lost response after actual import")
		}
	}
	return Result{Output: strings.TrimSpace(string(output))}, nil
}
func (r *localRemote) Download(_ context.Context, path string) (io.ReadCloser, error) {
	return os.Open(path)
}
func (r *localRemote) Upload(_ context.Context, path string, reader io.Reader) error {
	path = strings.Replace(path, "/sessions", r.destination, 1)
	file, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, err = io.Copy(file, reader)
	return errors.Join(err, file.Close())
}

type fixtureLegacy struct {
	info       LegacyInfo
	remote     *localRemote
	applyOpens int
}

func (f *fixtureLegacy) Inspect(context.Context, string) (LegacyInfo, error) { return f.info, nil }
func (f *fixtureLegacy) Open(_ context.Context, _ string, apply bool) (Remote, error) {
	if apply {
		f.applyOpens++
	}
	return f.remote, nil
}

type fixtureTargets struct {
	remote  *localRemote
	target  Target
	ensures int
	holds   map[string]bool
}

func (f *fixtureTargets) Ensure(context.Context, store.Session, LegacyInfo, string, string) (Target, error) {
	f.ensures++
	return f.target, nil
}
func (f *fixtureTargets) Open(context.Context, Target) (Remote, error) { return f.remote, nil }
func (f *fixtureTargets) Hold(_ context.Context, _ store.Session, _ Target, owner string) (string, error) {
	f.holds[owner] = true
	return owner, nil
}
func (f *fixtureTargets) Release(_ context.Context, _ store.Session, run string) error {
	delete(f.holds, run)
	return nil
}
func pythonFixture(t *testing.T) *localRemote {
	t.Helper()
	if os.Getenv("WISPJAR_MIGRATION_FS_TEST") != "1" {
		t.Skip("set WISPJAR_MIGRATION_FS_TEST=1 in isolated Linux root test container")
	}
	if runtime.GOOS != "linux" || os.Geteuid() != 0 {
		t.Fatal("requires isolated Linux root container")
	}
	if _, err := exec.LookPath("python3"); err != nil {
		t.Fatal(err)
	}
	r := &localRemote{source: t.TempDir(), destination: t.TempDir(), temp: t.TempDir()}
	if err := os.WriteFile(filepath.Join(r.source, "report.txt"), []byte("original"), 0640); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(r.source, "project"), 0750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(r.source, "project", "run.sh"), []byte("#!/bin/sh\n"), 0750); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("report.txt", filepath.Join(r.source, "relative")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(r.source, "project", "run.sh"), filepath.Join(r.source, "absolute")); err != nil {
		t.Fatal(err)
	}
	return r
}
func TestMigrationPythonSafetyIntegration(t *testing.T) {
	r := pythonFixture(t)
	ctx := context.Background()
	operation, sessionID := id.New(), id.New()
	var inventory json.RawMessage
	if err := runJSON(ctx, r, inventoryPython, map[string]string{}, &inventory); err != nil {
		t.Fatal(err)
	}
	var exported struct {
		Archive      string `json:"archive"`
		ArchiveHash  string `json:"archive_hash"`
		ManifestHash string `json:"manifest_hash"`
		SourceHash   string `json:"source_hash"`
	}
	if err := runJSON(ctx, r, exportPython, map[string]string{"operation_id": operation}, &exported); err != nil {
		t.Fatal(err)
	}
	args := map[string]string{"operation_id": operation, "session_id": sessionID, "archive_hash": exported.ArchiveHash, "manifest_hash": exported.ManifestHash}
	if err := runJSON(ctx, r, prepareTargetPython, args, nil); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(exported.Archive)
	if err != nil {
		t.Fatal(err)
	}
	err = r.Upload(ctx, "/sessions/.wispjar/migrations/"+operation+"/workspace.tar", file)
	_ = file.Close()
	if err != nil {
		t.Fatal(err)
	}
	if err := runJSON(ctx, r, importPython, args, nil); err != nil {
		t.Fatal(err)
	}
	if err := runJSON(ctx, r, importPython, args, nil); err != nil {
		t.Fatalf("idempotent publication: %v", err)
	}
	dest := filepath.Join(r.destination, sessionID)
	info, err := os.Stat(filepath.Join(dest, "report.txt"))
	if err != nil || info.Mode().Perm() != 0640 {
		t.Fatalf("mode changed: %v %v", info, err)
	}
	link, err := os.Readlink(filepath.Join(dest, "absolute"))
	if err != nil || link != "project/run.sh" {
		t.Fatalf("internal absolute link not normalized: %q %v", link, err)
	}
	for _, name := range []string{"inputs", "outputs"} {
		info, err := os.Stat(filepath.Join(dest, name))
		if err != nil || !info.IsDir() {
			t.Fatalf("missing default directory %s", name)
		}
	}
	// Verify the ownership mapping with the actual task identity, including
	// a 0700 workspace root and files that were previously owned by root.
	taskAccess := exec.CommandContext(ctx, "python3", "-c", `
import os,sys
os.chdir(sys.argv[1])
os.setgroups([])
os.setgid(65532)
os.setuid(65532)
assert open("report.txt").read()=="original"
assert open("absolute").read()=="#!/bin/sh\n"
with open("outputs/task.txt","w") as f: f.write("task output")
`, dest)
	if output, err := taskAccess.CombinedOutput(); err != nil {
		t.Fatalf("task UID cannot use migrated workspace: %v %s", err, output)
	}
	if err := os.WriteFile(filepath.Join(r.source, "report.txt"), []byte("changed"), 0640); err != nil {
		t.Fatal(err)
	}
	if err := runJSON(ctx, r, verifySourcePython, map[string]string{"source_hash": exported.SourceHash}, nil); err == nil || !strings.Contains(err.Error(), "source_changed") {
		t.Fatalf("expected explicit source_changed error: %v", err)
	}
	if err := os.Symlink("/etc/passwd", filepath.Join(r.source, "escape")); err != nil {
		t.Fatal(err)
	}
	if err := runJSON(ctx, r, inventoryPython, map[string]string{}, nil); err == nil {
		t.Fatal("escaping symlink accepted")
	}
	if err := os.Remove(filepath.Join(r.source, "escape")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(r.source, "inputs"), []byte("collision"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := runJSON(ctx, r, inventoryPython, map[string]string{}, nil); err == nil {
		t.Fatal("inputs file collision accepted")
	}
}

func migrationDBFixture(t *testing.T) (*gorm.DB, store.Session) {
	t.Helper()
	dsn := os.Getenv("WISPJAR_MIGRATION_TEST_DSN")
	if dsn == "" {
		t.Skip("set WISPJAR_MIGRATION_TEST_DSN to isolated migrated schema")
	}
	db, err := store.Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	pool, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := pool.Close(); err != nil {
			t.Error(err)
		}
	})
	org, user, provider, model, agent := id.New(), id.New(), id.New(), id.New(), id.New()
	sess := store.Session{ID: id.New(), OrgID: org, UserID: user, AgentID: agent, AgentSnapshot: json.RawMessage(`{}`), Sandbox: json.RawMessage(`{"id":"legacy-fixture","status":"running"}`), UsageTotal: json.RawMessage(`{}`), ModelParams: json.RawMessage(`{}`), Status: "active", LastActiveAt: time.Now()}
	records := []any{&store.Organization{ID: org, Name: "migration test", Status: "active"}, &store.User{ID: user, Username: "migrate-" + user, Status: "active"},
		&store.ModelProvider{ID: provider, Name: "test", Type: "openai", BaseURL: "http://localhost", APIKeyEnc: []byte{}, Extra: json.RawMessage(`{}`), CreatedBy: user, Status: "active"},
		&store.Model{ID: model, ProviderID: provider, ModelCode: "test", DisplayName: "test", Capabilities: json.RawMessage(`{}`), Status: "active"},
		&store.Agent{ID: agent, OrgID: org, Name: "test", ModelID: model, ToolsEnabled: json.RawMessage(`[]`), CreatedBy: user, Status: "active"}, &sess}
	for _, row := range records {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		for _, table := range []string{"resource_operation", "computer_run"} {
			if err := db.Table(table).Where("org_id = ?", org).Delete(map[string]any{}).Error; err != nil {
				t.Error(err)
			}
		}
		for i := len(records) - 1; i >= 0; i-- {
			if err := db.Delete(records[i]).Error; err != nil {
				t.Error(err)
			}
		}
	})
	return db, sess
}
func TestMigrationMySQLIntegration(t *testing.T) {
	r := pythonFixture(t)
	db, sess := migrationDBFixture(t)
	legacy := &fixtureLegacy{info: LegacyInfo{SandboxID: "legacy-fixture", State: "running", Image: "test-image", Metadata: map[string]string{"wispjar": "1", "wispjar-session": sess.ID, "wispjar-createdby": "wispjar-manager"}}, remote: r}
	targets := &fixtureTargets{remote: r, target: Target{ComputerID: id.New(), Generation: 1, SandboxID: "new-fixture"}, holds: map[string]bool{}}
	service := NewService(db, legacy, targets, t.TempDir())
	ctx := context.Background()
	var before int64
	if err := db.Model(&store.ResourceOperation{}).Count(&before).Error; err != nil {
		t.Fatal(err)
	}
	inventory, err := service.Inventory(ctx, []string{sess.ID})
	if err != nil || len(inventory) != 1 || !inventory[0].Eligible {
		t.Fatalf("inventory: %+v %v", inventory, err)
	}
	var after int64
	if err := db.Model(&store.ResourceOperation{}).Count(&after).Error; err != nil {
		t.Fatal(err)
	}
	if before != after || targets.ensures != 0 || legacy.applyOpens != 0 {
		t.Fatal("inventory performed writes/provisioning")
	}
	if _, err = service.Migrate(ctx, Request{SessionID: sess.ID}); !errors.Is(err, ErrConfirmationRequired) {
		t.Fatal("migration accepted no stop confirmation")
	}
	r.loseImport = true
	op, err := service.Migrate(ctx, Request{SessionID: sess.ID, ConfirmCommandsStopped: true})
	if !errors.Is(err, ErrUncertain) || op.Status != "blocked" {
		t.Fatalf("lost response not blocked: %s %v", op.Status, err)
	}
	var current store.Session
	if err := db.Where("id = ?", sess.ID).Take(&current).Error; err != nil {
		t.Fatal(err)
	}
	if current.ComputerID != nil {
		t.Fatal("bound before verification")
	}
	op, err = service.Migrate(ctx, Request{SessionID: sess.ID, ConfirmCommandsStopped: true})
	if err != nil {
		t.Fatal(err)
	}
	if op.Status != "completed" {
		t.Fatalf("not complete: %+v", op)
	}
	if err := db.Where("id = ?", sess.ID).Take(&current).Error; err != nil {
		t.Fatal(err)
	}
	if current.ComputerID == nil || *current.ComputerID != targets.target.ComputerID {
		t.Fatal("not bound after verification")
	}
	if len(targets.holds) != 0 {
		t.Fatal("maintenance occupant not released")
	}
	calls := targets.ensures
	if _, err = service.Migrate(ctx, Request{SessionID: sess.ID, ConfirmCommandsStopped: true}); err != nil {
		t.Fatal(err)
	}
	if targets.ensures != calls {
		t.Fatal("idempotent retry reprovisioned")
	}
	data, err := os.ReadFile(filepath.Join(r.source, "report.txt"))
	if err != nil || string(data) != "original" {
		t.Fatal("legacy source was changed/deleted")
	}
}

func TestMigrationSourceMutationBlocksBindingIntegration(t *testing.T) {
	r := pythonFixture(t)
	db, sess := migrationDBFixture(t)
	legacy := &fixtureLegacy{info: LegacyInfo{SandboxID: "legacy-fixture", State: "running", Image: "test-image", Metadata: map[string]string{"wispjar": "1", "wispjar-session": sess.ID, "wispjar-createdby": "wispjar-manager"}}, remote: r}
	targets := &fixtureTargets{remote: r, target: Target{ComputerID: id.New(), Generation: 1, SandboxID: "new-fixture"}, holds: map[string]bool{}}
	r.afterImport = func() {
		if err := os.WriteFile(filepath.Join(r.source, "report.txt"), []byte("manual concurrent edit"), 0640); err != nil {
			t.Error(err)
		}
	}
	service := NewService(db, legacy, targets, t.TempDir())
	if _, err := service.Migrate(context.Background(), Request{SessionID: sess.ID, ConfirmCommandsStopped: true}); err == nil || !strings.Contains(err.Error(), "source_changed") {
		t.Fatalf("expected explicit source_changed error: %v", err)
	}
	var current store.Session
	if err := db.Where("id = ?", sess.ID).Take(&current).Error; err != nil {
		t.Fatal(err)
	}
	if current.ComputerID != nil {
		t.Fatal("bound to outdated copy")
	}
	legacy.info.Metadata["wispjar-session"] = id.New()
	inventory, err := service.Inventory(context.Background(), []string{sess.ID})
	if err != nil {
		t.Fatal(err)
	}
	if inventory[0].Eligible {
		t.Fatal("foreign legacy identity accepted")
	}
}
