package runtime

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/hex"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/ravenmk2/wispjar/internal/auth"
	"github.com/ravenmk2/wispjar/internal/gateway/respond"
	"github.com/ravenmk2/wispjar/internal/id"
)

// SetPreviewStore is a startup dependency override for shared storage.
func (h *Handler) SetPreviewStore(store PreviewStore) { h.previews = store }

// RFC 4648 Base32 alphabet in lowercase; 16 random bytes encode to 26
// characters only when padding is explicitly disabled (the default adds six =).
var previewTokenEncoding = base32.NewEncoding("abcdefghijklmnopqrstuvwxyz234567").WithPadding(base32.NoPadding)

func newPreviewToken() (string, error) {
	var key [16]byte
	if _, err := rand.Read(key[:]); err != nil {
		return "", err
	}
	return previewTokenEncoding.EncodeToString(key[:]), nil
}

func validPreviewToken(token string) bool {
	if len(token) != 26 {
		return false
	}
	key, err := previewTokenEncoding.DecodeString(token)
	// Re-encoding rejects nonzero unused bits and noncanonical spellings.
	return err == nil && len(key) == 16 && previewTokenEncoding.EncodeToString(key) == token
}

func previewHash(token string) string {
	hash := sha256.Sum256([]byte(token))
	return hex.EncodeToString(hash[:])
}

func previewError(c *gin.Context, err error) {
	if errors.Is(err, ErrPreviewBusy) {
		c.Header("Retry-After", "1")
		respond.Error(c, 429, "file/preview_busy", "预览资源请求过多或已达到限额，请刷新预览", nil)
	} else if errors.Is(err, ErrPreviewNotFound) {
		respond.Error(c, 404, "file/preview_not_found", "预览不存在或已失效，请刷新预览", nil)
	} else {
		fail(c, err)
	}
}

func (h *Handler) CreatePreview(c *gin.Context) {
	c.Header("Cache-Control", "private, no-store")
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16*1024)
	var req struct {
		Scope string `json:"scope"`
		Root  string `json:"root"`
		Entry string `json:"entry"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Entry == "" {
		respond.Error(c, 400, respond.CodeInvalid, "scope、root 和 entry 参数无效", nil)
		return
	}
	root, ok := requestedRelative(c, req.Root)
	if !ok {
		return
	}
	entry, ok := requestedRelative(c, req.Entry)
	if !ok {
		return
	}
	ext := strings.ToLower(path.Ext(entry))
	if ext != ".html" && ext != ".htm" && ext != ".md" && ext != ".markdown" {
		respond.Error(c, 400, respond.CodeInvalid, "预览入口只支持 HTML 或 Markdown", nil)
		return
	}
	access, err := h.requestFileAccess(c, req.Scope)
	if err != nil {
		fail(c, err)
		return
	}
	defer access.close()
	// Creating a grant needs metadata only; the browser reads the entry once.
	result, err := access.operate(c.Request.Context(), access.root, path.Join(root, entry), "stat", nil)
	if err != nil {
		fail(c, err)
		return
	}
	if result.Kind != "file" {
		respond.Error(c, 400, "file/invalid", "预览入口必须是普通文件", nil)
		return
	}
	if result.Bytes > previewFileLimit {
		respond.Error(c, 413, "file/too_large", "预览入口超过 8 MiB", nil)
		return
	}
	token, err := newPreviewToken()
	if err != nil {
		fail(c, err)
		return
	}
	scope := req.Scope
	if scope == "" {
		scope = "workspace"
		if c.Param("orgId") != "" {
			scope = "storage"
		}
	}
	grant := PreviewGrant{ID: id.New(), TokenHash: previewHash(token), UserID: auth.UID(c), OrgID: access.session.OrgID,
		SessionID: access.session.ID, ComputerID: *access.session.ComputerID, AgentID: access.session.AgentID,
		Scope: scope, Root: root, Generation: access.generation, ExpiresAt: time.Now().Add(previewTTL), Space: requestFileSpace(c, scope)}
	if err = h.previews.Create(c.Request.Context(), grant); err != nil {
		previewError(c, err)
		return
	}
	base := "/api/v1/previews/" + token + "/files/"
	segments := strings.Split(entry, "/")
	for i := range segments {
		segments[i] = urlPathEscape(segments[i])
	}
	c.JSON(201, gin.H{"id": grant.ID, "entry_url": base + strings.Join(segments, "/"), "files_url": base, "expires_at": grant.ExpiresAt})
}

// PreviewOwner is used only by the resource-route account-state middleware.
func (h *Handler) PreviewOwner(ctx context.Context, token string) string {
	if !validPreviewToken(token) {
		return ""
	}
	grant, err := h.previews.ByTokenHash(ctx, previewHash(token))
	if err != nil {
		return ""
	}
	return grant.UserID
}

func (h *Handler) RevokePreview(c *gin.Context) {
	c.Header("Cache-Control", "private, no-store")
	resource := c.Param("id")
	if c.Param("orgId") != "" {
		// ResolveOrg has already rechecked membership; no computer is started.
		resource = "org:" + c.Param("orgId")
	} else {
		if _, err := h.sessions.Get(c.Request.Context(), auth.UID(c), resource); err != nil {
			fail(c, err)
			return
		}
	}
	err := h.previews.Revoke(c.Request.Context(), c.Param("previewId"), auth.UID(c), resource)
	// Idempotent cleanup of expired/revoked grants without revealing ownership.
	if err != nil && !errors.Is(err, ErrPreviewNotFound) {
		previewError(c, err)
		return
	}
	c.JSON(200, gin.H{"ok": true})
}

func (h *Handler) PreviewContent(c *gin.Context) {
	c.Header("Cache-Control", "private, no-store")
	c.Header("Referrer-Policy", "no-referrer")
	c.Header("X-Content-Type-Options", "nosniff")
	// Opaque-origin sandbox fonts/modules/fetch need CORS. The random token is
	// the sole resource credential; cookies and Authorization are never enabled.
	c.Header("Access-Control-Allow-Origin", "*")
	relative, ok := requestedRelative(c, strings.TrimPrefix(c.Param("path"), "/"))
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()
	select {
	case h.previewSlots <- struct{}{}:
		defer func() { <-h.previewSlots }()
	case <-ctx.Done():
		fail(c, ctx.Err())
		return
	}
	hash := previewHash(c.Param("token"))
	grant, err := h.previews.BeginRead(ctx, hash)
	if err != nil {
		previewError(c, err)
		return
	}
	finished := false
	defer func() {
		if !finished {
			_ = h.previews.FinishRead(context.WithoutCancel(ctx), hash, 0)
		}
	}()
	// Check binding and generation BEFORE EnsureRunning: stale tokens must not
	// start a different computer or a rebuilt instance.
	if grant.Space == nil {
		sess, err := h.sessions.Get(ctx, grant.UserID, grant.SessionID)
		if err != nil || sess.ComputerID == nil || *sess.ComputerID != grant.ComputerID || sess.OrgID != grant.OrgID || sess.AgentID != grant.AgentID {
			previewError(c, ErrPreviewNotFound)
			return
		}
	} else if _, err := h.resolveFileSpace(ctx, grant.UserID, *grant.Space); err != nil {
		previewError(c, ErrPreviewNotFound)
		return
	}
	computer, err := h.computers.GetOwned(ctx, grant.OrgID, grant.UserID, grant.ComputerID)
	if err != nil || computer.Generation != grant.Generation {
		previewError(c, ErrPreviewNotFound)
		return
	}
	var access *fileAccess
	if grant.Space != nil {
		access, err = h.openSpaceAccess(ctx, grant.UserID, *grant.Space)
	} else {
		access, err = h.openFileAccess(ctx, grant.UserID, grant.SessionID, grant.Scope)
	}
	if err != nil {
		fail(c, err)
		return
	}
	defer access.close()
	if access.generation != grant.Generation || access.session.ComputerID == nil || *access.session.ComputerID != grant.ComputerID || access.session.AgentID != grant.AgentID {
		previewError(c, ErrPreviewNotFound)
		return
	}
	op := "read"
	if c.Request.Method == http.MethodHead {
		op = "stat"
	}
	result, err := access.operate(ctx, access.root, path.Join(grant.Root, relative), op, map[string]any{"max_bytes": previewFileLimit})
	if err != nil {
		fail(c, err)
		return
	}
	if result.Kind != "file" {
		respond.Error(c, 400, "file/invalid", "只能预览普通文件", nil)
		return
	}
	if result.Truncated || result.Bytes > previewFileLimit {
		respond.Error(c, 413, "file/too_large", "预览资源超过 8 MiB，请下载查看", nil)
		return
	}
	data, err := fileContent(result)
	if err != nil {
		fail(c, err)
		return
	}
	cost := int64(len(data))
	if c.Request.Method == http.MethodHead {
		cost = 0
	}
	err = h.previews.FinishRead(context.WithoutCancel(ctx), hash, cost)
	finished = true
	if err != nil {
		previewError(c, err)
		return
	}
	if c.Request.Method == http.MethodHead {
		writePreviewHeaders(c, relative, result.Bytes)
		c.Status(200)
		return
	}
	writePreviewContent(c, relative, data)
}

func writePreviewHeaders(c *gin.Context, relative string, size int64) {
	previewSecurityHeaders(c)
	c.Header("Content-Length", fmt.Sprint(size))
	contentType := previewMIME(relative)
	c.Header("Content-Type", contentType)
	if contentType == "application/octet-stream" {
		c.Header("Content-Disposition", "attachment")
	}
}

func writePreviewContent(c *gin.Context, relative string, data []byte) {
	writePreviewHeaders(c, relative, int64(len(data)))
	if c.Request.Method == http.MethodHead {
		c.Status(200)
		return
	}
	c.Data(200, previewMIME(relative), data)
}

func previewMIME(name string) string {
	switch strings.ToLower(path.Ext(name)) {
	case ".html", ".htm":
		return "text/html; charset=utf-8"
	case ".md", ".markdown", ".txt":
		return "text/plain; charset=utf-8"
	case ".css":
		return "text/css; charset=utf-8"
	case ".js", ".mjs":
		return "text/javascript; charset=utf-8"
	case ".svg":
		return "image/svg+xml"
	case ".json", ".map":
		return "application/json"
	case ".woff":
		return "font/woff"
	case ".woff2":
		return "font/woff2"
	case ".ttf":
		return "font/ttf"
	case ".otf":
		return "font/otf"
	}
	if value := mime.TypeByExtension(strings.ToLower(path.Ext(name))); value != "" {
		return value
	}
	return "application/octet-stream"
}

func previewSecurityHeaders(c *gin.Context) {
	scheme := "http"
	if c.Request.TLS != nil || c.GetHeader("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	base := scheme + "://" + c.Request.Host + "/api/v1/previews/" + c.Param("token") + "/files/"
	c.Header("Content-Security-Policy", "sandbox allow-scripts; default-src 'none'; "+
		"script-src "+base+" 'unsafe-inline'; style-src "+base+" 'unsafe-inline'; "+
		"img-src "+base+" data: blob:; font-src "+base+" data:; media-src "+base+" blob:; "+
		"connect-src "+base+"; frame-src 'none'; object-src 'none'; base-uri 'none'; form-action 'none'; frame-ancestors 'self'")
}
