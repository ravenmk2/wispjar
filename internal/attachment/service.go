package attachment

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/ravenmk2/wispjar/internal/gateway/respond"
	"github.com/ravenmk2/wispjar/internal/session"
	"github.com/ravenmk2/wispjar/internal/store"
)

const (
	CodeInvalid  = "attachment/invalid"
	codeNotFound = "attachment/not_found"

	// MaxPerMessage 每条消息最多携带的图片数。
	MaxPerMessage = 9

	idPrefix = "sha256:"
)

// Error 是附件域的语义化错误（handlers 映射为统一错误信封）。
type Error struct {
	Status  int
	Code    string
	Message string
	Details any
}

func (e *Error) Error() string { return e.Message }

func errInvalid(msg string, details any) *Error {
	return &Error{Status: http.StatusUnprocessableEntity, Code: CodeInvalid, Message: msg, Details: details}
}

func errNotFound(msg string) *Error {
	return &Error{Status: http.StatusNotFound, Code: codeNotFound, Message: msg}
}

// errBadRef 是 prompt 附件校验失败（api.md 语义族：参数类问题 400 request/invalid）。
func errBadRef(msg string, details any) *Error {
	return &Error{Status: http.StatusBadRequest, Code: respond.CodeInvalid, Message: msg, Details: details}
}

var idPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

type Service struct {
	db    *gorm.DB
	blobs *BlobStore

	// loadRowsFn 覆盖按 id 批量查行（测试注入用；生产为 nil 走 gorm）。
	loadRowsFn func(ctx context.Context, ids []string) ([]store.Attachment, error)
}

func NewService(db *gorm.DB, dataDir string) *Service {
	return &Service{
		db:    db,
		blobs: NewBlobStore(filepath.Join(dataDir, "attachments", "v1", "objects")),
	}
}

func refOf(row *store.Attachment) session.AttachmentRef {
	return session.AttachmentRef{
		ID: row.ID, MIME: row.MIME, Bytes: row.Bytes,
		Width: row.Width, Height: row.Height, Name: row.Name,
	}
}

// Upload 校验图片 → 写内容寻址存储 → 插表（同内容命中去重，幂等返回既有行）。
func (s *Service) Upload(ctx context.Context, orgID, actorID, name string, data []byte) (*session.AttachmentRef, error) {
	mime, w, h, err := validateImage(data)
	if err != nil {
		return nil, errInvalid(err.Error(), map[string]any{"size": len(data)})
	}
	name = strings.TrimSpace(filepath.Base(name))
	if utf8.RuneCountInString(name) > 256 {
		name = string([]rune(name)[:256])
	}
	hexSum, err := s.blobs.Put(data)
	if err != nil {
		return nil, err
	}
	row := store.Attachment{
		ID: idPrefix + hexSum, OrgID: orgID, MIME: mime, Bytes: int64(len(data)),
		Width: w, Height: h, Name: name, CreatedBy: actorID,
	}
	// 内容寻址全局去重：已存在（可能由他人/他机构上传同字节）时保留首发行。
	if err := s.db.WithContext(ctx).
		Clauses(clause.OnConflict{DoNothing: true}).
		Create(&row).Error; err != nil {
		return nil, err
	}
	var existing store.Attachment
	if err := s.db.WithContext(ctx).First(&existing, "id = ?", row.ID).Error; err != nil {
		return nil, err
	}
	ref := refOf(&existing)
	return &ref, nil
}

// Resolve 校验 prompt 携带的附件引用：格式、存在性、机构一致、数量上限；按输入序返回引用。
func (s *Service) Resolve(ctx context.Context, orgID string, ids []string) ([]session.AttachmentRef, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	if len(ids) > MaxPerMessage {
		return nil, errBadRef("每条消息最多携带 9 张图片", map[string]any{"count": len(ids)})
	}
	for _, id := range ids {
		if !idPattern.MatchString(id) {
			return nil, errBadRef("附件 id 格式无效（应为 sha256:<64 hex>）", map[string]any{"id": id})
		}
	}
	rows, err := s.loadRows(ctx, ids)
	if err != nil {
		return nil, err
	}
	byID := map[string]store.Attachment{}
	for _, r := range rows {
		byID[r.ID] = r
	}
	refs := make([]session.AttachmentRef, 0, len(ids))
	for _, id := range ids {
		row, ok := byID[id]
		if !ok || row.OrgID != orgID {
			return nil, errBadRef("附件不存在或无权访问", map[string]any{"id": id})
		}
		refs = append(refs, refOf(&row))
	}
	return refs, nil
}

func (s *Service) loadRows(ctx context.Context, ids []string) ([]store.Attachment, error) {
	if s.loadRowsFn != nil {
		return s.loadRowsFn(ctx, ids)
	}
	rows := []store.Attachment{}
	err := s.db.WithContext(ctx).Where("id IN ?", ids).Find(&rows).Error
	return rows, err
}

// Row 按 id 取附件行（下载鉴权用）。
func (s *Service) Row(ctx context.Context, id string) (*store.Attachment, error) {
	if !idPattern.MatchString(id) {
		return nil, errNotFound("附件不存在")
	}
	var row store.Attachment
	if err := s.db.WithContext(ctx).First(&row, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errNotFound("附件不存在")
		}
		return nil, err
	}
	return &row, nil
}

// IsMember 判断用户是否为指定机构成员（下载鉴权用）。
func (s *Service) IsMember(ctx context.Context, orgID, uid string) (bool, error) {
	var n int64
	if err := s.db.WithContext(ctx).Model(&store.OrgMember{}).
		Where("org_id = ? AND user_id = ?", orgID, uid).
		Count(&n).Error; err != nil {
		return false, err
	}
	return n > 0, nil
}

// ReadBytes 按引用读 blob 字节（读时校验 sha256；orchestrator 组装模型输入用）。
func (s *Service) ReadBytes(ctx context.Context, id string) ([]byte, error) {
	if !idPattern.MatchString(id) {
		return nil, errNotFound("附件不存在")
	}
	return s.blobs.Get(strings.TrimPrefix(id, idPrefix))
}
