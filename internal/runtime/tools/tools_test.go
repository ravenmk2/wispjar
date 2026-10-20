package tools

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/tool"

	"github.com/ravenmk2/wispjar/internal/sandbox"
)

type memHandle struct {
	files       map[string][]byte
	exec        func(cmd string) (int, string, string)
	runErr      error
	infoErr     error
	downloadErr error
	uploadErr   error
}

func newMemHandle() *memHandle {
	return &memHandle{files: map[string][]byte{}, exec: func(cmd string) (int, string, string) {
		return 0, "", ""
	}}
}

func (h *memHandle) RunCommand(_ context.Context, cmd, cwd string, env map[string]string, timeoutMs int64, onEvent func(stream, chunk string)) (*sandbox.CommandResult, error) {
	if h.runErr != nil {
		return nil, h.runErr
	}
	code, out, errOut := h.exec(cmd)
	return &sandbox.CommandResult{ExitCode: code, Stdout: out, Stderr: errOut}, nil
}

func (h *memHandle) UploadFile(_ context.Context, p string, content []byte) error {
	if h.uploadErr != nil {
		return h.uploadErr
	}
	h.files[p] = append([]byte{}, content...)
	return nil
}

func (h *memHandle) DownloadFile(_ context.Context, p string) ([]byte, error) {
	if h.downloadErr != nil {
		return nil, h.downloadErr
	}
	return append([]byte{}, h.files[p]...), nil
}

func (h *memHandle) ReplaceInFile(_ context.Context, p, old, new string, replaceAll bool) (int, error) {
	content := string(h.files[p])
	n := strings.Count(content, old)
	if replaceAll {
		content = strings.ReplaceAll(content, old, new)
	} else {
		content = strings.Replace(content, old, new, 1)
	}
	h.files[p] = []byte(content)
	return n, nil
}

func (h *memHandle) SearchFiles(_ context.Context, dir, pattern string) ([]sandbox.FileInfo, error) {
	out := []sandbox.FileInfo{}
	for p, data := range h.files {
		if strings.HasPrefix(p, dir) {
			out = append(out, sandbox.FileInfo{Path: p, Type: "file", Size: int64(len(data)), ModifiedAt: time.Now()})
		}
	}
	return out, nil
}

func (h *memHandle) ListDir(_ context.Context, dir string) ([]sandbox.FileInfo, error) {
	return h.SearchFiles(context.Background(), dir, "*")
}

func (h *memHandle) FileInfo(_ context.Context, p string) (sandbox.FileInfo, error) {
	if h.infoErr != nil {
		return sandbox.FileInfo{}, h.infoErr
	}
	data, ok := h.files[p]
	if !ok {
		return sandbox.FileInfo{}, os.ErrNotExist
	}
	return sandbox.FileInfo{Path: p, Type: "file", Size: int64(len(data))}, nil
}

type memProvider struct{ h *memHandle }

func (p memProvider) Get(_ context.Context, sessionID string, opts sandbox.Options) (Handle, string, error) {
	return p.h, "mem-sandbox", nil
}

func testCtx(h *memHandle) context.Context {
	tc := NewContext("sess-1", "org-1")
	tc.Sandbox = memProvider{h: h}
	return WithContext(context.Background(), tc)
}

func invoke(t *testing.T, tt tool.BaseTool, ctx context.Context, args string) (string, error) {
	t.Helper()
	it, ok := tt.(tool.InvokableTool)
	if !ok {
		t.Fatal("tool is not invokable")
	}
	return it.InvokableRun(ctx, args)
}

func TestWriteOperationAndDiff(t *testing.T) {
	for _, tt := range []struct {
		name, before, after, operation  string
		exists, wantDiff                bool
		infoErr, downloadErr, uploadErr error
	}{
		{name: "create", after: "hello\n", operation: "create"},
		{name: "create empty", operation: "create"},
		{name: "overwrite", exists: true, before: "old\n", after: "new\n", operation: "update", wantDiff: true},
		{name: "overwrite empty", exists: true, after: "new\n", operation: "update", wantDiff: true},
		{name: "empty overwrite", exists: true, before: "old\n", operation: "update", wantDiff: true},
		{name: "identical", exists: true, before: "same", after: "same", operation: "update"},
		{name: "unreadable", exists: true, before: "old", after: "new", operation: "update", downloadErr: os.ErrPermission},
		{name: "unknown operation", after: "new", infoErr: os.ErrPermission},
		{name: "binary basis", exists: true, before: "\xff", after: "new", operation: "update"},
		{name: "large diff", exists: true, before: strings.Repeat("old\n", 2100), after: strings.Repeat("new\n", 2100), operation: "update"},
		{name: "upload failed", after: "new", uploadErr: os.ErrPermission},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h := newMemHandle()
			if tt.exists {
				h.files["/workspace/file.txt"] = []byte(tt.before)
			}
			h.infoErr, h.downloadErr, h.uploadErr = tt.infoErr, tt.downloadErr, tt.uploadErr
			ctx := testCtx(h)
			w, err := newWriteTool()
			if err != nil {
				t.Fatal(err)
			}
			args, _ := json.Marshal(writeArgs{Path: "/workspace/file.txt", Content: tt.after})
			out, err := invoke(t, w, ctx, string(args))
			meta := FromContext(ctx).TakeMeta("")
			if meta == nil {
				t.Fatal("missing write metadata")
			}
			if tt.uploadErr != nil {
				if err == nil || meta.Operation != "" || meta.Diff != "" {
					t.Fatalf("failed write: err=%v meta=%+v", err, meta)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var result struct {
				Operation string `json:"operation"`
				Bytes     int    `json:"bytes"`
			}
			if err := json.Unmarshal([]byte(out), &result); err != nil {
				t.Fatal(err)
			}
			if result.Operation != tt.operation || meta.Operation != tt.operation || (meta.Diff != "") != tt.wantDiff {
				t.Fatalf("operation=%q meta=%+v; want operation=%q diff=%v", result.Operation, meta, tt.operation, tt.wantDiff)
			}
			if result.Bytes != len(tt.after) || string(h.files["/workspace/file.txt"]) != tt.after {
				t.Fatal("write did not preserve the full content")
			}
		})
	}
}

func TestWriteEditReadFlow(t *testing.T) {
	h := newMemHandle()
	ctx := testCtx(h)

	w, err := newWriteTool()
	if err != nil {
		t.Fatal(err)
	}
	out, err := invoke(t, w, ctx, `{"path":"/workspace/a.txt","content":"hello\nworld\n"}`)
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if !strings.Contains(out, `"bytes":12`) {
		t.Fatalf("write result: %s", out)
	}

	e, err := newEditTool()
	if err != nil {
		t.Fatal(err)
	}
	out, err = invoke(t, e, ctx, `{"path":"/workspace/a.txt","old_string":"world","new_string":"wispjar"}`)
	if err != nil {
		t.Fatalf("edit: %v", err)
	}
	if !strings.Contains(out, `"replacements":1`) {
		t.Fatalf("edit result: %s", out)
	}
	if got := string(h.files["/workspace/a.txt"]); got != "hello\nwispjar\n" {
		t.Fatalf("edited content = %q", got)
	}
	tc := FromContext(ctx)
	metaFound := false
	tc.mu.Lock()
	for _, m := range tc.metas {
		if m.Render == RenderDiff && strings.Contains(m.Diff, "-world") && strings.Contains(m.Diff, "+wispjar") {
			metaFound = true
		}
	}
	tc.mu.Unlock()
	if !metaFound {
		t.Fatal("edit meta.diff missing expected unified diff")
	}

	if _, err := invoke(t, e, ctx, `{"path":"/workspace/a.txt","old_string":"nope","new_string":"x"}`); err == nil {
		t.Fatal("edit with missing old_string must fail")
	}

	r, err := newReadTool()
	if err != nil {
		t.Fatal(err)
	}
	out, err = invoke(t, r, ctx, `{"path":"/workspace/a.txt"}`)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var rr struct {
		Content    string `json:"content"`
		TotalLines int    `json:"total_lines"`
		Truncated  bool   `json:"truncated"`
	}
	if err := json.Unmarshal([]byte(out), &rr); err != nil {
		t.Fatal(err)
	}
	if rr.TotalLines != 2 || !strings.Contains(rr.Content, "1\thello") || !strings.Contains(rr.Content, "2\twispjar") {
		t.Fatalf("read result: %+v", rr)
	}
}

func TestExecTruncation(t *testing.T) {
	h := newMemHandle()
	h.exec = func(cmd string) (int, string, string) {
		return 0, strings.Repeat("x", execMaxOutput+100), ""
	}
	ctx := testCtx(h)
	x, err := newExecTool()
	if err != nil {
		t.Fatal(err)
	}
	out, err := invoke(t, x, ctx, `{"command":"spam"}`)
	if err != nil {
		t.Fatal(err)
	}
	var er struct {
		Stdout    string `json:"stdout"`
		Truncated bool   `json:"truncated"`
	}
	if err := json.Unmarshal([]byte(out), &er); err != nil {
		t.Fatal(err)
	}
	if !er.Truncated || len(er.Stdout) != execMaxOutput {
		t.Fatalf("exec truncation: truncated=%v len=%d", er.Truncated, len(er.Stdout))
	}
}

func TestGrepViaRg(t *testing.T) {
	h := newMemHandle()
	var gotCmd string
	h.exec = func(cmd string) (int, string, string) {
		gotCmd = cmd
		return 0, "a.go:1:package a\n", ""
	}
	ctx := testCtx(h)
	g, err := newGrepTool()
	if err != nil {
		t.Fatal(err)
	}
	out, err := invoke(t, g, ctx, `{"pattern":"package","path":"/workspace","glob":"*.go","ignore_case":true}`)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"rg --line-number --no-heading", "--ignore-case", "-g '*.go'", "-- 'package' '/workspace'"} {
		if !strings.Contains(gotCmd, want) {
			t.Fatalf("rg command %q missing %q", gotCmd, want)
		}
	}
	if !strings.Contains(out, "a.go:1:package a") {
		t.Fatalf("grep result: %s", out)
	}
}

func TestExecInfraErrorStructuredResult(t *testing.T) {
	h := newMemHandle()
	h.runErr = errors.New("sandbox unreachable")
	ctx := testCtx(h)
	x, err := newExecTool()
	if err != nil {
		t.Fatal(err)
	}
	out, err := invoke(t, x, ctx, `{"command":"ls"}`)
	if err != nil {
		t.Fatalf("exec infra error must not surface as Go error: %v", err)
	}
	var er struct {
		Error  string `json:"error"`
		Stderr string `json:"stderr"`
	}
	if err := json.Unmarshal([]byte(out), &er); err != nil {
		t.Fatalf("exec infra error result not JSON: %v (%s)", err, out)
	}
	if !strings.Contains(er.Error, "sandbox unreachable") {
		t.Fatalf("exec infra error result: %s", out)
	}
	if m := FromContext(ctx).TakeMeta(""); m == nil || m.Render != RenderTerminal {
		t.Fatalf("exec infra error meta render = %+v, want terminal", m)
	}
}

func TestGrepFallbackToGrep(t *testing.T) {
	h := newMemHandle()
	calls := []string{}
	h.exec = func(cmd string) (int, string, string) {
		calls = append(calls, cmd)
		if len(calls) == 1 {
			return 127, "", "bash: rg: command not found"
		}
		return 0, "a.go:1:package a\n", ""
	}
	ctx := testCtx(h)
	g, err := newGrepTool()
	if err != nil {
		t.Fatal(err)
	}
	out, err := invoke(t, g, ctx, `{"pattern":"package","path":"/workspace","glob":"*.go","ignore_case":true}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 2 {
		t.Fatalf("want 2 commands (rg then grep), got %v", calls)
	}
	if !strings.HasPrefix(calls[0], "rg ") {
		t.Fatalf("first command must be rg: %q", calls[0])
	}
	for _, want := range []string{"grep -rInE --color=never", "-i", "--include='*.go'", "-- 'package' '/workspace'"} {
		if !strings.Contains(calls[1], want) {
			t.Fatalf("grep fallback command %q missing %q", calls[1], want)
		}
	}
	if !strings.Contains(out, "a.go:1:package a") {
		t.Fatalf("grep fallback result: %s", out)
	}
}

func TestGrepFallbackAlsoFails(t *testing.T) {
	h := newMemHandle()
	calls := 0
	h.exec = func(cmd string) (int, string, string) {
		calls++
		if calls == 1 {
			return 127, "", "bash: rg: command not found"
		}
		return 2, "", "grep: /workspace: permission denied"
	}
	ctx := testCtx(h)
	g, err := newGrepTool()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := invoke(t, g, ctx, `{"pattern":"package"}`); err == nil ||
		!strings.Contains(err.Error(), "grep failed (exit 2)") {
		t.Fatalf("grep fallback failure want tool error, got %v", err)
	}
}

func TestUnifiedDiff(t *testing.T) {
	d := UnifiedDiff("/w/a.txt", "a\nb\nc\n", "a\nx\nc\n")
	for _, want := range []string{"--- a/w/a.txt", "+++ b/w/a.txt", "@@", "-b", "+x", " a", " c"} {
		if !strings.Contains(d, want) {
			t.Fatalf("diff missing %q:\n%s", want, d)
		}
	}
}
