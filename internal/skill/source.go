package skill

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ravenmk2/wispjar/internal/audit"
	"github.com/ravenmk2/wispjar/internal/id"
	"github.com/ravenmk2/wispjar/internal/store"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// File is the validated content installed in a sandbox. Source zip files and
// hidden staging material are never part of a runnable skill.
type File struct {
	Path   string `json:"path"`
	Data   []byte `json:"data"`
	SHA256 string `json:"sha256"`
}

func contentFiles(root string, names []string) ([]File, string, error) {
	names = append([]string(nil), names...)
	sort.Strings(names)
	files := make([]File, 0, len(names))
	manifest := sha256.New()
	for _, name := range names {
		if name == "_source.zip" {
			continue
		}
		rel, err := cleanRelPath(name)
		if err != nil {
			return nil, "", err
		}
		full := filepath.Join(root, filepath.FromSlash(rel))
		info, err := os.Lstat(full)
		if err != nil {
			return nil, "", err
		}
		if !info.Mode().IsRegular() {
			return nil, "", fmt.Errorf("skill source contains a non-regular file")
		}
		data, err := os.ReadFile(full)
		if err != nil {
			return nil, "", err
		}
		sum := sha256.Sum256(data)
		hash := hex.EncodeToString(sum[:])
		files = append(files, File{Path: rel, Data: data, SHA256: hash})
		manifest.Write([]byte(rel))
		manifest.Write([]byte{0})
		manifest.Write([]byte(hash))
		manifest.Write([]byte{'\n'})
	}
	return files, hex.EncodeToString(manifest.Sum(nil)), nil
}

func readPackageFile(fh *multipart.FileHeader) ([]byte, error) {
	if fh == nil {
		return nil, errInvalid("缺少上传文件（multipart 字段 file）", nil)
	}
	if fh.Size > MaxZipBytes {
		return nil, errInvalidSkill("zip 大小超过 20MB 限制", nil)
	}
	src, err := fh.Open()
	if err != nil {
		return nil, err
	}
	defer src.Close()
	data, err := io.ReadAll(io.LimitReader(src, MaxZipBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > MaxZipBytes {
		return nil, errInvalidSkill("zip 大小超过 20MB 限制", nil)
	}
	return data, nil
}

func (s *Service) ReplacePackage(ctx context.Context, actorID, orgID, skillID string, expectedRevision uint64, fh *multipart.FileHeader) (*ListItem, error) {
	if expectedRevision == 0 {
		return nil, errInvalid("expected_revision 必须大于 0", nil)
	}
	return s.publishPackage(ctx, actorID, orgID, skillID, expectedRevision, fh)
}

func (s *Service) publishPackage(ctx context.Context, actorID, orgID, skillID string, expected uint64, fh *multipart.FileHeader) (result *ListItem, err error) {
	if !validID(orgID) || (skillID != "" && !validID(skillID)) {
		return nil, errInvalid("无效的技能或机构 ID", nil)
	}
	data, err := readPackageFile(fh)
	if err != nil {
		return nil, err
	}
	if s.dataDir == "" {
		return nil, fmt.Errorf("storage.data_dir 未配置")
	}
	create := skillID == ""
	if create {
		skillID = id.New()
	}
	// Candidates live on the same filesystem. Only the committed StoragePath
	// is addressable through the API; candidates are not selectable versions.
	candidateRoot := filepath.Join(s.dataDir, "skills", orgID, ".sources", skillID)
	if err = os.MkdirAll(candidateRoot, 0700); err != nil {
		return nil, err
	}
	err = withDBLock(ctx, s.db, "source:"+orgID+":"+skillID, func() error {
		stage, err := os.MkdirTemp(candidateRoot, "candidate-")
		if err != nil {
			return err
		}
		pkg, err := ExtractZip(data, stage)
		if err != nil {
			_ = os.RemoveAll(stage)
			var invalid *InvalidError
			if errors.As(err, &invalid) {
				return errInvalidSkill(invalid.Error(), nil)
			}
			return err
		}
		for _, name := range pkg.Files {
			if name == "_source.zip" {
				return errInvalidSkill("_source.zip 是平台保留文件名", nil)
			}
		}
		meta, err := buildMeta(pkg)
		if err != nil {
			return err
		}
		_, hash, err := contentFiles(pkg.RootDir, pkg.Files)
		if err != nil {
			return err
		}
		if err = os.WriteFile(filepath.Join(pkg.RootDir, "_source.zip"), data, 0600); err != nil {
			return err
		}
		var row store.Skill
		err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if create {
				var count int64
				if err := tx.Model(&store.Skill{}).Where("org_id = ? AND name = ?", orgID, pkg.Frontmatter.Name).Count(&count).Error; err != nil {
					return err
				}
				if count > 0 {
					return errConflict("技能名称已存在", nil)
				}
				row = store.Skill{ID: skillID, OrgID: orgID, Name: pkg.Frontmatter.Name, CreatedBy: actorID, Status: StatusActive, Revision: 1}
			} else {
				if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND org_id = ?", skillID, orgID).Take(&row).Error; err != nil {
					return err
				}
				if row.Revision != expected {
					return errConflict("技能已更新，请刷新后重试", map[string]any{"revision": row.Revision})
				}
				if row.Name != pkg.Frontmatter.Name {
					return errInvalidSkill("替换包不能改变技能名称", nil)
				}
				row.Revision++
			}
			row.Description = pkg.Frontmatter.Description
			row.Meta = meta
			row.ZipSize = int64(len(data))
			row.StoragePath = pkg.RootDir
			row.ContentHash = hash
			if create {
				if err := tx.Create(&row).Error; err != nil {
					return err
				}
			} else {
				if err := tx.Save(&row).Error; err != nil {
					return err
				}
				// The desired revision is the barrier. Existing lease holders
				// remain valid; new runs cannot acquire the outdated install.
				if err := tx.Model(&store.UserSkillState{}).Where("org_id = ? AND skill_id = ?", orgID, skillID).
					Updates(map[string]any{"desired_revision": row.Revision}).Error; err != nil {
					return err
				}
			}
			action := audit.SkillUpload
			if !create {
				action = "skill.replace_package"
			}
			return audit.Log(tx, audit.Entry{OrgID: orgID, ActorID: actorID, Action: action, Target: skillID, Detail: map[string]any{"revision": row.Revision, "content_hash": hash}})
		})
		if err != nil {
			// Never delete a candidate after an ambiguous transaction commit.
			// RecoverSources re-reads the committed pointer under this lock.
			return err
		}
		item := makeListItem(row, map[string][]AgentRef{}, map[string]string{})
		result = &item
		return s.cleanCandidates(ctx, orgID, skillID, candidateRoot)
	})
	return result, err
}

// RecoverSources removes only unreferenced hidden candidates, after resolving
// the durable current pointer under the same cross-process publication lock.
// It is safe after an unknown SQL commit outcome. It never changes the row.
func (s *Service) RecoverSources(ctx context.Context, orgID, skillID string) error {
	if !validID(orgID) || !validID(skillID) {
		return errInvalid("无效的技能或机构 ID", nil)
	}
	return withDBLock(ctx, s.db, "source:"+orgID+":"+skillID, func() error {
		return s.cleanCandidates(ctx, orgID, skillID, filepath.Join(s.dataDir, "skills", orgID, ".sources", skillID))
	})
}

// RecoverAllSources finds even candidates left by an interrupted first upload
// before a skill row existed. It performs no publication and ignores unrelated
// names. Call explicitly on startup/maintenance with the shared source store.
func (s *Service) RecoverAllSources(ctx context.Context) error {
	root := filepath.Join(s.dataDir, "skills")
	orgs, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, org := range orgs {
		if !org.IsDir() || !validID(org.Name()) {
			continue
		}
		skills, err := os.ReadDir(filepath.Join(root, org.Name(), ".sources"))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		for _, skill := range skills {
			if !skill.IsDir() || !validID(skill.Name()) {
				continue
			}
			if err := s.RecoverSources(ctx, org.Name(), skill.Name()); err != nil {
				return err
			}
		}
	}
	return nil
}
func (s *Service) cleanCandidates(ctx context.Context, orgID, skillID, root string) error {
	var row store.Skill
	err := s.db.WithContext(ctx).Where("id = ? AND org_id = ?", skillID, orgID).Take(&row).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	entries, err := os.ReadDir(root)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		candidate := filepath.Join(root, entry.Name())
		rel, relErr := filepath.Rel(candidate, row.StoragePath)
		if row.StoragePath != "" && relErr == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
			continue
		}
		if err := os.RemoveAll(candidate); err != nil {
			return err
		}
	}
	// Older installations used the stable skill ID as the source directory.
	// Once the committed pointer moved into candidates it is no longer live.
	legacy := filepath.Join(s.dataDir, "skills", orgID, skillID)
	if row.StoragePath != legacy {
		if err := os.RemoveAll(legacy); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) sourceSnapshot(ctx context.Context, orgID, skillID string) (row store.Skill, files []File, err error) {
	err = withDBLock(ctx, s.db, "source:"+orgID+":"+skillID, func() error {
		if err := s.db.WithContext(ctx).Where("id = ? AND org_id = ?", skillID, orgID).Take(&row).Error; err != nil {
			return err
		}
		names, err := listFiles(row.StoragePath)
		if err != nil {
			return err
		}
		var hash string
		files, hash, err = contentFiles(row.StoragePath, names)
		if err != nil {
			return err
		}
		if row.ContentHash != "" && row.ContentHash != hash {
			return syncError("skill/sync_failed", "技能源包摘要不匹配")
		}
		if row.ContentHash == "" {
			if err := s.db.WithContext(ctx).Model(&store.Skill{}).Where("id = ? AND revision = ?", row.ID, row.Revision).Update("content_hash", hash).Error; err != nil {
				return err
			}
			row.ContentHash = hash
		}
		return nil
	})
	return
}
