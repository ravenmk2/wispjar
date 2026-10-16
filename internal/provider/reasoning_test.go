package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/schema"
)

func TestReasoningEffortRequests(t *testing.T) {
	efforts := []string{"none", "minimal", "low", "medium", "high", "xhigh", "max"}
	enabled := map[string]any{"type": "enabled"}
	disabled := map[string]any{"type": "disabled"}
	type requestCase struct {
		provider string
		model    string
		want     []map[string]any
	}
	cases := []requestCase{
		{"deepseek", "deepseek-flash", []map[string]any{
			{"thinking": disabled},
			{"thinking": enabled, "reasoning_effort": "low"},
			{"thinking": enabled, "reasoning_effort": "low"},
			{"thinking": enabled, "reasoning_effort": "high"},
			{"thinking": enabled, "reasoning_effort": "high"},
			{"thinking": enabled, "reasoning_effort": "high"},
			{"thinking": enabled, "reasoning_effort": "max"},
		}},
		{"qwen", "qwen-plus", []map[string]any{
			{"enable_thinking": false},
			{"enable_thinking": true, "thinking_budget": 128},
			{"enable_thinking": true, "thinking_budget": 1024},
			{"enable_thinking": true, "thinking_budget": 8192},
			{"enable_thinking": true, "thinking_budget": 16384},
			{"enable_thinking": true, "thinking_budget": 32768},
			{"enable_thinking": true},
		}},
		{"qwen", "qwen3.8-max-0902", []map[string]any{
			{"enable_thinking": false},
			{"enable_thinking": true, "reasoning_effort": "low"},
			{"enable_thinking": true, "reasoning_effort": "low"},
			{"enable_thinking": true, "reasoning_effort": "medium"},
			{"enable_thinking": true, "reasoning_effort": "xhigh"},
			{"enable_thinking": true, "reasoning_effort": "xhigh"},
			{"enable_thinking": true, "reasoning_effort": "xhigh"},
		}},
	}
	// 方舟按实际模型版本执行官方等效映射，包括无法从 ep-* 判断的接入点。
	for _, model := range []string{"doubao-seed-2-1-pro-260915", "ep-20261016000000-fixture"} {
		cases = append(cases, requestCase{provider: "ark", model: model})
	}
	cases = append(cases, requestCase{provider: "openai_compat", model: "fixture"})
	for _, tc := range cases {
		for i, effort := range efforts {
			t.Run(tc.provider+"/"+tc.model+"/"+effort, func(t *testing.T) {
				want := map[string]any{"reasoning_effort": effort}
				if tc.want != nil {
					want = tc.want[i]
				}
				assertReasoningRequest(t, tc.provider, tc.model, map[string]any{"reasoning_effort": effort}, want)
			})
		}
	}
}

func TestNoImplicitReasoningEffort(t *testing.T) {
	for _, provider := range []string{"deepseek", "ark", "qwen", "openai_compat"} {
		assertReasoningRequest(t, provider, "fixture", nil, map[string]any{})
		for _, effort := range []any{nil, "", "ultra", 1} {
			assertReasoningRequest(t, provider, "fixture", map[string]any{"reasoning_effort": effort}, map[string]any{})
		}
	}
}

// 验证真实 SDK 序列化后的 HTTP 请求，而不只断言配置结构。
func assertReasoningRequest(t *testing.T, provider, model string, params, want map[string]any) {
	t.Helper()
	requests := make(chan map[string]any, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		requests <- body
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"fixture","object":"chat.completion","model":"fixture","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`))
	}))
	defer server.Close()
	cfg := &openai.ChatModelConfig{Model: model, APIKey: "fixture", BaseURL: server.URL}
	AdapterFor(provider).ApplyParams(cfg, params)
	cm, err := openai.NewChatModel(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = cm.Generate(context.Background(), []*schema.Message{schema.UserMessage("ping")}); err != nil {
		t.Fatal(err)
	}
	body := <-requests
	got := map[string]any{}
	for _, key := range []string{"reasoning_effort", "thinking", "enable_thinking", "thinking_budget"} {
		if value, ok := body[key]; ok {
			got[key] = value
		}
	}
	// 统一 JSON 中数字的类型。
	raw, _ := json.Marshal(want)
	var normalized map[string]any
	_ = json.Unmarshal(raw, &normalized)
	if !reflect.DeepEqual(got, normalized) {
		t.Fatalf("%s/%s: want %v, got %v", provider, model, normalized, got)
	}
}

func TestParseReasoningCapabilities(t *testing.T) {
	for _, raw := range []string{
		`{"reasoning":true,"reasoning_efforts":["none","minimal","low","medium","high","xhigh","max"],"reasoning_effort_default":"none"}`,
		`{"reasoning":true,"reasoning_efforts":["xhigh","max"],"reasoning_effort_default":"xhigh"}`,
		`{"reasoning":true}`,
	} {
		if _, err := parseCapabilities(json.RawMessage(raw)); err != nil {
			t.Fatalf("valid capabilities rejected: %s: %v", raw, err)
		}
	}
	for _, raw := range []string{
		`{"reasoning_efforts":["ultra"]}`,
		`{"reasoning_efforts":["high","high"]}`,
		`{"reasoning_efforts":[""]}`,
		`{"reasoning_efforts":[" high "]}`,
		`{"reasoning_efforts":["low"],"reasoning_effort_default":"none"}`,
	} {
		if _, err := parseCapabilities(json.RawMessage(raw)); err == nil {
			t.Fatalf("invalid capabilities accepted: %s", raw)
		}
	}
}
