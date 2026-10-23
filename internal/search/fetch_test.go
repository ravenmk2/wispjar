package search

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/ravenmk2/wispjar/internal/store"
)

func TestFetchPublicAddressPolicy(t *testing.T) {
	for _, s := range []string{"127.0.0.1", "10.2.3.4", "172.16.0.1", "192.168.2.1", "169.254.169.254", "100.100.100.200", "0.0.0.0", "198.18.0.1", "192.0.2.1", "224.0.0.1", "255.255.255.255", "::", "::1", "::ffff:127.0.0.1", "fc00::1", "fe80::1", "64:ff9b::a00:1", "2002:7f00:1::", "2001:db8::1"} {
		if publicIP(net.ParseIP(s)) {
			t.Errorf("allowed %s", s)
		}
	}
	for _, s := range []string{"1.1.1.1", "8.8.8.8", "2606:4700:4700::1111"} {
		if !publicIP(net.ParseIP(s)) {
			t.Errorf("blocked public %s", s)
		}
	}
	for _, s := range []string{"localhost", "localhost.", "x.localhost", "mysql", "fe80::1%eth0"} {
		if _, err := publicAddresses(context.Background(), s, nil); !errors.Is(err, errBlockedTarget) {
			t.Errorf("allowed hostname %s: %v", s, err)
		}
	}
	mixed := func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("1.1.1.1")}, {IP: net.ParseIP("127.0.0.1")}}, nil
	}
	if _, err := publicAddresses(context.Background(), "mixed.example", mixed); !errors.Is(err, errBlockedTarget) {
		t.Fatal("mixed DNS allowed")
	}
}
func TestFetchDialPinsValidatedDNSAnswer(t *testing.T) {
	lookups, dials := 0, 0
	lookup := func(context.Context, string) ([]net.IPAddr, error) {
		lookups++
		return []net.IPAddr{{IP: net.ParseIP("1.1.1.1")}}, nil
	}
	dial := safeDialContext(lookup, func(_ context.Context, network, address string) (net.Conn, error) {
		dials++
		if address != "1.1.1.1:443" {
			t.Fatalf("not pinned: %s", address)
		}
		return nil, io.EOF
	})
	_, _ = dial(context.Background(), "tcp", "public.example:443")
	if lookups != 1 || dials != 1 {
		t.Fatalf("lookups=%d dials=%d", lookups, dials)
	}
	if _, err := dial(context.Background(), "tcp", "public.example:3306"); !errors.Is(err, errBlockedTarget) {
		t.Fatal("unexpected port allowed")
	}
	blocked := safeDialContext(func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}, nil
	}, func(context.Context, string, string) (net.Conn, error) {
		t.Fatal("dialed private address")
		return nil, nil
	})
	if _, err := blocked(context.Background(), "tcp", "public.example:80"); !errors.Is(err, errBlockedTarget) {
		t.Fatal(err)
	}
}
func TestFetchRedirectPolicy(t *testing.T) {
	client := newFetchClient()
	if client.Transport.(*http.Transport).Proxy != nil {
		t.Fatal("proxy can bypass policy")
	}
	for _, s := range []string{"http://127.0.0.1/", "http://[::ffff:127.0.0.1]/", "http://user:pass@example.com", "file:///etc/passwd", "http://example.com:3306/"} {
		req, _ := http.NewRequest(http.MethodGet, s, nil)
		if err := client.CheckRedirect(req, []*http.Request{}); err == nil {
			t.Fatalf("redirect allowed %s", s)
		}
	}
	req, _ := http.NewRequest(http.MethodGet, "https://example.com", nil)
	if err := client.CheckRedirect(req, make([]*http.Request, 5)); err == nil {
		t.Fatal("unbounded redirects")
	}
}
func TestExtractHTMLPreservesStructureAndRemovesNoise(t *testing.T) {
	u, _ := url.Parse("https://example.com/docs/start")
	title, content, err := extractHTML([]byte(`<html><head><title>中文文档</title></head><body><nav>NOISE</nav><main><h1>标题</h1><p>正文 <a href="../next">链接</a></p><pre><code class="language-go">fmt.Println("hi")</code></pre><table><tr><th>A</th><th>B</th></tr><tr><td>1</td><td>2</td></tr></table><script>SECRET</script><aside>NOISE</aside><p hidden>HIDDEN</p></main><footer>NOISE</footer></body></html>`), "text/html; charset=utf-8", u)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"标题", "正文", "https://example.com/next", "fmt.Println", "| A"} {
		if !strings.Contains(content, want) {
			t.Errorf("missing %s: %s", want, content)
		}
	}
	if title != "中文文档" || strings.Contains(content, "NOISE") || strings.Contains(content, "SECRET") || strings.Contains(content, "HIDDEN") {
		t.Fatalf("bad extraction: %s %s", title, content)
	}
}

type fetchTransport func(*http.Request) (*http.Response, error)

func (f fetchTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestHTTPFetchContentErrorsAndCancellation(t *testing.T) {
	for _, tc := range []struct {
		name, typ, body string
		status          int
		want            string
	}{
		{"plain", "text/plain; charset=utf-8", "正文", 200, ""},
		{"markdown", "text/markdown; charset=utf-8", "# 中文", 200, ""},
		{"json", "application/json", `{"a":1}`, 200, ""},
		{"binary", "application/pdf", "%PDF", 200, "仅支持"},
		{"notfound", "text/html", "secret upstream body", 404, "HTTP 404"},
		{"large", "text/plain", strings.Repeat("x", maxFetchBytes+1), 200, "2 MiB"},
		{"empty", "text/html", "<html><body><script>noise</script></body></html>", 200, "未提取"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := NewService(nil, nil)
			calls := 0
			svc.fetchClient = &http.Client{Transport: fetchTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				return &http.Response{StatusCode: tc.status, Header: http.Header{"Content-Type": []string{tc.typ}}, Body: io.NopCloser(strings.NewReader(tc.body)), Request: r}, nil
			})}
			out, err := svc.executeFetch(context.Background(), store.SearchProvider{ID: "native", Name: "Native", Type: "go_http"}, "", FetchRequest{URL: "https://example.com/", MaxChars: 20000})
			if tc.want != "" {
				if err == nil || !strings.Contains(err.Error(), tc.want) {
					t.Fatalf("want %s, got %v", tc.want, err)
				}
			} else if err != nil || out.Content != tc.body || out.FinalURL != "https://example.com/" {
				t.Fatalf("out=%+v err=%v", out, err)
			}
			if calls != 1 {
				t.Fatalf("retried %d times", calls)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := fetchNetworkError(ctx, ctx.Err()); !strings.Contains(err.Error(), "取消") {
		t.Fatal(err)
	}
	ctx, stop := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer stop()
	if err := fetchNetworkError(ctx, ctx.Err()); !strings.Contains(err.Error(), "超时") {
		t.Fatal(err)
	}
}
func TestDeepSeekFetchProtocolAndStrictErrors(t *testing.T) {
	for _, body := range []string{`{"title":"Source","content":"中文正文正文"}`, `{}`, `null`, `{"title":"","content":null}`, `{"title":"","content":""}`} {
		calls := 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls++
			if r.URL.Path != "/v1/tools/fetch" || r.Method != "POST" || r.Header.Get("Authorization") != "Bearer secret" {
				t.Error("wrong protocol")
			}
			b, _ := io.ReadAll(r.Body)
			if string(b) != `{"url":"https://example.com/"}` {
				t.Errorf("body=%s", b)
			}
			_, _ = w.Write([]byte(body))
		}))
		svc := NewService(nil, nil)
		out, err := svc.executeFetch(context.Background(), store.SearchProvider{ID: "ds", Type: "deepseek", BaseURL: server.URL + "/v1"}, "secret", FetchRequest{URL: "https://example.com/", MaxChars: 3})
		server.Close()
		if strings.Contains(body, "Source") {
			if err != nil || out.Content != "中文正" || !out.Truncated || !utf8.ValidString(out.Content) || out.FinalURL != "" {
				t.Fatalf("%+v %v", out, err)
			}
		} else if err == nil {
			t.Fatalf("invalid response accepted %s", body)
		}
		if calls != 1 {
			t.Fatal("retried")
		}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "http://127.0.0.1/private")
		w.WriteHeader(302)
	}))
	defer server.Close()
	_, err := NewService(nil, nil).deepseekFetch(context.Background(), store.SearchProvider{BaseURL: server.URL}, "secret", FetchRequest{URL: "https://example.com/"})
	if err == nil || !strings.Contains(err.Error(), "HTTP 302") {
		t.Fatalf("vendor redirect followed: %v", err)
	}
}
