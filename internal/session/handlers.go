package session

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/ravenmk2/wispjar/internal/auth"
	"github.com/ravenmk2/wispjar/internal/gateway/respond"
)

type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

func fail(c *gin.Context, err error) {
	var se *Error
	if errors.As(err, &se) {
		respond.Error(c, se.Status, se.Code, se.Message, se.Details)
		return
	}
	respond.Error(c, http.StatusInternalServerError, respond.CodeInternal, "服务器内部错误", nil)
}

type createSessionRequest struct {
	AgentID string `json:"agent_id" binding:"required"`
	Title   string `json:"title"`
}

func (h *Handler) CreateSession(c *gin.Context) {
	var req createSessionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respond.Error(c, http.StatusBadRequest, respond.CodeInvalid, "请求参数无效", nil)
		return
	}
	detail, err := h.svc.Create(c.Request.Context(), auth.UID(c), auth.OrgID(c), CreateInput(req))
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusCreated, detail)
}

func (h *Handler) ListSessions(c *gin.Context) {
	page, ok := respond.ParsePage(c)
	if !ok {
		return
	}
	items, next, err := h.svc.List(c.Request.Context(), auth.UID(c), auth.OrgID(c), ListFilter{
		Q:       c.Query("q"),
		Status:  c.Query("status"),
		AgentID: c.Query("agent_id"),
		Cursor:  page.Cursor,
		Limit:   page.Limit,
	})
	if err != nil {
		fail(c, err)
		return
	}
	respond.PageJSON(c, items, next)
}

func (h *Handler) GetSession(c *gin.Context) {
	detail, err := h.svc.Get(c.Request.Context(), auth.UID(c), c.Param("id"))
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, detail)
}

type patchSessionRequest struct {
	Title        *string `json:"title"`
	ApprovalMode *string `json:"approval_mode"`
	Status       *string `json:"status"`
	ModelID      *string `json:"model_id"`
}

func (h *Handler) PatchSession(c *gin.Context) {
	var req patchSessionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respond.Error(c, http.StatusBadRequest, respond.CodeInvalid, "请求参数无效", nil)
		return
	}
	detail, err := h.svc.Update(c.Request.Context(), auth.UID(c), c.Param("id"), UpdateInput(req))
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, detail)
}

func (h *Handler) DeleteSession(c *gin.Context) {
	if err := h.svc.Delete(c.Request.Context(), auth.UID(c), c.Param("id")); err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (h *Handler) ListEvents(c *gin.Context) {
	var afterSeq int64
	if s := c.Query("after_seq"); s != "" {
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil || n < 0 {
			respond.Error(c, http.StatusBadRequest, respond.CodeInvalid, "after_seq 必须是非负整数", gin.H{"after_seq": s})
			return
		}
		afterSeq = n
	}
	limit := 200
	if s := c.Query("limit"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 || n > 500 {
			respond.Error(c, http.StatusBadRequest, respond.CodeInvalid, "limit 必须是 1..500 的整数", gin.H{"limit": s})
			return
		}
		limit = n
	}
	items, lastSeq, err := h.svc.ListEvents(c.Request.Context(), auth.UID(c), c.Param("id"), afterSeq, limit)
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items, "last_seq": lastSeq})
}
