package attachment

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/ravenmk2/wispjar/internal/auth"
	"github.com/ravenmk2/wispjar/internal/gateway/respond"
	"github.com/ravenmk2/wispjar/internal/session"
)

// SessionLookup 附件上传时的会话归属校验（*session.Service 实现）。
type SessionLookup interface {
	Get(ctx context.Context, uid, sessionID string) (*session.Detail, error)
}

type Handler struct {
	svc      *Service
	sessions SessionLookup
}

func NewHandler(svc *Service, sessions SessionLookup) *Handler {
	return &Handler{svc: svc, sessions: sessions}
}

func fail(c *gin.Context, err error) {
	var ae *Error
	if errors.As(err, &ae) {
		respond.Error(c, ae.Status, ae.Code, ae.Message, ae.Details)
		return
	}
	var se *session.Error
	if errors.As(err, &se) {
		respond.Error(c, se.Status, se.Code, se.Message, se.Details)
		return
	}
	respond.Error(c, http.StatusInternalServerError, respond.CodeInternal, "服务器内部错误", nil)
}

// Upload POST /sessions/:id/attachments（multipart 字段 file）。
func (h *Handler) Upload(c *gin.Context) {
	ctx := c.Request.Context()
	sess, err := h.sessions.Get(ctx, auth.UID(c), c.Param("id"))
	if err != nil {
		fail(c, err)
		return
	}
	fh, err := c.FormFile("file")
	if err != nil {
		respond.Error(c, http.StatusBadRequest, respond.CodeInvalid, "缺少上传文件（multipart 字段 file）", gin.H{"field": "file"})
		return
	}
	if fh.Size > MaxImageBytes {
		fail(c, errInvalid(fmt.Sprintf("图片大小 %d 字节超过 8MB 限制", fh.Size), gin.H{"size": fh.Size}))
		return
	}
	src, err := fh.Open()
	if err != nil {
		fail(c, err)
		return
	}
	defer func() { _ = src.Close() }()
	data, err := io.ReadAll(io.LimitReader(src, MaxImageBytes+1))
	if err != nil {
		fail(c, err)
		return
	}
	ref, err := h.svc.Upload(ctx, sess.OrgID, auth.UID(c), fh.Filename, data)
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusCreated, ref)
}

// Download GET /attachments/:id：登录 + org 成员校验（非成员按 404 处理，防枚举）。
func (h *Handler) Download(c *gin.Context) {
	ctx := c.Request.Context()
	row, err := h.svc.Row(ctx, c.Param("id"))
	if err != nil {
		fail(c, err)
		return
	}
	member, err := h.svc.IsMember(ctx, row.OrgID, auth.UID(c))
	if err != nil {
		fail(c, err)
		return
	}
	if !member {
		fail(c, errNotFound("附件不存在"))
		return
	}
	etag := `"` + strings.TrimPrefix(row.ID, idPrefix) + `"`
	if c.GetHeader("If-None-Match") == etag {
		c.Status(http.StatusNotModified)
		return
	}
	data, err := h.svc.ReadBytes(ctx, row.ID)
	if err != nil {
		fail(c, err)
		return
	}
	c.Header("Cache-Control", "private, immutable")
	c.Header("ETag", etag)
	c.Data(http.StatusOK, row.MIME, data)
}
