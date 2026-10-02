package respond

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
)

const (
	CodeInvalid                = "request/invalid"
	CodeUnauthorized           = "auth/unauthorized"
	CodeCredentials            = "auth/credentials"
	CodePasswordChangeRequired = "auth/password_change_required"
	CodeForbidden              = "auth/forbidden"
	CodeNotMember              = "tenant/not_member"
	CodeNotFound               = "request/not_found"
	CodeConflict               = "request/conflict"
	CodeRateLimited            = "auth/rate_limited"
	CodeInternal               = "internal"
)

func Error(c *gin.Context, status int, code, message string, details any) {
	if details == nil {
		details = gin.H{}
	}
	c.AbortWithStatusJSON(status, gin.H{"error": gin.H{
		"code":    code,
		"message": message,
		"details": details,
	}})
}

type Page struct {
	Cursor string
	Limit  int
}

func ParsePage(c *gin.Context) (Page, bool) {
	p := Page{Cursor: c.Query("cursor"), Limit: 20}
	if s := c.Query("limit"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 || n > 100 {
			Error(c, http.StatusBadRequest, CodeInvalid, "limit 必须是 1..100 的整数", gin.H{"limit": s})
			return p, false
		}
		p.Limit = n
	}
	return p, true
}

func PageJSON(c *gin.Context, items any, nextCursor string) {
	c.JSON(http.StatusOK, gin.H{"items": items, "next_cursor": nextCursor})
}
