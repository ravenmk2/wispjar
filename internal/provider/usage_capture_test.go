package provider

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// doCapture 经 usageCaptureTransport 向 handler 发一次请求，读完响应并返回 sink 最新条目。
func doCapture(t *testing.T, adapter ProviderAdapter, withSink bool, handler http.HandlerFunc) (*UsageSink, string) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	sink := NewUsageSink()
	ctx := context.Background()
	if withSink {
		ctx = WithUsageSink(ctx, sink)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	cli := &http.Client{Transport: newUsageCaptureTransport(nil, adapter)}
	resp, err := cli.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return sink, string(body)
}

func TestCaptureNonStreamJSON(t *testing.T) {
	sink, body := doCapture(t, deepseekAdapter{}, true, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"x","usage":{"prompt_tokens":200,"completion_tokens":10,` +
			`"prompt_cache_hit_tokens":128,"prompt_cache_miss_tokens":72}}`))
	})
	if !strings.Contains(body, `"prompt_cache_hit_tokens":128`) {
		t.Fatalf("body must pass through intact: %s", body)
	}
	got := sink.TakeLatest()
	if got == nil {
		t.Fatal("no captured usage")
	}
	if !got.Reported || got.CacheRead != 128 || got.CacheWrite != 0 {
		t.Fatalf("want (128,0,true), got %+v", got)
	}
}

func TestCaptureStreamSSE(t *testing.T) {
	sse := "data: {\"id\":\"c1\",\"choices\":[{\"delta\":{\"content\":\"he\"}}]}\n\n" +
		"data: {\"id\":\"c1\",\"choices\":[{\"delta\":{\"content\":\"llo\"}}]}\n\n" +
		"data: {\"id\":\"c1\",\"choices\":[],\"usage\":{\"prompt_tokens\":3000,\"completion_tokens\":2," +
		"\"prompt_tokens_details\":{\"cached_tokens\":2048,\"cache_creation_input_tokens\":512}}}\n\n" +
		"data: [DONE]\n\n"
	sink, body := doCapture(t, qwenAdapter{}, true, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, sse)
	})
	if body != sse {
		t.Fatalf("SSE stream must pass through byte-identical:\nwant %q\ngot  %q", sse, body)
	}
	got := sink.TakeLatest()
	if got == nil {
		t.Fatal("no captured usage")
	}
	if !got.Reported || got.CacheRead != 2048 || got.CacheWrite != 512 {
		t.Fatalf("want (2048,512,true), got %+v", got)
	}
	if again := sink.TakeLatest(); again != nil {
		t.Fatalf("entry must be consumed once, got %+v", again)
	}
}

func TestCaptureSkipsWithoutSink(t *testing.T) {
	sink, body := doCapture(t, arkAdapter{}, false, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"usage":{"prompt_tokens_details":{"cached_tokens":100}}}`))
	})
	if !strings.Contains(body, "cached_tokens") {
		t.Fatalf("body must pass through intact: %s", body)
	}
	if got := sink.TakeLatest(); got != nil {
		t.Fatalf("no sink in ctx, must not capture, got %+v", got)
	}
}

func TestCaptureSkipsErrorStatus(t *testing.T) {
	sink, _ := doCapture(t, arkAdapter{}, true, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":"rate limited","usage":{"prompt_tokens_details":{"cached_tokens":100}}}`))
	})
	if got := sink.TakeLatest(); got != nil {
		t.Fatalf("error responses must not be captured, got %+v", got)
	}
}

func TestCaptureToleratesMalformed(t *testing.T) {
	sink, body := doCapture(t, compatAdapter{}, true, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"usage":`)) // 截断的畸形 JSON
	})
	if body != `{"usage":` {
		t.Fatalf("body must pass through intact: %q", body)
	}
	if got := sink.TakeLatest(); got != nil {
		t.Fatalf("malformed body must not capture, got %+v", got)
	}
}

func TestCaptureSSENullUsageSkipped(t *testing.T) {
	sse := "data: {\"id\":\"c1\",\"usage\":null}\n\n" +
		"data: [DONE]\n\n"
	sink, body := doCapture(t, compatAdapter{}, true, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, sse)
	})
	if body != sse {
		t.Fatalf("SSE stream must pass through byte-identical: %q", body)
	}
	if got := sink.TakeLatest(); got != nil {
		t.Fatalf("usage:null must not capture, got %+v", got)
	}
}
