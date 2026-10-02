package sandbox

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSandboxIntegration(t *testing.T) {
	if os.Getenv("WISPJAR_SANDBOX_IT") != "1" {
		t.Skip("set WISPJAR_SANDBOX_IT=1 to run against a real OpenSandbox server")
	}
	url := os.Getenv("WISPJAR_SANDBOX_URL")
	if url == "" {
		url = "http://127.0.0.1:8090"
	}
	key := os.Getenv("WISPJAR_SANDBOX_KEY")
	if key == "" {
		key = "opensandbox-dev-key"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	client := NewClient(url, key)
	svc := NewService(client, 1800)
	var events []Event
	svc.OnEvent(func(e Event) { events = append(events, e) })
	defer svc.Stop()

	opts := Options{
		Image:          "python:3.12-slim",
		TTLSeconds:     600,
		ResourceLimits: map[string]string{"cpu": "1", "memory": "1Gi"},
		NetworkPolicy:  json.RawMessage(`{"defaultAction":"allow","rules":[]}`),
	}

	h, err := svc.Get(ctx, "it-session-1", opts)
	if err != nil {
		t.Fatalf("create sandbox: %v", err)
	}
	t.Logf("created sandbox id=%s execd=%s", h.SandboxID, h.ExecdURL)

	var streamed []string
	res, err := h.RunCommand(ctx, "echo hello", "/workspace", nil, 30000, func(stream, chunk string) {
		streamed = append(streamed, stream+": "+chunk)
	})
	if err != nil {
		t.Fatalf("run echo: %v", err)
	}
	t.Logf("echo: exit=%d stdout=%q stderr=%q streamed=%v", res.ExitCode, res.Stdout, res.Stderr, streamed)
	if res.ExitCode != 0 || !strings.Contains(res.Stdout, "hello") {
		t.Fatalf("unexpected echo result: %+v", res)
	}
	if len(streamed) == 0 {
		t.Fatal("no streaming events received")
	}

	res, err = h.RunCommand(ctx, "ls /", "/", nil, 30000, nil)
	if err != nil {
		t.Fatalf("run ls: %v", err)
	}
	t.Logf("ls /: exit=%d output=%q", res.ExitCode, res.Output)
	if res.ExitCode != 0 {
		t.Fatalf("ls exit=%d", res.ExitCode)
	}

	res, err = h.RunCommand(ctx, "exit 3", "/", nil, 30000, nil)
	if err != nil {
		t.Fatalf("run exit 3: %v", err)
	}
	t.Logf("exit 3: exit=%d", res.ExitCode)
	if res.ExitCode != 3 {
		t.Fatalf("want exit 3, got %d", res.ExitCode)
	}

	content := []byte("hello wispjar\nsecond line\n")
	if err := h.UploadFile(ctx, "/workspace/it/hello.txt", content); err != nil {
		t.Fatalf("upload: %v", err)
	}
	back, err := h.DownloadFile(ctx, "/workspace/it/hello.txt")
	if err != nil {
		t.Fatalf("download: %v", err)
	}
	t.Logf("roundtrip: %q", string(back))
	if string(back) != string(content) {
		t.Fatalf("roundtrip mismatch: %q", string(back))
	}

	localDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(localDir, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(localDir, "SKILL.md"), []byte("# demo skill\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(localDir, "scripts", "run.py"), []byte("print('ok')\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := h.UploadDir(ctx, localDir, "/skills/demo"); err != nil {
		t.Fatalf("upload dir: %v", err)
	}
	if _, err := h.FileInfo(ctx, "/skills/demo/scripts/run.py"); err != nil {
		t.Fatalf("uploaded dir file missing: %v", err)
	}
	t.Log("upload dir ok: /skills/demo/{SKILL.md,scripts/run.py}")

	n, err := h.ReplaceInFile(ctx, "/workspace/it/hello.txt", "hello", "hi", false)
	if err != nil {
		t.Fatalf("replace: %v", err)
	}
	back, err = h.DownloadFile(ctx, "/workspace/it/hello.txt")
	if err != nil {
		t.Fatalf("download after replace: %v", err)
	}
	t.Logf("replace: count=%d content=%q", n, string(back))
	if n != 1 || !strings.Contains(string(back), "hi wispjar") {
		t.Fatalf("replace failed: n=%d content=%q", n, string(back))
	}

	found, err := h.SearchFiles(ctx, "/workspace", "**/*.txt")
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	var paths []string
	for _, fi := range found {
		paths = append(paths, fi.Path)
	}
	t.Logf("search **/*.txt under /workspace: %v", paths)
	if !strings.Contains(strings.Join(paths, "\n"), "hello.txt") {
		t.Fatalf("search did not find hello.txt: %v", paths)
	}

	entries, err := h.ListDir(ctx, "/workspace/it")
	if err != nil {
		t.Fatalf("list dir: %v", err)
	}
	for _, fi := range entries {
		t.Logf("list /workspace/it: %s (%d bytes, mode %o)", fi.Path, fi.Size, fi.Mode)
	}
	if len(entries) == 0 {
		t.Fatal("list dir empty")
	}

	fi, err := h.FileInfo(ctx, "/workspace/it/hello.txt")
	if err != nil {
		t.Fatalf("file info: %v", err)
	}
	t.Logf("file info: path=%s size=%d", fi.Path, fi.Size)
	if fi.Size != int64(len(back)) {
		t.Fatalf("file info size %d != %d", fi.Size, len(back))
	}

	before, err := client.getSandbox(ctx, h.SandboxID)
	if err != nil {
		t.Fatalf("get sandbox: %v", err)
	}
	time.Sleep(2 * time.Second)
	expiresAt, err := h.Renew(ctx)
	if err != nil {
		t.Fatalf("renew: %v", err)
	}
	t.Logf("renew: expiresAt %s -> %s", fmt.Sprint(before.ExpiresAt), expiresAt)
	if before.ExpiresAt != nil && !expiresAt.After(*before.ExpiresAt) {
		t.Fatalf("renew did not extend expiration: %s -> %s", *before.ExpiresAt, expiresAt)
	}

	listed, err := client.listWispJarSandboxes(ctx)
	if err != nil {
		t.Fatalf("list wispjar sandboxes: %v", err)
	}
	seen := false
	for _, info := range listed {
		if info.ID == h.SandboxID {
			seen = true
			t.Logf("listed with metadata: %v", info.Metadata)
		}
	}
	if !seen {
		t.Fatalf("sandbox %s not listed with wispjar metadata filter", h.SandboxID)
	}

	if err := svc.Recycle("it-session-1", "test_done"); err != nil {
		t.Fatalf("recycle: %v", err)
	}
	if _, err := client.getSandbox(ctx, h.SandboxID); err == nil {
		t.Fatalf("sandbox %s still exists after recycle", h.SandboxID)
	} else {
		t.Logf("deleted: %v", err)
	}

	var created, recycled bool
	for _, e := range events {
		if e.Action == EventCreated {
			created = true
		}
		if e.Action == EventRecycled && e.Reason == "test_done" {
			recycled = true
		}
	}
	if !created || !recycled {
		t.Fatalf("missing lifecycle events: %+v", events)
	}
	t.Logf("events: %+v", events)
}
