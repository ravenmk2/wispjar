package skill

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

type UploadFunc func(context.Context, string, []byte) error
type RunFunc func(ctx context.Context, command, cwd string) (exitCode int, output string, err error)
type SandboxInstaller struct {
	upload UploadFunc
	run    RunFunc
}

func NewSandboxInstaller(upload UploadFunc, run RunFunc) *SandboxInstaller {
	return &SandboxInstaller{upload: upload, run: run}
}

func (i *SandboxInstaller) Install(ctx context.Context, plan InstallPlan) error {
	if i.upload == nil || i.run == nil {
		return &StoppedInstallError{Err: fmt.Errorf("installer callbacks unavailable")}
	}
	if !validID(plan.Owner) || !validID(plan.SkillID) {
		return &StoppedInstallError{Err: fmt.Errorf("invalid install identity")}
	}
	data, err := json.Marshal(plan)
	if err != nil {
		return &StoppedInstallError{Err: err}
	}
	payload := "/agents/.wispjar/inbox/" + plan.Owner + ".json"
	code, _, err := i.run(ctx, "python3 -c "+shellQuote(preparePython), "/")
	if err != nil || code != 0 {
		// Preparation can only establish root-owned control directories; it
		// never modifies an installed skill or starts a background process.
		return &StoppedInstallError{Err: fmt.Errorf("prepare private skill inbox failed")}
	}
	// Upload alone cannot alter an installed skill. It is safe to retry if it
	// fails; no installer process has been launched at this point.
	if err = i.upload(ctx, payload, data); err != nil {
		return &StoppedInstallError{Err: err}
	}
	command := "python3 -c " + shellQuote(installPython) + " " + shellQuote(payload)
	code, _, err = i.run(ctx, command, "/")
	if err != nil {
		return err
	} // Remote process state is unknown.
	if code != 0 {
		return &StoppedInstallError{Err: fmt.Errorf("skill install exited with status %d", code)}
	}
	return nil
}

const preparePython = `
import os, stat
if os.geteuid() != 0:
    raise RuntimeError("skill installer requires platform identity")
for directory, mode in [("/agents",0o755),("/agents/.wispjar",0o700),("/agents/.wispjar/inbox",0o700)]:
    if os.path.lexists(directory):
        info = os.lstat(directory)
        if not stat.S_ISDIR(info.st_mode) or info.st_uid != 0:
            raise RuntimeError("untrusted control directory")
    else:
        os.mkdir(directory, mode)
    os.chmod(directory, mode)
`

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }

// The entire publication runs synchronously under flock on the shared agents
// volume. Fences prevent an old command, delayed by transport or scheduling,
// from modifying directories after an explicit recovery started a newer sync.
// Staging is root-only; a partial publication is never made runnable by the DB
// coordinator. Python is provided by the maintained sandbox base image.
const installPython = `
import os, sys, json, hashlib, base64, re, shutil, fcntl, stat
payload = sys.argv[1]
if os.geteuid() != 0:
    raise RuntimeError("skill installer requires platform identity")
payload_info = os.lstat(payload)
if not stat.S_ISREG(payload_info.st_mode) or payload_info.st_uid != 0 or payload_info.st_nlink != 1:
    raise RuntimeError("untrusted payload")
with os.fdopen(os.open(payload, os.O_RDONLY | os.O_NOFOLLOW), "rb") as f:
    p = json.load(f)
hexid = re.compile(r"^[0-9a-f]{32}$")
if not hexid.fullmatch(p["owner"]) or not hexid.fullmatch(p["skill_id"]):
    raise RuntimeError("invalid identity")
if not re.fullmatch(r"[a-z0-9]+(?:-[a-z0-9]+)*", p["name"]):
    raise RuntimeError("invalid skill name")
def trusted_dir(directory, mode=0o755):
    if os.path.lexists(directory):
        st = os.lstat(directory)
        if not stat.S_ISDIR(st.st_mode):
            raise RuntimeError("directory is a link or non-directory")
        if st.st_uid != 0:
            raise RuntimeError("directory is not platform owned")
    else:
        os.mkdir(directory, mode)
    os.chmod(directory, mode)
trusted_dir("/agents")
trusted_dir("/agents/.wispjar", 0o700)
trusted_dir("/agents/.wispjar/skills", 0o700)
control = "/agents/.wispjar/skills/" + p["skill_id"]
trusted_dir(control, 0o700)
lockpath = control + "/lock"
if os.path.islink(lockpath):
    raise RuntimeError("invalid lock")
with open(lockpath, "a+b") as lock:
    fcntl.flock(lock, fcntl.LOCK_EX)
    fencepath = control + "/fence.json"
    current = {"fence": 0, "owner": ""}
    if os.path.exists(fencepath):
        with open(fencepath) as f:
            current = json.load(f)
    if p["fence"] < current["fence"] or (p["fence"] == current["fence"] and p["owner"] != current["owner"]):
        raise RuntimeError("stale fencing token")
    with open(fencepath + ".tmp", "w") as f:
        json.dump({"fence": p["fence"], "owner": p["owner"]}, f)
        f.flush()
        os.fsync(f.fileno())
    os.replace(fencepath + ".tmp", fencepath)
    targets = p["targets"]
    previous = p["previous_targets"]
    for target in set(targets + previous):
        parts = target.split("/")
        if len(parts) != 5 or parts[1] != "agents" or not hexid.fullmatch(parts[2]) or parts[3] != "skills" or parts[4] != p["name"]:
            raise RuntimeError("invalid install target")
        trusted_dir("/agents/" + parts[2])
        trusted_dir("/agents/" + parts[2] + "/skills")
        if os.path.lexists(target) and (os.path.islink(target) or not os.path.isdir(target)):
            raise RuntimeError("invalid existing skill directory")
    files = sorted(p.get("files", []), key=lambda item: item["path"])
    manifest = hashlib.sha256()
    seen = set()
    for item in files:
        relative = item["path"]
        if relative in seen or relative.startswith("/") or "\\" in relative or any(part in ("", ".", "..") for part in relative.split("/")):
            raise RuntimeError("invalid file path")
        seen.add(relative)
        data = base64.b64decode(item["data"], validate=True)
        digest = hashlib.sha256(data).hexdigest()
        if digest != item["sha256"]:
            raise RuntimeError("content digest mismatch")
        item["_bytes"] = data
        manifest.update(relative.encode("utf-8") + b"\0" + digest.encode("ascii") + b"\n")
    if manifest.hexdigest() != p["content_hash"]:
        raise RuntimeError("manifest digest mismatch")
    staging = control + "/stage-" + p["owner"]
    if os.path.lexists(staging):
        if os.path.islink(staging):
            raise RuntimeError("invalid staging")
        shutil.rmtree(staging)
    os.mkdir(staging, 0o700)
    try:
        for number, target in enumerate(targets):
            staged = staging + "/" + str(number)
            os.mkdir(staged)
            for item in files:
                destination = staged + "/" + item["path"]
                os.makedirs(os.path.dirname(destination), exist_ok=True)
                with open(destination, "xb") as f:
                    f.write(item["_bytes"])
                    f.flush()
                    os.fsync(f.fileno())
                with open(destination, "rb") as f:
                    if hashlib.sha256(f.read()).hexdigest() != item["sha256"]:
                        raise RuntimeError("written content mismatch")
                os.chmod(destination, 0o444)
            for directory, _, _ in os.walk(staged, topdown=False):
                os.chmod(directory, 0o555)
        for target in set(previous + targets):
            if os.path.exists(target):
                shutil.rmtree(target)
        for number, target in enumerate(targets):
            os.replace(staging + "/" + str(number), target)
    finally:
        if os.path.exists(staging):
            shutil.rmtree(staging)
os.unlink(payload)
`
