package search

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ravenmk2/wispjar/internal/id"
	"github.com/ravenmk2/wispjar/internal/provider"
	"github.com/ravenmk2/wispjar/internal/store"
)

type FetchRequest struct {
	URL       string `json:"url"`
	MaxChars  int    `json:"max_chars"`
	OrgID     string `json:"-"`
	UserID    string `json:"-"`
	AgentID   string `json:"-"`
	SessionID string `json:"-"`
	CallID    string `json:"-"`
}
type FetchResponse struct {
	URL          string    `json:"url"`
	FinalURL     string    `json:"final_url,omitempty"`
	Title        string    `json:"title"`
	Content      string    `json:"content"`
	Format       string    `json:"format"`
	ProviderID   string    `json:"provider_id"`
	ProviderName string    `json:"provider_name"`
	FetchedAt    time.Time `json:"fetched_at"`
	DurationMS   int64     `json:"duration_ms"`
	Truncated    bool      `json:"truncated"`
}

func supports(typ string, fetch bool) bool {
	if fetch {
		return typ == "go_http" || typ == "deepseek"
	}
	return typ == "deepseek" || typ == "tavily" || typ == "bocha"
}
func instanceTimeout(p store.SearchProvider) time.Duration {
	n := p.TimeoutSeconds
	if n <= 0 || n > 120 {
		n = 20
	}
	return time.Duration(n) * time.Second
}
func fetchError(status int, code, message string) error {
	return &Error{status, "fetch/" + code, message}
}
func fetchNetworkError(ctx context.Context, err error) error {
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() && ctx.Err() == nil {
		return fetchError(504, "timeout", "网页读取超时")
	}
	if errors.Is(ctx.Err(), context.Canceled) {
		return fetchError(499, "cancelled", "网页读取已取消")
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return fetchError(504, "timeout", "网页读取超时")
	}
	if errors.Is(err, errBlockedTarget) {
		return fetchError(400, "blocked", "仅允许读取公网 HTTP(S) 地址；目标或跳转地址被访问策略阻止")
	}
	return fetchError(502, "network", "无法连接所选读取实例或目标网页")
}
func fetchURL(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil || !validURL(raw) || utf8.RuneCountInString(raw) > 8192 || u.Opaque != "" {
		return nil, invalid("url 必须是不含用户名和密码的公网 HTTP(S) 地址，最长 8192 字符")
	}
	if port := u.Port(); port != "" && port != "80" && port != "443" {
		return nil, fetchError(400, "blocked", "首版仅支持目标端口 80 和 443")
	}
	u.Fragment = ""
	return u, nil
}

// Fetch invokes only the selected instance, once. There is no provider fallback.
func (s *Service) Fetch(ctx context.Context, providerID string, in FetchRequest) (out *FetchResponse, err error) {
	u, err := fetchURL(in.URL)
	if err != nil {
		return nil, err
	}
	in.URL = u.String()
	if in.MaxChars == 0 {
		in.MaxChars = 20000
	}
	if in.MaxChars < 1 || in.MaxChars > 50000 {
		return nil, invalid("max_chars 必须为 1–50000")
	}
	if providerID == "" {
		cfg, e := s.Config(ctx)
		if e != nil {
			return nil, fetchError(503, "unavailable", "无法读取全局读取配置")
		}
		if cfg.DefaultFetchProviderID == nil {
			return nil, fetchError(503, "unavailable", "尚未配置全局默认读取实例")
		}
		providerID = *cfg.DefaultFetchProviderID
	}
	var p store.SearchProvider
	if e := s.db.WithContext(ctx).First(&p, "id = ?", providerID).Error; e != nil {
		return nil, fetchError(503, "unavailable", "所选读取实例不存在或无法读取")
	}
	if p.Status != "active" {
		return nil, fetchError(503, "unavailable", "所选读取实例已停用")
	}
	if !supports(p.Type, true) {
		return nil, fetchError(503, "unavailable", "所选实例不支持网页读取")
	}
	key := ""
	if p.Type == "deepseek" {
		var e error
		key, e = provider.Decrypt(s.masterKey, p.APIKeyEnc)
		if e != nil {
			return nil, fetchError(503, "unavailable", "读取实例密钥不可用")
		}
	}
	ctx, cancel := context.WithTimeout(ctx, instanceTimeout(p))
	defer cancel()
	// Reject private literal and DNS targets for both backends. Go HTTP also pins
	// the checked address at connect time and validates every redirect.
	if _, e := publicAddresses(ctx, u.Hostname(), netLookup); e != nil {
		return nil, fetchNetworkError(ctx, e)
	}
	start := time.Now()
	defer func() {
		call := store.FetchCall{ID: id.New(), ProviderID: p.ID, OrgID: in.OrgID, UserID: in.UserID, AgentID: in.AgentID, SessionID: in.SessionID, CallID: in.CallID, Success: err == nil, DurationMS: time.Since(start).Milliseconds()}
		if out != nil {
			out.DurationMS = call.DurationMS
			call.ContentChars = utf8.RuneCountInString(out.Content)
		}
		var se *Error
		if errors.As(err, &se) {
			call.ErrorCode = se.Code
		} else if err != nil {
			call.ErrorCode = "fetch/failed"
		}
		usageCtx, stop := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer stop()
		if e := s.db.WithContext(usageCtx).Create(&call).Error; e != nil {
			log.Print("record fetch usage failed")
		}
	}()
	out, err = s.executeFetch(ctx, p, key, in)
	return out, err
}

const maxFetchBytes = 2 * 1024 * 1024

func (s *Service) executeFetch(ctx context.Context, p store.SearchProvider, key string, in FetchRequest) (*FetchResponse, error) {
	var out *FetchResponse
	var err error
	if p.Type == "deepseek" {
		out, err = s.deepseekFetch(ctx, p, key, in)
	} else {
		out, err = s.httpFetch(ctx, in.URL)
	}
	if err != nil {
		return nil, err
	}
	if ctx.Err() != nil {
		return nil, fetchNetworkError(ctx, ctx.Err())
	}
	if strings.TrimSpace(out.Content) == "" {
		return nil, fetchError(502, "empty", "未提取到可读正文，页面可能需要登录或 JavaScript")
	}
	out.URL, out.ProviderID, out.ProviderName = in.URL, p.ID, p.Name
	out.Title = clip(out.Title, 300, &out.Truncated)
	out.Content = clip(out.Content, in.MaxChars, &out.Truncated)
	out.FetchedAt = time.Now().UTC()
	return out, nil
}
func (s *Service) deepseekFetch(ctx context.Context, p store.SearchProvider, key string, in FetchRequest) (*FetchResponse, error) {
	data, _ := json.Marshal(map[string]string{"url": in.URL})
	endpoint := strings.TrimSuffix(endpoint(p.BaseURL, "deepseek"), "/search") + "/fetch"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(data))
	if err != nil {
		return nil, fetchError(503, "unavailable", "读取实例地址无效")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)
	res, err := s.client.Do(req)
	if err != nil {
		return nil, fetchNetworkError(ctx, err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != 200 {
		return nil, upstreamFetchError(res.StatusCode, true)
	}
	raw, err := readFetchBody(ctx, res.Body)
	if err != nil {
		return nil, err
	}
	var v struct {
		Title   *string `json:"title"`
		Content *string `json:"content"`
	}
	if json.Unmarshal(raw, &v) != nil || v.Title == nil || v.Content == nil {
		return nil, fetchError(502, "response", "读取供应商返回了无效结果")
	}
	return &FetchResponse{Title: *v.Title, Content: *v.Content, Format: "markdown"}, nil
}
func upstreamFetchError(status int, vendor bool) error {
	msg := "目标网页请求失败"
	if vendor {
		msg = "读取供应商请求失败"
		switch status {
		case 401, 403:
			msg = "读取实例认证失败，请检查密钥和权限"
		case 402, 432, 433:
			msg = "读取实例余额或额度不足"
		case 429:
			msg = "读取实例请求过于频繁"
		}
	}
	return fetchError(502, "upstream", fmt.Sprintf("%s（HTTP %d）", msg, status))
}
func readFetchBody(ctx context.Context, r io.Reader) ([]byte, error) {
	raw, err := io.ReadAll(io.LimitReader(r, maxFetchBytes+1))
	if err != nil {
		if ctx.Err() != nil {
			return nil, fetchNetworkError(ctx, err)
		}
		return nil, fetchError(502, "response", "读取读取结果失败")
	}
	if len(raw) > maxFetchBytes {
		return nil, fetchError(502, "too_large", "网页响应超过 2 MiB 限制")
	}
	return raw, nil
}
func (s *Service) httpFetch(ctx context.Context, target string) (*FetchResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, invalid("url 无效")
	}
	req.Header.Set("User-Agent", "Wispjar/1.0 (web_fetch)")
	req.Header.Set("Accept", "text/html, application/xhtml+xml, text/plain, text/markdown, application/json;q=0.9")
	res, err := s.fetchClient.Do(req)
	if err != nil {
		return nil, fetchNetworkError(ctx, err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, upstreamFetchError(res.StatusCode, false)
	}
	if res.Header.Get("Content-Disposition") != "" {
		typ, _, _ := mime.ParseMediaType(res.Header.Get("Content-Disposition"))
		if typ == "attachment" {
			return nil, fetchError(415, "unsupported", "首版不支持下载附件")
		}
	}
	raw, err := readFetchBody(ctx, res.Body)
	if err != nil {
		return nil, err
	}
	contentType := res.Header.Get("Content-Type")
	if contentType == "" {
		contentType = http.DetectContentType(raw)
	}
	typ, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return nil, fetchError(415, "unsupported", "无法识别网页内容类型")
	}
	out := &FetchResponse{FinalURL: res.Request.URL.String(), Format: "text"}
	switch typ {
	case "text/html", "application/xhtml+xml":
		out.Title, out.Content, err = extractHTML(raw, contentType, res.Request.URL)
		out.Format = "markdown"
	case "text/plain", "text/markdown", "text/x-markdown", "application/json":
		out.Content, err = decodeText(raw, contentType)
		if typ == "text/markdown" || typ == "text/x-markdown" {
			out.Format = "markdown"
		}
	default:
		return nil, fetchError(415, "unsupported", "首版仅支持 HTML、纯文本、Markdown 和 JSON")
	}
	if err != nil {
		return nil, fetchError(502, "response", "网页正文解析失败")
	}
	return out, nil
}
