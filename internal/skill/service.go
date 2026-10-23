package skill

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mime/multipart"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/ravenmk2/wispjar/internal/audit"
	"github.com/ravenmk2/wispjar/internal/gateway/respond"
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
	return s.publishPackage(context.Background(), actorID, orgID, "", 0, fh)
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

func (s *Service) readFile(orgID, skillID, relPath string) (string, string, error) {
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

func (s *Service) ReadFile(orgID, skillID, relPath string) (rel, content string, err error) {
	err = withDBLock(context.Background(), s.db, "source:"+orgID+":"+skillID, func() error {
		var readErr error
		rel, content, readErr = s.readFile(orgID, skillID, relPath)
		return readErr
	})
	return
}

func (s *Service) Delete(actorID, orgID, skillID string) error {
	if !validID(orgID) || !validID(skillID) {
		return errInvalid("无效的技能或组织 ID", nil)
	}
	return withDBLock(context.Background(), s.db, "source:"+orgID+":"+skillID, func() error {
		var sk store.Skill
		err := s.db.Transaction(func(tx *gorm.DB) error {
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND org_id = ?", skillID, orgID).Take(&sk).Error; err != nil {
				return err
			}
			var count int64
			if err := tx.Model(&store.AgentSkill{}).Where("skill_id = ?", skillID).Count(&count).Error; err != nil {
				return err
			}
			if count > 0 {
				return errConflict("该 Skill 被智能体引用，不可删除", nil)
			}
			if err := tx.Model(&store.RunSkillLease{}).Where("org_id = ? AND skill_id = ?", orgID, skillID).Count(&count).Error; err != nil {
				return err
			}
			if count > 0 {
				return errConflict("该 Skill 仍被运行使用，不可删除", nil)
			}
			if err := tx.Model(&store.UserSkillState{}).Where("org_id = ? AND skill_id = ? AND status IN ?", orgID, skillID, []string{"syncing", "blocked"}).Count(&count).Error; err != nil {
				return err
			}
			if count > 0 {
				return errConflict("该 Skill 的安装状态未确认，不可删除", nil)
			}
			var operations []store.ResourceOperation
			if err := tx.Where("org_id = ? AND resource_id = ? AND action = ?", orgID, skillID, "skill/sync").Order("created_at DESC, id DESC").Find(&operations).Error; err != nil {
				return err
			}
			seen := map[string]bool{}
			for _, op := range operations {
				if seen[op.UserID] {
					continue
				}
				seen[op.UserID] = true
				var plan InstallPlan
				if err := json.Unmarshal(op.Progress, &plan); err != nil {
					return err
				}
				if len(plan.Targets) > 0 || op.Status != "completed" {
					return errConflict("请先在安全任务边界同步移除已安装的技能", nil)
				}
			}
			if err := tx.Delete(&store.Skill{}, "id = ?", skillID).Error; err != nil {
				return err
			}
			return audit.Log(tx, audit.Entry{OrgID: orgID, ActorID: actorID, Action: audit.SkillDelete, Target: skillID, Detail: map[string]any{"name": sk.Name}})
		})
		if err != nil {
			return err
		}
		return s.cleanCandidates(context.Background(), orgID, skillID, filepath.Join(s.dataDir, "skills", orgID, ".sources", skillID))
	})
}
