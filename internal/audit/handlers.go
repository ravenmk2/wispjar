package audit

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/ravenmk2/wispjar/internal/gateway/respond"
)

type Handler struct {
	db *gorm.DB
}

func NewHandler(db *gorm.DB) *Handler {
	return &Handler{db: db}
}

// List GET /platform/audit?org_id=&cursor=&limit=（platform）。
func (h *Handler) List(c *gin.Context) {
	page, ok := respond.ParsePage(c)
	if !ok {
		return
	}
	items, next, err := List(c.Request.Context(), h.db, c.Query("org_id"), page.Cursor, page.Limit)
	if err != nil {
		respond.Error(c, http.StatusInternalServerError, respond.CodeInternal, "服务器内部错误", nil)
		return
	}
	respond.PageJSON(c, items, next)
}
