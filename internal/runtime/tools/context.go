// Package tools 定义模型可见的沙箱工具与 skill（runtime.md §4）。
package tools

import (
	"context"
	"path"
	"sync"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"

	"github.com/ravenmk2/wispjar/internal/agent"
	"github.com/ravenmk2/wispjar/internal/sandbox"
	"github.com/ravenmk2/wispjar/internal/session"
)

type ctxKey struct{}

// Handle is the synchronous protocol used by supervised workload tools.
type Handle = sandbox.Operations

// Meta 记录一次工具执行的 tool/result meta（render/截断/diff/耗时/沙箱）。
type Meta struct {
	Render     string
	Truncated  bool
	SandboxID  string
	ComputerID string
	RunID      string
	Diff       string
	Operation  string // write 成功后的 create/update；无法判断时为空。
	DurationMS int64
	// Attachments 工具产出图片的持久引用（read_media；tool/result data 记录用，
	// 回放时按引用从附件存储读字节重组多模态 tool 消息）。
	Attachments []session.AttachmentRef
}

const (
	RenderTerminal = "terminal"
	RenderDiff     = "diff"
	RenderSearch   = "search"
	RenderRead     = "read"
	RenderGeneric  = "generic"
)

// Context 是一个 turn 内全部工具共享的运行上下文（经 ctx 传递）。
type Context struct {
	WebSearch WebSearcher
	WebFetch  WebFetcher
	SessionID string
	OrgID     string
	// UserID 会话所属用户（read_media 图片入库的 created_by，贴合 attachment 表 CHAR(32)）。
	UserID        string
	AgentID       string
	ComputerID    string
	RunID         string
	ParentRunID   string
	Generation    uint64
	WorkDir       string
	SkillsDir     string
	AgentFilesDir string
	// BoundHandle is already protected by the root run's durable use/skill leases.
	BoundHandle    Handle
	BoundSandboxID string

	// Env belongs to this Agent; ComputerEnv is the shared task baseline.
	ComputerEnv map[string]string
	Env         map[string]string

	Skills []agent.SnapshotSkill

	PublishToolOutput func(callID, stream, delta string)

	// Images read_media 图片入库（内容寻址存储 + attachment 表；*attachment.Service 实现）。
	// nil 时 read_media 拒绝执行。
	Images ImageSaver

	mu    sync.Mutex
	metas map[string]*Meta
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
	m := &Meta{Render: render, ComputerID: c.ComputerID, RunID: c.RunID}
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
	if c.BoundHandle != nil {
		return c.BoundHandle, c.BoundSandboxID, nil
	}
	return nil, "", errSandboxUnavailable
}

func (c *Context) ResolvePath(value string) string {
	base := c.WorkDir
	if base == "" {
		base = session.WorkspaceDir(c.SessionID)
	}
	if value == "" {
		return base
	}
	if path.IsAbs(value) {
		return path.Clean(value)
	}
	return path.Join(base, value)
}

// Fork creates a child identity with independent metadata. Only the immutable
// resource handle and callbacks are shared; skill bindings are supplied afresh.
func (c *Context) Fork(agentID, runID string, skills []agent.SnapshotSkill) *Context {
	child := NewContext(c.SessionID, c.OrgID)
	child.UserID, child.AgentID = c.UserID, agentID
	child.ComputerID, child.Generation = c.ComputerID, c.Generation
	child.RunID, child.ParentRunID = runID, c.RunID
	child.WorkDir = c.WorkDir
	child.ComputerEnv = sandbox.MergeEnv(c.ComputerEnv)
	child.Skills, child.Images = skills, c.Images
	child.WebSearch = c.WebSearch
	child.WebFetch = c.WebFetch
	child.BoundHandle, child.BoundSandboxID = c.BoundHandle, c.BoundSandboxID
	child.PublishToolOutput = c.PublishToolOutput
	child.SkillsDir = "/agents/" + agentID + "/skills"
	child.AgentFilesDir = "/agents/" + agentID + "/files"
	return child
}

func (c *Context) publish(ctx context.Context, stream, delta string) {
	if c.PublishToolOutput != nil {
		c.PublishToolOutput(callID(ctx), stream, delta)
	}
}

func sinceMS(start time.Time) int64 { return time.Since(start).Milliseconds() }

// All 返回按 tools_enabled 过滤后的工具清单；skills 非空时附带 skill。
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

// ActivateSkill 单独构建 skill（快照含 skills 时注册）。
func ActivateSkill() (tool.BaseTool, error) { return newActivateSkillTool() }
