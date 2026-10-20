package workspacemigration

import (
	"encoding/base64"
	"encoding/json"
	"strings"
)

func quote(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'" }
func command(script string, args any) string {
	raw, _ := json.Marshal(args)
	return "PYTHONDONTWRITEBYTECODE=1 python3 -B -c " + quote(pythonCommon+"\n"+script) + " " + quote(base64.StdEncoding.EncodeToString(raw))
}

const pythonCommon = `
import os, sys, json, stat, hashlib, base64, tarfile, io, shutil, posixpath
args = json.loads(base64.b64decode(sys.argv[1]))
SOURCE_ROOT = "/workspace"
TARGET_ROOT = "/sessions"
EXPORT_ROOT = "/tmp"
MAX_FILES = 100000
MAX_BYTES = 1073741824
def stable_json(value):
    return json.dumps(value, ensure_ascii=False, sort_keys=True, separators=(",",":")).encode("utf-8")
def digest(value):
    return hashlib.sha256(stable_json(value)).hexdigest()
def inside(root, path):
    return os.path.commonpath([root, path]) == root
def read_hash(path):
    fd = os.open(path, os.O_RDONLY | os.O_NOFOLLOW | getattr(os, "O_NOATIME", 0))
    h = hashlib.sha256()
    with os.fdopen(fd, "rb") as f:
        while True:
            data = f.read(1024*1024)
            if not data: break
            h.update(data)
    return h.hexdigest()
def scan(root):
    if os.path.islink(root) or not os.path.isdir(root):
        raise RuntimeError("workspace missing or not a real directory")
    for reserved in ("inputs","outputs"):
        p = root + "/" + reserved
        if os.path.lexists(p) and (os.path.islink(p) or not os.path.isdir(p)):
            raise RuntimeError("inputs/outputs must be real directories")
    entries = []
    total = 0
    def visit(path, relative):
        nonlocal total
        info = os.lstat(path)
        entry = {"path":relative,"mode":stat.S_IMODE(info.st_mode)&0o777,
                 "uid":info.st_uid,"gid":info.st_gid,"mtime_ns":info.st_mtime_ns}
        if stat.S_ISDIR(info.st_mode):
            entry["type"]="directory"
        elif stat.S_ISREG(info.st_mode):
            entry.update(type="file",size=info.st_size,sha256=read_hash(path))
            total += info.st_size
        elif stat.S_ISLNK(info.st_mode):
            link = os.readlink(path)
            absolute = os.path.normpath(link if os.path.isabs(link) else os.path.join(os.path.dirname(path),link))
            if not inside(root,absolute) or not inside(root,os.path.realpath(path)):
                raise RuntimeError("symlink escapes workspace")
            entry.update(type="symlink",link=os.path.relpath(absolute,os.path.dirname(path)))
        else:
            raise RuntimeError("unsupported special file")
        entries.append(entry)
        if len(entries)>MAX_FILES or total>MAX_BYTES:
            raise RuntimeError("workspace exceeds migration safety limit")
        if entry["type"]=="directory":
            for name in sorted(os.listdir(path)):
                visit(path+"/"+name,name if relative=="." else relative+"/"+name)
    visit(root,".")
    return entries,total
def trusted_dir(path,mode=0o700):
    if os.path.lexists(path):
        info=os.lstat(path)
        if not stat.S_ISDIR(info.st_mode) or info.st_uid!=0:
            raise RuntimeError("untrusted migration directory")
    else: os.mkdir(path,mode)
    os.chmod(path,mode)
def validate_id(value):
    if len(value)!=32 or any(c not in "0123456789abcdef" for c in value):
        raise RuntimeError("invalid migration identifier")
`

const inventoryPython = `
entries,total=scan(SOURCE_ROOT)
print(json.dumps({"source_hash":digest(entries),"entries":len(entries),"bytes":total,
                  "excluded_roots":["/root","/home","/tmp"],
                  "symlinks":sum(e["type"]=="symlink" for e in entries)}))
`

const exportPython = `
validate_id(args["operation_id"])
root=SOURCE_ROOT
before,total=scan(root)
expected=json.loads(json.dumps(before))
for entry in expected:
    entry["uid"]=65532
    entry["gid"]=65532
paths={e["path"] for e in expected}
for name in ("inputs","outputs"):
    if name not in paths:
        expected.append({"path":name,"type":"directory","mode":0o755,"uid":65532,"gid":65532,"mtime_ns":before[0]["mtime_ns"]})
expected.sort(key=lambda e:e["path"])
control=EXPORT_ROOT+"/.wispjar-migration-"+args["operation_id"]
trusted_dir(control)
archive=control+"/workspace.tar"
if os.path.islink(archive): raise RuntimeError("untrusted archive")
with tarfile.open(archive,"w",format=tarfile.PAX_FORMAT) as tar:
    encoded=stable_json(expected)
    header=tarfile.TarInfo("manifest.json")
    header.size=len(encoded)
    tar.addfile(header,io.BytesIO(encoded))
    for entry in expected:
        relative=entry["path"]
        name="workspace" if relative=="." else "workspace/"+relative
        header=tarfile.TarInfo(name)
        header.mode=entry["mode"]
        header.uid=entry["uid"]; header.gid=entry["gid"]
        header.mtime=entry["mtime_ns"]//1000000000
        if entry["type"]=="directory":
            header.type=tarfile.DIRTYPE
            tar.addfile(header)
        elif entry["type"]=="symlink":
            header.type=tarfile.SYMTYPE;header.linkname=entry["link"]
            tar.addfile(header)
        else:
            header.size=entry["size"]
            with os.fdopen(os.open(root+"/"+relative,os.O_RDONLY|os.O_NOFOLLOW|getattr(os,"O_NOATIME",0)),"rb") as f:
                tar.addfile(header,f)
after,_=scan(root)
if before!=after: raise RuntimeError("source_changed: source changed during export")
print(json.dumps({"archive":archive,"source_hash":digest(before),"manifest_hash":digest(expected),
                  "entries":len(expected),"bytes":total,"archive_hash":read_hash(archive)}))
`

const prepareTargetPython = `
validate_id(args["operation_id"]);validate_id(args["session_id"])
trusted_dir(TARGET_ROOT,0o755)
trusted_dir(TARGET_ROOT+"/.wispjar")
trusted_dir(TARGET_ROOT+"/.wispjar/migrations")
trusted_dir(TARGET_ROOT+"/.wispjar/migrations/"+args["operation_id"])
`

const importPython = `
validate_id(args["operation_id"]);validate_id(args["session_id"])
control=TARGET_ROOT+"/.wispjar/migrations/"+args["operation_id"]
trusted_dir(control)
archive=control+"/workspace.tar"
if read_hash(archive)!=args["archive_hash"]: raise RuntimeError("archive hash mismatch")
stage=control+"/workspace"
final=TARGET_ROOT+"/"+args["session_id"]
with tarfile.open(archive,"r:") as tar:
    member=tar.getmember("manifest.json")
    if not member.isfile() or member.size>64*1024*1024: raise RuntimeError("invalid manifest")
    expected=json.load(tar.extractfile(member))
    if digest(expected)!=args["manifest_hash"]: raise RuntimeError("manifest hash mismatch")
    if os.path.lexists(final):
        actual,_=scan(final)
        actual.sort(key=lambda e:e["path"])
        if actual!=expected: raise RuntimeError("existing destination differs; refusing overwrite")
        print(json.dumps({"verified":True,"published":True}))
        sys.exit(0)
    if os.path.lexists(stage):
        if os.path.islink(stage): raise RuntimeError("untrusted stage")
        shutil.rmtree(stage)
    os.mkdir(stage,0o700)
    entries={e["path"]:e for e in expected}
    for entry in expected:
        relative=entry["path"]
        if relative!="." and (relative.startswith("/") or any(p in ("",".","..") for p in relative.split("/"))):
            raise RuntimeError("invalid archive path")
        destination=stage if relative=="." else stage+"/"+relative
        if not inside(stage,os.path.realpath(os.path.dirname(destination))) and relative!=".":
            raise RuntimeError("archive parent escapes")
        if relative!=".": os.makedirs(os.path.dirname(destination),exist_ok=True)
        name="workspace" if relative=="." else "workspace/"+relative
        member=tar.getmember(name)
        if entry["type"]=="directory":
            if not member.isdir(): raise RuntimeError("archive type differs")
            os.makedirs(destination,exist_ok=True)
        elif entry["type"]=="symlink":
            if not member.issym() or member.linkname!=entry["link"]: raise RuntimeError("archive link differs")
            resolved=os.path.normpath(os.path.join(os.path.dirname(destination),entry["link"]))
            if not inside(stage,resolved): raise RuntimeError("archive link escapes")
            os.symlink(entry["link"],destination)
        elif entry["type"]=="file":
            if not member.isfile() or member.size!=entry["size"]: raise RuntimeError("archive file differs")
            with tar.extractfile(member) as source, open(destination,"xb") as out:
                shutil.copyfileobj(source,out,1024*1024)
        else: raise RuntimeError("invalid manifest type")
    for entry in sorted(expected,key=lambda e:e["path"].count("/"),reverse=True):
        destination=stage if entry["path"]=="." else stage+"/"+entry["path"]
        os.chown(destination,65532,65532,follow_symlinks=False)
        if entry["type"]!="symlink": os.chmod(destination,entry["mode"])
        os.utime(destination,ns=(entry["mtime_ns"],entry["mtime_ns"]),follow_symlinks=False)
    actual,_=scan(stage)
    actual.sort(key=lambda e:e["path"])
    if actual!=expected: raise RuntimeError("extracted metadata/content differs")
    os.rename(stage,final)
    print(json.dumps({"verified":True,"published":True}))
`

const verifySourcePython = `
entries,total=scan(SOURCE_ROOT)
if digest(entries)!=args["source_hash"]: raise RuntimeError("source_changed: source changed after export")
print(json.dumps({"verified":True}))
`
