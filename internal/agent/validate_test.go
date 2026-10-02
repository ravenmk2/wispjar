package agent

import (
	"strings"
	"testing"

	"github.com/ravenmk2/wispjar/internal/store"
)

func TestValidateSubagentDAGSelfReference(t *testing.T) {
	err := validateSubagentDAG("a", []string{"b", "a"}, nil)
	if err == nil || !strings.Contains(err.Message, "自身") {
		t.Fatalf("want self-reference error, got %v", err)
	}
}

func TestValidateSubagentDAGDuplicate(t *testing.T) {
	err := validateSubagentDAG("a", []string{"b", "b"}, nil)
	if err == nil || !strings.Contains(err.Message, "重复") {
		t.Fatalf("want duplicate error, got %v", err)
	}
}

func TestValidateSubagentDAGCycle(t *testing.T) {
	childOf := map[string][]string{
		"b": {"c"},
		"c": {"a"},
	}
	err := validateSubagentDAG("a", []string{"b"}, childOf)
	if err == nil || !strings.Contains(err.Message, "循环") {
		t.Fatalf("want cycle error, got %v", err)
	}
}

func TestValidateSubagentDAGDirectCycle(t *testing.T) {
	childOf := map[string][]string{
		"b": {"a"},
	}
	err := validateSubagentDAG("a", []string{"b"}, childOf)
	if err == nil || !strings.Contains(err.Message, "循环") {
		t.Fatalf("want cycle error, got %v", err)
	}
}

func TestValidateSubagentDAGDepthLimit(t *testing.T) {
	childOf := map[string][]string{
		"b": {"c"},
	}
	err := validateSubagentDAG("a", []string{"b"}, childOf)
	if err == nil || !strings.Contains(err.Message, "1 层") {
		t.Fatalf("want depth-limit error, got %v", err)
	}
}

func TestValidateSubagentDAGOK(t *testing.T) {
	childOf := map[string][]string{
		"x": {"y"},
	}
	if err := validateSubagentDAG("a", []string{"b", "c"}, childOf); err != nil {
		t.Fatalf("want nil, got %v", err)
	}
	if err := validateSubagentDAG("a", nil, childOf); err != nil {
		t.Fatalf("want nil, got %v", err)
	}
}

func TestValidatePublishable(t *testing.T) {
	published := store.Agent{ID: "sub1", Status: StatusPublished}
	draft := store.Agent{ID: "sub2", Status: StatusDraft}

	cases := []struct {
		name        string
		agent       store.Agent
		modelActive bool
		subs        []store.Agent
		wantErr     string
	}{
		{"ok", store.Agent{SystemPrompt: "你是助手", ModelID: "m1"}, true, []store.Agent{published}, ""},
		{"ok no subagents", store.Agent{SystemPrompt: "你是助手", ModelID: "m1"}, true, nil, ""},
		{"empty prompt", store.Agent{SystemPrompt: "  ", ModelID: "m1"}, true, nil, "system_prompt"},
		{"model inactive", store.Agent{SystemPrompt: "p", ModelID: "m1"}, false, nil, "模型"},
		{"model missing", store.Agent{SystemPrompt: "p"}, true, nil, "模型"},
		{"subagent not published", store.Agent{SystemPrompt: "p", ModelID: "m1"}, true, []store.Agent{published, draft}, "published"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validatePublishable(&tc.agent, tc.modelActive, tc.subs)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("want nil, got %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Message, tc.wantErr) {
				t.Fatalf("want error containing %q, got %v", tc.wantErr, err)
			}
		})
	}
}
