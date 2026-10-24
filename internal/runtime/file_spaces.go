package runtime

import (
	"context"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/ravenmk2/wispjar/internal/auth"
	"github.com/ravenmk2/wispjar/internal/session"
	"github.com/ravenmk2/wispjar/internal/store"
)

// FileSpace names a user's logical directory independently of its access computer.
// It is retained on preview/download capabilities and reauthorized on every read.
type FileSpace struct {
	OrgID, ComputerID, Scope, TargetID string
}

type FileSpaceResolver interface {
	Resolve(context.Context, string, FileSpace) (*session.Detail, error)
	Agents(context.Context, string, string) ([]FileAgent, error)
}

type FileAgent struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
}

type fileSpaceRepository struct{ db *gorm.DB }

func (h *Handler) SetFileSpaces(db *gorm.DB) { h.fileSpaces = &fileSpaceRepository{db: db} }

func (r *fileSpaceRepository) member(ctx context.Context, uid, org string) error {
	var count int64
	err := r.db.WithContext(ctx).Model(&store.OrgMember{}).
		Joins("JOIN organization ON organization.id = org_member.org_id AND organization.status = ?", "active").
		Where("org_member.org_id = ? AND org_member.user_id = ?", org, uid).Count(&count).Error
	if err != nil {
		return err
	}
	if count == 0 {
		return &session.Error{Status: 404, Code: "file/space_not_found", Message: "文件空间不存在或无权访问"}
	}
	return nil
}

func (r *fileSpaceRepository) agentQuery(ctx context.Context, uid, org string) *gorm.DB {
	// Archived agents remain discoverable when the user has a retained session.
	used := r.db.Model(&store.Session{}).Select("agent_id").Where("org_id = ? AND user_id = ? AND status <> ?", org, uid, "deleted")
	return r.db.WithContext(ctx).Model(&store.Agent{}).Where("org_id = ? AND (status = ? OR id IN (?))", org, "published", used)
}

func (r *fileSpaceRepository) Agents(ctx context.Context, uid, org string) ([]FileAgent, error) {
	if err := r.member(ctx, uid, org); err != nil {
		return nil, err
	}
	rows := []FileAgent{}
	err := r.agentQuery(ctx, uid, org).Select("id, name, status").Order("name, id").Scan(&rows).Error
	return rows, err
}

func (r *fileSpaceRepository) Resolve(ctx context.Context, uid string, space FileSpace) (*session.Detail, error) {
	if err := r.member(ctx, uid, space.OrgID); err != nil {
		return nil, err
	}
	// This is file context only, never a persisted or automatically created session.
	detail := &session.Detail{Session: store.Session{OrgID: space.OrgID, UserID: uid}}
	var err error
	switch space.Scope {
	case "storage":
		if space.TargetID != "" {
			return nil, &session.Error{Status: 400, Code: "request/invalid", Message: "个人存储不接受目标 ID"}
		}
	case "agent":
		var a store.Agent
		err = r.agentQuery(ctx, uid, space.OrgID).Where("id = ?", space.TargetID).First(&a).Error
		detail.AgentID = a.ID
	case "workspace":
		err = r.db.WithContext(ctx).Where("id = ? AND org_id = ? AND user_id = ? AND status <> ?", space.TargetID, space.OrgID, uid, "deleted").First(&detail.Session).Error
	default:
		return nil, &session.Error{Status: 400, Code: "request/invalid", Message: "scope 只支持 storage、agent、workspace"}
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, &session.Error{Status: 404, Code: "file/space_not_found", Message: "文件空间不存在或无权访问"}
	}
	if err != nil {
		return nil, err
	}
	return detail, nil
}

func requestFileSpace(c *gin.Context, scope string) *FileSpace {
	if c.Param("orgId") == "" {
		return nil
	}
	if scope == "" {
		scope = "storage"
	}
	return &FileSpace{OrgID: c.Param("orgId"), ComputerID: c.Query("computer_id"), Scope: scope, TargetID: c.Query("target_id")}
}

func (h *Handler) resolveFileSpace(ctx context.Context, uid string, space FileSpace) (*session.Detail, error) {
	if h.fileSpaces == nil || h.computers == nil {
		return nil, &session.Error{Status: 503, Code: "computer/unavailable", Message: "文件服务不可用"}
	}
	detail, err := h.fileSpaces.Resolve(ctx, uid, space)
	if err != nil {
		return nil, err
	}
	if space.ComputerID == "" {
		return nil, &session.Error{Status: 400, Code: "request/invalid", Message: "请选择访问电脑"}
	}
	if _, err = h.computers.GetOwned(ctx, space.OrgID, uid, space.ComputerID); err != nil {
		return nil, err
	}
	copy := *detail
	copy.ComputerID = &space.ComputerID
	return &copy, nil
}

func (h *Handler) fileIdentity(c *gin.Context, scope string) (*session.Detail, *FileSpace, error) {
	space := requestFileSpace(c, scope)
	if space != nil {
		detail, err := h.resolveFileSpace(c.Request.Context(), auth.UID(c), *space)
		return detail, space, err
	}
	detail, err := h.sessions.Get(c.Request.Context(), auth.UID(c), c.Param("id"))
	return detail, nil, err
}

func (h *Handler) requestFileAccess(c *gin.Context, scope string) (*fileAccess, error) {
	if space := requestFileSpace(c, scope); space != nil {
		return h.openSpaceAccess(c.Request.Context(), auth.UID(c), *space)
	}
	return h.openFileAccess(c.Request.Context(), auth.UID(c), c.Param("id"), scope)
}

func (h *Handler) openSpaceAccess(ctx context.Context, uid string, space FileSpace) (*fileAccess, error) {
	detail, err := h.resolveFileSpace(ctx, uid, space)
	if err != nil {
		return nil, err
	}
	root, err := fileRoot(detail, space.Scope)
	if err != nil {
		return nil, err
	}
	computer, err := h.computers.EnsureRunning(ctx, space.OrgID, uid, space.ComputerID)
	if err != nil {
		return nil, err
	}
	// This access is independent of the target session's computer binding.
	runID, err := h.computers.AcquireUse(ctx, space.OrgID, uid, computer.ID, "file-browser", computer.Generation)
	if err != nil {
		return nil, err
	}
	instance, err := h.computers.Handle(ctx, space.OrgID, uid, computer.ID)
	if err != nil {
		_ = h.computers.FinishRun(context.Background(), runID, uid, "failed")
		return nil, err
	}
	return &fileAccess{handler: h, instance: instance, session: detail, root: root, runID: runID, generation: computer.Generation}, nil
}

func (h *Handler) FileAgents(c *gin.Context) {
	rows, err := h.fileSpaces.Agents(c.Request.Context(), auth.UID(c), auth.OrgID(c))
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(200, gin.H{"items": rows})
}

func (h *Handler) CreateDirectory(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16*1024)
	var req struct {
		Path string `json:"path"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, &session.Error{Status: 400, Code: "request/invalid", Message: "目录参数无效"})
		return
	}
	relative, ok := requestedRelative(c, req.Path)
	if !ok {
		return
	}
	if relative == "." {
		fail(c, &session.Error{Status: 400, Code: "request/invalid", Message: "请输入新目录名称"})
		return
	}
	h.withFiles(c, func(_ *session.Detail, operate scopedFileOperation, root string) {
		result, err := operate(root, relative, "mkdir", nil)
		if err != nil {
			fail(c, err)
			return
		}
		c.JSON(201, result)
	})
}
