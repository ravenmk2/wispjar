package provider

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
)

// 原始 usage 旁路抓取（runtime.md §3，调研 .agents/run/research-provider-cache.md §3.1 方案 A）：
// eino acl 的 toEinoTokenUsage 只映射 OpenAI 标准字段，供应商特有缓存字段
// （deepseek 顶层 hit/miss、qwen cache_creation_input_tokens）被丢弃。这里在工厂
// 注入的 HTTPClient 上包一层 RoundTripper，旁路解析响应里的原始 usage，按适配器
// 归一化后写入 turn 级 UsageSink（经请求 ctx 路由，模型实例跨会话共享也安全）。
// 抓取失败一律静默降级——绝不影响主请求。

// maxUsageCaptureBody 非流式响应 tee 上限（usage 只在 JSON 尾部，超限放弃抓取）。
const maxUsageCaptureBody = 8 << 20

// CapturedUsage 是一次模型调用旁路抓取并归一化后的缓存用量。
type CapturedUsage struct {
	CacheRead  int64
	CacheWrite int64
	// Reported 为 false 表示原始 usage 里没有该供应商的任何缓存字段
	//（供应商未上报，区别于「上报了但命中为 0」）。
	Reported bool
}

// UsageSink 是 turn 级抓取结果收集器：transport 旁路追加，orchestrator 在
// 每个 step 提交时取走最新一条（主流程与 task 子运行均串行，天然一一对应）。
type UsageSink struct {
	mu       sync.Mutex
	entries  []CapturedUsage
	consumed int
}

func NewUsageSink() *UsageSink { return &UsageSink{} }

func (s *UsageSink) add(c CapturedUsage) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.entries = append(s.entries, c)
	s.mu.Unlock()
}

// TakeLatest 取最新一条未消费的抓取结果并标记消费；无新增返回 nil。
func (s *UsageSink) TakeLatest() *CapturedUsage {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.entries) <= s.consumed {
		return nil
	}
	c := s.entries[len(s.entries)-1]
	s.consumed = len(s.entries)
	return &c
}

type usageSinkCtxKey struct{}

// WithUsageSink 把 turn 级 sink 挂到 ctx；orchestrator 在 StartTurn/审批 resume 前调用。
func WithUsageSink(ctx context.Context, s *UsageSink) context.Context {
	return context.WithValue(ctx, usageSinkCtxKey{}, s)
}

func usageSinkFrom(ctx context.Context) *UsageSink {
	s, _ := ctx.Value(usageSinkCtxKey{}).(*UsageSink)
	return s
}

// usageCaptureTransport 包装底层 RoundTripper，旁路解析响应 usage。
// body 由本 transport 与 eino 顺序共享（无并发 tee goroutine），抓取只读副本。
type usageCaptureTransport struct {
	base    http.RoundTripper
	adapter ProviderAdapter
}

func newUsageCaptureTransport(base http.RoundTripper, adapter ProviderAdapter) *usageCaptureTransport {
	if base == nil {
		base = http.DefaultTransport
	}
	return &usageCaptureTransport{base: base, adapter: adapter}
}

func (t *usageCaptureTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.base.RoundTrip(req)
	if err != nil || resp == nil || resp.Body == nil {
		return resp, err
	}
	sink := usageSinkFrom(req.Context())
	if sink == nil || resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return resp, err
	}
	ct := resp.Header.Get("Content-Type")
	switch {
	case strings.Contains(ct, "text/event-stream"):
		resp.Body = newUsageSSEReader(resp.Body, t.capture(sink))
	case strings.Contains(ct, "json"):
		resp.Body = t.teeJSON(resp.Body, sink)
	}
	return resp, err
}

func (t *usageCaptureTransport) capture(sink *UsageSink) func(map[string]any) {
	return func(raw map[string]any) {
		read, write, reported := t.adapter.NormalizeUsage(raw)
		sink.add(CapturedUsage{CacheRead: read, CacheWrite: write, Reported: reported})
	}
}

// teeJSON 非流式：全量读入（有上限）解析 usage 后换新 body 交还 eino。
func (t *usageCaptureTransport) teeJSON(body io.ReadCloser, sink *UsageSink) io.ReadCloser {
	data, err := io.ReadAll(io.LimitReader(body, maxUsageCaptureBody))
	_ = body.Close()
	if err == nil {
		var parsed struct {
			Usage map[string]any `json:"usage"`
		}
		if json.Unmarshal(data, &parsed) == nil && parsed.Usage != nil {
			t.capture(sink)(parsed.Usage)
		}
	}
	return io.NopCloser(bytes.NewReader(data))
}

// usageSSEReader 流式 SSE：逐行透传，扫描含 "usage" 的 data 帧（usage 在末帧），
// 只解析命中帧，不全量缓冲。
type usageSSEReader struct {
	rc      io.ReadCloser
	br      *bufio.Reader
	pend    []byte
	err     error // pend 排空后返回的底层错误
	onUsage func(map[string]any)
}

func newUsageSSEReader(rc io.ReadCloser, onUsage func(map[string]any)) *usageSSEReader {
	return &usageSSEReader{rc: rc, br: bufio.NewReader(rc), onUsage: onUsage}
}

func (r *usageSSEReader) Read(p []byte) (int, error) {
	for len(r.pend) == 0 && r.err == nil {
		var line []byte
		line, r.err = r.br.ReadBytes('\n')
		r.scan(line)
		r.pend = line
	}
	if len(r.pend) == 0 {
		return 0, r.err
	}
	n := copy(p, r.pend)
	r.pend = r.pend[n:]
	return n, nil
}

func (r *usageSSEReader) Close() error { return r.rc.Close() }

// scan 解析 `data: {...}` 帧；[DONE] 与畸形帧静默跳过。
func (r *usageSSEReader) scan(line []byte) {
	if !bytes.HasPrefix(line, []byte("data:")) || !bytes.Contains(line, []byte(`"usage"`)) {
		return
	}
	payload := bytes.TrimSpace(line[len("data:"):])
	if !bytes.HasPrefix(payload, []byte("{")) {
		return
	}
	var chunk struct {
		Usage map[string]any `json:"usage"`
	}
	if json.Unmarshal(payload, &chunk) != nil || chunk.Usage == nil {
		return
	}
	r.onUsage(chunk.Usage)
}
