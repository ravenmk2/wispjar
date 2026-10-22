package auth

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/ravenmk2/wispjar/internal/gateway/respond"
	"github.com/ravenmk2/wispjar/internal/id"
	"github.com/ravenmk2/wispjar/internal/store"
)

const (
	ctxUID  = "wispjar.uid"
	ctxOrg  = "wispjar.org_id"
	ctxRole = "wispjar.role"
)

type Service struct {
	db      *gorm.DB
	secret  []byte
	ttl     time.Duration
	limiter *loginLimiter
}

func NewService(db *gorm.DB, jwtSecret string, tokenTTL time.Duration) *Service {
	return &Service{
		db:      db,
		secret:  []byte(jwtSecret),
		ttl:     tokenTTL,
		limiter: newLoginLimiter(),
	}
}

func UID(c *gin.Context) string   { return c.GetString(ctxUID) }
func OrgID(c *gin.Context) string { return c.GetString(ctxOrg) }
func Role(c *gin.Context) string  { return c.GetString(ctxRole) }

func (s *Service) AuthRequired() gin.HandlerFunc {
	return func(c *gin.Context) {
		token, ok := strings.CutPrefix(c.GetHeader("Authorization"), "Bearer ")
		if !ok || token == "" {
			respond.Error(c, http.StatusUnauthorized, respond.CodeUnauthorized, "缺少或无效的 Authorization 头", nil)
			return
		}
		uid, err := ParseToken(s.secret, token)
		if err != nil {
			respond.Error(c, http.StatusUnauthorized, respond.CodeUnauthorized, "登录状态已失效，请重新登录", nil)
			return
		}
		var user store.User
		if err := s.db.First(&user, "id = ?", uid).Error; err != nil || user.Status != "active" {
			respond.Error(c, http.StatusUnauthorized, respond.CodeUnauthorized, "登录状态已失效，请重新登录", nil)
			return
		}
		if user.MustChangePassword && c.Request.URL.Path != "/api/v1/auth/change-password" {
			respond.Error(c, http.StatusUnauthorized, respond.CodePasswordChangeRequired, "首次登录需先修改密码", nil)
			return
		}
		c.Set(ctxUID, user.ID)
		c.Next()
	}
}

// DownloadRequired accepts only a narrowly scoped, expiring download capability
// and still checks the owner's current account state. It never accepts a JWT in
// a query string or changes normal API authentication.
func (s *Service) DownloadRequired(owner func(string) string) gin.HandlerFunc {
	return func(c *gin.Context) {
		uid := owner(c.Param("downloadId"))
		var user store.User
		if uid == "" || s.db.WithContext(c.Request.Context()).First(&user, "id = ?", uid).Error != nil || user.Status != "active" || user.MustChangePassword {
			respond.Error(c, http.StatusNotFound, "file/download_not_found", "下载不存在或已失效", nil)
			return
		}
		c.Set(ctxUID, uid)
		c.Next()
	}
}

func (s *Service) ResolveOrg() gin.HandlerFunc {
	return func(c *gin.Context) {
		orgID := c.Param("orgId")
		var member store.OrgMember
		if err := s.db.Where("org_id = ? AND user_id = ?", orgID, UID(c)).First(&member).Error; err != nil {
			respond.Error(c, http.StatusForbidden, respond.CodeNotMember, "不是该机构成员", nil)
			return
		}
		c.Set(ctxOrg, orgID)
		c.Set(ctxRole, member.Role)
		c.Next()
	}
}

func RequireOrgAdmin() gin.HandlerFunc {
	return func(c *gin.Context) {
		if Role(c) != "admin" {
			respond.Error(c, http.StatusForbidden, respond.CodeForbidden, "需要机构管理员权限", gin.H{"required": "admin"})
			return
		}
		c.Next()
	}
}

func (s *Service) RequirePlatformAdmin() gin.HandlerFunc {
	return func(c *gin.Context) {
		var count int64
		if err := s.db.Model(&store.OrgMember{}).
			Where("org_id = ? AND user_id = ? AND role = ?", id.PlatformOrgID, UID(c), "admin").
			Count(&count).Error; err != nil || count == 0 {
			respond.Error(c, http.StatusForbidden, respond.CodeForbidden, "需要平台管理员权限", gin.H{"required": "platform admin"})
			return
		}
		c.Next()
	}
}
