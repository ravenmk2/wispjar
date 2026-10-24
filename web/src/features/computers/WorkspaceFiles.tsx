import { ArrowLeft, ArrowUp, FolderDown, HardDriveDownload, Upload, X } from "lucide-react"
import { useCallback, useEffect, useRef, useState } from "react"
import { toast } from "sonner"
import { errorMessage } from "@/api/client"
import { sessionsApi, type FileScope } from "@/api/sessions"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { FilePreview } from "@/features/sandbox/FilePreview"
import { FileIcon } from "@/components/FileIcon"
import { RefreshButton } from "@/components/RefreshButton"
import { downloadWorkspaceFile } from "@/features/sandbox/previewUtils"
import { FilePanel } from "./FilePanel"
import { formatBytes } from "@/lib/format"
import { workspaceRoot } from "@/lib/computers"
import { previewDirectory } from "@/lib/file-preview"
import type { SandboxFileItem } from "@/lib/types"

export function WorkspaceFiles({ sessionId, running, onClose, fixedScope, embedded = false }: { sessionId: string; running: boolean; onClose?: () => void; fixedScope?: FileScope; embedded?: boolean }) {
  const [scope, setScope] = useState<FileScope>(fixedScope ?? "workspace")
  const [path, setPath] = useState(".")
  const [root, setRoot] = useState(workspaceRoot(sessionId))
  const [items, setItems] = useState<SandboxFileItem[]>([])
  const [selected, setSelected] = useState<SandboxFileItem | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState("")
  const [revision, setRevision] = useState(0)
  const [destination, setDestination] = useState<string | null>(null)
  const [saving, setSaving] = useState(false)
  const [uploading, setUploading] = useState(false)
  const uploadRef = useRef<HTMLInputElement>(null)
  const request = useRef(0)
  const location = useRef({ sessionId, scope, path, selectedPath: selected?.path })
  location.current = { sessionId, scope, path, selectedPath: selected?.path }
  const linkRequest = useRef(0)
  const refresh = useCallback(async () => {
    const token = ++request.current
    setLoading(true); setError("")
    try {
      const result = await sessionsApi.files(sessionId, path, scope)
      if (token !== request.current) return
      setRoot(result.root)
      setItems([...result.items].sort((a, b) => {
        if (a.name === "outputs" && scope === "workspace" && path === ".") return -1
        if (b.name === "outputs" && scope === "workspace" && path === ".") return 1
        if (a.type !== b.type) return a.type === "dir" ? -1 : 1
        return a.name.localeCompare(b.name)
      }))
      setRevision((n) => n + 1)
    } catch (e) { if (token === request.current) setError(errorMessage(e)) }
    finally { if (token === request.current) setLoading(false) }
  }, [sessionId, path, scope])
  useEffect(() => { void refresh(); return () => { request.current++ } }, [refresh])
  useEffect(() => { setSelected(null) }, [sessionId, scope])
  const wasRunning = useRef(running)
  useEffect(() => { if (wasRunning.current && !running) void refresh(); wasRunning.current = running }, [running, refresh])
  function relative(item: SandboxFileItem): string { return item.path.startsWith(`${root}/`) ? item.path.slice(root.length + 1) : item.path }
  async function upload(file?: File) {
    if (!file || uploading) return
    const target = { sessionId, scope, path }
    setUploading(true)
    try {
      await sessionsApi.uploadFile(target.sessionId, file, target.path, target.scope)
      toast.success(`已上传到 ${scopeLabel(target.scope)}${target.path === "." ? "" : `/${target.path}`}`)
      if (location.current.sessionId === target.sessionId && location.current.scope === target.scope && location.current.path === target.path) await refresh()
    }
    catch (e) { toast.error(errorMessage(e)) } finally { setUploading(false); if (uploadRef.current) uploadRef.current.value = "" }
  }
  async function save() {
    if (!selected || !destination?.trim()) return
    setSaving(true)
    try { await sessionsApi.saveToStorage(sessionId, selected.path, destination.trim()); toast.success("已保存到存储"); setDestination(null) }
    catch (e) { toast.error(errorMessage(e)) } finally { setSaving(false) }
  }
  function scopeLabel(value: FileScope) { return { workspace: "当前会话", storage: "存储", agent: "智能体文件" }[value] }
  function navigate(next: string) { setSelected(null); setPath(next) }
  async function openLinkedFile(next: string) {
    const target = { ...location.current }
    const sequence = ++linkRequest.current
    const directory = previewDirectory(next)
    try {
      const result = await sessionsApi.files(target.sessionId, directory, target.scope)
      if (sequence !== linkRequest.current || location.current.sessionId !== target.sessionId || location.current.scope !== target.scope || location.current.path !== target.path || location.current.selectedPath !== target.selectedPath) return
      const item = result.items.find((item) => item.name === next.split("/").pop())
      if (!item || item.type === "dir") { toast.error("链接目标不是可预览文件"); return }
      setPath(directory)
      setSelected({ ...item, path: next })
    } catch (error) { toast.error(errorMessage(error)) }
  }
  function back() { if (selected) setSelected(null); else navigate(path.includes("/") ? path.slice(0, path.lastIndexOf("/")) : ".") }
  async function downloadDirectory(target: string) {
    try { await downloadWorkspaceFile(sessionId, target, scope, true) }
    catch (e) { toast.error(errorMessage(e)) }
  }
  const segments = path === "." ? [] : path.split("/")
  const navigation = <nav aria-label="目录导航" className="flex min-w-0 flex-1 items-center gap-1">
      <Button variant="ghost" size="icon" className="size-7 shrink-0" title={selected ? "返回文件列表" : "返回上级目录"} aria-label={selected ? "返回文件列表" : "返回上级目录"} disabled={!selected && path === "."} onClick={back}>{selected ? <ArrowLeft className="size-3.5" /> : <ArrowUp className="size-3.5" />}</Button>
      <div className="flex min-w-0 flex-1 items-center overflow-hidden text-[11px] text-muted-foreground" title={`${root}${path === "." ? "" : `/${path}`}${selected ? `/${selected.name}` : ""}`}>
        <button className="shrink-0 rounded px-1 py-1 hover:bg-accent" onClick={() => navigate(".")}>{scopeLabel(scope)}</button>
        {segments.map((segment, i) => <span key={i} className="flex min-w-0 shrink items-center"><span className="shrink-0 px-0.5 opacity-50">/</span><button className="max-w-40 truncate rounded px-1 py-1 hover:bg-accent last:text-foreground" title={segment} onClick={() => navigate(segments.slice(0, i + 1).join("/"))}>{segment}</button></span>)}
        {selected && <><span className="shrink-0 px-0.5 opacity-50">/</span><span className="min-w-12 flex-1 truncate px-1 text-foreground" title={selected.name}>{selected.name}</span></>}
      </div>
      {!selected && <Button variant="ghost" size="icon" className="size-7 shrink-0" title="打包下载当前目录（ZIP）" aria-label="打包下载当前目录" onClick={() => void downloadDirectory(path)}><FolderDown className="size-3.5" /></Button>}
    </nav>
  return <FilePanel embedded={embedded}>
    <div className="flex h-12 shrink-0 items-center gap-1 border-b px-3">
      <span className="mr-1 text-sm font-medium">文件</span>
      {!fixedScope && <><label className="sr-only" htmlFor="file-scope">文件目录</label>
      <select id="file-scope" className="h-7 min-w-0 max-w-36 flex-1 rounded-md border-0 bg-transparent px-1 text-xs text-muted-foreground hover:bg-accent focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring" value={scope} onChange={(e) => { setScope(e.target.value as FileScope); setPath("."); setSelected(null) }}>
        <option value="workspace">当前会话</option><option value="storage">存储</option><option value="agent">智能体文件</option>
      </select></>}
      <div className="ml-auto flex shrink-0 items-center gap-0.5">
        <Button variant="ghost" size="icon" className="size-7" title={uploading ? "正在上传…" : `上传文件到当前目录：${scopeLabel(scope)}${path === "." ? "" : `/${path}`}`} aria-label="上传文件到当前目录" disabled={uploading} onClick={() => uploadRef.current?.click()}><Upload className="size-3.5" /></Button>
        <RefreshButton variant="ghost" size="icon" className="size-7" iconClassName="size-3.5" title="刷新文件" aria-label="刷新文件" onClick={() => void refresh()} refreshing={loading}>{null}</RefreshButton>
        {!embedded && <Button variant="ghost" size="icon" className="size-7" title="关闭文件面板" aria-label="关闭文件面板" onClick={onClose}><X className="size-3.5" /></Button>}
      </div>
      <input type="file" ref={uploadRef} className="hidden" onChange={(e) => void upload(e.target.files?.[0])} />
    </div>
    {!selected && <div className="flex h-9 shrink-0 items-center border-b px-2">{navigation}</div>}
    {selected ? <FilePreview navigation={navigation} sessionId={sessionId} item={selected} scope={scope} reloadToken={revision} onNavigate={(path) => void openLinkedFile(path)} actions={scope === "workspace" && <Button variant="ghost" size="icon" className="size-7 shrink-0" title="保存到存储" aria-label="保存到存储" disabled={saving} onClick={() => setDestination(selected.name)}><HardDriveDownload className="size-3.5" /></Button>} /> :
      <div className="min-h-0 flex-1 space-y-1 overflow-y-auto p-2">
        {error ? <p role="alert" className="p-2 text-sm text-destructive">{error}</p> : loading ? <p role="status" className="p-2 text-sm text-muted-foreground">正在读取文件…</p> : items.length === 0 ? <p className="p-2 text-sm text-muted-foreground">目录为空</p> : items.map((item) => <div key={item.path} className="group flex items-center gap-2 rounded-md px-2 hover:bg-accent">
          <button type="button" className="flex min-w-0 flex-1 items-center gap-2 py-2 text-left text-sm" onClick={() => item.type === "dir" ? navigate(relative(item)) : setSelected({ ...item, path: relative(item) })}>
            <FileIcon name={item.name} directory={item.type === "dir"} /><span className="min-w-0 flex-1 truncate" title={item.name}>{item.name}</span>{item.type !== "dir" && <span className="shrink-0 text-xs text-muted-foreground">{formatBytes(item.size)}</span>}
          </button>
          {item.type === "dir" && <Button variant="ghost" size="icon" className="size-7 shrink-0 opacity-40 group-hover:opacity-100 focus-visible:opacity-100" title={`打包下载 ${item.name}`} aria-label={`打包下载 ${item.name}`} onClick={() => void downloadDirectory(relative(item))}><FolderDown className="size-3.5" /></Button>}
        </div>)}
      </div>}
    <Dialog open={destination !== null} onOpenChange={(open) => !open && !saving && setDestination(null)}><DialogContent><DialogHeader><DialogTitle>保存到存储</DialogTitle><DialogDescription>复制并校验成果，保留会话中的原文件。目标路径相对于 /storage。</DialogDescription></DialogHeader><Label htmlFor="save-destination">目标路径</Label><Input id="save-destination" value={destination ?? ""} onChange={(e) => setDestination(e.target.value)} /><Button disabled={saving || !destination?.trim()} onClick={() => void save()}>{saving ? "正在保存…" : "保存"}</Button></DialogContent></Dialog>
  </FilePanel>
}
