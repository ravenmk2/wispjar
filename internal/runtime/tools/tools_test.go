package tools

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/tool"

	"github.com/ravenmk2/wispjar/internal/sandbox"
)

type memHandle struct {
	files map[string][]byte
	exec  func(cmd string) (int, string, string)
}

func newMemHandle() *memHandle {
	return &memHandle{files: map[string][]byte{}, exec: func(cmd string) (int, string, string) {
		return 0, "", ""
	}}
}

func (h *memHandle) RunCommand(_ context.Context, cmd, cwd string, env map[string]string, timeoutMs int64, onEvent func(stream, chunk string)) (*sandbox.CommandResult, error) {
	code, out, errOut := h.exec(cmd)
	return &sandbox.CommandResult{ExitCode: code, Stdout: out, Stderr: errOut}, nil
}

func (h *memHandle) UploadFile(_ context.Context, p string, content []byte) error {
	h.files[p] = append([]byte{}, content...)
	return nil
}

func (h *memHandle) DownloadFile(_ context.Context, p string) ([]byte, error) {
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

func TestUnifiedDiff(t *testing.T) {
	d := UnifiedDiff("/w/a.txt", "a\nb\nc\n", "a\nx\nc\n")
	for _, want := range []string{"--- a/w/a.txt", "+++ b/w/a.txt", "@@", "-b", "+x", " a", " c"} {
		if !strings.Contains(d, want) {
			t.Fatalf("diff missing %q:\n%s", want, d)
		}
	}
}
