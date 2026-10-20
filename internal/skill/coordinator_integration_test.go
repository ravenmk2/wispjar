package skill

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"

	"github.com/ravenmk2/wispjar/internal/id"
	"github.com/ravenmk2/wispjar/internal/store"
	"gorm.io/gorm"
)

type memoryInstaller struct {
	mu        sync.Mutex
	plans     []InstallPlan
	files     map[string]map[string]string
	nextError error
}

func (i *memoryInstaller) Install(_ context.Context, p InstallPlan) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.plans = append(i.plans, p)
	for _, target := range p.PreviousTargets {
		delete(i.files, target)
	}
	for _, target := range p.Targets {
		i.files[target] = map[string]string{}
		for _, file := range p.Files {
			i.files[target][file.Path] = string(file.Data)
		}
	}
	err := i.nextError
	i.nextError = nil
	return err
}
func uploadHeader(t *testing.T, name string, extras ...zipEntry) *multipart.FileHeader {
	t.Helper()
	entries := append([]zipEntry{{name: "SKILL.md", data: skillMD("name: " + name + "\ndescription: Test skill")}}, extras...)
	data := buildZip(t, entries)
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "skill.zip")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = part.Write(data); err != nil {
		t.Fatal(err)
	}
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	if err = req.ParseMultipartForm(MaxZipBytes); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if req.MultipartForm != nil {
			_ = req.MultipartForm.RemoveAll()
		}
	})
	return req.MultipartForm.File["file"][0]
}

type skillFixture struct {
	db        *gorm.DB
	svc       *Service
	c         *Coordinator
	org, user string
	agents    []string
}

func integrationFixture(t *testing.T) *skillFixture {
	t.Helper()
	dsn := os.Getenv("WISPJAR_SKILL_TEST_DSN")
	if dsn == "" {
		t.Skip("set WISPJAR_SKILL_TEST_DSN to an isolated migrated database")
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
	f := &skillFixture{db: db, org: id.New(), user: id.New(), agents: []string{id.New(), id.New()}}
	provider, model := id.New(), id.New()
	records := []any{
		&store.Organization{ID: f.org, Name: "skill test", Status: "active"},
		&store.ModelProvider{ID: provider, Name: "skill test", Type: "openai", BaseURL: "http://localhost", APIKeyEnc: []byte{}, Extra: json.RawMessage(`{}`), Status: "active", CreatedBy: f.user},
		&store.Model{ID: model, ProviderID: provider, ModelCode: "test", DisplayName: "test", Capabilities: json.RawMessage(`{}`), DefaultParams: json.RawMessage(`{}`), Status: "active"},
	}
	for _, aid := range f.agents {
		records = append(records, &store.Agent{ID: aid, OrgID: f.org, Name: "skill agent", ModelID: model, ToolsEnabled: json.RawMessage(`[]`), ModelParams: json.RawMessage(`{}`), SandboxConfig: json.RawMessage(`{}`), Status: "active", CreatedBy: f.user})
	}
	for _, row := range records {
		if err = db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		for _, table := range []string{"run_skill_lease", "computer_run", "user_skill_state", "resource_operation", "audit_log"} {
			if err := db.Table(table).Where("org_id = ?", f.org).Delete(map[string]any{}).Error; err != nil {
				t.Error(err)
			}
		}
		if err := db.Where("agent_id IN ?", f.agents).Delete(&store.AgentSkill{}).Error; err != nil {
			t.Error(err)
		}
		for _, row := range []any{&store.Agent{}, &store.Skill{}} {
			if err := db.Where("org_id = ?", f.org).Delete(row).Error; err != nil {
				t.Error(err)
			}
		}
		for _, item := range []struct {
			row any
			id  string
		}{{&store.Model{}, model}, {&store.ModelProvider{}, provider}, {&store.Organization{}, f.org}} {
			if err := db.Where("id = ?", item.id).Delete(item.row).Error; err != nil {
				t.Error(err)
			}
		}
	})
	f.svc = NewService(db, t.TempDir())
	f.c = NewCoordinator(db, f.svc)
	return f
}
func (f *skillFixture) run(t *testing.T, agents ...string) AcquireRequest {
	t.Helper()
	runID := id.New()
	row := store.ComputerRun{ID: runID, OrgID: f.org, UserID: f.user, ComputerID: id.New(), SessionID: id.New(), AgentID: agents[0], Status: "running", Manifest: json.RawMessage(`{"generation":7}`)}
	if err := f.db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	return AcquireRequest{OrgID: f.org, UserID: f.user, RootRunID: runID, AgentIDs: agents}
}
func requireCode(t *testing.T, err error, code string) {
	t.Helper()
	var e *Error
	if !errors.As(err, &e) || e.Code != code {
		t.Fatalf("want %s, got %v", code, err)
	}
}

func TestCoordinatorMySQLIntegration(t *testing.T) {
	f := integrationFixture(t)
	ctx := context.Background()
	sk, err := f.svc.Upload(f.user, f.org, uploadHeader(t, "test-skill", zipEntry{name: "old.txt", data: []byte("old")}))
	if err != nil {
		t.Fatal(err)
	}
	if sk.Revision != 1 || len(sk.ContentHash) != 64 {
		t.Fatalf("missing initial revision/hash: %+v", sk.Skill)
	}
	for _, aid := range f.agents {
		if err = f.db.Create(&store.AgentSkill{AgentID: aid, SkillID: sk.ID}).Error; err != nil {
			t.Fatal(err)
		}
	}
	i := &memoryInstaller{files: map[string]map[string]string{}}
	r1, r2 := f.run(t, f.agents[0]), f.run(t, f.agents[1])
	var wg sync.WaitGroup
	for _, r := range []AcquireRequest{r1, r2} {
		wg.Go(func() {
			if _, err := f.c.Acquire(ctx, r, i); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if len(i.plans) != 1 || len(i.files) != 2 {
		t.Fatalf("expected one shared install, got %d / %d", len(i.plans), len(i.files))
	}
	updated, err := f.svc.ReplacePackage(ctx, f.user, f.org, sk.ID, 1, uploadHeader(t, "test-skill", zipEntry{name: "new.txt", data: []byte("new")}))
	if err != nil {
		t.Fatal(err)
	}
	if updated.ID != sk.ID || updated.Revision != 2 {
		t.Fatal("replacement changed identity")
	}
	r3 := f.run(t, f.agents[0], f.agents[1])
	_, err = f.c.Acquire(ctx, r3, i)
	requireCode(t, err, "skill/update_pending")
	old, err := f.c.Acquire(ctx, r1, i)
	if err != nil || old.Skills[0].Revision != 1 {
		t.Fatalf("approval resume lost original manifest: %+v %v", old, err)
	}
	var count int64
	if err = f.db.Model(&store.RunSkillLease{}).Where("run_id = ?", r3.RootRunID).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("partial leases on waiting run: %d %v", count, err)
	}
	requireCode(t, f.c.Release(ctx, f.org, f.user, r1.RootRunID, false), "skill/sync_blocked")
	if err = f.c.Release(ctx, f.org, f.user, r1.RootRunID, true); err != nil {
		t.Fatal(err)
	}
	_, err = f.c.Acquire(ctx, r3, i)
	requireCode(t, err, "skill/update_pending")
	if err = f.c.Release(ctx, f.org, f.user, r2.RootRunID, true); err != nil {
		t.Fatal(err)
	}
	current, err := f.c.Acquire(ctx, r3, i)
	if err != nil {
		t.Fatal(err)
	}
	if len(current.Skills) != 2 || current.Skills[0].Revision != 2 {
		t.Fatalf("wrong current manifest %+v", current)
	}
	for _, files := range i.files {
		if _, ok := files["old.txt"]; ok {
			t.Fatal("removed source file survived install")
		}
		if files["new.txt"] != "new" {
			t.Fatal("new source missing")
		}
	}
	var stored store.ComputerRun
	if err = f.db.Where("id = ?", r3.RootRunID).Take(&stored).Error; err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	_ = json.Unmarshal(stored.Manifest, &raw)
	if raw["generation"] != float64(7) {
		t.Fatal("coordinator overwrote computer manifest")
	}
	// Concurrent writers share a revision CAS; only one wins.
	headers := []*multipart.FileHeader{uploadHeader(t, "test-skill", zipEntry{name: "v3.txt", data: []byte("three")}), uploadHeader(t, "test-skill", zipEntry{name: "v3.txt", data: []byte("three")})}
	results := make(chan error, 2)
	for _, header := range headers {
		wg.Go(func() { _, err := f.svc.ReplacePackage(ctx, f.user, f.org, sk.ID, 2, header); results <- err })
	}
	wg.Wait()
	close(results)
	success, conflicts := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else {
			var e *Error
			if errors.As(err, &e) && e.Status == 409 {
				conflicts++
			} else {
				t.Error(err)
			}
		}
	}
	if success != 1 || conflicts != 1 {
		t.Fatalf("revision CAS: %d success %d conflicts", success, conflicts)
	}
	if err = f.svc.RecoverSources(ctx, f.org, sk.ID); err != nil {
		t.Fatal(err)
	}
	if err = f.c.Release(ctx, f.org, f.user, r3.RootRunID, true); err != nil {
		t.Fatal(err)
	}
	r4 := f.run(t, f.agents[0])
	i.nextError = fmt.Errorf("transport lost after remote write")
	_, err = f.c.Acquire(ctx, r4, i)
	requireCode(t, err, "skill/sync_failed")
	attempts := len(i.plans)
	_, err = f.c.Acquire(ctx, r4, i)
	requireCode(t, err, "skill/sync_blocked")
	if len(i.plans) != attempts {
		t.Fatal("unknown remote execution was retried")
	}
	recovery := RecoveryRequest{OrgID: f.org, UserID: f.user, SkillID: sk.ID, ActorID: f.user}
	requireCode(t, f.c.Recover(ctx, recovery), "skill/sync_blocked")
	recovery.CommandsStopped = true
	if err = f.c.Recover(ctx, recovery); err != nil {
		t.Fatal(err)
	}
	last, err := f.c.Acquire(ctx, r4, i)
	if err != nil {
		t.Fatal(err)
	}
	if last.Skills[0].Revision != 3 || i.plans[len(i.plans)-1].Fence <= i.plans[attempts-1].Fence {
		t.Fatal("recovery did not advance fence")
	}
	// Retiring bindings cleans every copy only after active users finish.
	if err = f.db.Where("skill_id = ?", sk.ID).Delete(&store.AgentSkill{}).Error; err != nil {
		t.Fatal(err)
	}
	if err = f.svc.Delete(f.user, f.org, sk.ID); err == nil {
		t.Fatal("deleted actively used skill")
	}
	if err = f.c.Release(ctx, f.org, f.user, r4.RootRunID, true); err != nil {
		t.Fatal(err)
	}
	r5 := f.run(t, f.agents[0])
	empty, err := f.c.Acquire(ctx, r5, i)
	if err != nil {
		t.Fatal(err)
	}
	if len(empty.Skills) != 0 || len(i.files) != 0 {
		t.Fatal("removed associations retained installed copies")
	}
	if err = f.svc.Delete(f.user, f.org, sk.ID); err != nil {
		t.Fatal(err)
	}
}

func TestCoordinatorKnownFailureRetryIntegration(t *testing.T) {
	f := integrationFixture(t)
	ctx := context.Background()
	sk, err := f.svc.Upload(f.user, f.org, uploadHeader(t, "retry-skill"))
	if err != nil {
		t.Fatal(err)
	}
	if err = f.db.Create(&store.AgentSkill{AgentID: f.agents[0], SkillID: sk.ID}).Error; err != nil {
		t.Fatal(err)
	}
	i := &memoryInstaller{files: map[string]map[string]string{}, nextError: &StoppedInstallError{Err: fmt.Errorf("confirmed process exit")}}
	r := f.run(t, f.agents[0])
	_, err = f.c.Acquire(ctx, r, i)
	requireCode(t, err, "skill/sync_failed")
	manifest, err := f.c.Acquire(ctx, r, i)
	if err != nil {
		t.Fatal(err)
	}
	if len(i.plans) != 2 {
		t.Fatal("safe failure was not retried")
	}
	resumed, err := f.c.Acquire(ctx, r, i)
	if err != nil || !reflect.DeepEqual(manifest, resumed) || len(i.plans) != 2 {
		t.Fatal("resume was not idempotent")
	}
}

func TestSourceLegacyCleanupIntegration(t *testing.T) {
	f := integrationFixture(t)
	ctx := context.Background()
	sk, err := f.svc.Upload(f.user, f.org, uploadHeader(t, "legacy-skill"))
	if err != nil {
		t.Fatal(err)
	}
	var row store.Skill
	if err = f.db.First(&row, "id = ?", sk.ID).Error; err != nil {
		t.Fatal(err)
	}
	legacy := filepath.Join(f.svc.dataDir, "skills", f.org, sk.ID)
	if err = os.Rename(row.StoragePath, legacy); err != nil {
		t.Fatal(err)
	}
	if err = f.db.Model(&row).Update("storage_path", legacy).Error; err != nil {
		t.Fatal(err)
	}
	if _, err = f.svc.ReplacePackage(ctx, f.user, f.org, sk.ID, 1, uploadHeader(t, "legacy-skill")); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(legacy); !os.IsNotExist(err) {
		t.Fatalf("legacy source retained after committed replacement: %v", err)
	}
	// A database path is never authority to recursively delete arbitrary data.
	unrelated := t.TempDir()
	sentinel := filepath.Join(unrelated, "keep")
	if err = os.WriteFile(sentinel, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = f.db.Model(&row).Update("storage_path", unrelated).Error; err != nil {
		t.Fatal(err)
	}
	if err = f.svc.Delete(f.user, f.org, sk.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(sentinel); err != nil {
		t.Fatalf("unrelated path removed: %v", err)
	}
	// Legacy-only sources have no .sources directory; recovery still collects
	// the known canonical path once the durable row has gone.
	legacyOnlyID := id.New()
	legacyOnly := filepath.Join(f.svc.dataDir, "skills", f.org, legacyOnlyID)
	if err = os.MkdirAll(legacyOnly, 0700); err != nil {
		t.Fatal(err)
	}
	if err = f.svc.RecoverSources(ctx, f.org, legacyOnlyID); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(legacyOnly); !os.IsNotExist(err) {
		t.Fatalf("legacy-only source not collected: %v", err)
	}
}
