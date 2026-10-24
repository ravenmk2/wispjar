package opensandbox

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"

	opensandbox "github.com/alibaba/OpenSandbox/sdks/sandbox/go"
	"github.com/ravenmk2/wispjar/internal/sandbox"
)

type Handle struct {
	ref       sandbox.InstanceRef
	owner     *ComputerBackend
	SandboxID string
	ExecdURL  string

	sandbox        *opensandbox.Sandbox
	downloadClient *opensandbox.ExecdClient
}

var errCommandComplete = errors.New("sandbox command stream complete")

func (h *Handle) RunCommand(ctx context.Context, cmd, cwd string, env map[string]string, timeoutMs int64, onEvent func(stream, chunk string)) (*CommandResult, error) {
	var stdout, stderr, merged strings.Builder
	// Execd release-1.1.1 preserves output bytes, including line endings.
	handlers := &opensandbox.ExecutionHandlers{
		// Execd can keep the HTTP stream open for a heartbeat interval after
		// execution_complete. The SDK has already recorded output and exit
		// status before this callback; stop reading at that terminal event.
		OnComplete: func(opensandbox.ExecutionComplete) error { return errCommandComplete },
		OnStdout: func(m opensandbox.OutputMessage) error {
			stdout.WriteString(m.Text)
			merged.WriteString(m.Text)
			if onEvent != nil {
				onEvent("stdout", m.Text)
			}
			return nil
		},
		OnStderr: func(m opensandbox.OutputMessage) error {
			stderr.WriteString(m.Text)
			merged.WriteString(m.Text)
			if onEvent != nil {
				onEvent("stderr", m.Text)
			}
			return nil
		},
	}
	exec, err := h.sandbox.RunCommandWithOpts(ctx, opensandbox.RunCommandRequest{
		Command: cmd,
		Cwd:     cwd,
		Envs:    env,
		Timeout: timeoutMs,
	}, handlers)
	if errors.Is(err, errCommandComplete) {
		err = nil
	}
	res := &CommandResult{
		ExitCode: -1,
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		Output:   merged.String(),
	}
	if exec != nil && exec.ExitCode != nil {
		res.ExitCode = *exec.ExitCode
	}
	if err != nil {
		return res, fmt.Errorf("sandbox: run command: %w", err)
	}
	if exec != nil && exec.Error != nil && exec.ExitCode == nil {
		return res, fmt.Errorf("sandbox: command error: %s: %s", exec.Error.Name, exec.Error.Value)
	}
	return res, nil
}

func (h *Handle) UploadFile(ctx context.Context, remotePath string, content []byte) error {
	return h.sandbox.UploadFile(ctx, bytes.NewReader(content), opensandbox.UploadFileOptions{
		FileName: path.Base(remotePath),
		Metadata: opensandbox.FileMetadata{Path: remotePath},
	})
}

func (h *Handle) DownloadFile(ctx context.Context, remotePath string) ([]byte, error) {
	rc, err := h.sandbox.DownloadFile(ctx, remotePath, "")
	if err != nil {
		return nil, err
	}
	defer func() { _ = rc.Close() }()
	return io.ReadAll(rc)
}

// DownloadFileStream is reserved for platform-owned immutable export artifacts.
// Callers must not pass an agent-controlled source path here.
func (h *Handle) DownloadFileStream(ctx context.Context, remotePath string) (io.ReadCloser, error) {
	if h.downloadClient != nil {
		return h.downloadClient.DownloadFile(ctx, remotePath, "")
	}
	return h.sandbox.DownloadFile(ctx, remotePath, "")
}

func (h *Handle) ReplaceInFile(ctx context.Context, remotePath, old, new string, replaceAll bool) (int, error) {
	resp, err := h.sandbox.ReplaceInFilesDetailed(ctx, opensandbox.ReplaceRequest{
		remotePath: opensandbox.ReplaceItem{Old: old, New: new},
	})
	if err != nil {
		return 0, err
	}
	r, ok := resp[remotePath]
	if !ok {
		return 0, fmt.Errorf("sandbox: replace response missing entry for %s", remotePath)
	}
	if !replaceAll && r.ReplacedCount > 1 {
		return r.ReplacedCount, fmt.Errorf("sandbox: %d occurrences of old_string in %s; expected exactly 1 (set replace_all to override)", r.ReplacedCount, remotePath)
	}
	return r.ReplacedCount, nil
}

func fileInfo(info opensandbox.FileInfo) FileInfo { return FileInfo(info) }
func fileInfos(items []opensandbox.FileInfo) []FileInfo {
	result := make([]FileInfo, len(items))
	for i, item := range items {
		result[i] = fileInfo(item)
	}
	return result
}

func (h *Handle) SearchFiles(ctx context.Context, dir, pattern string) ([]FileInfo, error) {
	items, err := h.sandbox.SearchFiles(ctx, dir, pattern)
	return fileInfos(items), backendError(err)
}

func (h *Handle) ListDir(ctx context.Context, remotePath string) ([]FileInfo, error) {
	items, err := h.sandbox.ListDirectory(ctx, remotePath)
	return fileInfos(items), backendError(err)
}

func (h *Handle) FileInfo(ctx context.Context, remotePath string) (FileInfo, error) {
	m, err := h.sandbox.GetFileInfo(ctx, remotePath)
	if err != nil {
		// /files/info 的缺失文件响应，供调用方区分不存在与探测失败。
		if isNotFound(err) {
			return FileInfo{}, fmt.Errorf("%w: %s", os.ErrNotExist, remotePath)
		}
		return FileInfo{}, err
	}
	if fi, ok := m[remotePath]; ok {
		return fileInfo(fi), nil
	}
	for _, fi := range m {
		return fileInfo(fi), nil
	}
	return FileInfo{}, fmt.Errorf("%w: %s", os.ErrNotExist, remotePath)
}
