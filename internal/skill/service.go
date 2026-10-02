package skill

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"gorm.io/gorm"

	"github.com/ravenmk2/wispjar/internal/audit"
	"github.com/ravenmk2/wispjar/internal/gateway/respond"
	"github.com/ravenmk2/wispjar/internal/id"
	"github.com/ravenmk2/wispjar/internal/store"
)

const (
	codeSkillInvalid = "skill/invalid"
	maxViewBytes     = 256 * 1024
	StatusActive     = "active"
)

type Error struct {
	Status  int
	Code    string
	Message string
	Details any
}

func (e *Error) Error() string { return e.Message }

func errInvalid(msg string, details any) *Error {
	return &Error{Status: http.StatusBadRequest, Code: respond.CodeInvalid, Message: msg, Details: details}
}

func errInvalidSkill(msg string, details any) *Error {
	return &Error{Status: http.StatusUnprocessableEntity, Code: codeSkillInvalid, Message: msg, Details: details}
}

func errNotFound(msg string) *Error {
	return &Error{Status: http.StatusNotFound, Code: respond.CodeNotFound, Message: msg}
}

func errConflict(msg string, details any) *Error {
	return &Error{Status: http.StatusConflict, Code: respond.CodeConflict, Message: msg, Details: details}
}

type Service struct {
	db      *gorm.DB
	dataDir string
}

func NewService(db *gorm.DB, dataDir string) *Service {
	return &Service{db: db, dataDir: dataDir}
}

func (s *Service) skillOf(orgID, skillID string) (*store.Skill, error) {
	var sk store.Skill
	if err := s.db.Where("id = ? AND org_id = ?", skillID, orgID).First(&sk).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errNotFound("Skill 不存在")
		}
		return nil, err
	}
	return &sk, nil
}

type AgentRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func (s *Service) referencedBy(skillIDs []string) (map[string][]AgentRef, error) {
	refs := map[string][]AgentRef{}
	if len(skillIDs) == 0 {
		return refs, nil
	}
	rows := []struct {
		SkillID string `gorm:"column:skill_id"`
		ID      string `gorm:"column:id"`
		Name    string `gorm:"column:name"`
	}{}
	err := s.db.Model(&store.AgentSkill{}).
		Select("agent_skill.skill_id, agent.id, agent.name").
		Joins("JOIN agent ON agent.id = agent_skill.agent_id").
		Where("agent_skill.skill_id IN ?", skillIDs).
		Order("agent_skill.sort").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		refs[r.SkillID] = append(refs[r.SkillID], AgentRef{ID: r.ID, Name: r.Name})
	}
	return refs, nil
}

func buildMeta(pkg *Package) (json.RawMessage, error) {
	meta := map[string]any{}
	for k, v := range pkg.RawMeta {
		meta[k] = v
	}
	meta["files"] = pkg.Files
	return json.Marshal(meta)
}

func (s *Service) Upload(actorID, orgID string, fh *multipart.FileHeader) (*ListItem, error) {
	if fh == nil {
		return nil, errInvalid("缺少上传文件（multipart 字段 file）", map[string]any{"field": "file"})
	}
	if fh.Size > MaxZipBytes {
		return nil, errInvalidSkill(fmt.Sprintf("zip 大小 %d 字节超过 20MB 限制", fh.Size), map[string]any{"size": fh.Size})
	}
	src, err := fh.Open()
	if err != nil {
		return nil, err
	}
	defer func() { _ = src.Close() }()
	data, err := io.ReadAll(io.LimitReader(src, MaxZipBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > MaxZipBytes {
		return nil, errInvalidSkill("zip 大小超过 20MB 限制", map[string]any{"size": len(data)})
	}
	if s.dataDir == "" {
		return nil, errors.New("storage.data_dir 未配置")
	}
	orgDir := filepath.Join(s.dataDir, "skills", orgID)
	if err := os.MkdirAll(orgDir, 0o755); err != nil {
		return nil, err
	}
	staging, err := os.MkdirTemp(orgDir, ".staging-*")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(staging) }()
	pkg, err := ExtractZip(data, staging)
	if err != nil {
		var ie *InvalidError
		if errors.As(err, &ie) {
			return nil, errInvalidSkill(ie.Error(), nil)
		}
		return nil, err
	}
	var cnt int64
	if err := s.db.Model(&store.Skill{}).
		Where("org_id = ? AND name = ?", orgID, pkg.Frontmatter.Name).
		Count(&cnt).Error; err != nil {
		return nil, err
	}
	if cnt > 0 {
		return nil, errConflict(fmt.Sprintf("skill 名称 %q 在本机构已存在", pkg.Frontmatter.Name), map[string]any{"name": pkg.Frontmatter.Name})
	}
	meta, err := buildMeta(pkg)
	if err != nil {
		return nil, err
	}
	row := store.Skill{
		ID:          id.New(),
		OrgID:       orgID,
		Name:        pkg.Frontmatter.Name,
		Description: pkg.Frontmatter.Description,
		Meta:        meta,
		ZipSize:     int64(len(data)),
		Status:      StatusActive,
		CreatedBy:   actorID,
	}
	dest := filepath.Join(orgDir, row.ID)
	if err := os.Rename(pkg.RootDir, dest); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(dest, "_source.zip"), data, 0o644); err != nil {
		_ = os.RemoveAll(dest)
		return nil, err
	}
	row.StoragePath = dest
	err = s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&row).Error; err != nil {
			return err
		}
		detail := map[string]any{"name": row.Name, "files": len(pkg.Files), "zip_size": row.ZipSize}
		if pkg.Deviation != "" {
			detail["dir_deviation"] = pkg.Deviation
		}
		return audit.Log(tx, audit.Entry{
			OrgID:   orgID,
			ActorID: actorID,
			Action:  audit.SkillUpload,
			Target:  row.ID,
			Detail:  detail,
		})
	})
	if err != nil {
		_ = os.RemoveAll(dest)
		return nil, err
	}
	uploaders, err := s.uploaderNames([]string{actorID})
	if err != nil {
		return nil, err
	}
	item := makeListItem(row, map[string][]AgentRef{}, uploaders)
	return &item, nil
}

type ListItem struct {
	store.Skill
	SizeBytes    int64      `json:"size_bytes"`
	UploaderName string     `json:"uploader_name"`
	ReferencedBy []AgentRef `json:"referenced_by"`
}

func (s *Service) uploaderNames(userIDs []string) (map[string]string, error) {
	names := map[string]string{}
	if len(userIDs) == 0 {
		return names, nil
	}
	users := []store.User{}
	if err := s.db.Select("id, username, display_name").Where("id IN ?", userIDs).Find(&users).Error; err != nil {
		return nil, err
	}
	for _, u := range users {
		n := u.DisplayName
		if n == "" {
			n = u.Username
		}
		names[u.ID] = n
	}
	return names, nil
}

func makeListItem(sk store.Skill, refs map[string][]AgentRef, uploaders map[string]string) ListItem {
	rb := refs[sk.ID]
	if rb == nil {
		rb = []AgentRef{}
	}
	return ListItem{Skill: sk, SizeBytes: sk.ZipSize, UploaderName: uploaders[sk.CreatedBy], ReferencedBy: rb}
}

func (s *Service) List(orgID string) ([]ListItem, error) {
	skills := []store.Skill{}
	if err := s.db.Where("org_id = ?", orgID).Order("created_at DESC").Find(&skills).Error; err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(skills))
	for _, sk := range skills {
		ids = append(ids, sk.ID)
	}
	refs, err := s.referencedBy(ids)
	if err != nil {
		return nil, err
	}
	uploaderIDs := make([]string, 0, len(skills))
	for _, sk := range skills {
		uploaderIDs = append(uploaderIDs, sk.CreatedBy)
	}
	uploaders, err := s.uploaderNames(uploaderIDs)
	if err != nil {
		return nil, err
	}
	items := make([]ListItem, 0, len(skills))
	for _, sk := range skills {
		items = append(items, makeListItem(sk, refs, uploaders))
	}
	return items, nil
}

type Detail struct {
	store.Skill
	SizeBytes    int64      `json:"size_bytes"`
	UploaderName string     `json:"uploader_name"`
	ReferencedBy []AgentRef `json:"referenced_by"`
	Files        []string   `json:"files"`
}

func (s *Service) skillDetail(sk *store.Skill) (*Detail, error) {
	refs, err := s.referencedBy([]string{sk.ID})
	if err != nil {
		return nil, err
	}
	uploaders, err := s.uploaderNames([]string{sk.CreatedBy})
	if err != nil {
		return nil, err
	}
	item := makeListItem(*sk, refs, uploaders)
	files := []string{}
	var meta map[string]any
	if err := json.Unmarshal(sk.Meta, &meta); err == nil {
		if raw, ok := meta["files"].([]any); ok {
			for _, f := range raw {
				if name, ok := f.(string); ok {
					files = append(files, name)
				}
			}
		}
	}
	return &Detail{
		Skill:        item.Skill,
		SizeBytes:    item.SizeBytes,
		UploaderName: item.UploaderName,
		ReferencedBy: item.ReferencedBy,
		Files:        files,
	}, nil
}

func (s *Service) Get(orgID, skillID string) (*Detail, error) {
	sk, err := s.skillOf(orgID, skillID)
	if err != nil {
		return nil, err
	}
	return s.skillDetail(sk)
}

var binaryExts = map[string]bool{
	".zip": true, ".gz": true, ".tar": true, ".7z": true, ".rar": true,
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true, ".ico": true, ".bmp": true,
	".pdf": true, ".exe": true, ".dll": true, ".so": true, ".dylib": true, ".bin": true,
	".woff": true, ".woff2": true, ".ttf": true, ".otf": true,
	".mp3": true, ".mp4": true, ".wav": true, ".mov": true,
	".class": true, ".jar": true, ".pyc": true, ".o": true, ".a": true,
	".sqlite": true, ".db": true,
}

func cleanRelPath(relPath string) (string, error) {
	rel := strings.TrimSpace(relPath)
	if rel == "" {
		return "", errInvalid("path 参数必填", map[string]any{"field": "path"})
	}
	if strings.ContainsRune(rel, '\\') || strings.HasPrefix(rel, "/") || path.IsAbs(rel) {
		return "", errInvalid("非法路径", map[string]any{"path": relPath})
	}
	for _, seg := range strings.Split(rel, "/") {
		if seg == ".." {
			return "", errInvalid("非法路径（路径穿越）", map[string]any{"path": relPath})
		}
	}
	return path.Clean(rel), nil
}

func (s *Service) ReadFile(orgID, skillID, relPath string) (string, string, error) {
	sk, err := s.skillOf(orgID, skillID)
	if err != nil {
		return "", "", err
	}
	rel, err := cleanRelPath(relPath)
	if err != nil {
		return "", "", err
	}
	if binaryExts[strings.ToLower(path.Ext(rel))] {
		return "", "", errInvalid("不支持查看二进制文件", map[string]any{"path": relPath})
	}
	base := filepath.Clean(sk.StoragePath)
	full := filepath.Join(base, filepath.FromSlash(rel))
	if !strings.HasPrefix(full, base+string(os.PathSeparator)) {
		return "", "", errInvalid("非法路径", map[string]any{"path": relPath})
	}
	fi, err := os.Stat(full)
	if err != nil {
		if os.IsNotExist(err) {
			return "", "", errNotFound("文件不存在")
		}
		return "", "", err
	}
	if !fi.Mode().IsRegular() {
		return "", "", errInvalid("仅支持查看常规文件", map[string]any{"path": relPath})
	}
	if fi.Size() > maxViewBytes {
		return "", "", errInvalid(fmt.Sprintf("文件超过 256KB（%d 字节），无法在线查看", fi.Size()), map[string]any{"path": relPath, "size": fi.Size()})
	}
	data, err := os.ReadFile(full)
	if err != nil {
		return "", "", err
	}
	if !utf8.Valid(data) {
		return "", "", errInvalid("不支持查看二进制文件", map[string]any{"path": relPath})
	}
	return rel, string(data), nil
}

func (s *Service) Delete(actorID, orgID, skillID string) error {
	sk, err := s.skillOf(orgID, skillID)
	if err != nil {
		return err
	}
	refs, err := s.referencedBy([]string{sk.ID})
	if err != nil {
		return err
	}
	if rb := refs[sk.ID]; len(rb) > 0 {
		return errConflict("该 Skill 被智能体引用，不可删除", map[string]any{"referenced_by": rb})
	}
	err = s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Delete(&store.Skill{}, "id = ?", sk.ID).Error; err != nil {
			return err
		}
		return audit.Log(tx, audit.Entry{
			OrgID:   orgID,
			ActorID: actorID,
			Action:  audit.SkillDelete,
			Target:  sk.ID,
			Detail:  map[string]any{"name": sk.Name},
		})
	})
	if err != nil {
		return err
	}
	_ = os.RemoveAll(sk.StoragePath)
	return nil
}
