package tenant

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
	var te *Error
	if errors.As(err, &te) {
		respond.Error(c, te.Status, te.Code, te.Message, te.Details)
		return
	}
	respond.Error(c, http.StatusInternalServerError, respond.CodeInternal, "服务器内部错误", nil)
}

func (h *Handler) ListOrgs(c *gin.Context) {
	page, ok := respond.ParsePage(c)
	if !ok {
		return
	}
	orgs, next, err := h.svc.ListOrgs(page.Cursor, page.Limit)
	if err != nil {
		fail(c, err)
		return
	}
	respond.PageJSON(c, orgs, next)
}

func (h *Handler) CreateOrg(c *gin.Context) {
	var req struct {
		Name string `json:"name" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		respond.Error(c, http.StatusBadRequest, respond.CodeInvalid, "请求参数无效", nil)
		return
	}
	org, err := h.svc.CreateOrg(auth.UID(c), req.Name)
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusCreated, org)
}

func (h *Handler) GetOrg(c *gin.Context) {
	detail, err := h.svc.GetOrg(c.Param("id"))
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, detail)
}

func (h *Handler) UpdateOrg(c *gin.Context) {
	var req struct {
		Name   *string `json:"name"`
		Status *string `json:"status"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		respond.Error(c, http.StatusBadRequest, respond.CodeInvalid, "请求参数无效", nil)
		return
	}
	org, err := h.svc.UpdateOrg(auth.UID(c), c.Param("id"), UpdateOrgInput{
		Name:   req.Name,
		Status: req.Status,
	})
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, org)
}

func (h *Handler) ListUsers(c *gin.Context) {
	page, ok := respond.ParsePage(c)
	if !ok {
		return
	}
	users, next, err := h.svc.ListUsers(c.Query("q"), page.Cursor, page.Limit)
	if err != nil {
		fail(c, err)
		return
	}
	respond.PageJSON(c, users, next)
}

func (h *Handler) CreateUser(c *gin.Context) {
	var req struct {
		Username    string   `json:"username" binding:"required"`
		DisplayName string   `json:"display_name"`
		Password    string   `json:"password"`
		OrgIDs      []string `json:"org_ids"`
		Role        string   `json:"role"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		respond.Error(c, http.StatusBadRequest, respond.CodeInvalid, "请求参数无效", nil)
		return
	}
	user, password, err := h.svc.CreateUser(auth.UID(c), CreateUserInput{
		Username:    req.Username,
		DisplayName: req.DisplayName,
		Password:    req.Password,
		OrgIDs:      req.OrgIDs,
		Role:        req.Role,
	})
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"user": user, "password": password})
}

func (h *Handler) UpdateUser(c *gin.Context) {
	var req struct {
		DisplayName   *string `json:"display_name"`
		Status        *string `json:"status"`
		ResetPassword bool    `json:"reset_password"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		respond.Error(c, http.StatusBadRequest, respond.CodeInvalid, "请求参数无效", nil)
		return
	}
	user, newPassword, err := h.svc.UpdateUser(auth.UID(c), c.Param("id"), UpdateUserInput{
		DisplayName:   req.DisplayName,
		Status:        req.Status,
		ResetPassword: req.ResetPassword,
	})
	if err != nil {
		fail(c, err)
		return
	}
	resp := gin.H{"user": user}
	if newPassword != "" {
		resp["password"] = newPassword
	}
	c.JSON(http.StatusOK, resp)
}

func (h *Handler) AddUserOrg(c *gin.Context) {
	var req struct {
		OrgID string `json:"org_id" binding:"required"`
		Role  string `json:"role" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		respond.Error(c, http.StatusBadRequest, respond.CodeInvalid, "请求参数无效", nil)
		return
	}
	member, err := h.svc.AddUserToOrg(auth.UID(c), c.Param("id"), req.OrgID, req.Role)
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusCreated, member)
}

func (h *Handler) RemoveUserOrg(c *gin.Context) {
	if err := h.svc.RemoveMember(auth.UID(c), c.Param("orgId"), c.Param("id")); err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (h *Handler) UpdateUserOrgRole(c *gin.Context) {
	var req struct {
		Role string `json:"role" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		respond.Error(c, http.StatusBadRequest, respond.CodeInvalid, "请求参数无效", nil)
		return
	}
	member, err := h.svc.UpdateMemberRole(auth.UID(c), c.Param("orgId"), c.Param("id"), req.Role)
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, member)
}

func (h *Handler) ListMembers(c *gin.Context) {
	members, err := h.svc.membersOf(auth.OrgID(c))
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": members})
}

func (h *Handler) AddMember(c *gin.Context) {
	var req struct {
		Username string `json:"username" binding:"required"`
		Role     string `json:"role" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		respond.Error(c, http.StatusBadRequest, respond.CodeInvalid, "请求参数无效", nil)
		return
	}
	member, err := h.svc.AddMemberByUsername(auth.UID(c), auth.OrgID(c), req.Username, req.Role)
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusCreated, member)
}

func (h *Handler) UpdateMemberRole(c *gin.Context) {
	var req struct {
		Role string `json:"role" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		respond.Error(c, http.StatusBadRequest, respond.CodeInvalid, "请求参数无效", nil)
		return
	}
	member, err := h.svc.UpdateMemberRole(auth.UID(c), auth.OrgID(c), c.Param("userId"), req.Role)
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, member)
}

func (h *Handler) RemoveMember(c *gin.Context) {
	if err := h.svc.RemoveMember(auth.UID(c), auth.OrgID(c), c.Param("userId")); err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}
