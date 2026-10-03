package provider

import (
	"encoding/json"
	"testing"
)

func mustRaw(t *testing.T, s string) map[string]any {
	t.Helper()
	m := map[string]any{}
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		t.Fatalf("bad golden json: %v", err)
	}
	return m
}

func TestNormalizeUsage(t *testing.T) {
	cases := []struct {
		name       string
		adapter    ProviderAdapter
		raw        string
		wantRead   int64
		wantWrite  int64
		wantReport bool
	}{
		{
			name:    "deepseek top-level hit wins",
			adapter: deepseekAdapter{},
			raw: `{"prompt_tokens":2000,"completion_tokens":50,
				"prompt_cache_hit_tokens":1500,"prompt_cache_miss_tokens":500,
				"prompt_tokens_details":{"cached_tokens":1400}}`,
			wantRead: 1500, wantReport: true,
		},
		{
			name:       "deepseek fallback to cached_tokens",
			adapter:    deepseekAdapter{},
			raw:        `{"prompt_tokens":2000,"prompt_tokens_details":{"cached_tokens":1400}}`,
			wantRead:   1400,
			wantReport: true,
		},
		{
			name:       "deepseek no cache fields unreported",
			adapter:    deepseekAdapter{},
			raw:        `{"prompt_tokens":2000,"completion_tokens":50}`,
			wantReport: false,
		},
		{
			name:       "deepseek hit zero is reported",
			adapter:    deepseekAdapter{},
			raw:        `{"prompt_tokens":100,"prompt_cache_hit_tokens":0,"prompt_cache_miss_tokens":100}`,
			wantRead:   0,
			wantReport: true,
		},
		{
			name:       "ark standard cached_tokens",
			adapter:    arkAdapter{},
			raw:        `{"prompt_tokens":3000,"prompt_tokens_details":{"cached_tokens":2048}}`,
			wantRead:   2048,
			wantReport: true,
		},
		{
			name:       "ark unreported",
			adapter:    arkAdapter{},
			raw:        `{"prompt_tokens":3000,"completion_tokens":10}`,
			wantReport: false,
		},
		{
			name:    "qwen explicit cache creation",
			adapter: qwenAdapter{},
			raw: `{"prompt_tokens":5000,"prompt_tokens_details":{
				"cached_tokens":2048,"cache_creation_input_tokens":1024}}`,
			wantRead: 2048, wantWrite: 1024, wantReport: true,
		},
		{
			name:       "qwen implicit cache read only",
			adapter:    qwenAdapter{},
			raw:        `{"prompt_tokens":5000,"prompt_tokens_details":{"cached_tokens":3072}}`,
			wantRead:   3072,
			wantReport: true,
		},
		{
			name:       "qwen unreported",
			adapter:    qwenAdapter{},
			raw:        `{"prompt_tokens":5000,"prompt_tokens_details":{}}`,
			wantReport: false,
		},
		{
			name:       "compat standard field",
			adapter:    compatAdapter{},
			raw:        `{"prompt_tokens":1024,"prompt_tokens_details":{"cached_tokens":1024}}`,
			wantRead:   1024,
			wantReport: true,
		},
		{
			name:       "compat malformed details tolerated",
			adapter:    compatAdapter{},
			raw:        `{"prompt_tokens":1024,"prompt_tokens_details":"oops"}`,
			wantReport: false,
		},
		{
			name:       "compat malformed value tolerated",
			adapter:    compatAdapter{},
			raw:        `{"prompt_tokens_details":{"cached_tokens":"many"}}`,
			wantReport: false,
		},
		{
			name:       "compat empty usage tolerated",
			adapter:    compatAdapter{},
			raw:        `{}`,
			wantReport: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			read, write, reported := tc.adapter.NormalizeUsage(mustRaw(t, tc.raw))
			if read != tc.wantRead || write != tc.wantWrite || reported != tc.wantReport {
				t.Fatalf("want (%d,%d,%v), got (%d,%d,%v)",
					tc.wantRead, tc.wantWrite, tc.wantReport, read, write, reported)
			}
		})
	}
}

func TestAdapterCapabilities(t *testing.T) {
	cases := []struct {
		providerType     string
		wantReadField    string
		wantWriteField   string
		wantCacheKey     bool
		wantMinCacheToks int
	}{
		{"deepseek", "prompt_cache_hit_tokens", "", false, 0},
		{"ark", "prompt_tokens_details.cached_tokens", "", true, 1024},
		{"qwen", "prompt_tokens_details.cached_tokens", "prompt_tokens_details.cache_creation_input_tokens", false, 1024},
		{"openai_compat", "prompt_tokens_details.cached_tokens", "", false, 0},
		{"something-new", "prompt_tokens_details.cached_tokens", "", false, 0},
	}
	for _, tc := range cases {
		t.Run(tc.providerType, func(t *testing.T) {
			c := AdapterFor(tc.providerType).Capabilities()
			if c.UsageCacheReadField != tc.wantReadField ||
				c.UsageCacheWriteField != tc.wantWriteField ||
				c.SupportsPromptCacheKey != tc.wantCacheKey ||
				c.MinCacheTokens != tc.wantMinCacheToks {
				t.Fatalf("capabilities mismatch: %+v", c)
			}
		})
	}
}
