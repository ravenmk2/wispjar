package skill

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/ravenmk2/wispjar/internal/id"
)

func TestContentHashIncludesPathsAndContent(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a"), []byte("one"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "b"), []byte("two"), 0600); err != nil {
		t.Fatal(err)
	}
	_, first, err := contentFiles(root, []string{"b", "a"})
	if err != nil {
		t.Fatal(err)
	}
	_, second, err := contentFiles(root, []string{"a", "b"})
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatal("hash depends on directory enumeration")
	}
	if err := os.Rename(filepath.Join(root, "a"), filepath.Join(root, "c")); err != nil {
		t.Fatal(err)
	}
	_, third, err := contentFiles(root, []string{"b", "c"})
	if err != nil {
		t.Fatal(err)
	}
	if first == third {
		t.Fatal("renaming a file did not change hash")
	}
}

func TestSandboxInstallerUnknownExecutionIsNotSafeRetry(t *testing.T) {
	count := 0
	i := NewSandboxInstaller(func(context.Context, string, []byte) error { return nil }, func(context.Context, string, string) (int, string, error) {
		count++
		if count == 1 {
			return 0, "", nil
		}
		return -1, "", context.DeadlineExceeded
	})
	err := i.Install(context.Background(), InstallPlan{Owner: id.New(), SkillID: id.New()})
	var stopped *StoppedInstallError
	if !errors.Is(err, context.DeadlineExceeded) || errors.As(err, &stopped) {
		t.Fatalf("unknown execution incorrectly marked stopped: %v", err)
	}
}

// Run only inside an isolated Linux root test container that provides python3.
// The test creates randomly named /agents directories and never uses config.
func TestSandboxInstallerPythonIntegration(t *testing.T) {
	if os.Getenv("WISPJAR_SKILL_INSTALLER_TEST") != "1" {
		t.Skip("set WISPJAR_SKILL_INSTALLER_TEST=1 in an isolated Linux root container")
	}
	if runtime.GOOS != "linux" || os.Geteuid() != 0 {
		t.Fatal("requires isolated Linux root test container")
	}
	if _, err := exec.LookPath("python3"); err != nil {
		t.Fatal(err)
	}
	aid, bid, skillID := id.New(), id.New(), id.New()
	targetA, targetB := "/agents/"+aid+"/skills/test-skill", "/agents/"+bid+"/skills/test-skill"
	t.Cleanup(func() {
		for _, path := range []string{"/agents/" + aid, "/agents/" + bid, "/agents/.wispjar/skills/" + skillID} {
			if err := os.RemoveAll(path); err != nil {
				t.Error(err)
			}
		}
	})
	upload := func(_ context.Context, path string, data []byte) error {
		t.Cleanup(func() { _ = os.Remove(path) })
		return os.WriteFile(path, data, 0600)
	}
	run := func(ctx context.Context, command, cwd string) (int, string, error) {
		cmd := exec.CommandContext(ctx, "sh", "-c", command)
		cmd.Dir = cwd
		output, err := cmd.CombinedOutput()
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return exit.ExitCode(), string(output), nil
		}
		return 0, string(output), err
	}
	i := NewSandboxInstaller(upload, run)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "SKILL.md"), skillMD("name: test-skill\ndescription: test"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "old.txt"), []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	files, hash, err := contentFiles(root, []string{"SKILL.md", "old.txt"})
	if err != nil {
		t.Fatal(err)
	}
	p := InstallPlan{OrgID: id.New(), UserID: id.New(), SkillID: skillID, Owner: id.New(), Name: "test-skill", Revision: 1, Fence: 1, Targets: []string{targetA, targetB}, PreviousTargets: []string{}, Files: files, ContentHash: hash}
	if err = i.Install(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(targetA + "/SKILL.md")
	if err != nil || info.Mode().Perm() != 0444 {
		t.Fatalf("skill file not readonly: %v %v", info, err)
	}
	if err = os.Remove(filepath.Join(root, "old.txt")); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, "new.txt"), []byte("new"), 0600); err != nil {
		t.Fatal(err)
	}
	files, hash, err = contentFiles(root, []string{"SKILL.md", "new.txt"})
	if err != nil {
		t.Fatal(err)
	}
	p.Fence = 2
	p.Revision = 2
	p.Owner = id.New()
	p.PreviousTargets = p.Targets
	p.Targets = []string{targetA}
	p.Files = files
	p.ContentHash = hash
	if err = i.Install(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(targetA + "/old.txt"); !os.IsNotExist(err) {
		t.Fatalf("old file survived: %v", err)
	}
	if _, err = os.Stat(targetB); !os.IsNotExist(err) {
		t.Fatalf("retired copy survived: %v", err)
	}
	data, err := os.ReadFile(targetA + "/new.txt")
	if err != nil || string(data) != "new" {
		t.Fatal("new content missing")
	}
	p.Fence = 1
	p.Owner = id.New()
	if err = i.Install(context.Background(), p); err == nil {
		t.Fatal("stale fence accepted")
	}
	// A tampered target must not write through a link or delete its referent.
	outside := t.TempDir()
	marker := filepath.Join(outside, "marker")
	if err = os.WriteFile(marker, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.RemoveAll(targetA); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(outside, targetA); err != nil {
		t.Fatal(err)
	}
	p.Fence = 3
	p.Owner = id.New()
	if err = i.Install(context.Background(), p); err == nil {
		t.Fatal("symlink target accepted")
	}
	data, err = os.ReadFile(marker)
	if err != nil || strings.TrimSpace(string(data)) != "keep" {
		t.Fatal("symlink referent was modified")
	}
}
