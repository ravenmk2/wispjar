package provider

import (
	"encoding/json"
	"testing"

	"github.com/cloudwego/eino-ext/components/model/openai"
)

func TestMergeParams(t *testing.T) {
	cases := []struct {
		name     string
		defaults json.RawMessage
		params   map[string]any
		want     map[string]any
	}{
		{
			name:     "nil defaults",
			defaults: nil,
			params:   map[string]any{"temperature": 0.5},
			want:     map[string]any{"temperature": 0.5},
		},
		{
			name:     "json null defaults",
			defaults: json.RawMessage("null"),
			params:   map[string]any{"temperature": 0.5},
			want:     map[string]any{"temperature": 0.5},
		},
		{
			name:     "invalid json defaults",
			defaults: json.RawMessage("{"),
			params:   map[string]any{"top_p": 1.0},
			want:     map[string]any{"top_p": 1.0},
		},
		{
			name:     "override wins",
			defaults: json.RawMessage(`{"temperature":0.7,"max_tokens":1024}`),
			params:   map[string]any{"temperature": 0.1},
			want:     map[string]any{"temperature": 0.1, "max_tokens": 1024.0},
		},
		{
			name:     "no params",
			defaults: json.RawMessage(`{"temperature":0.7}`),
			params:   nil,
			want:     map[string]any{"temperature": 0.7},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := mergeParams(tc.defaults, tc.params)
			if got == nil {
				t.Fatal("mergeParams returned nil map")
			}
			if len(got) != len(tc.want) {
				t.Fatalf("want %v, got %v", tc.want, got)
			}
			for k, v := range tc.want {
				if got[k] != v {
					t.Fatalf("key %s: want %v, got %v (all: %v)", k, v, got[k], got)
				}
			}
		})
	}
}

func TestApplyParams(t *testing.T) {
	t.Run("basic numeric params", func(t *testing.T) {
		cfg := &openai.ChatModelConfig{}
		applyParams(cfg, "deepseek", map[string]any{"temperature": 0.5, "top_p": 1.0, "max_tokens": 1024})
		if cfg.Temperature == nil || *cfg.Temperature != 0.5 {
			t.Fatalf("temperature not mapped: %+v", cfg)
		}
		if cfg.TopP == nil || *cfg.TopP != 1.0 {
			t.Fatalf("top_p not mapped: %+v", cfg)
		}
		if cfg.MaxTokens == nil || *cfg.MaxTokens != 1024 {
			t.Fatalf("max_tokens not mapped: %+v", cfg)
		}
	})
	t.Run("reasoning_effort passthrough", func(t *testing.T) {
		cfg := &openai.ChatModelConfig{}
		applyParams(cfg, "deepseek", map[string]any{"reasoning_effort": "max"})
		if cfg.ReasoningEffort != openai.ReasoningEffortLevel("max") {
			t.Fatalf("want passthrough, got %q", cfg.ReasoningEffort)
		}
		if len(cfg.ExtraFields) != 0 {
			t.Fatalf("non-qwen must not use ExtraFields, got %v", cfg.ExtraFields)
		}
	})
	t.Run("qwen thinking mapping", func(t *testing.T) {
		cases := []struct {
			effort  string
			enabled bool
			budget  int
		}{
			{"minimal", false, 0},
			{"low", true, 1024},
			{"medium", true, 8192},
			{"high", true, 32768},
			{"max", true, 32768},
		}
		for _, tc := range cases {
			cfg := &openai.ChatModelConfig{}
			applyParams(cfg, "qwen", map[string]any{"reasoning_effort": tc.effort})
			if cfg.ReasoningEffort != "" {
				t.Fatalf("%s: qwen must not set ReasoningEffort, got %q", tc.effort, cfg.ReasoningEffort)
			}
			en, ok := cfg.ExtraFields["enable_thinking"].(bool)
			if !ok || en != tc.enabled {
				t.Fatalf("%s: enable_thinking want %v, got %v", tc.effort, tc.enabled, cfg.ExtraFields["enable_thinking"])
			}
			if tc.enabled {
				b, ok := cfg.ExtraFields["thinking_budget"].(int)
				if !ok || b != tc.budget {
					t.Fatalf("%s: thinking_budget want %d, got %v", tc.effort, tc.budget, cfg.ExtraFields["thinking_budget"])
				}
			} else if _, ok := cfg.ExtraFields["thinking_budget"]; ok {
				t.Fatalf("%s: minimal must not set thinking_budget", tc.effort)
			}
		}
	})
	t.Run("qwen unknown effort ignored", func(t *testing.T) {
		cfg := &openai.ChatModelConfig{}
		applyParams(cfg, "qwen", map[string]any{"reasoning_effort": "ultra"})
		if cfg.ReasoningEffort != "" || len(cfg.ExtraFields) != 0 {
			t.Fatalf("unknown qwen effort must be ignored: %+v", cfg)
		}
	})
	t.Run("prompt_cache_key to ExtraFields", func(t *testing.T) {
		cfg := &openai.ChatModelConfig{}
		applyParams(cfg, "ark", map[string]any{"prompt_cache_key": "sess-1"})
		if cfg.ExtraFields["prompt_cache_key"] != "sess-1" {
			t.Fatalf("prompt_cache_key not mapped: %v", cfg.ExtraFields)
		}
	})
	t.Run("unknown keys ignored", func(t *testing.T) {
		cfg := &openai.ChatModelConfig{}
		applyParams(cfg, "deepseek", map[string]any{"foo": "bar", "n": 1})
		if cfg.ReasoningEffort != "" || len(cfg.ExtraFields) != 0 {
			t.Fatalf("unknown keys must be ignored: %+v", cfg)
		}
	})
}
