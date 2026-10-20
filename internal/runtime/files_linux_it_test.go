//go:build linux

package runtime

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"os"
	"os/exec"
	"path"
	"reflect"
	"testing"

	"github.com/ravenmk2/wispjar/internal/id"
	"github.com/ravenmk2/wispjar/internal/runtime/tools"
	"github.com/ravenmk2/wispjar/internal/sandbox"
	"github.com/ravenmk2/wispjar/internal/session"
	"github.com/ravenmk2/wispjar/internal/store"
)

type localFileHandle struct{ tools.Handle }

func (localFileHandle) RunCommand(ctx context.Context, command, cwd string, env map[string]string, _ int64, _ func(string, string)) (*sandbox.CommandResult, error) {
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", command)
	cmd.Dir = cwd
	cmd.Env = os.Environ()
	for key, value := range env {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	code := 0
	if err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			return nil, err
		}
		code = exit.ExitCode()
	}
	return &sandbox.CommandResult{ExitCode: code, Stdout: stdout.String(), Stderr: stderr.String()}, nil
}
func (localFileHandle) UploadFile(_ context.Context, name string, data []byte) error {
	return os.WriteFile(name, data, 0600)
}

func TestScopedFilesIntegration(t *testing.T) {
	if os.Getenv("WISPJAR_EXECUTION_TEST") != "1" {
		t.Skip("requires disposable root Linux container with Python")
	}
	if os.Geteuid() != 0 {
		t.Fatal("root required")
	}
	raw := localFileHandle{}
	sid, aid := id.New(), id.New()
	ctx := context.Background()
	if err := initializeWorkspace(ctx, raw, sid, []string{aid}); err != nil {
		t.Fatal(err)
	}
	root := session.WorkspaceDir(sid)
	handle := tools.NewExecutionHandle(raw)
	payload := bytes.Repeat([]byte("transfer-content\n"), 100000)
	encoded := base64.StdEncoding.EncodeToString(payload)
	result, err := remoteFileOperation(ctx, handle, root, "inputs/a.txt", "write", map[string]any{"data": encoded})
	if err != nil || result.Bytes != int64(len(payload)) {
		t.Fatalf("upload: %+v %v", result, err)
	}
	_, err = remoteFileOperation(ctx, handle, root, "inputs/a.txt", "write", map[string]any{"data": "eA=="})
	var conflict *session.Error
	if !errors.As(err, &conflict) || conflict.Code != "file/conflict" {
		t.Fatalf("no-clobber: %v", err)
	}
	read, err := remoteFileOperation(ctx, handle, root, "inputs/a.txt", "read", nil)
	if err != nil {
		t.Fatal(err)
	}
	data, err := base64.StdEncoding.DecodeString(read.Data)
	if err != nil || !bytes.Equal(data, payload) {
		t.Fatal("readback differs")
	}
	destination := sid + "/saved.txt"
	saved, err := remoteFileOperation(ctx, handle, root, "inputs/a.txt", "copy", map[string]any{"destination": destination})
	if err != nil || saved.SHA256 == "" || saved.Bytes != int64(len(payload)) {
		t.Fatalf("copy: %+v %v", saved, err)
	}
	stored, err := os.ReadFile(path.Join("/storage", destination))
	if err != nil || !bytes.Equal(stored, payload) {
		t.Fatal("cross-volume publication differs")
	}
	secret := path.Join(root, "private.txt")
	if err := os.WriteFile(secret, []byte("outside-scope"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../private.txt", path.Join(root, "inputs", "link")); err != nil {
		t.Fatal(err)
	}
	_, err = remoteFileOperation(ctx, handle, path.Join(root, "inputs"), "link", "read", nil)
	if err == nil {
		t.Fatal("final symlink escaped file root")
	}
	if err := os.Symlink("/storage", path.Join(root, "inputs", "linked-dir")); err != nil {
		t.Fatal(err)
	}
	_, err = remoteFileOperation(ctx, handle, root, "inputs/linked-dir/injected", "write", map[string]any{"data": "eA=="})
	if err == nil {
		t.Fatal("intermediate symlink escaped upload root")
	}
	if _, err = os.Stat("/storage/injected"); !os.IsNotExist(err) {
		t.Fatalf("unexpected outside write: %v", err)
	}
	if err := os.Remove(path.Join(root, "outputs")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/agents/"+aid+"/skills", path.Join(root, "outputs")); err != nil {
		t.Fatal(err)
	}
	if err := initializeWorkspace(ctx, raw, sid, []string{aid}); err == nil {
		t.Fatal("workspace initialization followed user symlink")
	}
	// The refusal has an explicit exit status: neither root initialization nor
	// file maintenance may turn this ordinary failure into a permanent occupant.
	o, rs, rootCalls := resourceRuntime()
	rs.skillAcquired = false
	err = initializeRunWorkspace(ctx, raw, sid, []string{aid}, rs)
	if err == nil || !controlCommandStopped(err) {
		t.Fatalf("symlink refusal was not confirmed stopped: %v", err)
	}
	o.finishResources(rs, "failed")
	if got := rootCalls.snapshot(); !reflect.DeepEqual(got, []string{"finish:root:failed"}) {
		t.Fatalf("root occupancy leaked: %v", got)
	}
	fileCalls := &resourceCalls{}
	handler := &Handler{computers: resourceComputer{calls: fileCalls}}
	detail := &session.Detail{Session: store.Session{ID: sid, AgentID: aid, UserID: "user"}}
	if err := handler.initializeFileWorkspace(ctx, raw, detail, "file-use"); err == nil {
		t.Fatal("file initialization accepted symlink")
	}
	if got := fileCalls.snapshot(); !reflect.DeepEqual(got, []string{"finish:file-use:failed"}) {
		t.Fatalf("file occupancy leaked: %v", got)
	}
	info, err := os.Stat("/agents/" + aid + "/skills")
	if err != nil || info.Mode().Perm() != 0755 {
		t.Fatal("platform skills permissions changed")
	}
}
