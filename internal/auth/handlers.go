package auth

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/ravenmk2/wispjar/internal/gateway/respond"
	"github.com/ravenmk2/wispjar/internal/store"
)

type userEntry struct {
	ID                 string `json:"id"`
	Username           string `json:"username"`
	DisplayName        string `json:"display_name"`
	MustChangePassword bool   `json:"must_change_password"`
}

type orgEntry struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Role       string `json:"role"`
	IsPlatform bool   `json:"is_platform"`
}

func toUserEntry(u store.User) userEntry {
	return userEntry{
		ID:                 u.ID,
		Username:           u.Username,
		DisplayName:        u.DisplayName,
		MustChangePassword: u.MustChangePassword,
	}
}

func (s *Service) orgsOf(uid string) ([]orgEntry, error) {
	orgs := []orgEntry{}
	err := s.db.Model(&store.OrgMember{}).
		Select("organization.id, organization.name, org_member.role, organization.is_platform").
		Joins("JOIN organization ON organization.id = org_member.org_id").
		Where("org_member.user_id = ?", uid).
		Order("organization.created_at").
		Scan(&orgs).Error
	return orgs, err
}

func (s *Service) sign(userID string) (string, error) {
	return SignToken(s.secret, userID, s.ttl)
}

func (s *Service) Login(c *gin.Context) {
	var req struct {
		Username string `json:"username" binding:"required"`
		Password string `json:"password" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		respond.Error(c, http.StatusBadRequest, respond.CodeInvalid, "请求参数无效", nil)
		return
	}
	key := c.ClientIP() + "\x00" + req.Username
	if !s.limiter.allow(key) {
		respond.Error(c, http.StatusTooManyRequests, respond.CodeRateLimited, "登录尝试过多，请 5 分钟后再试", nil)
		return
	}
	var user store.User
	err := s.db.Where("username = ?", req.Username).First(&user).Error
	if err != nil || user.Status != "active" || !CheckPassword(user.PasswordHash, req.Password) {
		s.limiter.fail(key)
		respond.Error(c, http.StatusUnauthorized, respond.CodeCredentials, "用户名或密码错误", nil)
		return
	}
	s.limiter.success(key)
	token, err := s.sign(user.ID)
	if err != nil {
		respond.Error(c, http.StatusInternalServerError, respond.CodeInternal, "签发令牌失败", nil)
		return
	}
	orgs, err := s.orgsOf(user.ID)
	if err != nil {
		respond.Error(c, http.StatusInternalServerError, respond.CodeInternal, "查询机构失败", nil)
		return
	}
	c.JSON(http.StatusOK, gin.H{"token": token, "user": toUserEntry(user), "orgs": orgs})
}

func (s *Service) ChangePassword(c *gin.Context) {
	var req struct {
		OldPassword string `json:"old_password" binding:"required"`
		NewPassword string `json:"new_password" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		respond.Error(c, http.StatusBadRequest, respond.CodeInvalid, "请求参数无效", nil)
		return
	}
	var user store.User
	if err := s.db.First(&user, "id = ?", UID(c)).Error; err != nil {
		respond.Error(c, http.StatusUnauthorized, respond.CodeUnauthorized, "登录状态已失效，请重新登录", nil)
		return
	}
	if !CheckPassword(user.PasswordHash, req.OldPassword) {
		respond.Error(c, http.StatusUnauthorized, respond.CodeCredentials, "原密码错误", nil)
		return
	}
	if !ValidateNewPassword(req.NewPassword) {
		respond.Error(c, http.StatusBadRequest, respond.CodeInvalid, "新密码至少 8 位且包含字母和数字",
			gin.H{"new_password": "至少 8 位，包含字母和数字"})
		return
	}
	hash, err := HashPassword(req.NewPassword)
	if err != nil {
		respond.Error(c, http.StatusInternalServerError, respond.CodeInternal, "密码哈希失败", nil)
		return
	}
	if err := s.db.Model(&user).Updates(map[string]any{
		"password_hash":        hash,
		"must_change_password": false,
	}).Error; err != nil {
		respond.Error(c, http.StatusInternalServerError, respond.CodeInternal, "更新密码失败", nil)
		return
	}
	token, err := s.sign(user.ID)
	if err != nil {
		respond.Error(c, http.StatusInternalServerError, respond.CodeInternal, "签发令牌失败", nil)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "token": token})
}

func (s *Service) Me(c *gin.Context) {
	var user store.User
	if err := s.db.First(&user, "id = ?", UID(c)).Error; err != nil {
		respond.Error(c, http.StatusUnauthorized, respond.CodeUnauthorized, "登录状态已失效，请重新登录", nil)
		return
	}
	orgs, err := s.orgsOf(user.ID)
	if err != nil {
		respond.Error(c, http.StatusInternalServerError, respond.CodeInternal, "查询机构失败", nil)
		return
	}
	c.JSON(http.StatusOK, gin.H{"user": toUserEntry(user), "orgs": orgs})
}
