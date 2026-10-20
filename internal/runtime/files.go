package runtime

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/ravenmk2/wispjar/internal/auth"
	"github.com/ravenmk2/wispjar/internal/gateway/respond"
	"github.com/ravenmk2/wispjar/internal/runtime/tools"
	"github.com/ravenmk2/wispjar/internal/session"
)

const fileTransferLimit = 32 * 1024 * 1024

func (h *Handler) SetComputers(computers ComputerService) { h.computers = computers }
func fileRoot(sess *session.Detail, scope string) (string, error) {
	switch scope {
	case "", "workspace":
		return session.WorkspaceDir(sess.ID), nil
	case "storage":
		return "/storage", nil
	case "agent":
		return "/agents/" + sess.AgentID + "/files", nil
	default:
		return "", &session.Error{Status: 400, Code: "request/invalid", Message: "scope 只支持 workspace、storage、agent"}
	}
}
func relativeFilePath(value string) (string, error) {
	if value == "" {
		return ".", nil
	}
	if path.IsAbs(value) || strings.Contains(value, "\\") || strings.ContainsRune(value, 0) {
		return "", fmt.Errorf("path 必须是相对路径")
	}
	for _, part := range strings.Split(value, "/") {
		if part == ".." {
			return "", fmt.Errorf("path 不允许包含父目录段（..）")
		}
	}
	return path.Clean(value), nil
}

type scopedFileOperation func(root, relative, op string, extra map[string]any) (fileResponse, error)

// withFiles keeps a durable maintenance occupant through any lazy initialization
// and the final file operation; pause/delete cannot race a detached HTTP handler.
func (h *Handler) withFiles(c *gin.Context, fn func(*session.Detail, scopedFileOperation, string)) {
	sess, err := h.sessions.Get(c.Request.Context(), auth.UID(c), c.Param("id"))
	if err != nil {
		fail(c, err)
		return
	}
	if sess.ComputerID == nil {
		fail(c, &session.Error{Status: 409, Code: "workspace/migration_required", Message: "此会话仍使用旧工作区，请先迁移"})
		return
	}
	if err = workspaceAvailable(sess.Workspace); err != nil {
		fail(c, err)
		return
	}
	if h.computers == nil {
		fail(c, &session.Error{Status: 503, Code: "computer/unavailable", Message: "云电脑服务不可用"})
		return
	}
	root, err := fileRoot(sess, c.DefaultQuery("scope", "workspace"))
	if err != nil {
		fail(c, err)
		return
	}
	computer, err := h.computers.EnsureRunning(c.Request.Context(), sess.OrgID, sess.UserID, *sess.ComputerID)
	if err != nil {
		fail(c, err)
		return
	}
	// Register against the session, so migrations and binding changes see this
	// occupant under the same session row lock as normal root turns.
	runID, err := h.computers.AcquireSessionUse(c.Request.Context(), sess.OrgID, sess.UserID, computer.ID, sess.ID, "files", computer.Generation)
	if err != nil {
		fail(c, err)
		return
	}
	handle, err := h.computers.Handle(c.Request.Context(), sess.OrgID, sess.UserID, computer.ID)
	if err != nil {
		_ = h.computers.FinishRun(context.Background(), runID, sess.UserID, "failed")
		fail(c, err)
		return
	}
	execution := tools.NewExecutionHandle(handle)
	initializationOK := true
	defer func() {
		if initializationOK && execution.CommandsStopped() {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_ = h.computers.FinishRun(ctx, runID, sess.UserID, "completed")
		}
	}()
	operate := func(root, relative, op string, extra map[string]any) (fileResponse, error) {
		result, err := remoteFileOperation(c.Request.Context(), execution, root, relative, op, extra)
		var fileErr *session.Error
		if !errors.As(err, &fileErr) || fileErr.Code != "file/root_not_found" {
			return result, err
		}
		// Existing workspaces are read without root maintenance. A new session
		// may not have its directories yet; initialize only on that cold path.
		if err = h.initializeFileWorkspace(c.Request.Context(), handle, sess, runID); err != nil {
			// initializeFileWorkspace releases a confirmed failure itself, and
			// retains occupancy when remote completion is unknown.
			initializationOK = false
			return fileResponse{}, err
		}
		return remoteFileOperation(c.Request.Context(), execution, root, relative, op, extra)
	}
	fn(sess, operate, root)
}

func (h *Handler) initializeFileWorkspace(ctx context.Context, handle tools.Handle, sess *session.Detail, runID string) error {
	err := initializeWorkspace(ctx, handle, sess.ID, []string{sess.AgentID})
	if err != nil && controlCommandStopped(err) {
		// The request context may already be canceled. Cleanup still needs its
		// own bounded lifetime after a confirmed remote failure.
		finishCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = h.computers.FinishRun(finishCtx, runID, sess.UserID, "failed")
	}
	// Only unknown completion retains the maintenance occupant for recovery.
	return err
}

type fileResponse struct {
	Root      string           `json:"root"`
	Path      string           `json:"path"`
	Items     []map[string]any `json:"items,omitempty"`
	Data      string           `json:"data,omitempty"`
	Truncated bool             `json:"truncated,omitempty"`
	Bytes     int64            `json:"bytes,omitempty"`
	SHA256    string           `json:"sha256,omitempty"`
}

func remoteFileOperation(ctx context.Context, h *tools.ExecutionHandle, root, relative, op string, extra map[string]any) (fileResponse, error) {
	args := map[string]any{"root": root, "path": relative, "op": op, "max_bytes": fileTransferLimit}
	for key, value := range extra {
		args[key] = value
	}
	raw, _ := json.Marshal(args)
	res, err := h.RunPayload(ctx, scopedFilePython, raw, 120000)
	if err != nil {
		return fileResponse{}, err
	}
	if res.ExitCode != 0 {
		code, status, msg := "file/invalid", 400, "文件路径不可访问或操作失败"
		if res.ExitCode == 44 {
			code, status, msg = "file/not_found", 404, "文件不存在"
		}
		if res.ExitCode == 45 {
			code, status, msg = "file/root_not_found", 404, "文件根目录尚未创建"
		}
		if res.ExitCode == 49 {
			code, status, msg = "file/conflict", 409, "目标已存在"
		}
		if res.ExitCode == 43 {
			code, status, msg = "file/too_large", 413, "文件超过传输限制"
		}
		return fileResponse{}, &session.Error{Status: status, Code: code, Message: msg}
	}
	var response fileResponse
	err = json.Unmarshal([]byte(res.Stdout), &response)
	return response, err
}
func requestedRelative(c *gin.Context, value string) (string, bool) {
	relative, err := relativeFilePath(value)
	if err != nil {
		respond.Error(c, 400, respond.CodeInvalid, err.Error(), nil)
		return "", false
	}
	return relative, true
}
func (h *Handler) Files(c *gin.Context) {
	relative, ok := requestedRelative(c, c.DefaultQuery("path", "."))
	if !ok {
		return
	}
	h.withFiles(c, func(_ *session.Detail, operate scopedFileOperation, root string) {
		result, err := operate(root, relative, "list", nil)
		if err != nil {
			fail(c, err)
			return
		}
		if result.Items == nil {
			result.Items = []map[string]any{}
		}
		c.JSON(200, gin.H{"root": root, "path": relative, "items": result.Items})
	})
}
func (h *Handler) File(c *gin.Context) {
	relative, ok := requestedRelative(c, c.Query("path"))
	if !ok {
		return
	}
	limit := 256 * 1024
	if c.Query("download") == "1" {
		limit = fileTransferLimit
	} else if raw := c.Query("max_bytes"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 4*1024*1024 {
			respond.Error(c, 400, respond.CodeInvalid, "max_bytes 必须是 1..4194304", nil)
			return
		}
		limit = n
	}
	h.withFiles(c, func(_ *session.Detail, operate scopedFileOperation, root string) {
		result, err := operate(root, relative, "read", map[string]any{"max_bytes": limit})
		if err != nil {
			fail(c, err)
			return
		}
		data, err := base64.StdEncoding.DecodeString(result.Data)
		if err != nil {
			fail(c, err)
			return
		}
		if c.Query("download") == "1" {
			if result.Truncated {
				respond.Error(c, 413, "file/too_large", "文件超过下载限制", nil)
				return
			}
			c.Header("Content-Disposition", fmt.Sprintf("attachment; filename*=UTF-8''%s", urlPathEscape(path.Base(relative))))
			c.Data(200, "application/octet-stream", data)
			return
		}
		if !utf8.Valid(data) {
			c.Data(200, "application/octet-stream", data)
			return
		}
		c.JSON(200, gin.H{"root": root, "path": relative, "content": string(data), "truncated": result.Truncated})
	})
}
func urlPathEscape(value string) string {
	var out strings.Builder
	for _, b := range []byte(value) {
		if (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') || strings.ContainsRune("-._", rune(b)) {
			out.WriteByte(b)
		} else {
			fmt.Fprintf(&out, "%%%02X", b)
		}
	}
	return out.String()
}
func (h *Handler) UploadFile(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, fileTransferLimit+1024*1024)
	file, err := c.FormFile("file")
	if err != nil {
		respond.Error(c, 400, respond.CodeInvalid, "file 必填或超过 32MB", nil)
		return
	}
	if file.Size > fileTransferLimit {
		respond.Error(c, 413, "file/too_large", "文件超过 32MB", nil)
		return
	}
	filename := file.Filename
	if filename == "" || path.Base(filename) != filename || strings.Contains(filename, "\\") {
		respond.Error(c, 400, respond.CodeInvalid, "文件名无效", nil)
		return
	}
	dir := c.DefaultPostForm("path", "inputs")
	relative, ok := requestedRelative(c, dir)
	if !ok {
		return
	}
	relative = path.Join(relative, filename)
	stream, err := file.Open()
	if err != nil {
		fail(c, err)
		return
	}
	defer func() { _ = stream.Close() }()
	data, err := io.ReadAll(io.LimitReader(stream, fileTransferLimit+1))
	if err != nil {
		fail(c, err)
		return
	}
	if len(data) > fileTransferLimit {
		respond.Error(c, 413, "file/too_large", "文件超过 32MB", nil)
		return
	}
	h.withFiles(c, func(_ *session.Detail, operate scopedFileOperation, root string) {
		result, err := operate(root, relative, "write", map[string]any{"data": base64.StdEncoding.EncodeToString(data)})
		if err != nil {
			fail(c, err)
			return
		}
		c.JSON(201, result)
	})
}
func (h *Handler) SaveToStorage(c *gin.Context) {
	var req struct {
		Source      string `json:"source"`
		Destination string `json:"destination"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Source == "" || req.Destination == "" {
		respond.Error(c, 400, respond.CodeInvalid, "source 和 destination 必填", nil)
		return
	}
	source, ok := requestedRelative(c, req.Source)
	if !ok {
		return
	}
	destination, ok := requestedRelative(c, req.Destination)
	if !ok {
		return
	}
	h.withFiles(c, func(sess *session.Detail, operate scopedFileOperation, _ string) {
		result, err := operate(session.WorkspaceDir(sess.ID), source, "copy", map[string]any{"destination": destination})
		if err != nil {
			fail(c, err)
			return
		}
		c.JSON(201, result)
	})
}

// Every component is opened relative to a pinned directory FD with NOFOLLOW;
// symlink swaps cannot redirect reads, uploads, or cross-volume copies.
const scopedFilePython = `
import os,sys,json,base64,stat,hashlib,datetime,secrets
p=json.load(open(sys.argv[1]))
flags=os.O_RDONLY|os.O_DIRECTORY|os.O_NOFOLLOW
def parts(value):
    if "\\" in value or "\0" in value or value.startswith("/") or ".." in value.split("/"): raise ValueError("invalid relative path")
    return [x for x in value.split("/") if x not in ("",".")]
def rootfd(root):
    fd=os.open("/",flags)
    for part in parts(root.lstrip("/")):
        try: nxt=os.open(part,flags,dir_fd=fd)
        except FileNotFoundError: os.close(fd);sys.exit(45)
        os.close(fd);fd=nxt
    return fd
def parent(root,relative,create=False):
    fd=rootfd(root);components=parts(relative)
    for part in components[:-1]:
        if create:
            try: os.mkdir(part,0o755,dir_fd=fd)
            except FileExistsError: pass
        nxt=os.open(part,flags,dir_fd=fd);os.close(fd);fd=nxt
    return fd,components[-1] if components else "."
def readfile(root,relative):
    directory,name=parent(root,relative)
    fd=os.open(name,os.O_RDONLY|os.O_NOFOLLOW|os.O_NONBLOCK,dir_fd=directory);os.close(directory)
    if not stat.S_ISREG(os.fstat(fd).st_mode): os.close(fd);raise ValueError("not a regular file")
    return os.fdopen(fd,"rb")
def publish(root,relative,data):
    directory,name=parent(root,relative,True)
    temporary=".wispjar-upload-"+secrets.token_hex(16)
    fd=os.open(temporary,os.O_WRONLY|os.O_CREAT|os.O_EXCL|os.O_NOFOLLOW,0o644,dir_fd=directory)
    try:
        with os.fdopen(fd,"wb") as f: f.write(data);f.flush();os.fsync(f.fileno())
        # link is an atomic no-clobber publication on the destination volume.
        os.link(temporary,name,src_dir_fd=directory,dst_dir_fd=directory,follow_symlinks=False)
        os.fsync(directory)
        verify=os.open(name,os.O_RDONLY|os.O_NOFOLLOW,dir_fd=directory)
        with os.fdopen(verify,"rb") as f:
            if hashlib.sha256(f.read()).digest()!=hashlib.sha256(data).digest(): raise ValueError("copy verification failed")
    finally:
        os.unlink(temporary,dir_fd=directory);os.close(directory)
result={"root":p["root"],"path":p["path"]}
try:
    if p["op"]=="list":
        directory,name=parent(p["root"],p["path"]);fd=os.open(name,flags,dir_fd=directory);os.close(directory)
        items=[]
        for name in sorted(os.listdir(fd)):
            s=os.stat(name,dir_fd=fd,follow_symlinks=False)
            # File APIs do not follow links, and do not advertise them as files.
            if not stat.S_ISREG(s.st_mode) and not stat.S_ISDIR(s.st_mode): continue
            relative="/".join(parts(p["path"])+[name])
            items.append({"name":name,"path":relative,"type":"dir" if stat.S_ISDIR(s.st_mode) else "file","size":s.st_size,"modified_at":datetime.datetime.fromtimestamp(s.st_mtime,datetime.timezone.utc).isoformat()})
        os.close(fd);result["items"]=items
    elif p["op"]=="read":
        with readfile(p["root"],p["path"]) as f: data=f.read(p["max_bytes"]+1)
        result["truncated"]=len(data)>p["max_bytes"];result["data"]=base64.b64encode(data[:p["max_bytes"]]).decode()
    elif p["op"]=="write":
        data=base64.b64decode(p["data"],validate=True)
        if len(data)>p["max_bytes"]:sys.exit(43)
        publish(p["root"],p["path"],data);result["bytes"]=len(data)
    elif p["op"]=="copy":
        with readfile(p["root"],p["path"]) as f:data=f.read(p["max_bytes"]+1)
        if len(data)>p["max_bytes"]:sys.exit(43)
        publish("/storage",p["destination"],data)
        result={"root":"/storage","path":p["destination"],"bytes":len(data),"sha256":hashlib.sha256(data).hexdigest()}
    print(json.dumps(result))
except FileNotFoundError:sys.exit(44)
except FileExistsError:sys.exit(49)
`
