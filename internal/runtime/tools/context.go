// Package tools 定义模型可见的沙箱工具与 activate_skill（runtime.md §4）。
package tools

import (
	"context"
	"fmt"
	"log"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"

	"github.com/ravenmk2/wispjar/internal/agent"
	"github.com/ravenmk2/wispjar/internal/sandbox"
)

type ctxKey struct{}

// Handle 是沙箱操作句柄的接口（*sandbox.Handle 实现；测试可 fake）。
type Handle interface {
	RunCommand(ctx context.Context, cmd, cwd string, env map[string]string, timeoutMs int64, onEvent func(stream, chunk string)) (*sandbox.CommandResult, error)
	UploadFile(ctx context.Context, remotePath string, content []byte) error
	DownloadFile(ctx context.Context, remotePath string) ([]byte, error)
	ReplaceInFile(ctx context.Context, remotePath, old, new string, replaceAll bool) (int, error)
	SearchFiles(ctx context.Context, dir, pattern string) ([]sandbox.FileInfo, error)
	ListDir(ctx context.Context, remotePath string) ([]sandbox.FileInfo, error)
	FileInfo(ctx context.Context, remotePath string) (sandbox.FileInfo, error)
}

// Meta 记录一次工具执行的 tool/result meta（render/截断/diff/耗时/沙箱）。
type Meta struct {
	Render     string
	Truncated  bool
	SandboxID  string
	Diff       string
	DurationMS int64
}

const (
	RenderTerminal = "terminal"
	RenderDiff     = "diff"
	RenderSearch   = "search"
	RenderRead     = "read"
	RenderGeneric  = "generic"
)

// SandboxProvider 抽象沙箱获取（sandbox.Manager 适配 / 测试 fake）。
type SandboxProvider interface {
	Get(ctx context.Context, sessionID string, opts sandbox.Options) (Handle, string, error)
}

type sandboxManagerAdapter struct{ m sandbox.Manager }

func (a sandboxManagerAdapter) Get(ctx context.Context, sessionID string, opts sandbox.Options) (Handle, string, error) {
	h, err := a.m.Get(ctx, sessionID, opts)
	if err != nil {
		return nil, "", err
	}
	return h, h.SandboxID, nil
}

func WrapManager(m sandbox.Manager) SandboxProvider { return sandboxManagerAdapter{m} }

// Context 是一个 turn 内全部工具共享的运行上下文（经 ctx 传递）。
type Context struct {
	SessionID string
	OrgID     string

	Sandbox   SandboxProvider
	Image     string
	Resources map[string]string

	DataDir string
	Skills  []agent.SnapshotSkill

	PublishToolOutput func(callID, stream, delta string)

	// OnSkillUploadFailed 技能目录上传沙箱失败回调（非阻断；builder 注入以记录会话事件）。
	OnSkillUploadFailed func(skillName, sandboxID string, err error)

	mu     sync.Mutex
	handle *cachedHandle
	metas  map[string]*Meta
}

func NewContext(sessionID, orgID string) *Context {
	return &Context{SessionID: sessionID, OrgID: orgID, metas: map[string]*Meta{}}
}

func WithContext(ctx context.Context, tc *Context) context.Context {
	return context.WithValue(ctx, ctxKey{}, tc)
}

func FromContext(ctx context.Context) *Context {
	tc, _ := ctx.Value(ctxKey{}).(*Context)
	return tc
}

func callID(ctx context.Context) string { return compose.GetToolCallID(ctx) }

// BeginMeta 导出 beginMeta，供 runtime 包内非沙箱工具（task）登记 tool/result meta。
func (c *Context) BeginMeta(ctx context.Context, render string) *Meta {
	return c.beginMeta(ctx, render)
}

func (c *Context) beginMeta(ctx context.Context, render string) *Meta {
	m := &Meta{Render: render}
	c.mu.Lock()
	c.metas[callID(ctx)] = m
	c.mu.Unlock()
	return m
}

// TakeMeta 取出并删除某次调用的 meta（tool/result 落库时调用）。
func (c *Context) TakeMeta(id string) *Meta {
	c.mu.Lock()
	defer c.mu.Unlock()
	m := c.metas[id]
	delete(c.metas, id)
	return m
}

func (c *Context) sandboxHandle(ctx context.Context) (Handle, string, error) {
	c.mu.Lock()
	h := c.handle
	c.mu.Unlock()
	if h != nil {
		return h.h, h.id, nil
	}
	if c.Sandbox == nil {
		return nil, "", errSandboxUnavailable
	}
	opts := sandbox.Options{Image: c.Image, ResourceLimits: c.Resources}
	sh, sid, err := c.Sandbox.Get(ctx, c.SessionID, opts)
	if err != nil {
		log.Printf("tools: sandbox get for session %s: %v", c.SessionID, err)
		return nil, "", errSandboxUnavailable
	}
	c.uploadSkills(ctx, sh, sid)
	return c.setHandle(sh, sid), sid, nil
}

// uploadSkills 在沙箱就绪后上传会话激活 Skills 到 /skills/<name>/（runtime.md §7）。
// 单个失败只回调记录，不阻断其余技能与工具调用。
func (c *Context) uploadSkills(ctx context.Context, h Handle, sandboxID string) {
	UploadSkills(ctx, h, c.DataDir, c.OrgID, c.Skills, func(skillName string, err error) {
		if c.OnSkillUploadFailed != nil {
			c.OnSkillUploadFailed(skillName, sandboxID, err)
		}
	})
}

// UploadSkills 上传激活 Skills 到沙箱 /skills/<name>/。除 turn 内工具上下文外，
// REST 沙箱面板在沙箱被重建后（如服务重启句柄丢失）也需保证技能目录一致。
// 单技能失败回调记录，不阻断其余技能。
func UploadSkills(ctx context.Context, h Handle, dataDir, orgID string, skills []agent.SnapshotSkill, onFailed func(skillName string, err error)) {
	if len(skills) == 0 || dataDir == "" {
		return
	}
	for i := range skills {
		sk := skills[i]
		dir, err := skillDir(dataDir, orgID, sk.ID)
		if err == nil {
			err = uploadSkillDir(ctx, h, dir, "/skills/"+sk.Name)
		}
		if err != nil && onFailed != nil {
			onFailed(sk.Name, err)
		}
	}
}

func uploadSkillDir(ctx context.Context, h Handle, localDir, remoteBase string) error {
	dirs := []string{}
	type fileEntry struct{ rel, abs string }
	files := []fileEntry{}
	err := filepath.WalkDir(localDir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(localDir, p)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			dirs = append(dirs, rel)
			return nil
		}
		if rel == "_source.zip" {
			return nil
		}
		files = append(files, fileEntry{rel: rel, abs: p})
		return nil
	})
	if err != nil {
		return err
	}
	mkdirs := make([]string, 0, len(dirs)+1)
	mkdirs = append(mkdirs, shellQuote(remoteBase))
	for _, d := range dirs {
		mkdirs = append(mkdirs, shellQuote(path.Join(remoteBase, d)))
	}
	res, err := h.RunCommand(ctx, "mkdir -p "+strings.Join(mkdirs, " "), "/", nil, 30000, nil)
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("mkdir %s: exit %d: %s", remoteBase, res.ExitCode, strings.TrimSpace(res.Stderr))
	}
	for _, f := range files {
		data, err := os.ReadFile(f.abs)
		if err != nil {
			return err
		}
		if err := h.UploadFile(ctx, path.Join(remoteBase, f.rel), data); err != nil {
			return err
		}
	}
	return nil
}

type cachedHandle struct {
	h  Handle
	id string
}

// setHandle 缓存句柄。
func (c *Context) setHandle(h Handle, id string) Handle {
	c.mu.Lock()
	c.handle = &cachedHandle{h: h, id: id}
	c.mu.Unlock()
	return h
}

func (c *Context) publish(ctx context.Context, stream, delta string) {
	if c.PublishToolOutput != nil {
		c.PublishToolOutput(callID(ctx), stream, delta)
	}
}

func sinceMS(start time.Time) int64 { return time.Since(start).Milliseconds() }

// All 返回按 tools_enabled 过滤后的工具清单；skills 非空时附带 activate_skill。
func All(enabled map[string]bool) ([]tool.BaseTool, error) {
	defs := []struct {
		name string
		make func() (tool.BaseTool, error)
	}{
		{"exec", newExecTool},
		{"read", newReadTool},
		{"write", newWriteTool},
		{"edit", newEditTool},
		{"glob", newGlobTool},
		{"grep", newGrepTool},
	}
	out := make([]tool.BaseTool, 0, len(defs)+1)
	for _, d := range defs {
		if !enabled[d.name] {
			continue
		}
		t, err := d.make()
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, nil
}

// ActivateSkill 单独构建 activate_skill（快照含 skills 时注册）。
func ActivateSkill() (tool.BaseTool, error) { return newActivateSkillTool() }
