package provider

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/ravenmk2/wispjar/internal/auth"
	"github.com/ravenmk2/wispjar/internal/gateway/respond"
	"github.com/ravenmk2/wispjar/internal/store"
)

type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

func fail(c *gin.Context, err error) {
	var pe *Error
	if errors.As(err, &pe) {
		respond.Error(c, pe.Status, pe.Code, pe.Message, pe.Details)
		return
	}
	respond.Error(c, http.StatusInternalServerError, respond.CodeInternal, "服务器内部错误", nil)
}

func (h *Handler) ListProviders(c *gin.Context) {
	views, err := h.svc.ListProviders()
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": views})
}

func (h *Handler) CreateProvider(c *gin.Context) {
	var req struct {
		Name    string          `json:"name" binding:"required"`
		Type    string          `json:"type" binding:"required"`
		BaseURL string          `json:"base_url" binding:"required"`
		APIKey  string          `json:"api_key" binding:"required"`
		Extra   json.RawMessage `json:"extra"`
		Remark  string          `json:"remark"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		respond.Error(c, http.StatusBadRequest, respond.CodeInvalid, "请求参数无效", nil)
		return
	}
	view, err := h.svc.CreateProvider(auth.UID(c), CreateProviderInput{
		Name:    req.Name,
		Type:    req.Type,
		BaseURL: req.BaseURL,
		APIKey:  req.APIKey,
		Extra:   req.Extra,
		Remark:  req.Remark,
	})
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusCreated, view)
}

func (h *Handler) GetProvider(c *gin.Context) {
	view, err := h.svc.GetProvider(c.Param("id"))
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, view)
}

func (h *Handler) UpdateProvider(c *gin.Context) {
	var req struct {
		Name    *string `json:"name"`
		Type    *string `json:"type"`
		BaseURL *string `json:"base_url"`
		APIKey  *string `json:"api_key"`
		Remark  *string `json:"remark"`
		Status  *string `json:"status"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		respond.Error(c, http.StatusBadRequest, respond.CodeInvalid, "请求参数无效", nil)
		return
	}
	view, err := h.svc.UpdateProvider(auth.UID(c), c.Param("id"), UpdateProviderInput{
		Name:    req.Name,
		Type:    req.Type,
		BaseURL: req.BaseURL,
		APIKey:  req.APIKey,
		Remark:  req.Remark,
		Status:  req.Status,
	})
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, view)
}

func (h *Handler) DeleteProvider(c *gin.Context) {
	if err := h.svc.DeleteProvider(auth.UID(c), c.Param("id")); err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (h *Handler) CheckProvider(c *gin.Context) {
	res, err := h.svc.CheckProvider(c.Param("id"))
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, res)
}

func (h *Handler) ListModels(c *gin.Context) {
	views, err := h.svc.ListModels(c.Query("provider_id"))
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": views})
}

func (h *Handler) CreateModel(c *gin.Context) {
	var req struct {
		ProviderID    string          `json:"provider_id" binding:"required"`
		ModelCode     string          `json:"model_code" binding:"required"`
		DisplayName   string          `json:"display_name" binding:"required"`
		Capabilities  json.RawMessage `json:"capabilities"`
		ContextSize   int             `json:"context_size"`
		DefaultParams json.RawMessage `json:"default_params"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		respond.Error(c, http.StatusBadRequest, respond.CodeInvalid, "请求参数无效", nil)
		return
	}
	view, err := h.svc.CreateModel(auth.UID(c), CreateModelInput{
		ProviderID:    req.ProviderID,
		ModelCode:     req.ModelCode,
		DisplayName:   req.DisplayName,
		Capabilities:  req.Capabilities,
		ContextSize:   req.ContextSize,
		DefaultParams: req.DefaultParams,
	})
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusCreated, view)
}

func (h *Handler) UpdateModel(c *gin.Context) {
	var req struct {
		ModelCode     *string         `json:"model_code"`
		DisplayName   *string         `json:"display_name"`
		Capabilities  json.RawMessage `json:"capabilities"`
		ContextSize   *int            `json:"context_size"`
		DefaultParams json.RawMessage `json:"default_params"`
		Status        *string         `json:"status"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		respond.Error(c, http.StatusBadRequest, respond.CodeInvalid, "请求参数无效", nil)
		return
	}
	view, err := h.svc.UpdateModel(auth.UID(c), c.Param("id"), UpdateModelInput{
		ModelCode:     req.ModelCode,
		DisplayName:   req.DisplayName,
		Capabilities:  req.Capabilities,
		ContextSize:   req.ContextSize,
		DefaultParams: req.DefaultParams,
		Status:        req.Status,
	})
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, view)
}

func (h *Handler) DeleteModel(c *gin.Context) {
	if err := h.svc.DeleteModel(auth.UID(c), c.Param("id")); err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (h *Handler) ListWorkspaceModels(c *gin.Context) {
	views, err := h.svc.ListWorkspaceModels()
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": views})
}

func (h *Handler) GetSettings(c *gin.Context) {
	settings, err := h.svc.ListSettings()
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": settings})
}

// GetSandboxSettings GET /orgs/:orgId/admin/sandbox-settings：机构 admin 只读
// sandbox.* 设置子集（编辑器沙箱分区取镜像列表/默认资源用，不暴露其它平台 KV）。
func (h *Handler) GetSandboxSettings(c *gin.Context) {
	settings, err := h.svc.ListSettings()
	if err != nil {
		fail(c, err)
		return
	}
	out := make([]store.PlatformSetting, 0, len(settings))
	for _, s := range settings {
		if strings.HasPrefix(s.Key, "sandbox.") {
			out = append(out, s)
		}
	}
	c.JSON(http.StatusOK, gin.H{"items": out})
}

func (h *Handler) UpdateSetting(c *gin.Context) {
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, 1<<20))
	if err != nil || len(body) == 0 {
		respond.Error(c, http.StatusBadRequest, respond.CodeInvalid, "请求体必须是 value JSON", nil)
		return
	}
	setting, err := h.svc.UpdateSetting(auth.UID(c), c.Param("key"), json.RawMessage(body))
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, setting)
}
