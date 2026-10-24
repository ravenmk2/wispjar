package opensandbox

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/ravenmk2/wispjar/internal/config"
	"github.com/ravenmk2/wispjar/internal/id"
	"github.com/ravenmk2/wispjar/internal/sandbox"
	"github.com/ravenmk2/wispjar/internal/store"
	"github.com/ravenmk2/wispjar/internal/volume"
)

// This test needs an explicitly selected isolated application config, migrated
// database, Docker endpoint and OpenSandbox. It never starts the legacy reaper.
// It deletes only its identity-checked sandbox instances, retaining the three
// test volumes and their database identities for persistence verification.
func TestComputerPersistenceIntegration(t *testing.T) {
	configPath := os.Getenv("WISPJAR_COMPUTER_IT_CONFIG")
	if configPath == "" {
		t.Skip("set WISPJAR_COMPUTER_IT_CONFIG to an isolated test configuration")
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(cfg.Database.DSN)
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	repo := volume.NewGORMRepository(db)
	provider, err := volume.NewDocker(cfg.Storage.Volumes.DockerEndpoint)
	if err != nil {
		t.Fatal(err)
	}
	volumes, err := volume.NewService(repo, provider, volume.Options{
		Namespace: cfg.Storage.Volumes.Namespace, NamePattern: cfg.Storage.Volumes.NamePattern,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	orgID, userID := id.New(), id.New()
	mounts, err := volumes.Ensure(ctx, orgID, userID)
	if err != nil {
		t.Fatal(err)
	}
	installation, err := repo.InstallationID(ctx)
	if err != nil {
		t.Fatal(err)
	}
	backend := NewComputerBackend(NewClient(cfg.OpenSandbox.LifecycleURL, cfg.OpenSandbox.APIKey, true))
	ensureHandle := func(ctx context.Context, spec ComputerSpec, create bool) (*Handle, error) {
		instance, err := backend.Ensure(ctx, spec, sandbox.EnsurePolicy{AllowCreate: create, AllowResume: true})
		if err != nil {
			return nil, err
		}
		maintenance, err := backend.Maintainer(instance)
		if err != nil {
			return nil, err
		}
		return maintenance.(*Handle), nil
	}
	newSpec := func() ComputerSpec {
		return ComputerSpec{
			Installation: installation, ComputerID: id.New(), OrgID: orgID, UserID: userID,
			Generation: 1, Mounts: mounts,
			Options: Options{Image: "python:3.12-slim", TTLSeconds: 600,
				ResourceLimits: map[string]string{"cpu": "1", "memory": "256Mi"}},
		}
	}
	first, second := newSpec(), newSpec()
	t.Cleanup(func() {
		cleanupCtx, stop := context.WithTimeout(context.Background(), time.Minute)
		defer stop()
		for _, spec := range []ComputerSpec{first, second} {
			if err := backend.Delete(cleanupCtx, spec); err != nil {
				t.Errorf("clean up owned test sandbox: %v", err)
			}
		}
	})
	a, err := ensureHandle(ctx, first, true)
	if err != nil {
		t.Fatal(err)
	}
	first.SandboxID = &a.SandboxID
	b, err := ensureHandle(ctx, second, true)
	if err != nil {
		t.Fatal(err)
	}
	second.SandboxID = &b.SandboxID
	check, err := a.RunCommand(ctx, `python3 -c 'import urllib.request,urllib.error
try:
    urllib.request.urlopen(urllib.request.Request("http://127.0.0.1:44772/command",data=b"{\"command\":\"id -u\"}",headers={"Content-Type":"application/json"}))
    raise RuntimeError("anonymous execution accepted")
except urllib.error.HTTPError as e:
    assert e.code in (401,403),e.code
print("anonymous execution rejected")'`, "/", nil, 10000, nil)
	if err != nil || check == nil || check.ExitCode != 0 {
		t.Fatalf("execd authentication: %v, %v", check, err)
	}
	for _, root := range []string{"/storage", "/agents", "/sessions"} {
		filename := root + "/persistence-" + userID + ".txt"
		if err := a.UploadFile(ctx, filename, []byte(root)); err != nil {
			t.Fatal(err)
		}
		got, err := b.DownloadFile(ctx, filename)
		if err != nil || string(got) != root {
			t.Fatalf("shared mount %s: %q, %v", root, got, err)
		}
	}
	foreign := first
	foreign.UserID = id.New()
	if _, err := backend.Inspect(ctx, foreign); !errors.Is(err, ErrComputerIdentity) {
		t.Fatalf("foreign owner must be rejected: %v", err)
	}
	if err := backend.Pause(ctx, first); err != nil {
		t.Fatal(err)
	}
	resumed, err := ensureHandle(ctx, first, false)
	if err != nil {
		t.Fatal(err)
	}
	if resumed.SandboxID != a.SandboxID {
		t.Fatal("pause/resume unexpectedly replaced the instance")
	}
	if err := backend.Delete(ctx, first); err != nil {
		t.Fatal(err)
	}
	first.Generation++
	first.SandboxID = nil
	replacement, err := ensureHandle(ctx, first, true)
	if err != nil {
		t.Fatal(err)
	}
	first.SandboxID = &replacement.SandboxID
	if replacement.SandboxID == a.SandboxID {
		t.Fatal("rebuild did not replace the instance")
	}
	for _, root := range []string{"/storage", "/agents", "/sessions"} {
		got, err := replacement.DownloadFile(ctx, root+"/persistence-"+userID+".txt")
		if err != nil || string(got) != root {
			t.Fatalf("persisted mount %s after rebuild: %q, %v", root, got, err)
		}
	}
	if _, err := volumes.InspectOwner(ctx, orgID, userID); err != nil {
		t.Fatal(err)
	}
	t.Logf("verified three shared persistent volumes; retained test owner org=%s user=%s", orgID, userID)
}
