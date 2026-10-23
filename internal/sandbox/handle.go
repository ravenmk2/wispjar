package sandbox

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"

	opensandbox "github.com/alibaba/OpenSandbox/sdks/sandbox/go"
)

type Options struct {
	Image          string
	TTLSeconds     int
	ResourceLimits map[string]string
	NetworkPolicy  json.RawMessage
	// Env 注入沙箱的环境变量（创建时生效）。
	Env map[string]string
}

type CommandResult struct {
	ExitCode int
	Stdout   string
	Stderr   string
	Output   string
}

type Handle struct {
	SandboxID string
	ExecdURL  string

	sandbox        *opensandbox.Sandbox
	downloadClient *opensandbox.ExecdClient
}

var errCommandComplete = errors.New("sandbox command stream complete")

func (h *Handle) RunCommand(ctx context.Context, cmd, cwd string, env map[string]string, timeoutMs int64, onEvent func(stream, chunk string)) (*CommandResult, error) {
	var stdout, stderr, merged strings.Builder
	// SDK 的 OutputMessage 是按行切分且不含换行符（见 Execution.Text 的 join 语义），重组时需补回。
	var outLines, errLines, mergedLines int
	writeLine := func(b *strings.Builder, n *int, text string) {
		if *n > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(text)
		*n++
	}
	handlers := &opensandbox.ExecutionHandlers{
		// Execd can keep the HTTP stream open for a heartbeat interval after
		// execution_complete. The SDK has already recorded output and exit
		// status before this callback; stop reading at that terminal event.
		OnComplete: func(opensandbox.ExecutionComplete) error { return errCommandComplete },
		OnStdout: func(m opensandbox.OutputMessage) error {
			writeLine(&stdout, &outLines, m.Text)
			writeLine(&merged, &mergedLines, m.Text)
			if onEvent != nil {
				onEvent("stdout", m.Text+"\n")
			}
			return nil
		},
		OnStderr: func(m opensandbox.OutputMessage) error {
			writeLine(&stderr, &errLines, m.Text)
			writeLine(&merged, &mergedLines, m.Text)
			if onEvent != nil {
				onEvent("stderr", m.Text+"\n")
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

type FileInfo = opensandbox.FileInfo

func (h *Handle) SearchFiles(ctx context.Context, dir, pattern string) ([]FileInfo, error) {
	return h.sandbox.SearchFiles(ctx, dir, pattern)
}

func (h *Handle) ListDir(ctx context.Context, remotePath string) ([]FileInfo, error) {
	return h.sandbox.ListDirectory(ctx, remotePath)
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
		return fi, nil
	}
	for _, fi := range m {
		return fi, nil
	}
	return FileInfo{}, fmt.Errorf("%w: %s", os.ErrNotExist, remotePath)
}
