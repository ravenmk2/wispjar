package search

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/ravenmk2/wispjar/internal/store"
)

func TestAdapters(t *testing.T) {
	cases := []struct{ typ, path, fixture string }{
		{"deepseek", "/v1/tools/search", `{"results":[{"title":"Release","url":"https://example.com/a","snippet":"preview","summary":"model context","date":"2026-10-23T00:00:00Z"}]}`},
		{"tavily", "/search", `{"results":[{"title":"Release","url":"https://example.com/a","content":"model context","published_date":"2026-10-23"}],"usage":{"credits":1}}`},
		{"bocha", "/v1/web-search", `{"code":200,"data":{"webPages":{"value":[{"name":"Release","url":"https://example.com/a","snippet":"preview","summary":"model context","datePublished":"2026-10-23"}]}}}`},
	}
	for _, tc := range cases {
		t.Run(tc.typ, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.URL.Path != tc.path || r.Method != "POST" || r.Header.Get("Authorization") != "Bearer test-secret" {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
				}
				var in map[string]any
				if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
					t.Error(err)
				}
				if in["query"] != "中文 搜索" {
					t.Errorf("query changed: %#v", in)
				}
				if tc.typ == "tavily" {
					if in["max_results"] != float64(3) || in["include_answer"] != false || in["include_usage"] != true {
						t.Errorf("unexpected Tavily options: %#v", in)
					}
				} else if in["count"] != float64(3) {
					t.Errorf("unexpected count: %#v", in)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(tc.fixture))
			}))
			defer server.Close()
			svc := NewService(nil, nil)
			out, err := svc.execute(context.Background(), store.SearchProvider{ID: "instance-a", Name: "Account A", Type: tc.typ, BaseURL: server.URL}, "test-secret", Request{Query: "中文 搜索", Count: 3})
			if err != nil {
				t.Fatal(err)
			}
			if calls.Load() != 1 || out.ProviderID != "instance-a" || out.ProviderName != "Account A" || len(out.Results) != 1 || out.Results[0].Content != "model context" || out.Results[0].PublishedAt == "" {
				t.Fatalf("unexpected result: %+v; calls=%d", out, calls.Load())
			}
			if tc.typ == "tavily" && (out.Credits == nil || *out.Credits != 1) {
				t.Fatalf("missing credits: %+v", out)
			}
		})
	}
}

func TestUpstreamErrorsNeverRetryOrExposeResponse(t *testing.T) {
	for _, status := range []int{400, 401, 402, 429, 500, 503} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.WriteHeader(status)
				_, _ = w.Write([]byte("secret-upstream-body"))
			}))
			defer server.Close()
			_, err := NewService(nil, nil).execute(context.Background(), store.SearchProvider{Type: "deepseek", BaseURL: server.URL}, "secret-key", Request{Query: "q", Count: 5})
			if err == nil || calls.Load() != 1 || strings.Contains(err.Error(), "secret") {
				t.Fatalf("error=%v; calls=%d", err, calls.Load())
			}
		})
	}
}

func TestRedirectsDoNotForwardCredentials(t *testing.T) {
	var received atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { received.Add(1) }))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer redirect.Close()
	_, err := NewService(nil, nil).execute(context.Background(), store.SearchProvider{Type: "tavily", BaseURL: redirect.URL}, "secret", Request{Query: "q", Count: 1})
	if err == nil || received.Load() != 0 {
		t.Fatalf("redirect followed: error=%v requests=%d", err, received.Load())
	}
}

func TestCancellationAndResponseLimits(t *testing.T) {
	t.Run("cancel", func(t *testing.T) {
		started := make(chan struct{})
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.Copy(io.Discard, r.Body)
			close(started)
			<-r.Context().Done()
		}))
		defer server.Close()
		ctx, cancel := context.WithCancel(context.Background())
		go func() { <-started; cancel() }()
		_, err := NewService(nil, nil).execute(ctx, store.SearchProvider{Type: "deepseek", BaseURL: server.URL}, "secret", Request{Query: "q", Count: 1})
		if err == nil || !strings.Contains(err.Error(), "取消") {
			t.Fatalf("want cancellation, got %v", err)
		}
	})
	t.Run("oversized", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(strings.Repeat("x", 2*1024*1024+1)))
		}))
		defer server.Close()
		_, err := NewService(nil, nil).execute(context.Background(), store.SearchProvider{Type: "deepseek", BaseURL: server.URL}, "secret", Request{Query: "q", Count: 1})
		if err == nil || !strings.Contains(err.Error(), "大小限制") {
			t.Fatalf("want size limit, got %v", err)
		}
	})
	t.Run("deadline", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.Copy(io.Discard, r.Body); <-r.Context().Done() }))
		defer server.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
		defer cancel()
		_, err := NewService(nil, nil).execute(ctx, store.SearchProvider{Type: "deepseek", BaseURL: server.URL}, "secret", Request{Query: "q", Count: 1})
		if err == nil || !strings.Contains(err.Error(), "超时") {
			t.Fatalf("want timeout, got %v", err)
		}
	})
}

func TestNormalizeSafeLinksDedupAndUnicodeBudget(t *testing.T) {
	out := &Response{Results: []Result{
		{URL: "javascript:alert(1)"}, {URL: "https://user:secret@example.com/a"},
		{URL: "https://example.com/a#one", Content: strings.Repeat("界", 3000)},
		{URL: "https://example.com/a#two", Content: "duplicate"},
		{URL: "https://example.com/b", Content: "second"},
	}}
	normalize(out, 1)
	if len(out.Results) != 1 || out.Results[0].URL != "https://example.com/a" || !out.Truncated || !utf8.ValidString(out.Results[0].Content) || utf8.RuneCountInString(out.Results[0].Content) != 2000 {
		t.Fatalf("unexpected normalization: %+v", out)
	}
}
func TestEmptyAndMalformedResponses(t *testing.T) {
	if out, err := decode("bocha", []byte(`{"code":200,"data":{"webPages":null}}`)); err != nil || len(out.Results) != 0 {
		t.Fatalf("Bocha empty pages must succeed: %+v %v", out, err)
	}
	for _, typ := range []string{"deepseek", "tavily", "bocha"} {
		fixture := `{"results":[]}`
		if typ == "bocha" {
			fixture = `{"code":200,"data":{"webPages":{"value":[]}}}`
		}
		out, err := decode(typ, []byte(fixture))
		if err != nil || len(out.Results) != 0 {
			t.Fatalf("%s empty result: %+v %v", typ, out, err)
		}
		for _, raw := range []string{`null`, `{}`, `{"results":{}}`, `{"code":500,"data":{"webPages":{"value":[]}}}`} {
			if _, err := decode(typ, []byte(raw)); err == nil {
				t.Fatalf("%s accepted malformed: %s", typ, raw)
			}
		}
	}
}
func TestValidationAndVersionedBaseURLs(t *testing.T) {
	for _, raw := range []string{"https://user:secret@example.com", "https://example.com?key=secret", "https://example.com#fragment", "file:///tmp/key", "https://"} {
		if err := validate(Input{Name: "n", Type: "deepseek", BaseURL: raw, Status: "active"}); err == nil {
			t.Fatalf("accepted URL: %s", raw)
		}
	}
	for _, typ := range []string{"deepseek", "bocha"} {
		if strings.Contains(endpoint("https://example.com/v1", typ), "/v1/v1/") {
			t.Fatalf("duplicate version for %s", typ)
		}
	}
}
