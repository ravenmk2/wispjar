package sandbox

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	opensandbox "github.com/alibaba/OpenSandbox/sdks/sandbox/go"
)

type Options struct {
	Image          string
	TTLSeconds     int
	ResourceLimits map[string]string
	NetworkPolicy  json.RawMessage
}

const (
	EventCreated  = "created"
	EventRecycled = "recycled"
	EventFailed   = "failed"
)

type Event struct {
	SessionID string
	SandboxID string
	Action    string
	Reason    string
}

type Manager interface {
	Get(ctx context.Context, sessionID string, opts Options) (*Handle, error)
	Recycle(sessionID string, reason string) error
}

type Service struct {
	client   *Client
	idleTTL  time.Duration
	settings SettingsSource

	mu      sync.Mutex
	handles map[string]*Handle
	hooks   []func(Event)

	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func NewService(client *Client, idleTTLSeconds int, settings SettingsSource) *Service {
	if idleTTLSeconds <= 0 {
		idleTTLSeconds = 1800
	}
	return &Service{
		client:   client,
		idleTTL:  time.Duration(idleTTLSeconds) * time.Second,
		settings: settings,
		handles:  make(map[string]*Handle),
	}
}

func (s *Service) OnEvent(fn func(Event)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hooks = append(s.hooks, fn)
}

func (s *Service) emit(e Event) {
	s.mu.Lock()
	hooks := append([]func(Event){}, s.hooks...)
	s.mu.Unlock()
	for _, fn := range hooks {
		fn(e)
	}
}

func (s *Service) Get(ctx context.Context, sessionID string, opts Options) (*Handle, error) {
	s.mu.Lock()
	if h, ok := s.handles[sessionID]; ok && !h.expired() {
		s.mu.Unlock()
		h.Touch()
		return h, nil
	}
	if h, ok := s.handles[sessionID]; ok {
		delete(s.handles, sessionID)
		s.mu.Unlock()
		s.killBestEffort(h)
	} else {
		s.mu.Unlock()
	}

	h, err := s.createWithRetry(ctx, sessionID, s.applySettings(ctx, opts))
	if err != nil {
		s.emit(Event{SessionID: sessionID, Action: EventFailed, Reason: err.Error()})
		return nil, err
	}

	s.mu.Lock()
	if existing, ok := s.handles[sessionID]; ok {
		s.mu.Unlock()
		s.killBestEffort(h)
		existing.Touch()
		return existing, nil
	}
	s.handles[sessionID] = h
	s.mu.Unlock()
	s.emit(Event{SessionID: sessionID, SandboxID: h.SandboxID, Action: EventCreated})
	return h, nil
}

// applySettings 用平台设置补齐 opts 缺省字段（调用方只传会话/Agent 级覆盖），
// 设置经短 TTL 缓存源读取，PUT /platform/settings 后无需重启即可生效。
func (s *Service) applySettings(ctx context.Context, opts Options) Options {
	if s.settings == nil {
		return opts
	}
	st, err := s.settings.Load(ctx)
	if err != nil {
		log.Printf("sandbox: load settings: %v", err)
		return opts
	}
	if opts.Image == "" {
		opts.Image = st.DefaultImage
	}
	if opts.TTLSeconds <= 0 {
		opts.TTLSeconds = st.TTLSeconds
	}
	if len(opts.ResourceLimits) == 0 {
		opts.ResourceLimits = st.ResourceLimits
	}
	if len(opts.NetworkPolicy) == 0 {
		opts.NetworkPolicy = st.EgressPolicy
	}
	return opts
}

// createWithRetry 创建沙箱失败时退避重试一次（冷启动镜像拉取等瞬时超时；
// 超时前可能已在服务端建出的孤儿沙箱由 reaper 的 reconcileOrphans 兜底回收）。
func (s *Service) createWithRetry(ctx context.Context, sessionID string, opts Options) (*Handle, error) {
	h, err := s.createHandle(ctx, sessionID, opts)
	if err == nil {
		return h, nil
	}
	first := err
	select {
	case <-ctx.Done():
		return nil, first
	case <-time.After(2 * time.Second):
	}
	log.Printf("sandbox: create for session %s failed, retrying once: %v", sessionID, first)
	return s.createHandle(ctx, sessionID, opts)
}

func (s *Service) createHandle(ctx context.Context, sessionID string, opts Options) (*Handle, error) {
	sb, err := s.client.createSandbox(ctx, sessionID, opts)
	if err != nil {
		return nil, err
	}
	execdURL, err := s.client.execdURL(ctx, sb)
	if err != nil {
		_ = sb.Kill(context.Background())
		return nil, err
	}
	h := &Handle{
		SandboxID:  sb.ID(),
		ExecdURL:   execdURL,
		sandbox:    sb,
		sessionID:  sessionID,
		ttlSeconds: opts.TTLSeconds,
	}
	h.Touch()
	if ttl := opts.TTLSeconds; ttl > 0 {
		h.expiresAt.Store(time.Now().Add(time.Duration(ttl) * time.Second).UnixNano())
	}
	if _, err := h.RunCommand(ctx, "mkdir -p /workspace", "/", nil, 10000, nil); err != nil {
		_ = sb.Kill(context.Background())
		return nil, fmt.Errorf("sandbox: prepare workspace: %w", err)
	}
	return h, nil
}

func (s *Service) Recycle(sessionID string, reason string) error {
	s.mu.Lock()
	h, ok := s.handles[sessionID]
	if ok {
		delete(s.handles, sessionID)
	}
	s.mu.Unlock()
	if !ok {
		return nil
	}
	return s.kill(h, reason)
}

func (s *Service) kill(h *Handle, reason string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	err := h.sandbox.Kill(ctx)
	s.emit(Event{SessionID: h.sessionID, SandboxID: h.SandboxID, Action: EventRecycled, Reason: reason})
	return err
}

func (s *Service) killBestEffort(h *Handle) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_ = h.sandbox.Kill(ctx)
}

func (s *Service) Stop() {
	if s.cancel != nil {
		s.cancel()
	}
	s.wg.Wait()
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

	sandbox    *opensandbox.Sandbox
	sessionID  string
	ttlSeconds int

	lastActive atomic.Int64
	expiresAt  atomic.Int64

	renewFn func(ctx context.Context, d time.Duration) (time.Time, error)
}

func (h *Handle) Touch() {
	h.lastActive.Store(time.Now().UnixNano())
}

func (h *Handle) LastActive() time.Time {
	return time.Unix(0, h.lastActive.Load())
}

func (h *Handle) expired() bool {
	exp := h.expiresAt.Load()
	return exp > 0 && time.Now().UnixNano() > exp
}

func (h *Handle) ExpiresAt() time.Time {
	return time.Unix(0, h.expiresAt.Load())
}

func (h *Handle) Renew(ctx context.Context) (time.Time, error) {
	d := time.Duration(h.ttlSeconds) * time.Second
	if d <= 0 {
		d = opensandbox.DefaultTimeoutSeconds * time.Second
	}
	if h.renewFn != nil {
		exp, err := h.renewFn(ctx, d)
		if err != nil {
			return time.Time{}, err
		}
		h.expiresAt.Store(exp.UnixNano())
		return exp, nil
	}
	resp, err := h.sandbox.Renew(ctx, d)
	if err != nil {
		return time.Time{}, err
	}
	h.expiresAt.Store(resp.ExpiresAt.UnixNano())
	return resp.ExpiresAt, nil
}

func (h *Handle) RunCommand(ctx context.Context, cmd, cwd string, env map[string]string, timeoutMs int64, onEvent func(stream, chunk string)) (*CommandResult, error) {
	h.Touch()
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
	h.Touch()
	return h.sandbox.UploadFile(ctx, bytes.NewReader(content), opensandbox.UploadFileOptions{
		FileName: path.Base(remotePath),
		Metadata: opensandbox.FileMetadata{Path: remotePath},
	})
}

func (h *Handle) UploadDir(ctx context.Context, localDir, remoteBase string) error {
	h.Touch()
	return filepath.WalkDir(localDir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(localDir, p)
		if err != nil {
			return err
		}
		remote := path.Join(remoteBase, filepath.ToSlash(rel))
		if d.IsDir() {
			if rel == "." {
				return nil
			}
			return h.sandbox.CreateDirectory(ctx, remote, opensandbox.OctalMode(0o755))
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return h.UploadFile(ctx, remote, data)
	})
}

func (h *Handle) DownloadFile(ctx context.Context, remotePath string) ([]byte, error) {
	h.Touch()
	rc, err := h.sandbox.DownloadFile(ctx, remotePath, "")
	if err != nil {
		return nil, err
	}
	defer func() { _ = rc.Close() }()
	return io.ReadAll(rc)
}

func (h *Handle) ReplaceInFile(ctx context.Context, remotePath, old, new string, replaceAll bool) (int, error) {
	h.Touch()
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
	h.Touch()
	return h.sandbox.SearchFiles(ctx, dir, pattern)
}

func (h *Handle) ListDir(ctx context.Context, remotePath string) ([]FileInfo, error) {
	h.Touch()
	return h.sandbox.ListDirectory(ctx, remotePath)
}

func (h *Handle) FileInfo(ctx context.Context, remotePath string) (FileInfo, error) {
	h.Touch()
	m, err := h.sandbox.GetFileInfo(ctx, remotePath)
	if err != nil {
		return FileInfo{}, err
	}
	if fi, ok := m[remotePath]; ok {
		return fi, nil
	}
	for _, fi := range m {
		return fi, nil
	}
	return FileInfo{}, fmt.Errorf("sandbox: no file info for %s", remotePath)
}
