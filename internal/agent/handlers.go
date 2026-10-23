package agent

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/ravenmk2/wispjar/internal/auth"
	"github.com/ravenmk2/wispjar/internal/gateway/respond"
	"github.com/ravenmk2/wispjar/internal/sandbox"
)

type Handler struct {
	svc *Service
}

func (h *Handler) UpdateEnv(c *gin.Context) {
	var patch sandbox.EnvPatch
	if err := c.ShouldBindJSON(&patch); err != nil {
		fail(c, errInvalid("环境变量参数无效", nil))
		return
	}
	if err := h.svc.PatchEnv(c.Request.Context(), auth.UID(c), auth.OrgID(c), c.Param("id"), patch); err != nil {
		fail(c, err)
		return
	}
	c.JSON(200, gin.H{"ok": true})
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

type agentRequest struct {
	SearchProviderID *string         `json:"search_provider_id"`
	FetchProviderID  *string         `json:"fetch_provider_id"`
	Name             string          `json:"name" binding:"required"`
	Description      string          `json:"description"`
	Avatar           string          `json:"avatar"`
	SystemPrompt     string          `json:"system_prompt"`
	ModelID          string          `json:"model_id" binding:"required"`
	ModelParams      json.RawMessage `json:"model_params"`
	MaxIterations    int             `json:"max_iterations"`
	ToolsEnabled     json.RawMessage `json:"tools_enabled"`
	SkillIDs         []string        `json:"skill_ids"`
	SubAgentIDs      []string        `json:"sub_agent_ids"`
	SandboxConfig    json.RawMessage `json:"sandbox_config"`
}

func (r *agentRequest) input() AgentInput {
	return AgentInput{
		SearchProviderID: r.SearchProviderID,
		FetchProviderID:  r.FetchProviderID,
		Name:             r.Name,
		Description:      r.Description,
		Avatar:           r.Avatar,
		SystemPrompt:     r.SystemPrompt,
		ModelID:          r.ModelID,
		ModelParams:      r.ModelParams,
		MaxIterations:    r.MaxIterations,
		ToolsEnabled:     r.ToolsEnabled,
		SkillIDs:         r.SkillIDs,
		SubAgentIDs:      r.SubAgentIDs,
		SandboxConfig:    r.SandboxConfig,
	}
}

func bindAgentRequest(c *gin.Context) (*agentRequest, bool) {
	var req agentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respond.Error(c, http.StatusBadRequest, respond.CodeInvalid, "请求参数无效", nil)
		return nil, false
	}
	return &req, true
}

func (h *Handler) ListAgents(c *gin.Context) {
	page, ok := respond.ParsePage(c)
	if !ok {
		return
	}
	items, next, err := h.svc.List(auth.OrgID(c), page.Cursor, page.Limit)
	if err != nil {
		fail(c, err)
		return
	}
	respond.PageJSON(c, items, next)
}

func (h *Handler) ListWorkspaceAgents(c *gin.Context) {
	items, err := h.svc.ListPublished(auth.OrgID(c))
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

func (h *Handler) CreateAgent(c *gin.Context) {
	req, ok := bindAgentRequest(c)
	if !ok {
		return
	}
	detail, err := h.svc.Create(auth.UID(c), auth.OrgID(c), req.input())
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusCreated, detail)
}

func (h *Handler) GetAgent(c *gin.Context) {
	detail, err := h.svc.Get(auth.OrgID(c), c.Param("id"))
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, detail)
}

func (h *Handler) UpdateAgent(c *gin.Context) {
	req, ok := bindAgentRequest(c)
	if !ok {
		return
	}
	detail, err := h.svc.Update(auth.UID(c), auth.OrgID(c), c.Param("id"), req.input())
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, detail)
}

func (h *Handler) PublishAgent(c *gin.Context) {
	detail, err := h.svc.Publish(auth.UID(c), auth.OrgID(c), c.Param("id"))
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, detail)
}

func (h *Handler) ArchiveAgent(c *gin.Context) {
	detail, err := h.svc.Archive(auth.UID(c), auth.OrgID(c), c.Param("id"))
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, detail)
}

func (h *Handler) DeleteAgent(c *gin.Context) {
	if err := h.svc.Delete(auth.UID(c), auth.OrgID(c), c.Param("id")); err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}
