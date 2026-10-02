package usage

import (
	"errors"
	"net/http"

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
	var ue *Error
	if errors.As(err, &ue) {
		respond.Error(c, ue.Status, ue.Code, ue.Message, ue.Details)
		return
	}
	respond.Error(c, http.StatusInternalServerError, respond.CodeInternal, "服务器内部错误", nil)
}

// OrgUsage GET /orgs/:orgId/admin/usage（admin）。
func (h *Handler) OrgUsage(c *gin.Context) {
	report, err := h.svc.Report(c.Request.Context(),
		c.Query("from"), c.Query("to"), c.Query("group_by"), auth.OrgID(c), false)
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, report)
}

// PlatformUsage GET /platform/usage（platform）。
func (h *Handler) PlatformUsage(c *gin.Context) {
	report, err := h.svc.Report(c.Request.Context(),
		c.Query("from"), c.Query("to"), c.Query("group_by"), "", true)
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, report)
}
