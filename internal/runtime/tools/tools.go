package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"

	"github.com/ravenmk2/wispjar/internal/agent"
)

var errSandboxUnavailable = errors.New("execution environment unavailable; please retry once")

const (
	execMaxOutput     = 64 * 1024
	readMaxBytes      = 2 * 1024 * 1024
	globMaxResults    = 100
	grepMaxLines      = 250
	skillMaxBytes     = 32 * 1024
	writeDiffMaxCells = 4_000_000
	defaultWorkDir    = "/workspace"
)

type execArgs struct {
	Command   string            `json:"command" jsonschema:"description=shell command to execute (bash when available, otherwise sh)"`
	Cwd       string            `json:"cwd,omitempty" jsonschema:"description=working directory, defaults to this session workspace"`
	TimeoutMs int64             `json:"timeout_ms,omitempty" jsonschema:"description=timeout in milliseconds, default 30000, max 600000"`
	Env       map[string]string `json:"env,omitempty" jsonschema:"description=additional environment variables"`
}

func newExecTool() (tool.BaseTool, error) {
	return utils.InferTool("exec",
		"Execute a shell command (bash when available, otherwise sh). Returns exit code, stdout and stderr.",
		func(ctx context.Context, in execArgs) (string, error) {
			tc := FromContext(ctx)
			meta := tc.beginMeta(ctx, RenderTerminal)
			start := time.Now()
			defer func() { meta.DurationMS = sinceMS(start) }()
			h, sid, err := tc.sandboxHandle(ctx)
			if err != nil {
				return execErrorResult(err)
			}
			meta.SandboxID = sid
			cwd := in.Cwd
			cwd = tc.ResolvePath(cwd)
			timeout := in.TimeoutMs
			if timeout <= 0 {
				timeout = 30000
			}
			if timeout > 600000 {
				timeout = 600000
			}
			env := make(map[string]string, len(tc.Env)+len(in.Env))
			for k, v := range tc.Env {
				env[k] = v
			}
			for k, v := range in.Env {
				env[k] = v
			}
			res, err := h.RunCommand(ctx, in.Command, cwd, env, timeout,
				func(stream, chunk string) { tc.publish(ctx, stream, chunk) })
			if err != nil {
				return execErrorResult(err)
			}
			stdout, outTrunc := tailTruncate(res.Stdout, execMaxOutput)
			stderr, errTrunc := tailTruncate(res.Stderr, execMaxOutput)
			meta.Truncated = outTrunc || errTrunc
			return marshalResult(map[string]any{
				"exit_code": res.ExitCode,
				"stdout":    stdout,
				"stderr":    stderr,
				"truncated": meta.Truncated,
			})
		})
}

type readArgs struct {
	Path   string `json:"path" jsonschema:"description=file path, absolute or relative to the session workspace"`
	Offset int    `json:"offset,omitempty" jsonschema:"description=start line (1-based), default 1"`
	Limit  int    `json:"limit,omitempty" jsonschema:"description=max lines to return, default 200, max 2000"`
}

func newReadTool() (tool.BaseTool, error) {
	return utils.InferTool("read",
		"Read a text file with line numbers. Supports paging via offset/limit.",
		func(ctx context.Context, in readArgs) (string, error) {
			tc := FromContext(ctx)
			meta := tc.beginMeta(ctx, RenderRead)
			start := time.Now()
			defer func() { meta.DurationMS = sinceMS(start) }()
			h, sid, err := tc.sandboxHandle(ctx)
			if err != nil {
				return "", err
			}
			meta.SandboxID = sid
			in.Path = tc.ResolvePath(in.Path)
			if fi, err := h.FileInfo(ctx, in.Path); err == nil && fi.Size > readMaxBytes {
				return "", fmt.Errorf("file %s is %d bytes (>2MB); use exec with head/tail/sed to read it in segments", in.Path, fi.Size)
			}
			data, err := h.DownloadFile(ctx, in.Path)
			if err != nil {
				return "", fmt.Errorf("read %s: %w", in.Path, err)
			}
			if len(data) > readMaxBytes {
				return "", fmt.Errorf("file %s is %d bytes (>2MB); use exec with head/tail/sed to read it in segments", in.Path, len(data))
			}
			if !utf8.Valid(data) {
				return "", fmt.Errorf("file %s is not valid UTF-8; use exec with xxd to inspect binary content", in.Path)
			}
			offset := in.Offset
			if offset < 1 {
				offset = 1
			}
			limit := in.Limit
			if limit <= 0 {
				limit = 200
			}
			if limit > 2000 {
				limit = 2000
			}
			lines := strings.Split(string(data), "\n")
			if len(lines) > 0 && lines[len(lines)-1] == "" {
				lines = lines[:len(lines)-1]
			}
			total := len(lines)
			if offset > total {
				lines = nil
			} else {
				lines = lines[offset-1:]
			}
			truncated := len(lines) > limit
			if truncated {
				lines = lines[:limit]
			}
			meta.Truncated = truncated
			var b strings.Builder
			for i, ln := range lines {
				fmt.Fprintf(&b, "%d\t%s\n", offset+i, ln)
			}
			return marshalResult(map[string]any{
				"path":        in.Path,
				"content":     b.String(),
				"total_lines": total,
				"truncated":   truncated,
			})
		})
}

type writeArgs struct {
	Path    string `json:"path" jsonschema:"description=file path, absolute or relative to the session workspace"`
	Content string `json:"content" jsonschema:"description=full file content (overwrites existing file)"`
}

func newWriteTool() (tool.BaseTool, error) {
	return utils.InferTool("write",
		"Write a file (overwrite). Parent directories are created automatically.",
		func(ctx context.Context, in writeArgs) (string, error) {
			tc := FromContext(ctx)
			meta := tc.beginMeta(ctx, RenderDiff)
			start := time.Now()
			defer func() { meta.DurationMS = sinceMS(start) }()
			h, sid, err := tc.sandboxHandle(ctx)
			if err != nil {
				return "", err
			}
			meta.SandboxID = sid
			in.Path = tc.ResolvePath(in.Path)
			var old, operation string
			oldKnown := false
			if fi, err := h.FileInfo(ctx, in.Path); err == nil && fi.Type != "directory" {
				operation = "update"
				if fi.Size <= readMaxBytes {
					if data, err := h.DownloadFile(ctx, in.Path); err == nil && len(data) <= readMaxBytes && utf8.Valid(data) {
						old, oldKnown = string(data), true
					}
				}
			} else if errors.Is(err, os.ErrNotExist) {
				operation = "create"
			}
			if dir := path.Dir(in.Path); dir != "/" && dir != "." {
				if _, err := h.RunCommand(ctx, "mkdir -p "+shellQuote(dir), "/", nil, 10000, nil); err != nil {
					return "", fmt.Errorf("create parent dir: %w", err)
				}
			}
			if err := h.UploadFile(ctx, in.Path, []byte(in.Content)); err != nil {
				return "", fmt.Errorf("write %s: %w", in.Path, err)
			}
			meta.Operation = operation
			// Diff 是可选展示信息；大文件直接由 UI 展示调用参数中的全文，避免 LCS 占用过多内存。
			if oldKnown && old != in.Content && len(in.Content) <= readMaxBytes &&
				int64(strings.Count(old, "\n")+1)*int64(strings.Count(in.Content, "\n")+1) <= writeDiffMaxCells {
				meta.Diff = UnifiedDiff(in.Path, old, in.Content)
			}
			result := map[string]any{"path": in.Path, "bytes": len(in.Content)}
			if operation != "" {
				result["operation"] = operation
			}
			return marshalResult(result)
		})
}

type editArgs struct {
	Path       string `json:"path" jsonschema:"description=file path, absolute or relative to the session workspace"`
	OldString  string `json:"old_string" jsonschema:"description=exact string to replace; must match exactly once unless replace_all is set"`
	NewString  string `json:"new_string" jsonschema:"description=replacement string"`
	ReplaceAll bool   `json:"replace_all,omitempty" jsonschema:"description=replace all occurrences, default false"`
}

func newEditTool() (tool.BaseTool, error) {
	return utils.InferTool("edit",
		"Edit a file by exact string replacement. old_string must match exactly once unless replace_all is true.",
		func(ctx context.Context, in editArgs) (string, error) {
			tc := FromContext(ctx)
			meta := tc.beginMeta(ctx, RenderDiff)
			start := time.Now()
			defer func() { meta.DurationMS = sinceMS(start) }()
			if in.OldString == "" {
				return "", errors.New("old_string must not be empty")
			}
			h, sid, err := tc.sandboxHandle(ctx)
			if err != nil {
				return "", err
			}
			meta.SandboxID = sid
			in.Path = tc.ResolvePath(in.Path)
			var old string
			if data, err := h.DownloadFile(ctx, in.Path); err == nil && utf8.Valid(data) {
				old = string(data)
			}
			n, err := h.ReplaceInFile(ctx, in.Path, in.OldString, in.NewString, in.ReplaceAll)
			if err != nil {
				return "", err
			}
			if n == 0 {
				return "", fmt.Errorf("old_string not found in %s; read the file and retry with exact content", in.Path)
			}
			if old != "" {
				var updated string
				if in.ReplaceAll {
					updated = strings.ReplaceAll(old, in.OldString, in.NewString)
				} else {
					updated = strings.Replace(old, in.OldString, in.NewString, 1)
				}
				if updated != old {
					meta.Diff = UnifiedDiff(in.Path, old, updated)
				}
			}
			return marshalResult(map[string]any{"path": in.Path, "replacements": n})
		})
}

type globArgs struct {
	Pattern string `json:"pattern" jsonschema:"description=glob pattern, e.g. **/*.go"`
	Path    string `json:"path,omitempty" jsonschema:"description=search root directory, defaults to this session workspace"`
}

func newGlobTool() (tool.BaseTool, error) {
	return utils.InferTool("glob",
		"Search file names by glob pattern. Returns matches sorted by modification time (newest first), up to 100 entries.",
		func(ctx context.Context, in globArgs) (string, error) {
			tc := FromContext(ctx)
			meta := tc.beginMeta(ctx, RenderSearch)
			start := time.Now()
			defer func() { meta.DurationMS = sinceMS(start) }()
			h, sid, err := tc.sandboxHandle(ctx)
			if err != nil {
				return "", err
			}
			meta.SandboxID = sid
			root := tc.ResolvePath(in.Path)
			infos, err := h.SearchFiles(ctx, root, in.Pattern)
			if err != nil {
				return "", fmt.Errorf("glob: %w", err)
			}
			sort.Slice(infos, func(i, j int) bool { return infos[i].ModifiedAt.After(infos[j].ModifiedAt) })
			truncated := len(infos) > globMaxResults
			if truncated {
				infos = infos[:globMaxResults]
			}
			meta.Truncated = truncated
			files := make([]map[string]any, 0, len(infos))
			for _, fi := range infos {
				files = append(files, map[string]any{
					"path":        fi.Path,
					"size":        fi.Size,
					"modified_at": fi.ModifiedAt.UTC().Format(time.RFC3339),
				})
			}
			return marshalResult(map[string]any{"files": files, "truncated": truncated})
		})
}

type grepArgs struct {
	Pattern    string `json:"pattern" jsonschema:"description=regular expression (ripgrep syntax)"`
	Path       string `json:"path,omitempty" jsonschema:"description=search root directory, defaults to this session workspace"`
	Glob       string `json:"glob,omitempty" jsonschema:"description=file name filter, e.g. *.go"`
	IgnoreCase bool   `json:"ignore_case,omitempty" jsonschema:"description=case-insensitive search, default false"`
	OutputMode string `json:"output_mode,omitempty" jsonschema:"description=content or files_with_matches, default content"`
}

func newGrepTool() (tool.BaseTool, error) {
	return utils.InferTool("grep",
		"Search file contents (ripgrep semantics). Returns matching lines as path:line:text, up to 250 lines.",
		func(ctx context.Context, in grepArgs) (string, error) {
			tc := FromContext(ctx)
			meta := tc.beginMeta(ctx, RenderSearch)
			start := time.Now()
			defer func() { meta.DurationMS = sinceMS(start) }()
			h, sid, err := tc.sandboxHandle(ctx)
			if err != nil {
				return "", err
			}
			meta.SandboxID = sid
			root := tc.ResolvePath(in.Path)
			bin, cmdStr := "rg", rgCommand(in, root)
			res, err := h.RunCommand(ctx, cmdStr, "/", nil, 30000, nil)
			if err != nil {
				return "", err
			}
			// 沙箱缺 rg（精简镜像，如 python:3.12）：同进程内降级 grep -rInE 重试。
			if rgUnavailable(res.ExitCode, res.Stderr) {
				bin, cmdStr = "grep", grepCommand(in, root)
				res, err = h.RunCommand(ctx, cmdStr, "/", nil, 30000, nil)
				if err != nil {
					return "", err
				}
			}
			// rg/grep: exit 1 = no matches, >1 = real error（经中间件归一化为文本结果）
			if res.ExitCode > 1 {
				return "", fmt.Errorf("%s failed (exit %d): %s", bin, res.ExitCode, tail(res.Stderr, 512))
			}
			out, truncated := headLines(res.Stdout, grepMaxLines)
			meta.Truncated = truncated
			matches := []string{}
			for _, ln := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
				if ln != "" {
					matches = append(matches, ln)
				}
			}
			return marshalResult(map[string]any{"matches": matches, "truncated": truncated})
		})
}

type activateSkillArgs struct {
	Name string `json:"name" jsonschema:"description=skill name from the available skills index"`
}

func newActivateSkillTool() (tool.BaseTool, error) {
	return utils.InferTool("skill",
		"Load the full instructions of a skill by name (SKILL.md content plus the file manifest of its package).",
		func(ctx context.Context, in activateSkillArgs) (string, error) {
			tc := FromContext(ctx)
			meta := tc.beginMeta(ctx, RenderGeneric)
			start := time.Now()
			defer func() { meta.DurationMS = sinceMS(start) }()
			var skill *agent.SnapshotSkill
			for i := range tc.Skills {
				if strings.EqualFold(tc.Skills[i].Name, in.Name) {
					skill = &tc.Skills[i]
					break
				}
			}
			if skill == nil {
				return "", fmt.Errorf("skill %q not found; use a name from the available skills index", in.Name)
			}
			if tc.SkillsDir != "" {
				h, sid, err := tc.sandboxHandle(ctx)
				if err != nil {
					return "", err
				}
				meta.SandboxID = sid
				dir := path.Join(tc.SkillsDir, skill.Name)
				data, err := h.DownloadFile(ctx, path.Join(dir, "SKILL.md"))
				if err != nil {
					return "", err
				}
				meta.Truncated = len(data) > skillMaxBytes
				if meta.Truncated {
					data = data[:skillMaxBytes]
				}
				files, err := h.SearchFiles(ctx, dir, "**/*")
				if err != nil {
					return "", err
				}
				return marshalResult(map[string]any{"name": skill.Name, "instructions": string(data), "files": files, "dir": dir + "/", "truncated": meta.Truncated})
			}
			dir, err := skillDir(tc.DataDir, tc.OrgID, skill.ID)
			if err != nil {
				return "", err
			}
			content, truncated, err := readSkillMD(dir)
			if err != nil {
				return "", fmt.Errorf("skill %q: %w", in.Name, err)
			}
			meta.Truncated = truncated
			files, _ := listSkillFiles(dir)
			if truncated {
				content += "\n\n[SKILL.md truncated at 32KB; use the read tool on specific files in the package]"
			}
			return marshalResult(map[string]any{
				"name":         skill.Name,
				"instructions": content,
				"files":        files,
				"dir":          "/skills/" + skill.Name + "/",
			})
		})
}

func marshalResult(v map[string]any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func tailTruncate(s string, max int) (string, bool) {
	if len(s) <= max {
		return s, false
	}
	return s[len(s)-max:], true
}

func tail(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[len(s)-max:]
}

func headLines(s string, max int) (string, bool) {
	lines := strings.SplitAfter(s, "\n")
	if len(lines) <= max {
		return s, false
	}
	return strings.Join(lines[:max], ""), true
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// execErrorResult exec 的 infra 错误（命令超时/沙箱不可达/启动失败）结构化结果：
// 与正常执行结果同构（JSON），render 仍走 terminal，模型可读 error 自行调整。
func execErrorResult(err error) (string, error) {
	return marshalResult(map[string]any{"error": err.Error(), "stderr": ""})
}

// rgUnavailable 判定 rg 不存在（exit 127 或 shell 报 command not found）。
func rgUnavailable(exitCode int, stderr string) bool {
	return exitCode == 127 ||
		strings.Contains(stderr, "command not found") ||
		strings.Contains(stderr, "not found")
}

func rgCommand(in grepArgs, root string) string {
	var cmd strings.Builder
	cmd.WriteString("rg --line-number --no-heading --color never")
	if in.IgnoreCase {
		cmd.WriteString(" --ignore-case")
	}
	if in.Glob != "" {
		cmd.WriteString(" -g " + shellQuote(in.Glob))
	}
	if in.OutputMode == "files_with_matches" {
		cmd.WriteString(" -l")
	}
	cmd.WriteString(" -- " + shellQuote(in.Pattern) + " " + shellQuote(root))
	return cmd.String()
}

// grepCommand rg 不可用时的降级命令；输出同为 path:line:text。
func grepCommand(in grepArgs, root string) string {
	var cmd strings.Builder
	cmd.WriteString("grep -rInE --color=never")
	if in.IgnoreCase {
		cmd.WriteString(" -i")
	}
	if in.Glob != "" {
		cmd.WriteString(" --include=" + shellQuote(in.Glob))
	}
	if in.OutputMode == "files_with_matches" {
		cmd.WriteString(" -l")
	}
	cmd.WriteString(" -- " + shellQuote(in.Pattern) + " " + shellQuote(root))
	return cmd.String()
}
