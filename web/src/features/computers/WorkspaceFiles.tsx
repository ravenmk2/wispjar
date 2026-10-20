import { ArrowLeft, FileText, Folder, RefreshCw, Upload, X } from "lucide-react"
import { useCallback, useEffect, useRef, useState } from "react"
import { toast } from "sonner"
import { errorMessage } from "@/api/client"
import { sessionsApi, type FileScope } from "@/api/sessions"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { FilePreview } from "@/features/sandbox/FilePreview"
import { formatBytes } from "@/lib/format"
import { workspaceRoot } from "@/lib/computers"
import type { SandboxFileItem } from "@/lib/types"

export function WorkspaceFiles({ sessionId, running, onClose }: { sessionId: string; running: boolean; onClose: () => void }) {
  const [scope, setScope] = useState<FileScope>("workspace")
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
  useEffect(() => { setSelected(null); void refresh(); return () => { request.current++ } }, [refresh])
  const wasRunning = useRef(running)
  useEffect(() => { if (wasRunning.current && !running) void refresh(); wasRunning.current = running }, [running, refresh])
  function relative(item: SandboxFileItem): string { return item.path.startsWith(`${root}/`) ? item.path.slice(root.length + 1) : item.path }
  async function upload(file?: File) {
    if (!file || uploading) return
    setUploading(true)
    try { await sessionsApi.uploadFile(sessionId, file); toast.success("已上传到 inputs"); await refresh() }
    catch (e) { toast.error(errorMessage(e)) } finally { setUploading(false); if (uploadRef.current) uploadRef.current.value = "" }
  }
  async function save() {
    if (!selected || !destination?.trim()) return
    setSaving(true)
    try { await sessionsApi.saveToStorage(sessionId, selected.path, destination.trim()); toast.success("已保存到我的存储"); setDestination(null) }
    catch (e) { toast.error(errorMessage(e)) } finally { setSaving(false) }
  }
  return <aside className="flex h-full w-[420px] max-w-[90vw] shrink-0 flex-col border-l bg-background" aria-label="会话文件">
    <div className="flex h-12 items-center gap-2 border-b px-3"><span className="flex-1 text-sm font-medium">文件</span><Button variant="ghost" size="icon" title="刷新文件" onClick={() => void refresh()} disabled={loading}><RefreshCw className="size-4" /></Button><Button variant="ghost" size="icon" title="关闭文件面板" onClick={onClose}><X className="size-4" /></Button></div>
    <div className="space-y-2 border-b p-3">
      <label className="sr-only" htmlFor="file-scope">文件范围</label>
      <select id="file-scope" className="h-8 w-full rounded-md border bg-background px-2 text-sm" value={scope} onChange={(e) => { setScope(e.target.value as FileScope); setPath("."); setSelected(null) }}>
        <option value="workspace">当前会话</option><option value="storage">我的存储</option><option value="agent">智能体文件</option>
      </select>
      <code className="block break-all text-xs text-muted-foreground">{root}{path === "." ? "" : `/${path}`}</code>
      {scope === "workspace" && <><Button variant="outline" size="sm" disabled={uploading} onClick={() => uploadRef.current?.click()}><Upload className="size-3" />{uploading ? "正在上传…" : "上传到 inputs"}</Button><input type="file" ref={uploadRef} className="hidden" onChange={(e) => void upload(e.target.files?.[0])} /></>}
    </div>
    {selected ? <><div className="flex items-center gap-2 border-b p-2"><Button variant="ghost" size="sm" onClick={() => setSelected(null)}><ArrowLeft className="size-3" />文件列表</Button>{scope === "workspace" && <Button variant="outline" size="sm" onClick={() => setDestination(selected.name)}>保存到我的存储</Button>}</div><FilePreview sessionId={sessionId} item={selected} scope={scope} reloadToken={revision} /></> :
      <div className="min-h-0 flex-1 space-y-1 overflow-y-auto p-2">
        {path !== "." && <Button variant="ghost" size="sm" onClick={() => setPath(path.includes("/") ? path.slice(0, path.lastIndexOf("/")) : ".")}><ArrowLeft className="size-3" />上级目录</Button>}
        {error ? <p role="alert" className="p-2 text-sm text-destructive">{error}</p> : loading ? <p role="status" className="p-2 text-sm text-muted-foreground">正在读取文件…</p> : items.length === 0 ? <p className="p-2 text-sm text-muted-foreground">目录为空</p> : items.map((item) => <button type="button" key={item.path} className="flex w-full items-center gap-2 rounded-md px-2 py-2 text-left text-sm hover:bg-accent" onClick={() => item.type === "dir" ? setPath(relative(item)) : setSelected({ ...item, path: relative(item) })}>
          {item.type === "dir" ? <Folder className="size-4 shrink-0" /> : <FileText className="size-4 shrink-0" />}<span className="min-w-0 flex-1 truncate">{item.name}</span>{item.type !== "dir" && <span className="text-xs text-muted-foreground">{formatBytes(item.size)}</span>}
        </button>)}
      </div>}
    <Dialog open={destination !== null} onOpenChange={(open) => !open && !saving && setDestination(null)}><DialogContent><DialogHeader><DialogTitle>保存到我的存储</DialogTitle><DialogDescription>复制并校验成果，保留会话中的原文件。目标路径相对于 /storage。</DialogDescription></DialogHeader><Label htmlFor="save-destination">目标路径</Label><Input id="save-destination" value={destination ?? ""} onChange={(e) => setDestination(e.target.value)} /><Button disabled={saving || !destination?.trim()} onClick={() => void save()}>{saving ? "正在保存…" : "保存"}</Button></DialogContent></Dialog>
  </aside>
}
