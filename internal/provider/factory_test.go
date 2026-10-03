package provider

import (
	"encoding/json"
	"testing"
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
