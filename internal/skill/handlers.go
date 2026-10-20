package skill

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

func (h *Handler) ReplacePackage(c *gin.Context) {
	revision, err := strconv.ParseUint(c.PostForm("expected_revision"), 10, 64)
	if err != nil || revision == 0 {
		fail(c, errInvalid("expected_revision 必须是大于 0 的整数", nil))
		return
	}
	fh, err := c.FormFile("file")
	if err != nil {
		fail(c, errInvalid("缺少上传文件（multipart 字段 file）", nil))
		return
	}
	item, err := h.svc.ReplacePackage(c.Request.Context(), auth.UID(c), auth.OrgID(c), c.Param("id"), revision, fh)
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, item)
}

func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

func fail(c *gin.Context, err error) {
	var ae *Error
	if errors.As(err, &ae) {
		respond.Error(c, ae.Status, ae.Code, ae.Message, ae.Details)
		return
	}
	respond.Error(c, http.StatusInternalServerError, respond.CodeInternal, "服务器内部错误", nil)
}

func (h *Handler) UploadSkill(c *gin.Context) {
	fh, err := c.FormFile("file")
	if err != nil {
		respond.Error(c, http.StatusBadRequest, respond.CodeInvalid, "缺少上传文件（multipart 字段 file）", nil)
		return
	}
	sk, err := h.svc.Upload(auth.UID(c), auth.OrgID(c), fh)
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusCreated, sk)
}

func (h *Handler) ListSkills(c *gin.Context) {
	items, err := h.svc.List(auth.OrgID(c))
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

func (h *Handler) GetSkill(c *gin.Context) {
	detail, err := h.svc.Get(auth.OrgID(c), c.Param("id"))
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, detail)
}

func (h *Handler) GetSkillFile(c *gin.Context) {
	rel, content, err := h.svc.ReadFile(auth.OrgID(c), c.Param("id"), c.Query("path"))
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"path": rel, "content": content, "truncated": false})
}

func (h *Handler) DeleteSkill(c *gin.Context) {
	if err := h.svc.Delete(auth.UID(c), auth.OrgID(c), c.Param("id")); err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}
