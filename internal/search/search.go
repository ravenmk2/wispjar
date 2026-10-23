package search

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ravenmk2/wispjar/internal/id"
	"github.com/ravenmk2/wispjar/internal/provider"
	"github.com/ravenmk2/wispjar/internal/store"
)

type Request struct {
	Query     string `json:"query"`
	Count     int    `json:"count"`
	OrgID     string `json:"-"`
	UserID    string `json:"-"`
	AgentID   string `json:"-"`
	SessionID string `json:"-"`
	CallID    string `json:"-"`
}
type Result struct {
	Title       string `json:"title"`
	URL         string `json:"url"`
	Snippet     string `json:"snippet"`
	Content     string `json:"content"`
	PublishedAt string `json:"published_at,omitempty"`
}
type Response struct {
	Query        string   `json:"query"`
	ProviderID   string   `json:"provider_id"`
	ProviderName string   `json:"provider_name"`
	Results      []Result `json:"results"`
	DurationMS   int64    `json:"duration_ms"`
	Truncated    bool     `json:"truncated"`
	Credits      *float64 `json:"credits,omitempty"`
}

func (s *Service) selected(ctx context.Context, providerID string) (*store.SearchProvider, error) {
	if providerID == "" {
		cfg, err := s.Config(ctx)
		if err != nil {
			return nil, unavailable("无法读取全局搜索配置")
		}
		if cfg.DefaultProviderID == nil {
			return nil, unavailable("尚未配置全局默认搜索实例，请联系系统管理员")
		}
		providerID = *cfg.DefaultProviderID
	}
	var p store.SearchProvider
	if err := s.db.WithContext(ctx).First(&p, "id = ?", providerID).Error; err != nil {
		return nil, unavailable("所选搜索实例不存在或无法读取，请联系管理员")
	}
	if p.Status != "active" {
		return nil, unavailable("所选搜索实例已停用，请联系管理员")
	}
	return &p, nil
}

// Search calls exactly one selected instance once. It never retries or falls back.
func (s *Service) Search(ctx context.Context, providerID string, in Request) (out *Response, err error) {
	in.Query = strings.TrimSpace(in.Query)
	if in.Query == "" || utf8.RuneCountInString(in.Query) > 512 {
		return nil, invalid("query 必须为 1–512 个 Unicode 字符")
	}
	if in.Count == 0 {
		in.Count = 5
	}
	if in.Count < 1 || in.Count > 10 {
		return nil, invalid("count 必须为 1–10")
	}
	p, err := s.selected(ctx, providerID)
	if err != nil {
		return nil, err
	}
	if !supports(p.Type, false) {
		return nil, unavailable("所选实例不支持网络搜索")
	}
	if p.Type == "deepseek" && utf8.RuneCountInString(in.Query) > 128 {
		return nil, invalid("DeepSeek query 最长 128 个 Unicode 字符，请缩短搜索词后重试")
	}
	key, err := provider.Decrypt(s.masterKey, p.APIKeyEnc)
	if err != nil {
		return nil, unavailable("搜索实例密钥不可用，请联系系统管理员")
	}
	ctx, cancel := context.WithTimeout(ctx, instanceTimeout(*p))
	defer cancel()
	start := time.Now()
	defer func() {
		call := store.SearchCall{ID: id.New(), ProviderID: p.ID, OrgID: in.OrgID, UserID: in.UserID, AgentID: in.AgentID, SessionID: in.SessionID, CallID: in.CallID, Success: err == nil, DurationMS: time.Since(start).Milliseconds()}
		if out != nil {
			out.DurationMS = call.DurationMS
			call.ResultCount, call.Credits = len(out.Results), out.Credits
		}
		var se *Error
		if errors.As(err, &se) {
			call.ErrorCode = se.Code
		} else if err != nil {
			call.ErrorCode = "search/failed"
		}
		// Account for cancelled requests too; writing has its own bounded lifetime.
		usageCtx, stop := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer stop()
		if e := s.db.WithContext(usageCtx).Create(&call).Error; e != nil {
			log.Print("record search usage failed")
		}
	}()
	out, err = s.execute(ctx, *p, key, in)
	return out, err
}

func endpoint(base, typ string) string {
	base = strings.TrimRight(base, "/")
	switch typ {
	case "deepseek":
		if strings.HasSuffix(base, "/v1") {
			return base + "/tools/search"
		}
		return base + "/v1/tools/search"
	case "tavily":
		return base + "/search"
	default:
		if strings.HasSuffix(base, "/v1") {
			return base + "/web-search"
		}
		return base + "/v1/web-search"
	}
}

func (s *Service) execute(ctx context.Context, p store.SearchProvider, key string, in Request) (*Response, error) {
	body := map[string]any{"query": in.Query}
	switch p.Type {
	case "deepseek":
		body["count"], body["need_content"] = in.Count, false
	case "tavily":
		body["max_results"], body["search_depth"] = in.Count, "basic"
		body["include_answer"], body["include_raw_content"], body["include_usage"] = false, false, true
	case "bocha":
		body["count"], body["summary"], body["freshness"] = in.Count, true, "noLimit"
	default:
		return nil, unavailable("搜索供应商类型不受支持")
	}
	data, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint(p.BaseURL, p.Type), bytes.NewReader(data))
	if err != nil {
		return nil, unavailable("搜索实例地址无效")
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	res, err := s.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			if errors.Is(ctx.Err(), context.Canceled) {
				return nil, &Error{499, "search/cancelled", "网络搜索已取消"}
			}
			return nil, &Error{504, "search/timeout", "网络搜索超时，请稍后重试"}
		}
		// Never expose transport errors containing request URLs or upstream secrets.
		return nil, &Error{502, "search/network", "无法连接所选搜索实例"}
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		msg := "搜索供应商请求失败"
		switch res.StatusCode {
		case 401, 403:
			msg = "搜索实例认证失败，请检查密钥及访问权限"
		case 402, 432, 433:
			msg = "搜索实例余额或额度不足"
		case 429:
			msg = "搜索实例请求过于频繁，请稍后重试"
		case 400, 422:
			msg = "搜索供应商拒绝了查询参数，请调整搜索词"
		}
		return nil, &Error{502, "search/upstream", fmt.Sprintf("%s（HTTP %d）", msg, res.StatusCode)}
	}
	const maxBytes = 2 * 1024 * 1024
	raw, err := io.ReadAll(io.LimitReader(res.Body, maxBytes+1))
	if err != nil {
		if ctx.Err() != nil {
			if errors.Is(ctx.Err(), context.Canceled) {
				return nil, &Error{499, "search/cancelled", "网络搜索已取消"}
			}
			return nil, &Error{504, "search/timeout", "网络搜索超时，请稍后重试"}
		}
		return nil, &Error{502, "search/response", "读取搜索结果失败"}
	}
	if len(raw) > maxBytes {
		return nil, &Error{502, "search/response", "搜索响应超过大小限制"}
	}
	out, err := decode(p.Type, raw)
	if err != nil {
		return nil, &Error{502, "search/response", "搜索供应商返回了无效结果"}
	}
	out.Query, out.ProviderID, out.ProviderName = in.Query, p.ID, p.Name
	normalize(out, in.Count)
	return out, nil
}

func decode(typ string, raw []byte) (*Response, error) {
	out := &Response{Results: []Result{}}
	switch typ {
	case "deepseek":
		var v struct {
			Results *[]struct{ Title, URL, Snippet, Summary, Date string } `json:"results"`
		}
		if err := json.Unmarshal(raw, &v); err != nil || v.Results == nil {
			return nil, errors.New("invalid search response")
		}
		for _, r := range *v.Results {
			out.Results = append(out.Results, Result{Title: r.Title, URL: r.URL, Snippet: r.Snippet, Content: r.Summary, PublishedAt: r.Date})
		}
	case "tavily":
		var v struct {
			Results *[]struct {
				Title, URL, Content string
				PublishedDate       string `json:"published_date"`
			} `json:"results"`
			Usage struct {
				Credits *float64 `json:"credits"`
			} `json:"usage"`
		}
		if err := json.Unmarshal(raw, &v); err != nil || v.Results == nil {
			return nil, errors.New("invalid search response")
		}
		out.Credits = v.Usage.Credits
		for _, r := range *v.Results {
			out.Results = append(out.Results, Result{Title: r.Title, URL: r.URL, Snippet: r.Content, Content: r.Content, PublishedAt: r.PublishedDate})
		}
	case "bocha":
		var v struct {
			Code int `json:"code"`
			Data *struct {
				WebPages struct {
					Value *[]struct{ Name, URL, Snippet, Summary, DatePublished string } `json:"value"`
				} `json:"webPages"`
			} `json:"data"`
		}
		if err := json.Unmarshal(raw, &v); err != nil || v.Code != 200 || v.Data == nil {
			return nil, errors.New("invalid search response")
		}
		if v.Data.WebPages.Value == nil {
			return out, nil
		}
		for _, r := range *v.Data.WebPages.Value {
			out.Results = append(out.Results, Result{Title: r.Name, URL: r.URL, Snippet: r.Snippet, Content: r.Summary, PublishedAt: r.DatePublished})
		}
	default:
		return nil, errors.New("unsupported provider")
	}
	return out, nil
}

func clip(s string, max int, truncated *bool) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	*truncated = true
	return string(r[:max])
}
func normalize(out *Response, count int) {
	seen := map[string]bool{}
	results := make([]Result, 0, count)
	budget := 12000
	for _, r := range out.Results {
		if !validURL(r.URL) || len(r.URL) > 4096 {
			continue
		}
		u, _ := url.Parse(r.URL)
		u.Fragment = ""
		r.URL = u.String()
		if seen[r.URL] {
			continue
		}
		seen[r.URL] = true
		if len(results) == count || budget <= 0 {
			out.Truncated = true
			break
		}
		r.Title = clip(r.Title, 300, &out.Truncated)
		if r.Snippet == "" {
			r.Snippet = r.Content
		}
		r.Snippet = clip(r.Snippet, 300, &out.Truncated)
		if r.Content == "" {
			r.Content = r.Snippet
		}
		r.Content = clip(r.Content, min(2000, budget), &out.Truncated)
		r.PublishedAt = clip(r.PublishedAt, 128, &out.Truncated)
		budget -= utf8.RuneCountInString(r.Content)
		results = append(results, r)
	}
	out.Results = results
}
