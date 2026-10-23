// Package search owns web search instances independently of model providers.
package search

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/ravenmk2/wispjar/internal/audit"
	"github.com/ravenmk2/wispjar/internal/gateway/respond"
	"github.com/ravenmk2/wispjar/internal/id"
	"github.com/ravenmk2/wispjar/internal/provider"
	"github.com/ravenmk2/wispjar/internal/store"
)

type Error struct {
	Status  int
	Code    string
	Message string
}

func (e *Error) Error() string     { return e.Message }
func invalid(msg string) error     { return &Error{400, respond.CodeInvalid, msg} }
func unavailable(msg string) error { return &Error{503, "search/unavailable", msg} }
func conflict(msg string) error    { return &Error{409, respond.CodeConflict, msg} }

type Service struct {
	db        *gorm.DB
	masterKey []byte
	client    *http.Client
}

func NewService(db *gorm.DB, masterKey []byte) *Service {
	return &Service{db: db, masterKey: masterKey, client: &http.Client{
		// A redirect must never forward credentials to a different endpoint.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

type ProviderView struct {
	store.SearchProvider
	MaskedKey string `json:"masked_key"`
}
type Option struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Type   string `json:"type"`
	Status string `json:"status"`
}
type Input struct {
	Name    string `json:"name"`
	Type    string `json:"type"`
	BaseURL string `json:"base_url"`
	APIKey  string `json:"api_key"`
	Status  string `json:"status"`
}

func validURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Hostname() != "" && u.User == nil
}
func validate(in Input) error {
	if in.Name == "" || utf8.RuneCountInString(in.Name) > 128 {
		return invalid("搜索实例名称必填且不超过 128 字符")
	}
	switch in.Type {
	case "deepseek", "tavily", "bocha":
	default:
		return invalid("搜索供应商类型必须是 deepseek / tavily / bocha")
	}
	u, err := url.Parse(in.BaseURL)
	if !validURL(in.BaseURL) || len(in.BaseURL) > 512 || err != nil || u.RawQuery != "" || u.Fragment != "" {
		return invalid("base_url 必须是不含凭据、查询参数和片段的 HTTP(S) 地址")
	}
	if in.Status != "active" && in.Status != "disabled" {
		return invalid("status 必须是 active / disabled")
	}
	return nil
}
func (s *Service) view(p store.SearchProvider) (*ProviderView, error) {
	key, err := provider.Decrypt(s.masterKey, p.APIKeyEnc)
	if err != nil {
		return nil, errors.New("搜索实例密钥解密失败")
	}
	return &ProviderView{p, provider.MaskKey(key)}, nil
}
func (s *Service) List(ctx context.Context) ([]ProviderView, error) {
	rows := []store.SearchProvider{}
	if err := s.db.WithContext(ctx).Order("id DESC").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]ProviderView, 0, len(rows))
	for _, p := range rows {
		v, err := s.view(p)
		if err != nil {
			return nil, err
		}
		out = append(out, *v)
	}
	return out, nil
}

// Options exposes only safe selection metadata to organization administrators.
func (s *Service) Options(ctx context.Context) ([]Option, error) {
	rows := []Option{}
	err := s.db.WithContext(ctx).Model(&store.SearchProvider{}).Select("id, name, type, status").Order("id DESC").Scan(&rows).Error
	return rows, err
}
func (s *Service) Config(ctx context.Context) (*store.SearchConfig, error) {
	var cfg store.SearchConfig
	if err := s.db.WithContext(ctx).First(&cfg, 1).Error; err != nil {
		return nil, err
	}
	return &cfg, nil
}

// Locking the singleton serializes default changes with instance deletion.
func lockConfig(tx *gorm.DB) (*store.SearchConfig, error) {
	var cfg store.SearchConfig
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&cfg, 1).Error
	return &cfg, err
}
func (s *Service) SetDefault(ctx context.Context, actor string, providerID *string) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, err := lockConfig(tx); err != nil {
			return err
		}
		if providerID != nil {
			var p store.SearchProvider
			if err := tx.First(&p, "id = ?", *providerID).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return invalid("默认搜索实例不存在或已停用")
				}
				return err
			}
			if p.Status != "active" {
				return invalid("默认搜索实例不存在或已停用")
			}
		}
		if err := tx.Model(&store.SearchConfig{}).Where("id = 1").Update("default_provider_id", providerID).Error; err != nil {
			return err
		}
		return audit.Log(tx, audit.Entry{ActorID: actor, Action: "search.default_update", Target: "web_search", Detail: map[string]any{"provider_id": providerID}})
	})
}
func (s *Service) Save(ctx context.Context, actor, providerID string, in Input) (*ProviderView, error) {
	in.Name, in.BaseURL = strings.TrimSpace(in.Name), strings.TrimRight(strings.TrimSpace(in.BaseURL), "/")
	in.APIKey = strings.TrimSpace(in.APIKey)
	if in.Status == "" {
		in.Status = "active"
	}
	if err := validate(in); err != nil {
		return nil, err
	}
	var row store.SearchProvider
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, err := lockConfig(tx); err != nil {
			return err
		}
		action := "search.provider_create"
		if providerID == "" {
			if strings.TrimSpace(in.APIKey) == "" {
				return invalid("api_key 必填")
			}
			row.ID, row.CreatedBy = id.New(), actor
		} else {
			if err := tx.First(&row, "id = ?", providerID).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return &Error{404, respond.CodeNotFound, "搜索实例不存在"}
				}
				return err
			}
			if row.Type != in.Type {
				return invalid("已有实例不能更改供应商类型，请新建实例")
			}
			action = "search.provider_update"
		}
		row.Name, row.Type, row.BaseURL, row.Status = in.Name, in.Type, in.BaseURL, in.Status
		if strings.TrimSpace(in.APIKey) != "" {
			var err error
			row.APIKeyEnc, err = provider.Encrypt(s.masterKey, in.APIKey)
			if err != nil {
				return err
			}
		}
		if err := tx.Save(&row).Error; err != nil {
			return err
		}
		return audit.Log(tx, audit.Entry{ActorID: actor, Action: action, Target: row.ID, Detail: map[string]any{"name": row.Name, "type": row.Type, "status": row.Status, "key_updated": in.APIKey != ""}})
	})
	if err != nil {
		return nil, err
	}
	return s.view(row)
}
func (s *Service) Delete(ctx context.Context, actor, providerID string) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		cfg, err := lockConfig(tx)
		if err != nil {
			return err
		}
		if cfg.DefaultProviderID != nil && *cfg.DefaultProviderID == providerID {
			return conflict("请先解除全局默认，再删除此搜索实例")
		}
		var n int64
		if err := tx.Model(&store.Agent{}).Where("search_provider_id = ?", providerID).Count(&n).Error; err != nil {
			return err
		}
		if n > 0 {
			return conflict("此搜索实例仍被智能体引用，请先修改智能体配置")
		}
		res := tx.Delete(&store.SearchProvider{}, "id = ?", providerID)
		if res.Error != nil {
			return conflict("搜索实例被引用，无法删除")
		}
		if res.RowsAffected == 0 {
			return &Error{404, respond.CodeNotFound, "搜索实例不存在"}
		}
		return audit.Log(tx, audit.Entry{ActorID: actor, Action: "search.provider_delete", Target: providerID})
	})
}
