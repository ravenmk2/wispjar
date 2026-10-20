//go:build linux

package tools

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/ravenmk2/wispjar/internal/sandbox"
)

type processHandle struct{ Handle }
type captureWriter struct {
	buffer bytes.Buffer
	event  func(string)
}

func (w *captureWriter) Write(data []byte) (int, error) {
	n, e := w.buffer.Write(data)
	if w.event != nil {
		w.event(string(data))
	}
	return n, e
}
func (w *captureWriter) String() string { return w.buffer.String() }
func (processHandle) RunCommand(ctx context.Context, cmd, cwd string, env map[string]string, _ int64, event func(string, string)) (*sandbox.CommandResult, error) {
	command := exec.CommandContext(ctx, "/bin/sh", "-c", cmd)
	command.Dir = cwd
	command.Env = os.Environ()
	for k, v := range env {
		command.Env = append(command.Env, k+"="+v)
	}
	out, errOut := &captureWriter{}, &captureWriter{}
	if event != nil {
		out.event = func(v string) { event("stdout", v) }
		errOut.event = func(v string) { event("stderr", v) }
	}
	command.Stdout, command.Stderr = out, errOut
	err := command.Run()
	code := 0
	if err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			return nil, err
		}
		code = exit.ExitCode()
	}
	return &sandbox.CommandResult{ExitCode: code, Stdout: out.String(), Stderr: errOut.String()}, nil
}
func (processHandle) UploadFile(_ context.Context, p string, data []byte) error {
	return os.WriteFile(p, data, 0600)
}

// Explicit opt-in: run only in a disposable Linux container with Python and root
// (the production supervisor drops to UID 65532; normal go test skips it).
func TestExecutionSupervisorIntegration(t *testing.T) {
	if os.Getenv("WISPJAR_EXECUTION_TEST") != "1" {
		t.Skip("requires disposable Linux container")
	}
	if os.Geteuid() != 0 {
		t.Fatal("requires root supervisor")
	}
	directory, err := os.MkdirTemp("/tmp", "wispjar-execution-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	if err = os.Chown(directory, 65532, 65532); err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(directory, 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("EXECD_ACCESS_TOKEN", "must-not-reach-user")
	raw := processHandle{}
	h := NewExecutionHandle(raw)
	variables, err := h.RunCommand(context.Background(), `test "$(id -u)" = 65532; test -z "$EXECD_ACCESS_TOKEN"; test "$WISPJAR" = 1; printf '%s' "$TASK_ENV"`, directory, map[string]string{"TASK_ENV": "task-value", "WISPJAR": "1"}, 1000, nil)
	if err != nil || variables.ExitCode != 0 || variables.Stdout != "task-value" {
		t.Fatalf("task environment: %+v %v", variables, err)
	}
	result, err := h.RunCommand(context.Background(), "id -u; test -z \"$EXECD_ACCESS_TOKEN\"; grep NoNewPrivs /proc/self/status", directory, nil, 1000, nil)
	if err != nil || result.ExitCode != 0 || !strings.Contains(result.Stdout, "65532") || !strings.Contains(result.Stdout, "NoNewPrivs:\t1") {
		t.Fatalf("identity: %+v %v", result, err)
	}
	readonly := filepath.Join(directory, "readonly")
	if err = os.WriteFile(readonly, []byte("root-owned"), 0444); err != nil {
		t.Fatal(err)
	}
	if err = h.UploadFile(context.Background(), readonly, []byte("overwrite")); err == nil {
		t.Fatal("unprivileged upload overwrote platform file")
	}
	large := bytes.Repeat([]byte("large payload\n"), 100000)
	target := filepath.Join(directory, "large")
	if err = h.UploadFile(context.Background(), target, large); err != nil {
		t.Fatal(err)
	}
	actual, err := h.DownloadFile(context.Background(), target)
	if err != nil || !bytes.Equal(actual, large) {
		t.Fatalf("large file transfer: %v, %d bytes", err, len(actual))
	}

	detached, err := h.RunCommand(context.Background(), `python3 -c 'import subprocess; p=subprocess.Popen(["sleep","30"],start_new_session=True); print(p.pid,flush=True)'`, directory, nil, 1000, nil)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(detached.Stdout))
	if err != nil {
		t.Fatal(err)
	}
	if err = syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("detached child %d still exists: %v", pid, err)
	}
	if !h.CommandsStopped() {
		t.Fatal("successful operations were not certified stopped")
	}

	a, b := NewExecutionHandle(raw), NewExecutionHandle(raw)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{})
	var once sync.Once
	aDone := make(chan error, 1)
	go func() {
		_, err := a.RunCommand(ctx, "echo started; sleep 30 & wait", directory, nil, 60000, func(stream, chunk string) {
			if stream == "stdout" && strings.Contains(chunk, "started") {
				once.Do(func() { close(started) })
			}
		})
		aDone <- err
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("command did not stream startup")
	}
	bDone := make(chan error, 1)
	go func() {
		r, e := b.RunCommand(context.Background(), "sleep 0.3; echo unrelated-survived", directory, nil, 2000, nil)
		if e == nil && (!strings.Contains(r.Stdout, "unrelated-survived") || r.ExitCode != 0) {
			e = errors.New("unrelated command interrupted")
		}
		bDone <- e
	}()
	cancel()
	select {
	case err := <-aDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancel was not confirmed promptly")
	}
	if err := <-bDone; err != nil {
		t.Fatal(err)
	}
	if !a.CommandsStopped() || !b.CommandsStopped() {
		t.Fatal("normal cancel leaked an uncertain occupancy")
	}
}
