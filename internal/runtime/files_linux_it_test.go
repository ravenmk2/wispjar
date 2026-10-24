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

type localFileHandle struct {
	tools.Handle
	commands, uploads *int
}

func (h localFileHandle) RunCommand(ctx context.Context, command, cwd string, env map[string]string, _ int64, _ func(string, string)) (*sandbox.CommandResult, error) {
	if h.commands != nil {
		*h.commands++
	}
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
func (h localFileHandle) UploadFile(_ context.Context, name string, data []byte) error {
	if h.uploads != nil {
		*h.uploads++
	}
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
	commands, uploads := 0, 0
	handle := tools.NewExecutionHandle(localFileHandle{commands: &commands, uploads: &uploads})
	listing, err := remoteFileOperation(ctx, handle, root, ".", "list", nil)
	if err != nil || len(listing.Items) != 2 || commands != 1 || uploads != 0 || !handle.CommandsStopped() {
		t.Fatalf("listing must need one supervised command without uploads: %+v commands=%d uploads=%d err=%v", listing, commands, uploads, err)
	}
	// Personal storage initialization must not create a placeholder session.
	beforeSessions, _ := os.ReadDir("/sessions")
	if err := initializeWorkspace(ctx, raw, "", []string{}); err != nil {
		t.Fatal(err)
	}
	afterSessions, _ := os.ReadDir("/sessions")
	if len(beforeSessions) != len(afterSessions) {
		t.Fatal("storage initialization created a session directory")
	}
	if _, err := os.Stat("/sessions/inputs"); !os.IsNotExist(err) {
		t.Fatal("storage initialization created session inputs at volume root")
	}
	if _, err := remoteFileOperation(ctx, handle, root, "outputs/资料", "mkdir", nil); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(path.Join(root, "outputs/资料")); err != nil || !info.IsDir() {
		t.Fatal("new directory missing", err)
	}
	for _, relative := range []string{"outputs/资料", "missing-parent/new"} {
		if _, err := remoteFileOperation(ctx, handle, root, relative, "mkdir", nil); err == nil {
			t.Fatalf("mkdir accepted conflict or missing parent: %s", relative)
		}
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, path.Join(root, "outputs/linked")); err != nil {
		t.Fatal(err)
	}
	if _, err := remoteFileOperation(ctx, handle, root, "outputs/linked/escape", "mkdir", nil); err == nil {
		t.Fatal("mkdir followed a symlink parent")
	}
	if _, err := os.Stat(path.Join(outside, "escape")); !os.IsNotExist(err) {
		t.Fatal("mkdir wrote outside the requested directory")
	}
	for _, relative := range []string{"outputs/资料", "outputs/linked"} {
		if err := os.Remove(path.Join(root, relative)); err != nil {
			t.Fatal(err)
		}
	}
	// Shell metacharacters and UTF-8 must survive the inline stdin path literally.
	name := "outputs/quote'\"$().txt"
	small := []byte("literal ' \" $(id) `id` \\ 中文\n")
	_, err = remoteFileOperation(ctx, handle, root, name, "write", map[string]any{"data": base64.StdEncoding.EncodeToString(small)})
	if err != nil {
		t.Fatal(err)
	}
	commands, uploads = 0, 0
	smallRead, err := remoteFileOperation(ctx, handle, root, name, "read", nil)
	decoded, decodeErr := base64.StdEncoding.DecodeString(smallRead.Data)
	if err != nil || decodeErr != nil || !bytes.Equal(decoded, small) || commands != 1 || uploads != 0 {
		t.Fatalf("inline read: %v %v commands=%d uploads=%d", err, decodeErr, commands, uploads)
	}
	if err = os.Remove(path.Join(root, name)); err != nil {
		t.Fatal(err)
	}
	_, err = remoteFileOperation(ctx, handle, root, "missing", "read", nil)
	var missing *session.Error
	if !errors.As(err, &missing) || missing.Code != "file/not_found" {
		t.Fatalf("missing child must not request workspace initialization: %v", err)
	}
	_, err = remoteFileOperation(ctx, handle, root+"/missing-root", ".", "list", nil)
	if !errors.As(err, &missing) || missing.Code != "file/root_not_found" {
		t.Fatalf("missing workspace root: %v", err)
	}
	payload := bytes.Repeat([]byte("transfer-content\n"), 100000)
	encoded := base64.StdEncoding.EncodeToString(payload)
	result, err := remoteFileOperation(ctx, handle, root, "inputs/a.txt", "write", map[string]any{"data": encoded})
	if err != nil || result.Bytes != int64(len(payload)) {
		t.Fatalf("upload: %+v %v", result, err)
	}
	if uploads != 1 {
		t.Fatalf("large upload must retain staged transfer: uploads=%d", uploads)
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
	listing, err = remoteFileOperation(ctx, handle, root, "inputs", "list", nil)
	if err != nil || len(listing.Items) != 1 || listing.Items[0]["name"] != "a.txt" {
		t.Fatalf("listing advertised a symlink: %+v %v", listing, err)
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

func TestPreviewResourcesReadFullFilesAndRejectLinks(t *testing.T) {
	if os.Getenv("WISPJAR_EXECUTION_TEST") != "1" {
		t.Skip("requires disposable root Linux container with Python")
	}
	root, err := os.MkdirTemp("", "wispjar-preview-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	if err := os.Chmod(root, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(path.Join(root, "site/assets"), 0755); err != nil {
		t.Fatal(err)
	}
	full := bytes.Repeat([]byte("/* complete stylesheet */\n"), 14000)
	if err := os.WriteFile(path.Join(root, "site/assets/style.css"), full, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/etc/passwd", path.Join(root, "site/assets/leak.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/etc", path.Join(root, "site/escape")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path.Join(root, "site/huge.bin"), make([]byte, previewFileLimit+1), 0644); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		file              string
		reject, truncated bool
	}{
		{"site/assets/style.css", false, false}, {"site/assets/leak.txt", true, false}, {"site/escape/passwd", true, false}, {"site/huge.bin", false, true},
	} {
		handle := tools.NewExecutionHandle(localFileHandle{})
		result, err := remoteFileOperation(context.Background(), handle, root, test.file, "read", map[string]any{"max_bytes": previewFileLimit})
		if test.reject {
			if err == nil {
				t.Fatalf("followed %s", test.file)
			}
			continue
		}
		if err != nil || result.Truncated != test.truncated {
			t.Fatalf("%s: %+v %v", test.file, result, err)
		}
		if !test.truncated {
			data, err := base64.StdEncoding.DecodeString(result.Data)
			if err != nil || !bytes.Equal(data, full) {
				t.Fatal("resource was truncated or wrapped")
			}
		}
		if !handle.CommandsStopped() {
			t.Fatal("reader did not stop")
		}
	}
}
