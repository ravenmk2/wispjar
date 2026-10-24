package runtime

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/ravenmk2/wispjar/internal/auth"
	"github.com/ravenmk2/wispjar/internal/gateway/respond"
	"github.com/ravenmk2/wispjar/internal/runtime/tools"
	"github.com/ravenmk2/wispjar/internal/session"
)

type DownloadOptions struct {
	MaxBytes, MaxStagedBytes int64
	MaxEntries               int
	TTL, PrepareTimeout      time.Duration
}

type publicDownloadError string

func (e publicDownloadError) Error() string { return string(e) }

func downloadMessage(err error) string {
	if errors.Is(err, context.Canceled) {
		return "下载准备已取消"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "下载准备超时，请重试"
	}
	var public publicDownloadError
	if errors.As(err, &public) {
		return public.Error()
	}
	var resource *session.Error
	if errors.As(session.ComputerError(err), &resource) {
		return resource.Message
	}
	return "下载准备失败，请检查云电脑连接、可用磁盘空间和文件权限后重试"
}

func (h *Handler) SetDownloadOptions(options DownloadOptions) { h.downloadOptions = options }

func (h *Handler) downloadLimits() DownloadOptions {
	o := h.downloadOptions
	if o.MaxBytes == 0 {
		o.MaxBytes = 20 << 30
	}
	if o.MaxStagedBytes == 0 {
		o.MaxStagedBytes = 40 << 30
	}
	if o.MaxEntries == 0 {
		o.MaxEntries = 100000
	}
	if o.TTL == 0 {
		o.TTL = 30 * time.Minute
	}
	if o.PrepareTimeout == 0 {
		o.PrepareTimeout = 30 * time.Minute
	}
	return o
}

type fileDownload struct {
	ID                                                    string    `json:"id"`
	Status                                                string    `json:"status"`
	Name                                                  string    `json:"name"`
	Bytes                                                 int64     `json:"bytes"`
	Message                                               string    `json:"message,omitempty"`
	URL                                                   string    `json:"url,omitempty"`
	ExpiresAt                                             time.Time `json:"expires_at"`
	uid, sessionID, scope, source, directory, local, etag string
	archive                                               bool
	expires                                               time.Time
	cancel                                                context.CancelFunc
	readers                                               int
}

// CreateDownload authenticates a resource-scoped capability. The bearer JWT is
// never placed in a URL; the random capability grants only this fixed snapshot.
func (h *Handler) CreateDownload(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64*1024)
	var req struct {
		Path    string `json:"path"`
		Scope   string `json:"scope"`
		Archive bool   `json:"archive"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		respond.Error(c, 400, respond.CodeInvalid, "下载参数无效", nil)
		return
	}
	relative, ok := requestedRelative(c, req.Path)
	if !ok {
		return
	}
	sess, err := h.sessions.Get(c.Request.Context(), auth.UID(c), c.Param("id"))
	if err != nil {
		fail(c, err)
		return
	}
	if _, err = fileRoot(sess, req.Scope); err != nil {
		fail(c, err)
		return
	}
	if sess.ComputerID == nil {
		respond.Error(c, 409, "computer/required", "会话必须绑定云电脑", nil)
		return
	}
	name := path.Base(relative)
	if relative == "." {
		name = map[string]string{"": "workspace", "workspace": "workspace", "storage": "storage", "agent": "agent-files"}[req.Scope]
	}
	if req.Archive {
		name += ".zip"
	}
	key := make([]byte, 32)
	if _, err = rand.Read(key); err != nil {
		fail(c, err)
		return
	}
	limits := h.downloadLimits()
	ctx, cancel := context.WithTimeout(context.Background(), limits.PrepareTimeout)
	job := &fileDownload{ID: hex.EncodeToString(key), Status: "preparing", Name: name,
		uid: auth.UID(c), sessionID: sess.ID, source: relative, scope: req.Scope, archive: req.Archive,
		expires: time.Now().Add(limits.PrepareTimeout + limits.TTL), cancel: cancel}
	h.downloadMu.Lock()
	active, userActive := 0, 0
	for _, old := range h.downloads {
		if old.Status == "preparing" {
			active++
			if old.uid == job.uid {
				userActive++
			}
		}
	}
	if active >= 4 || userActive >= 2 || len(h.downloads) >= 128 {
		h.downloadMu.Unlock()
		cancel()
		respond.Error(c, 429, "file/download_busy", "下载任务较多，请等待当前任务完成", nil)
		return
	}
	if h.downloads == nil {
		h.downloads = make(map[string]*fileDownload)
	}
	h.downloads[job.ID] = job
	view := *job
	h.downloadMu.Unlock()
	go h.prepareDownload(ctx, job)
	c.JSON(http.StatusAccepted, view)
}

func (h *Handler) downloadForUser(c *gin.Context) *fileDownload {
	// Recheck current membership and ownership on every status/cancel operation.
	if _, err := h.sessions.Get(c.Request.Context(), auth.UID(c), c.Param("id")); err != nil {
		fail(c, err)
		return nil
	}
	h.downloadMu.Lock()
	defer h.downloadMu.Unlock()
	job := h.downloads[c.Param("downloadId")]
	if job == nil || job.uid != auth.UID(c) || job.sessionID != c.Param("id") || time.Now().After(job.expires) {
		respond.Error(c, 404, "file/download_not_found", "下载任务不存在或已过期", nil)
		return nil
	}
	copy := *job
	return &copy
}

func (h *Handler) DownloadStatus(c *gin.Context) {
	if job := h.downloadForUser(c); job != nil {
		c.Header("Cache-Control", "no-store")
		c.JSON(200, job)
	}
}

func (h *Handler) CancelDownload(c *gin.Context) {
	job := h.downloadForUser(c)
	if job == nil {
		return
	}
	job.cancel()
	h.downloadMu.Lock()
	if current := h.downloads[job.ID]; current != nil && current.Status != "preparing" {
		current.Status, current.expires = "canceled", time.Now()
		if current.readers == 0 {
			delete(h.downloads, job.ID)
			_ = os.RemoveAll(current.directory)
		}
	}
	h.downloadMu.Unlock()
	c.JSON(200, gin.H{"ok": true})
}

func (h *Handler) expireDownload(id string) {
	h.downloadMu.Lock()
	job := h.downloads[id]
	if job != nil {
		job.Status = "expired"
		if job.readers == 0 {
			delete(h.downloads, id)
			_ = os.RemoveAll(job.directory)
		}
	}
	h.downloadMu.Unlock()
}

func (h *Handler) prepareDownload(ctx context.Context, job *fileDownload) {
	limits := h.downloadLimits()
	var prepareErr error
	defer func() {
		job.cancel()
		h.downloadMu.Lock()
		if prepareErr != nil {
			log.Printf("runtime: download preparation %s failed: %v", job.ID[:12], prepareErr)
			job.Status, job.Message, job.Bytes = "failed", downloadMessage(prepareErr), 0
			if errors.Is(prepareErr, context.Canceled) {
				job.Status, job.Message = "canceled", "下载准备已取消"
			}
			_ = os.RemoveAll(job.directory)
		}
		if job.Status == "ready" {
			job.expires = job.ExpiresAt
		} else {
			job.expires = time.Now().Add(limits.TTL)
		}
		job.ExpiresAt = job.expires
		h.downloadMu.Unlock()
		time.AfterFunc(limits.TTL, func() { h.expireDownload(job.ID) })
	}()
	access, err := h.openFileAccess(ctx, job.uid, job.sessionID, job.scope)
	if err != nil {
		prepareErr = err
		return
	}
	defer access.close()
	// The existing descriptor-based stat also initializes a missing resource root.
	if _, err = access.operate(ctx, access.root, job.source, "stat", nil); err != nil {
		prepareErr = err
		return
	}
	args := map[string]any{"op": "export", "id": job.ID, "root": access.root, "path": job.source,
		"archive": job.archive, "name": strings.TrimSuffix(job.Name, ".zip"), "max_bytes": limits.MaxBytes,
		"max_entries": limits.MaxEntries, "timeout": limits.PrepareTimeout.Seconds()}
	if err = access.ensureMaintenance(); err != nil {
		prepareErr = err
		return
	}
	result, stopped, err := exportFile(ctx, access.handle, args, limits.PrepareTimeout)
	access.controlUncertain = !stopped
	if stopped {
		defer func() {
			cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_, _, _ = exportFile(cleanup, access.handle, map[string]any{"op": "remove", "id": job.ID}, 8*time.Second)
		}()
	}
	if err != nil {
		prepareErr = err
		return
	}
	streamer, ok := access.handle.(interface {
		DownloadFileStream(context.Context, string) (io.ReadCloser, error)
	})
	if !ok {
		prepareErr = publicDownloadError("云电脑不支持流式下载")
		return
	}
	h.downloadMu.Lock()
	var staged int64
	for _, existing := range h.downloads {
		staged += existing.Bytes
	}
	if result.Bytes < 0 || result.Bytes > limits.MaxBytes || result.Bytes > limits.MaxStagedBytes-staged {
		h.downloadMu.Unlock()
		prepareErr = publicDownloadError("下载暂存空间不足或文件超过配置限制")
		return
	}
	job.Bytes = result.Bytes // Reserve disk quota before copying any content.
	h.downloadMu.Unlock()
	spool := filepath.Join(h.dataDir, ".downloads")
	if err = os.MkdirAll(spool, 0700); err != nil {
		prepareErr = err
		return
	}
	// Restarted processes reclaim expired jobs; current jobs also have TTL timers.
	if entries, readErr := os.ReadDir(spool); readErr == nil {
		for _, entry := range entries {
			if entry.IsDir() && strings.HasPrefix(entry.Name(), "download-") {
				if info, infoErr := entry.Info(); infoErr == nil && time.Since(info.ModTime()) > limits.TTL+limits.PrepareTimeout {
					_ = os.RemoveAll(filepath.Join(spool, entry.Name()))
				}
			}
		}
	}
	directory, err := os.MkdirTemp(spool, "download-")
	if err != nil {
		prepareErr = err
		return
	}
	h.downloadMu.Lock()
	job.directory = directory
	h.downloadMu.Unlock()
	local := filepath.Join(directory, "content")
	f, err := os.OpenFile(local, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		prepareErr = err
		return
	}
	defer func() { _ = f.Close() }()
	stream, err := streamer.DownloadFileStream(ctx, result.Path)
	if err != nil {
		prepareErr = err
		return
	}
	defer func() { _ = stream.Close() }()
	hash := sha256.New()
	n, err := io.CopyBuffer(io.MultiWriter(f, hash), io.LimitReader(stream, result.Bytes+1), make([]byte, 128*1024))
	if err != nil {
		prepareErr = err
		return
	}
	if n != result.Bytes {
		prepareErr = publicDownloadError("下载内容长度不一致，请重试")
		return
	}
	if err = ctx.Err(); err != nil {
		prepareErr = err
		return
	}
	if err = f.Close(); err != nil {
		prepareErr = err
		return
	}
	h.downloadMu.Lock()
	job.local, job.etag, job.Status = local, `"`+hex.EncodeToString(hash.Sum(nil))+`"`, "ready"
	job.ExpiresAt = time.Now().Add(limits.TTL)
	job.expires = job.ExpiresAt
	job.URL = "/api/v1/downloads/" + job.ID + "/file"
	h.downloadMu.Unlock()
}

// DownloadContent serves an immutable local snapshot with bounded memory and
// native HTTP Range/If-Range support. The capability remains valid for retries
// until TTL expiry; deletion of a Unix pathname does not truncate an open stream.
func (h *Handler) DownloadContent(c *gin.Context) {
	h.downloadMu.Lock()
	job := h.downloads[c.Param("downloadId")]
	if job == nil || job.Status != "ready" || time.Now().After(job.expires) {
		h.downloadMu.Unlock()
		respond.Error(c, 404, "file/download_not_found", "下载不存在、尚未就绪或已过期", nil)
		return
	}
	view := *job
	f, err := os.Open(job.local)
	if err == nil {
		job.readers++
	}
	h.downloadMu.Unlock()
	if err != nil {
		respond.Error(c, 404, "file/download_not_found", "下载已过期", nil)
		return
	}
	defer func() {
		_ = f.Close()
		h.downloadMu.Lock()
		job.readers--
		if job.readers == 0 && (job.Status == "expired" || job.Status == "canceled") {
			delete(h.downloads, job.ID)
			_ = os.RemoveAll(job.directory)
		}
		h.downloadMu.Unlock()
	}()
	if _, err = h.sessions.Get(c.Request.Context(), view.uid, view.sessionID); err != nil {
		fail(c, err)
		return
	}
	c.Header("Content-Disposition", "attachment; filename*=UTF-8''"+urlPathEscape(view.Name))
	c.Header("Content-Type", "application/octet-stream")
	if strings.HasSuffix(strings.ToLower(view.Name), ".zip") {
		c.Header("Content-Type", "application/zip")
	}
	c.Header("Cache-Control", "private, no-store")
	c.Header("Referrer-Policy", "no-referrer")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("ETag", view.etag)
	http.ServeContent(c.Writer, c.Request, view.Name, time.Time{}, f)
}

func (h *Handler) DownloadOwner(id string) string {
	h.downloadMu.Lock()
	defer h.downloadMu.Unlock()
	job := h.downloads[id]
	if job == nil || job.Status != "ready" || time.Now().After(job.expires) {
		return ""
	}
	return job.uid
}

type exportResult struct {
	Path  string `json:"path"`
	Bytes int64  `json:"bytes"`
}

func exportFile(ctx context.Context, handle tools.Handle, args map[string]any, timeout time.Duration) (exportResult, bool, error) {
	raw, _ := json.Marshal(args)
	command := "python3 -I -c " + quoteShell(downloadExportPython) + " " + quoteShell(string(raw))
	// Cancellation sends a root-only marker. The remote parent kills and waits
	// for its sole unprivileged child before returning an explicit exit status.
	done := make(chan struct{})
	if args["op"] == "export" {
		go func() {
			select {
			case <-done:
				return
			case <-ctx.Done():
			}
			cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_, _, _ = exportFile(cleanup, handle, map[string]any{"op": "cancel", "id": args["id"]}, 8*time.Second)
		}()
	}
	remoteCtx, cancel := context.WithTimeout(context.Background(), timeout+20*time.Second)
	defer cancel()
	res, err := handle.RunCommand(remoteCtx, command, "/", tools.PlatformEnv(), timeout.Milliseconds()+10000, nil)
	close(done)
	if err != nil || res == nil || res.ExitCode < 0 {
		if err == nil {
			err = fmt.Errorf("无法确认下载准备进程已停止")
		}
		return exportResult{}, false, err
	}
	if ctx.Err() != nil {
		return exportResult{}, true, ctx.Err()
	}
	if res.ExitCode != 0 {
		message := map[int]string{43: "下载超过配置大小或文件数量限制", 44: "文件不存在", 46: "目录包含符号链接、特殊文件或不可访问的路径", 47: "文件在打包期间发生变化，请重试", 124: "下载准备超时", 125: "下载准备已取消"}[res.ExitCode]
		if message == "" {
			message = "下载准备失败，请检查可用磁盘空间和文件权限"
		}
		return exportResult{}, true, publicDownloadError(message)
	}
	var result exportResult
	if args["op"] == "export" {
		err = json.Unmarshal([]byte(res.Stdout), &result)
	}
	return result, true, err
}

const downloadExportPython = `
import os,sys,json,stat,time,signal,ctypes,zipfile,re
p=json.loads(sys.argv[1])
if not re.fullmatch("[a-f0-9]{64}",p["id"]): raise ValueError("invalid download id")
control="/tmp/.wispjar-downloads"
try:os.mkdir(control,0o700)
except FileExistsError:pass
directory=os.open(control,os.O_RDONLY|os.O_DIRECTORY|os.O_NOFOLLOW)
s=os.fstat(directory)
if s.st_uid!=0 or s.st_mode&0o077: raise ValueError("untrusted export directory")
token=p["id"]
def unlink(name):
    try:os.unlink(name,dir_fd=directory)
    except FileNotFoundError:pass
if p["op"]=="cancel":
    fd=os.open(token+".cancel",os.O_WRONLY|os.O_CREAT|os.O_NOFOLLOW,0o600,dir_fd=directory);os.close(fd)
elif p["op"]=="remove":
    unlink(token);unlink(token+".cancel")
elif p["op"]=="export":
    # Artifacts of a crashed application cannot accumulate indefinitely.
    for name in os.listdir(directory):
        if re.fullmatch("[a-f0-9]{64}(\.cancel)?",name):
            try:
                if time.time()-os.stat(name,dir_fd=directory,follow_symlinks=False).st_mtime>7200:unlink(name)
            except FileNotFoundError:pass
    outfd=os.open(token,os.O_RDWR|os.O_CREAT|os.O_EXCL|os.O_NOFOLLOW,0o600,dir_fd=directory)
    child=os.fork()
    if child==0:
        os.close(directory)
        class Limit(Exception):pass
        class Changed(Exception):pass
        try:
            if ctypes.CDLL(None).prctl(38,1,0,0,0)!=0:raise RuntimeError("no_new_privs")
            os.setgroups([]);os.setgid(65532);os.setuid(65532)
            flags=os.O_RDONLY|os.O_DIRECTORY|os.O_NOFOLLOW
            def parts(value):
                if "\\" in value or "\0" in value or value.startswith("/") or ".." in value.split("/"):raise ValueError("invalid path")
                return [x for x in value.split("/") if x not in ("", ".")]
            fd=os.open("/",flags)
            components=parts(p["root"].lstrip("/"))+parts(p["path"])
            for i,name in enumerate(components):
                mode=flags if i<len(components)-1 else os.O_RDONLY|os.O_NOFOLLOW|os.O_NONBLOCK
                nxt=os.open(name,mode,dir_fd=fd);os.close(fd);fd=nxt
            source=os.fstat(fd)
            class Bounded:
                def __init__(self,f):self.f=f
                def __getattr__(self,name):return getattr(self.f,name)
                def write(self,data):
                    if self.f.tell()+len(data)>p["max_bytes"]:raise Limit()
                    return self.f.write(data)
            total=0;entries=0
            def unchanged(before,after):
                if (before.st_size,before.st_mtime_ns,before.st_ctime_ns)!=(after.st_size,after.st_mtime_ns,after.st_ctime_ns):raise Changed()
            def copy(src,dst,before):
                global total
                count=0
                while True:
                    data=src.read(1024*1024)
                    if not data:break
                    count+=len(data);total+=len(data)
                    if total>p["max_bytes"]:raise Limit()
                    dst.write(data)
                if count!=before.st_size:raise Changed()
                unchanged(before,os.fstat(src.fileno()))
            with os.fdopen(outfd,"w+b",buffering=0) as output:
                bounded=Bounded(output)
                if not p["archive"]:
                    if not stat.S_ISREG(source.st_mode):raise ValueError("not a regular file")
                    if source.st_size>p["max_bytes"]:raise Limit()
                    with os.fdopen(fd,"rb") as src:copy(src,bounded,source)
                else:
                    if not stat.S_ISDIR(source.st_mode):raise ValueError("not a directory")
                    def visit(current,name,depth,archive):
                        global entries
                        entries+=1
                        if entries>p["max_entries"] or depth>128:raise Limit()
                        before=os.fstat(current)
                        stamp=time.localtime(max(315532800,min(before.st_mtime,4354819199)))[:6]
                        if stat.S_ISDIR(before.st_mode):
                            info=zipfile.ZipInfo(name+"/",date_time=stamp);info.external_attr=(stat.S_IFDIR|0o755)<<16
                            archive.writestr(info,b"")
                            for childname in sorted(os.listdir(current)):
                                if "\\" in childname or any(ord(c)<32 for c in childname):raise ValueError("invalid archive name")
                                nested=os.open(childname,os.O_RDONLY|os.O_NOFOLLOW|os.O_NONBLOCK,dir_fd=current)
                                try:visit(nested,name+"/"+childname,depth+1,archive)
                                finally:os.close(nested)
                            unchanged(before,os.fstat(current))
                        elif stat.S_ISREG(before.st_mode):
                            if total+before.st_size>p["max_bytes"]:raise Limit()
                            info=zipfile.ZipInfo(name,date_time=stamp)
                            ext=name.rsplit(".",1)[-1].lower()
                            info.compress_type=zipfile.ZIP_STORED if ext in ("zip","gz","7z","rar","jpg","jpeg","png","webp","mp4","mov","mp3","safetensors","bin") else zipfile.ZIP_DEFLATED
                            info.external_attr=(stat.S_IFREG|0o644)<<16
                            info.file_size=before.st_size
                            with os.fdopen(os.dup(current),"rb") as src,archive.open(info,"w",force_zip64=True) as dst:copy(src,dst,before)
                        else:raise ValueError("unsupported file")
                    with zipfile.ZipFile(bounded,"w",compression=zipfile.ZIP_DEFLATED,allowZip64=True,compresslevel=1) as archive:visit(fd,p["name"],0,archive)
                    os.close(fd)
                output.flush();os.fsync(output.fileno())
            os._exit(0)
        except Limit:os._exit(43)
        except FileNotFoundError:os._exit(44)
        except Changed:os._exit(47)
        except (ValueError,PermissionError,IsADirectoryError,NotADirectoryError):os._exit(46)
        except Exception:os._exit(1)
    deadline=time.monotonic()+p["timeout"];code=0
    while True:
        done,status=os.waitpid(child,os.WNOHANG)
        if done:code=os.waitstatus_to_exitcode(status);break
        if time.monotonic()>deadline or os.path.exists(control+"/"+token+".cancel"):
            code=124 if time.monotonic()>deadline else 125
            os.kill(child,signal.SIGKILL);os.waitpid(child,0);break
        time.sleep(0.05)
    if code!=0:
        os.close(outfd);unlink(token);unlink(token+".cancel");sys.exit(code if code>0 else 1)
    os.fchmod(outfd,0o400);size=os.fstat(outfd).st_size;os.close(outfd)
    print(json.dumps({"path":control+"/"+token,"bytes":size}))
os.close(directory)
`
