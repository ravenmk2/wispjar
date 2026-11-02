package requestlog

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"gorm.io/gorm/clause"
)

var payloadPattern = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}/[0-9a-f]{32}\.json$`)

func (s *Service) path(relative string) (string, error) {
	if !payloadPattern.MatchString(relative) {
		return "", errors.New("invalid payload path")
	}
	return filepath.Join(s.root, filepath.FromSlash(relative)), nil
}

func (s *Service) persist(e entry) error {
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		if err = s.persistOnce(e); err == nil {
			return nil
		}
		if s.closed.Load() {
			break
		}
	}
	return err
}

func (s *Service) persistOnce(e entry) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	row := e.record
	row.Status = "writing"
	row.PayloadBytes = int64(len(e.body))
	sum := sha256.Sum256(e.body)
	row.PayloadSHA256 = hex.EncodeToString(sum[:])
	var existing []Record
	if err := s.db.WithContext(ctx).Where("id = ?", row.ID).Limit(1).Find(&existing).Error; err != nil {
		return err
	}
	if len(existing) > 0 && existing[0].Status == "ready" {
		return nil
	}
	incoming := row.PayloadBytes
	if len(existing) > 0 {
		incoming -= existing[0].PayloadBytes
	}
	if err := s.enforceCapacity(ctx, incoming); err != nil {
		return err
	}
	if err := s.db.WithContext(ctx).Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "id"}}, DoUpdates: clause.AssignmentColumns([]string{"status", "payload_bytes", "payload_sha256", "updated_at"})}).Create(&row).Error; err != nil {
		return err
	}
	path, err := s.path(row.PayloadPath)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0700); err == nil {
		err = os.WriteFile(path+".tmp", e.body, 0600)
	}
	if err == nil {
		err = os.Rename(path+".tmp", path)
	}
	if err != nil {
		_ = os.Remove(path + ".tmp")
		detail, _ := json.Marshal(map[string]string{"message": "请求正文保存失败"})
		_ = s.db.WithContext(ctx).Model(&row).Updates(map[string]any{"status": "failed", "error_detail": detail, "payload_bytes": 0}).Error
		return err
	}
	return s.db.WithContext(ctx).Model(&row).Updates(map[string]any{"status": "ready", "error_detail": nil}).Error
}

func (s *Service) deleteRecord(ctx context.Context, row Record) error {
	if err := s.db.WithContext(ctx).Model(&row).Update("status", "deleting").Error; err != nil {
		return err
	}
	path, err := s.path(row.PayloadPath)
	if err != nil {
		return err
	}
	for _, p := range []string{path, path + ".tmp"} {
		if err = os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if err = s.db.WithContext(ctx).Delete(&row).Error; err != nil {
		return err
	}
	_ = os.Remove(filepath.Dir(path)) // only removes an empty date directory
	return nil
}

func (s *Service) markAndDelete(query string, args ...any) error {
	markCtx, markCancel := context.WithTimeout(context.Background(), 10*time.Second)
	markErr := s.db.WithContext(markCtx).Model(&Record{}).Where(query, args...).Update("status", "deleting").Error
	markCancel()
	if markErr != nil {
		return markErr
	}
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		var rows []Record
		err := s.db.WithContext(ctx).Where("status = ?", "deleting").Order("started_at,id").Limit(100).Find(&rows).Error
		if err == nil {
			for _, row := range rows {
				if err = s.deleteRecord(ctx, row); err != nil {
					break
				}
			}
		}
		cancel()
		if err != nil {
			return err
		}
		if len(rows) < 100 {
			return nil
		}
	}
}

func (s *Service) enforceCapacity(ctx context.Context, incoming int64) error {
	var used int64
	if err := s.db.WithContext(ctx).Model(&Record{}).Select("COALESCE(SUM(payload_bytes),0)").Scan(&used).Error; err != nil {
		return err
	}
	limit := s.config.Load().MaxStorageBytes
	if incoming > limit {
		return errors.New("payload exceeds storage capacity")
	}
	for used+incoming > limit {
		var row Record
		if err := s.db.WithContext(ctx).Order("started_at,id").First(&row).Error; err != nil {
			return err
		}
		if err := s.deleteRecord(ctx, row); err != nil {
			return err
		}
		used -= row.PayloadBytes
	}
	return nil
}

func (s *Service) maintenance(recoverPending bool) {
	if recoverPending {
		if err := s.recoverFiles(); err != nil {
			s.failures.Add(1)
			log.Printf("requestlog: recovery: %v", err)
		}
	}
	cutoff := time.Now().Add(-time.Duration(s.config.Load().RetentionDays) * 24 * time.Hour)
	if err := s.markAndDelete("started_at < ? OR status = ?", cutoff, "deleting"); err != nil {
		s.failures.Add(1)
		log.Printf("requestlog: cleanup: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := s.enforceCapacity(ctx, 0); err != nil {
		s.failures.Add(1)
		log.Printf("requestlog: capacity: %v", err)
	}
	if err := s.sweepOrphans(); err != nil {
		s.failures.Add(1)
		log.Printf("requestlog: orphan cleanup: %v", err)
	}
}

func (s *Service) recoverFiles() error {
	var cursor string
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		var rows []Record
		err := s.db.WithContext(ctx).Where("status = ? AND id > ?", "writing", cursor).Order("id").Limit(100).Find(&rows).Error
		if err == nil {
			for _, row := range rows {
				cursor = row.ID
				path, pathErr := s.path(row.PayloadPath)
				if pathErr != nil {
					err = pathErr
					break
				}
				data, readErr := os.ReadFile(path)
				sum := sha256.Sum256(data)
				status := "failed"
				detail := json.RawMessage(`{"message":"上次退出时请求正文未完整保存"}`)
				if readErr == nil && int64(len(data)) == row.PayloadBytes && hex.EncodeToString(sum[:]) == row.PayloadSHA256 {
					status = "ready"
					detail = nil
				}
				if err = s.db.WithContext(ctx).Model(&row).Updates(map[string]any{"status": status, "error_detail": detail}).Error; err != nil {
					break
				}
			}
		}
		cancel()
		if err != nil {
			return err
		}
		if len(rows) < 100 {
			return nil
		}
	}
}

func (s *Service) sweepOrphans() error {
	return filepath.WalkDir(s.root, func(path string, d fs.DirEntry, walkErr error) error {
		if errors.Is(walkErr, os.ErrNotExist) {
			return nil
		}
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if time.Since(info.ModTime()) < time.Hour {
			return nil
		}
		relative, err := filepath.Rel(s.root, path)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		if !payloadPattern.MatchString(strings.TrimSuffix(relative, ".tmp")) {
			return nil
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var count int64
		if err = s.db.WithContext(ctx).Model(&Record{}).Where("payload_path = ?", strings.TrimSuffix(relative, ".tmp")).Count(&count).Error; err != nil {
			return err
		}
		if count == 0 || strings.HasSuffix(relative, ".tmp") {
			if err = os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("remove orphan: %w", err)
			}
		}
		return nil
	})
}
