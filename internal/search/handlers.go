package search

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/ravenmk2/wispjar/internal/auth"
	"github.com/ravenmk2/wispjar/internal/gateway/respond"
)

type Handler struct{ svc *Service }

func NewHandler(svc *Service) *Handler { return &Handler{svc} }
func fail(c *gin.Context, err error) {
	var se *Error
	if errors.As(err, &se) {
		respond.Error(c, se.Status, se.Code, se.Message, nil)
		return
	}
	respond.Error(c, 500, respond.CodeInternal, "服务器内部错误", nil)
}
func (h *Handler) List(c *gin.Context) {
	rows, err := h.svc.List(c.Request.Context())
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(200, gin.H{"items": rows})
}
func (h *Handler) Options(c *gin.Context) {
	rows, err := h.svc.Options(c.Request.Context())
	if err != nil {
		fail(c, err)
		return
	}
	cfg, err := h.svc.Config(c.Request.Context())
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(200, gin.H{"items": rows, "default_provider_id": cfg.DefaultProviderID})
}
func (h *Handler) Config(c *gin.Context) {
	cfg, err := h.svc.Config(c.Request.Context())
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(200, cfg)
}
func (h *Handler) SetDefault(c *gin.Context) {
	var req map[string]json.RawMessage
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, invalid("请求参数无效"))
		return
	}
	raw, exists := req["default_provider_id"]
	var providerID *string
	if !exists || json.Unmarshal(raw, &providerID) != nil {
		fail(c, invalid("default_provider_id 必须是实例 ID 或 null"))
		return
	}
	if err := h.svc.SetDefault(c.Request.Context(), auth.UID(c), providerID); err != nil {
		fail(c, err)
		return
	}
	c.JSON(200, gin.H{"default_provider_id": providerID})
}
func (h *Handler) Save(c *gin.Context) {
	var req Input
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, invalid("请求参数无效"))
		return
	}
	view, err := h.svc.Save(c.Request.Context(), auth.UID(c), c.Param("id"), req)
	if err != nil {
		fail(c, err)
		return
	}
	status := http.StatusOK
	if c.Param("id") == "" {
		status = http.StatusCreated
	}
	c.JSON(status, view)
}
func (h *Handler) Delete(c *gin.Context) {
	if err := h.svc.Delete(c.Request.Context(), auth.UID(c), c.Param("id")); err != nil {
		fail(c, err)
		return
	}
	c.JSON(200, gin.H{"ok": true})
}
func (h *Handler) Check(c *gin.Context) {
	var req struct {
		Query string `json:"query"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, invalid("请提供测试搜索词"))
		return
	}
	out, err := h.svc.Search(c.Request.Context(), c.Param("id"), Request{Query: req.Query, Count: 3, UserID: auth.UID(c)})
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(200, out)
}
