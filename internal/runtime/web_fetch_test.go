package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"
	"github.com/ravenmk2/wispjar/internal/agent"
	"github.com/ravenmk2/wispjar/internal/runtime/tools"
	"github.com/ravenmk2/wispjar/internal/search"
	"github.com/ravenmk2/wispjar/internal/session"
	"github.com/ravenmk2/wispjar/internal/store"
)

type fetchRecorder struct {
	selected []string
	requests []search.FetchRequest
	fail     bool
}

func (s *fetchRecorder) Fetch(_ context.Context, providerID string, in search.FetchRequest) (*search.FetchResponse, error) {
	s.selected = append(s.selected, providerID)
	s.requests = append(s.requests, in)
	if s.fail {
		return nil, errors.New("所选搜索实例已停用")
	}
	return &search.FetchResponse{URL: in.URL, ProviderID: providerID, ProviderName: "Test", Truncated: true, Title: "Source", Content: "evidence", Format: "markdown"}, nil
}

func TestWebFetchRootAndChildSelectionAndReplay(t *testing.T) {
	for _, nested := range []bool{false, true} {
		for _, explicit := range []bool{false, true} {
			for _, fail := range []bool{false, true} {
				name := "root"
				if nested {
					name = "child"
				}
				if explicit {
					name += "/explicit"
				} else {
					name += "/default"
				}
				if fail {
					name += "/failure"
				}
				t.Run(name, func(t *testing.T) {
					searchScript := func(call int, input []*schema.Message) []*schema.Message {
						if call == 1 {
							return toolCallChunks("search-one", "web_fetch", `{"url":"https://example.com/a","max_chars":100}`)
						}
						found := false
						for _, m := range input {
							if m.Role == schema.Tool && m.ToolCallID == "search-one" {
								found = true
								if fail && !strings.Contains(m.Content, "已停用") {
									t.Errorf("missing explicit failure: %s", m.Content)
								}
							}
						}
						if !found {
							t.Error("missing search evidence in next model input")
						}
						return textChunks("done", nil)
					}
					var env *testEnv
					if nested {
						e := newTaskTestEnv(t, session.ApprovalStrict, func(call int, _ []*schema.Message) []*schema.Message {
							if call == 1 {
								return toolCallChunks("delegate", "task", `{"subagent":"data-analyst","prompt":"search release notes"}`)
							}
							return textChunks("done", nil)
						}, searchScript)
						env = e.testEnv
					} else {
						env = newTestEnv(t, session.ApprovalStrict, searchScript)
					}
					var snap agent.Snapshot
					if err := json.Unmarshal(env.session.AgentSnapshot, &snap); err != nil {
						t.Fatal(err)
					}
					selected := ""
					if explicit {
						selected = "pinned-instance"
					}
					if nested {
						snap.SubAgents[0].ToolsEnabled = json.RawMessage(`{"web_fetch":true}`)
						if explicit {
							snap.SubAgents[0].FetchProviderID = &selected
						}
					} else {
						snap.ToolsEnabled = json.RawMessage(`{"web_fetch":true}`)
						if explicit {
							snap.FetchProviderID = &selected
						}
					}
					env.session.AgentSnapshot, _ = json.Marshal(snap)
					recorder := &fetchRecorder{fail: fail}
					o := env.orch.(*orchestrator)
					previous := o.deps.ToolContextFactory
					o.deps.ToolContextFactory = func(sess *store.Session, snap *agent.Snapshot) *tools.Context {
						tc := previous(sess, snap)
						tc.AgentID, tc.UserID = snap.AgentID, sess.UserID
						tc.WebFetch = recorder
						return tc
					}
					env.prompt(t, "search", "request-one")
					if err := env.orch.StartTurn(context.Background(), env.session.ID); err != nil {
						t.Fatal(err)
					}
					if len(recorder.selected) != 1 || recorder.selected[0] != selected {
						t.Fatalf("wrong instance or fallback: %+v", recorder.selected)
					}
					wantAgent := snap.AgentID
					if nested {
						wantAgent = snap.SubAgents[0].ID
					}
					if r := recorder.requests[0]; r.AgentID != wantAgent || r.OrgID != env.session.OrgID || r.SessionID != env.session.ID || r.CallID != "search-one" {
						t.Fatalf("wrong search ownership: %+v", r)
					}
					found := false
					for _, ev := range env.es.byType(session.EventToolResult) {
						var r session.ToolResultData
						if err := json.Unmarshal(ev.Data, &r); err != nil {
							t.Fatal(err)
						}
						if r.CallID != "search-one" {
							continue
						}
						found = true
						if r.Meta == nil || r.Meta.Render != "web_fetch" {
							t.Fatalf("missing replay renderer: %+v", r)
						}
						if fail {
							if !strings.HasPrefix(r.Content, toolFailurePrefix) {
								t.Fatalf("failure not persisted: %+v", r)
							}
						} else if !strings.Contains(r.Content, "https://example.com/a") || !r.Meta.Truncated {
							t.Fatalf("missing replay evidence: %+v", r)
						}
					}
					if !found {
						t.Fatal("search result not persisted")
					}
					var end session.TurnEndData
					ends := env.es.byType(session.EventTurnEnd)
					if len(ends) != 1 {
						t.Fatalf("turn did not finish: %+v", env.es.types())
					}
					_ = json.Unmarshal(ends[0].Data, &end)
					if end.Reason != "completed" {
						t.Fatalf("search failure interrupted turn: %+v", end)
					}
				})
			}
		}
	}
}
