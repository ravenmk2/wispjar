package session

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"path"
	"strings"

	"github.com/ravenmk2/wispjar/internal/computer"
	"github.com/ravenmk2/wispjar/internal/sandbox"
	"github.com/ravenmk2/wispjar/internal/store"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ComputerBindings resolves user-owned computers without exposing lifecycle SDKs.
type ComputerBindings interface {
	ResolveBinding(context.Context, string, string, string, string) (store.Computer, error)
}

func lockSessionComputer(tx *gorm.DB, sess *store.Session) error {
	if sess.ComputerID == nil {
		return nil
	}
	var target store.Computer
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND org_id = ? AND user_id = ? AND status NOT IN ?", *sess.ComputerID, sess.OrgID, sess.UserID, []string{"deleting", "deleted"}).Take(&target).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ComputerError(computer.ErrNotFound)
	}
	return err
}

type ComputerSummary struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Status     string `json:"status"`
	Generation uint64 `json:"generation"`
}

func (s *Service) detail(ctx context.Context, sess *store.Session, started bool) (*Detail, error) {
	d := detailOf(sess, started)
	if sess.ComputerID != nil {
		if reader, ok := s.computers.(interface {
			GetOwned(context.Context, string, string, string) (store.Computer, error)
		}); ok {
			c, err := reader.GetOwned(ctx, sess.OrgID, sess.UserID, *sess.ComputerID)
			if err != nil {
				return nil, ComputerError(err)
			}
			d.Computer = &ComputerSummary{ID: c.ID, Name: c.Name, Status: c.Status, Generation: c.Generation}
		}
	}
	return d, nil
}

func (s *Service) SetComputers(computers ComputerBindings) {
	s.computers = computers
}

func WorkspaceDir(sessionID string) string { return path.Join("/sessions", sessionID) }

func newWorkspace() json.RawMessage {
	return json.RawMessage(`{"state":"pending","layout_version":1}`)
}

func (s *Service) createComputer(ctx context.Context, sess *store.Session, explicit string) error {
	if s.computers == nil {
		return &Error{Status: http.StatusServiceUnavailable, Code: "computer/unavailable", Message: "云电脑服务不可用"}
	}
	c, err := s.computers.ResolveBinding(ctx, sess.OrgID, sess.UserID, sess.AgentID, strings.TrimSpace(explicit))
	if err != nil {
		return ComputerError(err)
	}
	sess.ComputerID, sess.Workspace = &c.ID, newWorkspace()
	return nil
}

func (s *Service) planComputerUpdate(ctx context.Context, sess *store.Session, target string) (map[string]any, []EventInput, error) {
	if sess.ComputerID == nil {
		return nil, nil, &Error{Status: http.StatusConflict, Code: "computer/required", Message: "会话必须绑定云电脑"}
	}
	if sess.Running {
		return nil, nil, &Error{Status: http.StatusConflict, Code: "computer/busy", Message: "会话有活动任务"}
	}
	var active int64
	if err := s.db.WithContext(ctx).Model(&store.ComputerRun{}).
		Where("session_id = ? AND status NOT IN ?", sess.ID, []string{"completed", "failed", "canceled"}).Count(&active).Error; err != nil {
		return nil, nil, err
	}
	if active > 0 {
		return nil, nil, &Error{Status: http.StatusConflict, Code: "computer/busy", Message: "会话仍有未结束的远程执行"}
	}
	if s.computers == nil || strings.TrimSpace(target) == "" {
		return nil, nil, errInvalid("computer_id 必须指定可用云电脑", nil)
	}
	c, err := s.computers.ResolveBinding(ctx, sess.OrgID, sess.UserID, sess.AgentID, target)
	if err != nil {
		return nil, nil, ComputerError(err)
	}
	return map[string]any{"computer_id": c.ID}, []EventInput{{Type: "session/computer", Data: map[string]any{"computer_id": c.ID, "workspace_dir": WorkspaceDir(sess.ID)}}}, nil
}

func ComputerError(err error) error {
	for _, item := range []struct {
		err     error
		status  int
		message string
	}{
		{computer.ErrRequired, 409, "请选择或创建云电脑"},
		{computer.ErrNotFound, 404, "云电脑不存在或无权访问"},
		{computer.ErrBusy, 409, "云电脑有活动任务"},
		{computer.ErrInUse, 409, "云电脑仍被引用"},
		{computer.ErrUnavailable, 503, "云电脑服务不可用"},
		{computer.ErrConflict, 409, "资源操作冲突"},
		{computer.ErrInvalid, 400, "云电脑参数无效"},
		{sandbox.ErrComputerInsecure, 409, "云电脑需要重建以启用安全访问"},
	} {
		if errors.Is(err, item.err) {
			return &Error{Status: item.status, Code: item.err.Error(), Message: item.message}
		}
	}
	return err
}
