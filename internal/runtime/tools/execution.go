package tools

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/ravenmk2/wispjar/internal/id"
	"github.com/ravenmk2/wispjar/internal/sandbox"
)

// ExecutionHandle runs task code as an unprivileged identity. The root-owned
// supervisor is a Linux child subreaper, so detached descendants are still
// reaped before the command is certified stopped. Each run owns its tracker.
// Transport failure is deliberately sticky: cancellation is not proof of exit.
type ExecutionHandle struct {
	Handle
	mu        sync.Mutex
	active    int
	uncertain bool
	closed    bool
	OnSettled func()
}

func NewExecutionHandle(h Handle) *ExecutionHandle { return &ExecutionHandle{Handle: h} }
func (h *ExecutionHandle) CloseAdmission() {
	h.mu.Lock()
	h.closed = true
	h.mu.Unlock()
}

// PlatformEnv is for root maintenance only. User environment is supplied to the
// already unprivileged child, never to the supervisor/interpreter loader.
func PlatformEnv() map[string]string {
	return map[string]string{
		"PATH": "/usr/local/bin:/usr/bin:/bin", "PYTHONPATH": "", "PYTHONHOME": "",
		"LD_PRELOAD": "", "LD_LIBRARY_PATH": "", "ENV": "", "BASH_ENV": "",
	}
}
func (h *ExecutionHandle) CommandsStopped() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.active == 0 && !h.uncertain
}
func (h *ExecutionHandle) RunCommand(ctx context.Context, cmd, cwd string, env map[string]string, timeoutMs int64, onEvent func(string, string)) (*sandbox.CommandResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if timeoutMs <= 0 {
		timeoutMs = 30000
	}
	commandID := id.New()
	marker := "WISPJAR_STOPPED_" + commandID
	payload, _ := json.Marshal(map[string]any{"command": cmd, "cwd": cwd, "env": env, "timeout": float64(timeoutMs) / 1000, "marker": marker, "id": commandID})
	command := "python3 -I -c " + shellQuote(commandSupervisor) + " " + shellQuote(base64.StdEncoding.EncodeToString(payload))
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return nil, fmt.Errorf("execution run has ended")
	}
	if err := ctx.Err(); err != nil {
		h.mu.Unlock()
		return nil, err
	}
	h.active++
	h.mu.Unlock()
	remoteCtx, remoteCancel := context.WithTimeout(context.Background(), time.Duration(timeoutMs)*time.Millisecond+20*time.Second)
	defer remoteCancel()
	done := make(chan struct{})
	go func() {
		select {
		case <-done:
		case <-ctx.Done():
			cancelCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_, _ = h.Handle.RunCommand(cancelCtx, "python3 -I -c "+shellQuote(cancelCommandPython)+" "+shellQuote(commandID), "/", PlatformEnv(), 10000, nil)
		}
	}()
	result, err := h.Handle.RunCommand(remoteCtx, command, "/", PlatformEnv(), timeoutMs+10000, func(stream, chunk string) {
		if onEvent != nil && !strings.Contains(chunk, marker) {
			onEvent(stream, chunk)
		}
	})
	close(done)
	stopped := err == nil && result != nil && strings.HasSuffix(strings.TrimSpace(result.Stderr), marker)
	h.mu.Lock()
	h.active--
	h.uncertain = h.uncertain || !stopped
	h.mu.Unlock()
	defer func() {
		if h.OnSettled != nil {
			h.OnSettled()
		}
	}()
	if result != nil && stopped {
		result.Stderr = strings.TrimSuffix(strings.TrimRight(result.Stderr, "\r\n"), marker)
		result.Stderr = strings.TrimRight(result.Stderr, "\r\n")
		result.Output = result.Stdout + result.Stderr
	}
	if err == nil && !stopped {
		err = fmt.Errorf("remote execution termination could not be confirmed")
	}
	return result, err
}

const commandSupervisor = `
import os, sys, json, base64, subprocess, signal, ctypes, time
p=json.loads(base64.b64decode(sys.argv[1]))
libc=ctypes.CDLL(None, use_errno=True)
if libc.prctl(36,1,0,0,0) != 0:
    raise RuntimeError("cannot establish child subreaper")
control="/tmp/.wispjar-commands"
os.makedirs(control,mode=0o700,exist_ok=True)
st=os.lstat(control)
if not __import__("stat").S_ISDIR(st.st_mode) or st.st_uid!=0 or st.st_mode & 0o077: raise RuntimeError("invalid command control directory")
cancelpath=control+"/"+p["id"]+".cancel"
def identity():
    if libc.prctl(38,1,0,0,0)!=0: raise RuntimeError("cannot disable privilege elevation")
    os.setgroups([])
    os.setgid(65532)
    os.setuid(65532)
    os.umask(0o022)
env={k:os.environ[k] for k in ("PATH","LANG","LC_ALL","TZ","TERM") if k in os.environ}
env.update(p.get("env") or {})
env["HOME"]="/tmp"
shell="/bin/bash" if os.path.isfile("/bin/bash") else "/bin/sh"
child=subprocess.Popen([shell,"-c",p["command"]],cwd=p["cwd"],env=env,preexec_fn=identity,start_new_session=True)
code=124
try:
    deadline=time.monotonic()+p["timeout"]
    while child.poll() is None and time.monotonic()<deadline and not os.path.exists(cancelpath):
        time.sleep(0.02)
    if child.poll() is not None: code=child.returncode
finally:
    deadline=time.monotonic()+5
    while True:
        parents={}
        for name in os.listdir("/proc"):
            if name.isdigit():
                try:
                    raw=open("/proc/"+name+"/stat").read()
                    parents[int(name)]=int(raw[raw.rfind(")")+2:].split()[1])
                except (OSError,ValueError,IndexError):
                    pass
        descendants=set()
        changed=True
        while changed:
            changed=False
            for pid,parent in parents.items():
                if (parent==os.getpid() or parent in descendants) and pid not in descendants:
                    descendants.add(pid);changed=True
        for pid in descendants:
            try: os.kill(pid,signal.SIGKILL)
            except ProcessLookupError: pass
        while True:
            try:
                pid,_=os.waitpid(-1,os.WNOHANG)
                if pid==0: break
            except ChildProcessError: break
        if not descendants: break
        if time.monotonic()>deadline:
            raise RuntimeError("descendant termination not confirmed")
        time.sleep(0.02)
try: os.unlink(cancelpath)
except FileNotFoundError: pass
print("\n"+p["marker"],file=sys.stderr,flush=True)
sys.exit(code if code>=0 else 128-code)
`

const cancelCommandPython = `
import os,sys,stat,re
if not re.fullmatch("[a-f0-9]{32}",sys.argv[1]): raise RuntimeError("invalid command")
directory="/tmp/.wispjar-commands"
os.makedirs(directory,mode=0o700,exist_ok=True)
s=os.lstat(directory)
if not stat.S_ISDIR(s.st_mode) or s.st_uid!=0 or s.st_mode & 0o077: raise RuntimeError("invalid control directory")
fd=os.open(directory,os.O_RDONLY|os.O_DIRECTORY|os.O_NOFOLLOW)
out=os.open(sys.argv[1]+".cancel",os.O_WRONLY|os.O_CREAT|os.O_NOFOLLOW,0o600,dir_fd=fd)
os.close(out);os.close(fd)
`

// File operations use the same identity, including real open permissions.
// In particular SDK uploads never run as root on agent-controlled paths.
func (h *ExecutionHandle) fileOp(ctx context.Context, op string, args map[string]any) ([]byte, error) {
	args["op"] = op
	raw, _ := json.Marshal(args)
	result, err := h.RunPayload(ctx, filePython, raw, 30000)
	if err != nil {
		return nil, err
	}
	if result.ExitCode == 44 {
		return nil, os.ErrNotExist
	}
	if result.ExitCode != 0 {
		return nil, fmt.Errorf("file operation failed: %s", result.Stderr)
	}
	return []byte(result.Stdout), nil
}

// RunPayload uses a platform-owned transfer directory instead of putting large
// file bodies in argv (Linux limits a single argument to about 128 KiB).
func (h *ExecutionHandle) RunPayload(ctx context.Context, script string, payload []byte, timeoutMs int64) (*sandbox.CommandResult, error) {
	token := id.New()
	stage := "/tmp/.wispjar-transfer/" + token
	prepared, err := h.Handle.RunCommand(ctx, "python3 -I -c "+shellQuote(transferPython)+" prepare "+shellQuote(token), "/", PlatformEnv(), 10000, nil)
	if err != nil {
		return nil, err
	}
	if prepared.ExitCode != 0 {
		return nil, fmt.Errorf("prepare file transfer failed")
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = h.Handle.RunCommand(cleanup, "python3 -I -c "+shellQuote(transferPython)+" remove "+shellQuote(token), "/", PlatformEnv(), 5000, nil)
	}()
	if err = h.Handle.UploadFile(ctx, stage, payload); err != nil {
		return nil, err
	}
	ready, err := h.Handle.RunCommand(ctx, "python3 -I -c "+shellQuote(transferPython)+" ready "+shellQuote(token), "/", PlatformEnv(), 10000, nil)
	if err != nil {
		return nil, err
	}
	if ready.ExitCode != 0 {
		return nil, fmt.Errorf("publish file transfer failed")
	}
	return h.RunCommand(ctx, "python3 -I -c "+shellQuote(script)+" "+shellQuote(stage), "/", nil, timeoutMs, nil)
}

const transferPython = `
import os,sys,stat,re
op,token=sys.argv[1:]
if not re.fullmatch("[a-f0-9]{32}",token): raise RuntimeError("invalid transfer identity")
directory="/tmp/.wispjar-transfer"
os.makedirs(directory,mode=0o711,exist_ok=True)
s=os.lstat(directory)
if not stat.S_ISDIR(s.st_mode) or s.st_uid!=0 or s.st_mode & 0o022:raise RuntimeError("invalid transfer directory")
fd=os.open(directory,os.O_RDONLY|os.O_DIRECTORY|os.O_NOFOLLOW)
try:
    if op=="prepare":
        out=os.open(token,os.O_WRONLY|os.O_CREAT|os.O_EXCL|os.O_NOFOLLOW,0o600,dir_fd=fd);os.close(out)
    elif op=="ready":
        out=os.open(token,os.O_RDONLY|os.O_NOFOLLOW,dir_fd=fd)
        if not stat.S_ISREG(os.fstat(out).st_mode) or os.fstat(out).st_uid!=0:raise RuntimeError("invalid transfer file")
        os.fchmod(out,0o444);os.close(out)
    elif op=="remove":
        try:os.unlink(token,dir_fd=fd)
        except FileNotFoundError:pass
finally:os.close(fd)
`

func (h *ExecutionHandle) UploadFile(ctx context.Context, p string, data []byte) error {
	_, err := h.fileOp(ctx, "write", map[string]any{"path": p, "data": base64.StdEncoding.EncodeToString(data)})
	return err
}
func (h *ExecutionHandle) DownloadFile(ctx context.Context, p string) ([]byte, error) {
	raw, err := h.fileOp(ctx, "read", map[string]any{"path": p})
	if err != nil {
		return nil, err
	}
	return base64.StdEncoding.DecodeString(strings.TrimSpace(string(raw)))
}
func (h *ExecutionHandle) ReplaceInFile(ctx context.Context, p, old, new string, all bool) (int, error) {
	raw, err := h.fileOp(ctx, "edit", map[string]any{"path": p, "old": old, "new": new, "all": all})
	if err != nil {
		return 0, err
	}
	var n int
	err = json.Unmarshal(raw, &n)
	return n, err
}

type fileRecord struct {
	Path     string  `json:"path"`
	Size     int64   `json:"size"`
	Type     string  `json:"type"`
	Modified float64 `json:"modified"`
}

func records(raw []byte) ([]sandbox.FileInfo, error) {
	var rows []fileRecord
	if err := json.Unmarshal(raw, &rows); err != nil {
		return nil, err
	}
	out := make([]sandbox.FileInfo, 0, len(rows))
	for _, r := range rows {
		out = append(out, sandbox.FileInfo{Path: r.Path, Size: r.Size, Type: r.Type, ModifiedAt: time.UnixMilli(int64(r.Modified * 1000))})
	}
	return out, nil
}
func (h *ExecutionHandle) FileInfo(ctx context.Context, p string) (sandbox.FileInfo, error) {
	raw, err := h.fileOp(ctx, "stat", map[string]any{"path": p})
	if err != nil {
		return sandbox.FileInfo{}, err
	}
	rows, err := records(raw)
	if err != nil {
		return sandbox.FileInfo{}, err
	}
	if len(rows) != 1 {
		return sandbox.FileInfo{}, os.ErrNotExist
	}
	return rows[0], nil
}
func (h *ExecutionHandle) ListDir(ctx context.Context, p string) ([]sandbox.FileInfo, error) {
	raw, err := h.fileOp(ctx, "list", map[string]any{"path": p})
	if err != nil {
		return nil, err
	}
	return records(raw)
}
func (h *ExecutionHandle) SearchFiles(ctx context.Context, p, pattern string) ([]sandbox.FileInfo, error) {
	raw, err := h.fileOp(ctx, "glob", map[string]any{"path": p, "pattern": pattern})
	if err != nil {
		return nil, err
	}
	return records(raw)
}

const filePython = `
import os,sys,json,base64,glob,stat
p=json.load(open(sys.argv[1]));path=p["path"];op=p["op"]
def info(name):
    s=os.stat(name)
    return {"path":name,"size":s.st_size,"type":"directory" if stat.S_ISDIR(s.st_mode) else "file","modified":s.st_mtime}
try:
    if op=="read":
        fd=os.open(path,os.O_RDONLY|os.O_NONBLOCK)
        if not stat.S_ISREG(os.fstat(fd).st_mode):os.close(fd);raise RuntimeError("not a regular file")
        with os.fdopen(fd,"rb") as f: data=f.read(32*1024*1024+1)
        if len(data)>32*1024*1024:raise RuntimeError("file exceeds transfer limit")
        print(base64.b64encode(data).decode())
    elif op=="write":
        with open(path,"wb") as f: f.write(base64.b64decode(p["data"]))
    elif op=="edit":
        with open(path) as f: text=f.read()
        n=text.count(p["old"])
        if n>1 and not p["all"]: raise RuntimeError("old_string has multiple matches")
        if n:
            with open(path,"w") as f: f.write(text.replace(p["old"],p["new"],n if p["all"] else 1))
        print(n)
    elif op=="stat": print(json.dumps([info(path)]))
    elif op=="list": print(json.dumps([info(os.path.join(path,n)) for n in os.listdir(path)]))
    elif op=="glob":
        pattern=p["pattern"]
        if os.path.isabs(pattern) or ".." in pattern.split("/"): raise RuntimeError("invalid glob")
        print(json.dumps([info(n) for n in glob.glob(os.path.join(path,pattern),recursive=True)][:1000]))
except FileNotFoundError:
    sys.exit(44)
`
