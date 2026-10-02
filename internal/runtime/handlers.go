package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"

	"github.com/ravenmk2/wispjar/internal/agent"
	"github.com/ravenmk2/wispjar/internal/auth"
	"github.com/ravenmk2/wispjar/internal/gateway/respond"
	"github.com/ravenmk2/wispjar/internal/runtime/tools"
	"github.com/ravenmk2/wispjar/internal/sandbox"
	"github.com/ravenmk2/wispjar/internal/session"
	"github.com/ravenmk2/wispjar/internal/store"
)

// Handler 承载会话运行时相关 REST 端点（api.md §3）。
type Handler struct {
	orch     Orchestrator
	sessions *session.Service
	events   EventStore
	repo     SessionRepo
	sandbox  sandbox.Manager
	bus      *Bus
	model    func(ctx context.Context, modelID string) (modelCode, providerID string, err error)
	dataDir  string

	skillsMu     sync.Mutex
	skillsSynced map[string]string // sessionID -> 已完成技能目录同步的 sandboxID
}

func NewHandler(orch Orchestrator, sessions *session.Service, events EventStore, repo SessionRepo,
	sb sandbox.Manager, bus *Bus,
	modelLookup func(ctx context.Context, modelID string) (string, string, error), dataDir string) *Handler {
	return &Handler{
		orch: orch, sessions: sessions, events: events, repo: repo,
		sandbox: sb, bus: bus, model: modelLookup,
		dataDir: dataDir, skillsSynced: map[string]string{},
	}
}

// publishCommitted 持久事件提交后广播 event 帧 + control 投影（与 orchestrator.commit 同序约束）。
func (h *Handler) publishCommitted(sessionID string, events []store.SessionEvent) {
	if h.bus == nil {
		return
	}
	for _, ev := range events {
		h.bus.Publish(sessionID, EventFrame(session.Event{Seq: ev.Seq, Type: ev.Type, Time: ev.TimeMs, Data: ev.Data}))
	}
	go PublishControlProjection(h.repo, h.bus, sessionID)
}

func fail(c *gin.Context, err error) {
	var re *Error
	if errors.As(err, &re) {
		respond.Error(c, re.Status, re.Code, re.Message, re.Details)
		return
	}
	var se *session.Error
	if errors.As(err, &se) {
		respond.Error(c, se.Status, se.Code, se.Message, se.Details)
		return
	}
	respond.Error(c, http.StatusInternalServerError, respond.CodeInternal, "服务器内部错误", nil)
}

type promptRequest struct {
	Content   string `json:"content" binding:"required"`
	RequestID string `json:"request_id" binding:"required"`
}

// Prompt POST /sessions/:id/prompts：幂等投递 + 异步 StartTurn。
func (h *Handler) Prompt(c *gin.Context) {
	var req promptRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respond.Error(c, http.StatusBadRequest, respond.CodeInvalid, "content 与 request_id 必填", nil)
		return
	}
	req.Content = strings.TrimSpace(req.Content)
	if req.Content == "" {
		respond.Error(c, http.StatusBadRequest, respond.CodeInvalid, "content 不能为空", nil)
		return
	}
	ctx := c.Request.Context()
	uid := auth.UID(c)
	sess, err := h.sessions.Get(ctx, uid, c.Param("id"))
	if err != nil {
		fail(c, err)
		return
	}
	if existing, err := h.events.FindUserMessageByRequestID(ctx, sess.ID, req.RequestID); err != nil {
		fail(c, err)
		return
	} else if existing != nil {
		c.JSON(http.StatusOK, gin.H{"accepted": true, "seq": existing.Seq})
		return
	}
	if sess.Running {
		fail(c, ErrSessionRunning)
		return
	}

	var snap agent.Snapshot
	if err := json.Unmarshal(sess.AgentSnapshot, &snap); err != nil {
		fail(c, err)
		return
	}
	modelCode, providerID, err := h.model(ctx, sess.ModelID)
	if err != nil {
		fail(c, err)
		return
	}
	toolNames := enabledToolNames(&snap)
	turn, err := h.repo.TurnCount(ctx, sess.ID)
	if err != nil {
		fail(c, err)
		return
	}
	turn++

	events, err := h.events.AppendBatch(ctx, sess.ID, []session.EventInput{
		{Type: session.EventUserMessage, Data: session.UserMessageData{Turn: turn, Content: req.Content, RequestID: req.RequestID}},
		{Type: session.EventTurnStart, Data: session.TurnStartData{Turn: turn, RequestID: req.RequestID}},
		{Type: session.EventRequestHeader, Data: session.RequestHeaderData{
			Turn: turn, ProviderID: providerID, ModelID: sess.ModelID, ModelCode: modelCode,
			Params: snap.ModelParams, Tools: toolNames,
		}},
	})
	if err != nil {
		fail(c, err)
		return
	}
	h.publishCommitted(sess.ID, events)
	if o, ok := h.orch.(*orchestrator); ok {
		o.maybeGenerateTitle(sess.ID)
	}
	go func() { _ = h.orch.StartTurn(context.Background(), sess.ID) }()
	c.JSON(http.StatusAccepted, gin.H{"accepted": true, "seq": events[0].Seq})
}

func enabledToolNames(snap *agent.Snapshot) []string {
	enabled := parseToolsEnabled(snap.ToolsEnabled)
	names := make([]string, 0, len(enabled)+1)
	for name, on := range enabled {
		if on {
			names = append(names, name)
		}
	}
	if len(snap.Skills) > 0 {
		names = append(names, "activate_skill")
	}
	sort.Strings(names)
	return names
}

// Cancel POST /sessions/:id/cancel。
func (h *Handler) Cancel(c *gin.Context) {
	if _, err := h.sessions.Get(c.Request.Context(), auth.UID(c), c.Param("id")); err != nil {
		fail(c, err)
		return
	}
	if err := h.orch.Cancel(c.Param("id")); err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

type approvalRequest struct {
	Decision string `json:"decision" binding:"required"`
}

// Approval POST /sessions/:id/approvals/:callId。
func (h *Handler) Approval(c *gin.Context) {
	var req approvalRequest
	if err := c.ShouldBindJSON(&req); err != nil || (req.Decision != "allow" && req.Decision != "deny") {
		respond.Error(c, http.StatusBadRequest, respond.CodeInvalid, "decision 仅支持 allow|deny", nil)
		return
	}
	if _, err := h.sessions.Get(c.Request.Context(), auth.UID(c), c.Param("id")); err != nil {
		fail(c, err)
		return
	}
	err := h.orch.DecideApproval(c.Param("id"), c.Param("callId"), req.Decision == "allow", auth.UID(c))
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (h *Handler) sandboxHandle(c *gin.Context, sess *session.Detail) (*sandbox.Handle, bool) {
	var sb struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	_ = json.Unmarshal(sess.Sandbox, &sb)
	if sb.ID == "" || h.sandbox == nil {
		fail(c, ErrSandboxNotFound)
		return nil, false
	}
	var snap agent.Snapshot
	if err := json.Unmarshal(sess.AgentSnapshot, &snap); err != nil {
		fail(c, err)
		return nil, false
	}
	sc := parseSandboxConfig(snap.SandboxConfig)
	handle, err := h.sandbox.Get(c.Request.Context(), sess.ID, sandbox.Options{Image: sc.Image, ResourceLimits: sc.Resources})
	if err != nil {
		respond.Error(c, http.StatusBadGateway, "sandbox/upstream", "调用沙箱失败", gin.H{"reason": err.Error()})
		return nil, false
	}
	h.syncSkills(c.Request.Context(), sess, &snap, handle)
	return handle, true
}

// syncSkills 保证 REST 侧拿到的沙箱含 /skills/<name>/（沙箱可能被重建过，
// 例如服务重启后句柄丢失再按需新建；turn 内路径由 tools.Context 自行上传）。
func (h *Handler) syncSkills(ctx context.Context, sess *session.Detail, snap *agent.Snapshot, handle *sandbox.Handle) {
	if len(snap.Skills) == 0 || h.dataDir == "" {
		return
	}
	h.skillsMu.Lock()
	synced := h.skillsSynced[sess.ID] == handle.SandboxID
	if !synced {
		h.skillsSynced[sess.ID] = handle.SandboxID
	}
	h.skillsMu.Unlock()
	if synced {
		return
	}
	tools.UploadSkills(ctx, handle, h.dataDir, sess.OrgID, snap.Skills, func(skillName string, err error) {
		log.Printf("runtime: skill %s upload to sandbox %s (session %s): %v", skillName, handle.SandboxID, sess.ID, err)
	})
}

// SandboxFiles GET /sessions/:id/sandbox/files?path=。
func (h *Handler) SandboxFiles(c *gin.Context) {
	sess, err := h.sessions.Get(c.Request.Context(), auth.UID(c), c.Param("id"))
	if err != nil {
		fail(c, err)
		return
	}
	handle, ok := h.sandboxHandle(c, sess)
	if !ok {
		return
	}
	dir := c.DefaultQuery("path", "/workspace")
	infos, err := handle.ListDir(c.Request.Context(), dir)
	if err != nil {
		respond.Error(c, http.StatusBadGateway, "sandbox/upstream", "列出目录失败", gin.H{"reason": err.Error()})
		return
	}
	items := make([]gin.H, 0, len(infos))
	for _, fi := range infos {
		typ := "file"
		if fi.Type == "directory" {
			typ = "dir"
		}
		items = append(items, gin.H{
			"name": path.Base(fi.Path), "path": fi.Path, "type": typ,
			"size": fi.Size, "modified_at": fi.ModifiedAt.UTC().Format(time.RFC3339),
		})
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

// SandboxFile GET /sessions/:id/sandbox/file?path=&max_bytes=。
func (h *Handler) SandboxFile(c *gin.Context) {
	sess, err := h.sessions.Get(c.Request.Context(), auth.UID(c), c.Param("id"))
	if err != nil {
		fail(c, err)
		return
	}
	filePath := strings.TrimSpace(c.Query("path"))
	if filePath == "" {
		respond.Error(c, http.StatusBadRequest, respond.CodeInvalid, "path 必填", nil)
		return
	}
	maxBytes := 256 * 1024
	if s := c.Query("max_bytes"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 || n > 4*1024*1024 {
			respond.Error(c, http.StatusBadRequest, respond.CodeInvalid, "max_bytes 必须是 1..4194304 的整数", gin.H{"max_bytes": s})
			return
		}
		maxBytes = n
	}
	handle, ok := h.sandboxHandle(c, sess)
	if !ok {
		return
	}
	data, err := handle.DownloadFile(c.Request.Context(), filePath)
	if err != nil {
		respond.Error(c, http.StatusBadGateway, "sandbox/upstream", "下载文件失败", gin.H{"reason": err.Error()})
		return
	}
	truncated := len(data) > maxBytes
	if truncated {
		data = data[:maxBytes]
	}
	if !utf8.Valid(data) {
		c.Data(http.StatusOK, "application/octet-stream", data)
		return
	}
	c.JSON(http.StatusOK, gin.H{"path": filePath, "content": string(data), "truncated": truncated})
}

// DeleteSession DELETE /sessions/:id：先 Cancel，软删后异步回收沙箱（接线点）。
func (h *Handler) DeleteSession(c *gin.Context) {
	ctx := c.Request.Context()
	uid := auth.UID(c)
	sess, err := h.sessions.Get(ctx, uid, c.Param("id"))
	if err != nil {
		fail(c, err)
		return
	}
	if err := h.orch.Cancel(sess.ID); err != nil && !errors.Is(err, ErrSessionRunning) {
		fail(c, err)
		return
	}
	h.skillsMu.Lock()
	delete(h.skillsSynced, sess.ID)
	h.skillsMu.Unlock()
	if err := h.sessions.Delete(ctx, uid, sess.ID); err != nil {
		fail(c, err)
		return
	}
	if h.bus != nil {
		go PublishControlProjection(h.repo, h.bus, sess.ID)
	}
	if h.sandbox != nil {
		go func() { _ = h.sandbox.Recycle(sess.ID, "session deleted") }()
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}
