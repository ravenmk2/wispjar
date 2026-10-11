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
	"github.com/ravenmk2/wispjar/internal/attachment"
	"github.com/ravenmk2/wispjar/internal/auth"
	"github.com/ravenmk2/wispjar/internal/gateway/respond"
	"github.com/ravenmk2/wispjar/internal/id"
	"github.com/ravenmk2/wispjar/internal/runtime/tools"
	"github.com/ravenmk2/wispjar/internal/sandbox"
	"github.com/ravenmk2/wispjar/internal/session"
	"github.com/ravenmk2/wispjar/internal/store"
)

// SessionService 是 Handler 对会话服务的窄接口（*session.Service 实现；测试可替换）。
type SessionService interface {
	Get(ctx context.Context, uid, sessionID string) (*session.Detail, error)
	Delete(ctx context.Context, uid, sessionID string) error
}

// AttachmentResolver 校验并解析 prompt 携带的附件引用（internal/attachment.Service 实现）。
type AttachmentResolver interface {
	Resolve(ctx context.Context, orgID string, ids []string) ([]session.AttachmentRef, error)
}

// Handler 承载会话运行时相关 REST 端点（api.md §3）。
type Handler struct {
	orch        Orchestrator
	sessions    SessionService
	events      EventStore
	repo        SessionRepo
	sandbox     sandbox.Manager
	bus         *Bus
	model       func(ctx context.Context, modelID string) (modelCode, providerID string, vision bool, err error)
	attachments AttachmentResolver
	dataDir     string

	skillsMu     sync.Mutex
	skillsSynced map[string]string // sessionID -> 已完成技能目录同步的 sandboxID
}

func NewHandler(orch Orchestrator, sessions SessionService, events EventStore, repo SessionRepo,
	sb sandbox.Manager, bus *Bus,
	modelLookup func(ctx context.Context, modelID string) (string, string, bool, error), dataDir string,
	attachments AttachmentResolver) *Handler {
	return &Handler{
		orch: orch, sessions: sessions, events: events, repo: repo,
		sandbox: sb, bus: bus, model: modelLookup, attachments: attachments,
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
	var ae *attachment.Error
	if errors.As(err, &ae) {
		respond.Error(c, ae.Status, ae.Code, ae.Message, ae.Details)
		return
	}
	respond.Error(c, http.StatusInternalServerError, respond.CodeInternal, "服务器内部错误", nil)
}

type promptRequest struct {
	Content     string   `json:"content" binding:"required"`
	RequestID   string   `json:"request_id" binding:"required"`
	Attachments []string `json:"attachments"`
}

// Prompt POST /sessions/:id/prompts：幂等投递 + 异步 StartTurn。
func (h *Handler) Prompt(c *gin.Context) {
	var req promptRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respond.Error(c, http.StatusBadRequest, respond.CodeInvalid, "content 与 request_id 必填", nil)
		return
	}
	req.Content = strings.TrimSpace(req.Content)
	if req.Content == "" && len(req.Attachments) == 0 {
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

	var attachments []session.AttachmentRef
	if len(req.Attachments) > 0 {
		if h.attachments == nil {
			fail(c, ErrAttachmentsUnavailable)
			return
		}
		refs, err := h.attachments.Resolve(ctx, sess.OrgID, req.Attachments)
		if err != nil {
			fail(c, err)
			return
		}
		attachments = refs
	}
	seq, err := h.beginTurn(c, sess, req.Content, req.RequestID, attachments, nil, nil)
	if err != nil {
		fail(c, err)
		return
	}
	if o, ok := h.orch.(*orchestrator); ok {
		o.maybeGenerateTitle(sess.ID)
	}
	go func() { _ = h.orch.StartTurn(context.Background(), sess.ID) }()
	c.JSON(http.StatusAccepted, gin.H{"accepted": true, "seq": seq})
}

// beginTurn 追加 user/message + turn/start + request/header 并广播（Prompt 管线与
// 迟到提问答复共用）。pre 为需要排在这三个事件之前落库的额外事件（如 question/answered）。
// 返回 user/message 的 seq。调用方负责 sess.Running 前置检查与后续 StartTurn。
func (h *Handler) beginTurn(c *gin.Context, sess *session.Detail, content, requestID string,
	attachments []session.AttachmentRef, source *session.MessageSource, pre []session.EventInput) (int64, error) {
	ctx := c.Request.Context()
	var snap agent.Snapshot
	if err := json.Unmarshal(sess.AgentSnapshot, &snap); err != nil {
		return 0, err
	}
	modelCode, providerID, vision, err := h.model(ctx, sess.ModelID)
	if err != nil {
		return 0, err
	}
	if len(attachments) > 0 && !vision {
		return 0, ErrModelNotVision
	}
	toolNames := enabledToolNames(&snap)
	turn, err := h.repo.TurnCount(ctx, sess.ID)
	if err != nil {
		return 0, err
	}
	turn++
	reqParams, err := json.Marshal(mergeModelParams(snap.ModelParams, sess.ModelParams))
	if err != nil {
		return 0, err
	}
	headerData := session.RequestHeaderData{
		Turn: turn, ProviderID: providerID, ModelID: sess.ModelID, ModelCode: modelCode,
		Params: reqParams, Tools: toolNames,
	}
	if len(attachments) > 0 {
		headerData.ImageOffload = imageOffloadVersion
	}

	inputs := append([]session.EventInput{}, pre...)
	inputs = append(inputs,
		session.EventInput{Type: session.EventUserMessage, Data: session.UserMessageData{
			Turn: turn, Content: content, RequestID: requestID, Attachments: attachments, Source: source,
		}},
		session.EventInput{Type: session.EventTurnStart, Data: session.TurnStartData{Turn: turn, RequestID: requestID}},
		session.EventInput{Type: session.EventRequestHeader, Data: headerData},
	)
	events, err := h.events.AppendBatch(ctx, sess.ID, inputs)
	if err != nil {
		return 0, err
	}
	h.publishCommitted(sess.ID, events)
	return events[len(pre)].Seq, nil
}

func enabledToolNames(snap *agent.Snapshot) []string {
	enabled := parseToolsEnabled(snap.ToolsEnabled)
	names := make([]string, 0, len(enabled)+2)
	for name, on := range enabled {
		if on {
			names = append(names, name)
		}
	}
	if len(snap.Skills) > 0 {
		names = append(names, "skill")
	}
	names = append(names, "question")
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

type answerQuestionRequest struct {
	Answers []session.QuestionAnswer `json:"answers" binding:"required"`
}

// AnswerQuestion POST /sessions/:id/questions/:callId/answer（runtime.md §4.9）。
// open（工具仍在前台等待）→ 投递内存 channel 立即返回；continued（超时/中断/重启后）
// → 落 question/answered + 合成 user/message（source=question-reply）开启新 turn。
func (h *Handler) AnswerQuestion(c *gin.Context) {
	var req answerQuestionRequest
	if err := c.ShouldBindJSON(&req); err != nil || len(req.Answers) == 0 {
		respond.Error(c, http.StatusBadRequest, respond.CodeInvalid, "answers 必填且非空", nil)
		return
	}
	ctx := c.Request.Context()
	sess, err := h.sessions.Get(ctx, auth.UID(c), c.Param("id"))
	if err != nil {
		fail(c, err)
		return
	}
	callID := c.Param("callId")
	asked, err := h.repo.QuestionAsked(ctx, sess.ID, callID)
	if err != nil {
		fail(c, err)
		return
	}
	if asked == nil {
		fail(c, ErrQuestionNotFound)
		return
	}
	answered, err := h.repo.QuestionAnswered(ctx, sess.ID, callID)
	if err != nil {
		fail(c, err)
		return
	}
	if answered {
		fail(c, ErrQuestionNotPending)
		return
	}
	if err := validateQuestionAnswers(asked.Questions, req.Answers); err != nil {
		fail(c, &Error{Status: http.StatusBadRequest, Code: "question/bad_answer",
			Message: "每题必须恰好一条答案", Details: gin.H{"reason": err.Error()}})
		return
	}

	uid := auth.UID(c)
	delivered, err := h.orch.AnswerQuestion(sess.ID, callID, req.Answers, uid)
	if err != nil {
		fail(c, err)
		return
	}
	if delivered {
		c.JSON(http.StatusOK, gin.H{"ok": true, "delivered": "open"})
		return
	}

	// continued：合成 user/message 开新 turn（问题文本随正文回显，模型可直接续接）。
	if sess.Running {
		fail(c, ErrSessionRunning)
		return
	}
	body, err := json.Marshal(map[string]any{
		"kind":      "answer_to_pending_question",
		"call_id":   callID,
		"questions": asked.Questions,
		"answers":   req.Answers,
	})
	if err != nil {
		fail(c, err)
		return
	}
	pre := []session.EventInput{
		{Type: session.EventQuestionAnswered, Data: session.QuestionAnsweredData{
			CallID: callID, Answers: req.Answers, AnsweredBy: uid,
		}},
	}
	seq, err := h.beginTurn(c, sess, string(body), id.New(), nil,
		&session.MessageSource{Kind: "question-reply", CallID: callID}, pre)
	if err != nil {
		fail(c, err)
		return
	}
	go func() { _ = h.orch.StartTurn(context.Background(), sess.ID) }()
	c.JSON(http.StatusAccepted, gin.H{"accepted": true, "delivered": "continued", "seq": seq})
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
		respond.Error(c, http.StatusBadGateway, "sandbox/upstream", "调用云电脑失败", gin.H{"reason": err.Error()})
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
