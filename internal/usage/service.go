package usage

import (
	"context"
	"net/http"
	"time"

	"gorm.io/gorm"

	"github.com/ravenmk2/wispjar/internal/gateway/respond"
	"github.com/ravenmk2/wispjar/internal/store"
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

const (
	GroupByOrg   = "org"
	GroupByUser  = "user"
	GroupByAgent = "agent"
	GroupByModel = "model"
	GroupByDay   = "day"
)

type reportQuery struct {
	from     string
	to       string
	groupBy  string
	orgID    string
	platform bool
}

func parseReportQuery(from, to, groupBy, orgID string, platform bool) (reportQuery, error) {
	if from == "" || to == "" {
		return reportQuery{}, errInvalid("from/to 必填（YYYY-MM-DD）", map[string]any{"from": from, "to": to})
	}
	fd, err := time.ParseInLocation("2006-01-02", from, reportTZ)
	if err != nil {
		return reportQuery{}, errInvalid("from 必须是 YYYY-MM-DD 格式", map[string]any{"from": from})
	}
	td, err := time.ParseInLocation("2006-01-02", to, reportTZ)
	if err != nil {
		return reportQuery{}, errInvalid("to 必须是 YYYY-MM-DD 格式", map[string]any{"to": to})
	}
	if fd.After(td) {
		return reportQuery{}, errInvalid("from 不能晚于 to", map[string]any{"from": from, "to": to})
	}
	if _, err := groupKeyExpr(groupBy); err != nil {
		return reportQuery{}, err
	}
	if groupBy == GroupByOrg && !platform {
		return reportQuery{}, errInvalid("组织用量不支持 group_by=org", map[string]any{"group_by": groupBy})
	}
	return reportQuery{from: from, to: to, groupBy: groupBy, orgID: orgID, platform: platform}, nil
}

func groupKeyExpr(groupBy string) (string, error) {
	switch groupBy {
	case GroupByOrg:
		return "org_id", nil
	case GroupByUser:
		return "user_id", nil
	case GroupByAgent:
		return "agent_id", nil
	case GroupByModel:
		return "model_id", nil
	case GroupByDay:
		return "DATE_FORMAT(date, '%Y-%m-%d')", nil
	default:
		return "", errInvalid("group_by 仅支持 org|user|agent|model|day", map[string]any{"group_by": groupBy})
	}
}

type Item struct {
	Key              string `json:"key"`
	Requests         int64  `json:"requests"`
	InputTokens      int64  `json:"input_tokens"`
	OutputTokens     int64  `json:"output_tokens"`
	CacheReadTokens  int64  `json:"cache_read_tokens"`
	CacheWriteTokens int64  `json:"cache_write_tokens"`
	ReasoningTokens  int64  `json:"reasoning_tokens"`
	TotalTokens      int64  `json:"total_tokens"`
}

type Totals struct {
	Requests         int64 `json:"requests"`
	InputTokens      int64 `json:"input_tokens"`
	OutputTokens     int64 `json:"output_tokens"`
	CacheReadTokens  int64 `json:"cache_read_tokens"`
	CacheWriteTokens int64 `json:"cache_write_tokens"`
	ReasoningTokens  int64 `json:"reasoning_tokens"`
	TotalTokens      int64 `json:"total_tokens"`
}

type Report struct {
	Items  []Item            `json:"items"`
	Totals Totals            `json:"totals"`
	Names  map[string]string `json:"names"`
}

type Service struct {
	db *gorm.DB
}

func NewService(db *gorm.DB) *Service {
	return &Service{db: db}
}

func (s *Service) Report(ctx context.Context, from, to, groupBy, orgID string, platform bool) (*Report, error) {
	q, err := parseReportQuery(from, to, groupBy, orgID, platform)
	if err != nil {
		return nil, err
	}
	keyExpr, _ := groupKeyExpr(q.groupBy)
	rows := []struct {
		Grp              string `gorm:"column:grp"`
		Requests         int64  `gorm:"column:requests"`
		InputTokens      int64  `gorm:"column:input_tokens"`
		OutputTokens     int64  `gorm:"column:output_tokens"`
		CacheReadTokens  int64  `gorm:"column:cache_read_tokens"`
		CacheWriteTokens int64  `gorm:"column:cache_write_tokens"`
		ReasoningTokens  int64  `gorm:"column:reasoning_tokens"`
	}{}
	dbq := s.db.WithContext(ctx).Model(&store.UsageDaily{}).
		Select(keyExpr+` AS grp, SUM(requests) AS requests, SUM(input_tokens) AS input_tokens,`+
			` SUM(output_tokens) AS output_tokens, SUM(cache_read_tokens) AS cache_read_tokens,`+
			` SUM(cache_write_tokens) AS cache_write_tokens, SUM(reasoning_tokens) AS reasoning_tokens`).
		Where("date >= ? AND date <= ?", q.from, q.to).
		Group(keyExpr).
		Order("grp")
	if !q.platform {
		dbq = dbq.Where("org_id = ?", q.orgID)
	}
	if err := dbq.Scan(&rows).Error; err != nil {
		return nil, err
	}
	report := &Report{Items: make([]Item, 0, len(rows)), Names: map[string]string{}}
	for _, r := range rows {
		item := Item{
			Key:              r.Grp,
			Requests:         r.Requests,
			InputTokens:      r.InputTokens,
			OutputTokens:     r.OutputTokens,
			CacheReadTokens:  r.CacheReadTokens,
			CacheWriteTokens: r.CacheWriteTokens,
			ReasoningTokens:  r.ReasoningTokens,
			TotalTokens:      r.InputTokens + r.OutputTokens,
		}
		report.Items = append(report.Items, item)
		report.Totals.Requests += item.Requests
		report.Totals.InputTokens += item.InputTokens
		report.Totals.OutputTokens += item.OutputTokens
		report.Totals.CacheReadTokens += item.CacheReadTokens
		report.Totals.CacheWriteTokens += item.CacheWriteTokens
		report.Totals.ReasoningTokens += item.ReasoningTokens
		report.Totals.TotalTokens += item.TotalTokens
	}
	if err := s.fillNames(ctx, q.groupBy, report); err != nil {
		return nil, err
	}
	return report, nil
}

func (s *Service) fillNames(ctx context.Context, groupBy string, report *Report) error {
	if groupBy == GroupByDay || len(report.Items) == 0 {
		return nil
	}
	keys := make([]string, 0, len(report.Items))
	for _, item := range report.Items {
		keys = append(keys, item.Key)
	}
	switch groupBy {
	case GroupByUser:
		users := []store.User{}
		if err := s.db.WithContext(ctx).Select("id", "display_name").Where("id IN ?", keys).Find(&users).Error; err != nil {
			return err
		}
		for _, u := range users {
			report.Names[u.ID] = u.DisplayName
		}
	case GroupByAgent:
		agents := []store.Agent{}
		if err := s.db.WithContext(ctx).Select("id", "name").Where("id IN ?", keys).Find(&agents).Error; err != nil {
			return err
		}
		for _, a := range agents {
			report.Names[a.ID] = a.Name
		}
	case GroupByModel:
		models := []store.Model{}
		if err := s.db.WithContext(ctx).Select("id", "display_name").Where("id IN ?", keys).Find(&models).Error; err != nil {
			return err
		}
		for _, m := range models {
			report.Names[m.ID] = m.DisplayName
		}
	case GroupByOrg:
		orgs := []store.Organization{}
		if err := s.db.WithContext(ctx).Select("id", "name").Where("id IN ?", keys).Find(&orgs).Error; err != nil {
			return err
		}
		for _, o := range orgs {
			report.Names[o.ID] = o.Name
		}
	}
	return nil
}
