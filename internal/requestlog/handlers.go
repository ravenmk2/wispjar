package requestlog

import (
	"errors"
	"net/http"
	"os"
	"regexp"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/ravenmk2/wispjar/internal/auth"
	"github.com/ravenmk2/wispjar/internal/gateway/respond"
)

var idPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

// Register must only be called on the platform-admin route group.
func (s *Service) Register(group *gin.RouterGroup) {
	group.GET("/request-logs/config", s.getConfig)
	group.PUT("/request-logs/config", s.putConfig)
	group.PUT("/request-logs/models/:id", s.putModel)
	group.GET("/request-logs", s.list)
	group.GET("/request-logs/:id/payload", s.payload)
	group.DELETE("/request-logs", s.clearLogs)
}
func fail(c *gin.Context, err error) {
	status := http.StatusInternalServerError
	message := "请求日志操作失败"
	if errors.Is(err, gorm.ErrRecordNotFound) || errors.Is(err, os.ErrNotExist) {
		status = http.StatusNotFound
		message = "请求日志或文件不存在"
	}
	respond.Error(c, status, respond.CodeInternal, message, nil)
}
func (s *Service) getConfig(c *gin.Context) {
	snap := s.config.Load()
	c.JSON(http.StatusOK, gin.H{"config": snap.Config, "models": snap.Models, "stats": gin.H{
		"pending_bytes": s.pending.Load(), "queue_length": len(s.queue), "saved": s.saved.Load(), "dropped_full": s.droppedFull.Load(), "dropped_large": s.droppedLarge.Load(), "dropped_incomplete": s.droppedIncomplete.Load(), "failures": s.failures.Load(), "clearing": s.clearing.Load(), "max_payload_bytes": maxPayload, "max_pending_bytes": maxPending,
	}})
}
func (s *Service) putConfig(c *gin.Context) {
	var cfg Config
	if err := c.ShouldBindJSON(&cfg); err != nil {
		respond.Error(c, 400, respond.CodeInvalid, "配置格式无效", nil)
		return
	}
	if err := validateConfig(cfg); err != nil {
		respond.Error(c, 400, respond.CodeInvalid, err.Error(), nil)
		return
	}
	if err := s.SetConfig(c.Request.Context(), cfg, auth.UID(c)); err != nil {
		fail(c, err)
		return
	}
	s.getConfig(c)
}
func (s *Service) putModel(c *gin.Context) {
	var body struct {
		Enabled *bool `json:"enabled"`
	}
	if !idPattern.MatchString(c.Param("id")) || c.ShouldBindJSON(&body) != nil || body.Enabled == nil {
		respond.Error(c, 400, respond.CodeInvalid, "模型开关格式无效", nil)
		return
	}
	if err := s.SetModel(c.Request.Context(), c.Param("id"), *body.Enabled); err != nil {
		fail(c, err)
		return
	}
	s.getConfig(c)
}
func (s *Service) list(c *gin.Context) {
	query := s.db.WithContext(c.Request.Context()).Model(&Record{})
	if model := c.Query("model_id"); model != "" {
		query = query.Where("model_id = ?", model)
	}
	if before := c.Query("before"); before != "" {
		if !idPattern.MatchString(before) {
			respond.Error(c, 400, respond.CodeInvalid, "分页游标无效", nil)
			return
		}
		query = query.Where("id < ?", before)
	}
	var rows []Record
	if err := query.Order("id DESC").Limit(51).Find(&rows).Error; err != nil {
		fail(c, err)
		return
	}
	next := ""
	if len(rows) > 50 {
		rows = rows[:50]
		next = rows[49].ID
	}
	if rows == nil {
		rows = []Record{}
	}
	c.JSON(http.StatusOK, gin.H{"items": rows, "next_cursor": next})
}
func (s *Service) payload(c *gin.Context) {
	if !idPattern.MatchString(c.Param("id")) {
		respond.Error(c, 400, respond.CodeInvalid, "日志 ID 无效", nil)
		return
	}
	var row Record
	if err := s.db.WithContext(c.Request.Context()).First(&row, "id = ?", c.Param("id")).Error; err != nil {
		fail(c, err)
		return
	}
	if row.Status != "ready" {
		respond.Error(c, 409, respond.CodeInvalid, "正文尚未保存完成或保存失败", nil)
		return
	}
	path, err := s.path(row.PayloadPath)
	if err != nil {
		fail(c, err)
		return
	}
	file, err := os.Open(path)
	if err != nil {
		fail(c, err)
		return
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		fail(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Content-Disposition", `attachment; filename="`+row.ID+`.json"`)
	c.DataFromReader(http.StatusOK, info.Size(), "application/json", file, nil)
}
func (s *Service) clearLogs(c *gin.Context) {
	if s.clearing.CompareAndSwap(false, true) {
		s.clear <- time.Now()
	}
	c.JSON(http.StatusAccepted, gin.H{"ok": true, "message": "已安排后台清理，清理开始后的新请求保留"})
}
