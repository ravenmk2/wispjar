import { ArrowDown, ArrowUp, ChevronRight, Download, FolderDown, FolderOpen, FolderPlus, ListTree, Loader2, Maximize2, Minimize2, PanelLeft, Search, Upload } from "lucide-react"
import { useCallback, useEffect, useMemo, useRef, useState, type CSSProperties, type ReactNode } from "react"
import { toast } from "sonner"
import { errorMessage } from "@/api/client"
import { filesApi, type FileSpaceTarget } from "@/api/files"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { FileIcon } from "@/components/FileIcon"
import { RefreshButton } from "@/components/RefreshButton"
import { FilePreview } from "@/features/sandbox/FilePreview"
import { fileAncestors, fileParent, sortFiles, validFolderName } from "@/lib/file-browser"
import { formatBytes, formatDateTime } from "@/lib/format"
import type { SandboxFileItem } from "@/lib/types"
import { cn } from "@/lib/utils"

export interface FileLocation { directory: string; selected: SandboxFileItem | null; expanded: string[] }
interface BrowserProps {
  target?: FileSpaceTarget; rootName: string; location: FileLocation
  onLocation: (location: FileLocation) => void; onConnecting: (connecting: boolean) => void; onWriting: (writing: boolean) => void
  onAccessError: (error: unknown) => boolean; onAccessReady: () => void
  sourceSelector: ReactNode; computerSelector: ReactNode; unavailable: ReactNode
}

export function FileBrowser({ target, rootName, location, onLocation, onConnecting, onWriting, onAccessError, onAccessReady, sourceSelector, computerSelector, unavailable }: BrowserProps) {
  const source = useMemo(() => target ? filesApi.source(target) : undefined, [target])
  const [directories, setDirectories] = useState<Record<string, SandboxFileItem[]>>({})
  const [pending, setPending] = useState<Set<string>>(new Set())
  const [errors, setErrors] = useState<Record<string, string>>({})
  const [ready, setReady] = useState(false)
  const [query, setQuery] = useState("")
  const [revision, setRevision] = useState(0)
  const [ascending, setAscending] = useState(true)
  const [treeOpen, setTreeOpen] = useState(false)
  const [immersive, setImmersive] = useState(false)
  const [uploading, setUploading] = useState(false)
  const [folderOpen, setFolderOpen] = useState(false)
  const [folderName, setFolderName] = useState("")
  const [folderError, setFolderError] = useState("")
  const [creating, setCreating] = useState(false)
  const [dragging, setDragging] = useState(false)
  const [width, setWidth] = useState(() => { const stored = Number(localStorage.getItem("wispjar-file-tree-width")); return stored >= 230 && stored <= 480 ? stored : 292 })
  const [resizing, setResizing] = useState(false)
  const tree = useRef<HTMLElement>(null)
  const uploadInput = useRef<HTMLInputElement>(null)
  const mounted = useRef(true)
  const accessKey = useRef(source?.key)
  const latestLocation = useRef(location)
  const latestCallbacks = useRef({ onAccessError, onConnecting, onAccessReady })
  latestLocation.current = location
  latestCallbacks.current = { onAccessError, onConnecting, onAccessReady }
  accessKey.current = source?.key
  const requests = useRef(new Map<string, number>())
  const writes = useRef(false)
  const rootItem: SandboxFileItem = { name: rootName, path: ".", type: "dir", size: 0, modified_at: "" }
  const selected = location.selected
  const currentPath = selected?.path ?? location.directory
  const currentItems = directories[location.directory] ?? []
  const busy = uploading || creating
  useEffect(() => { mounted.current = true; return () => { mounted.current = false; onConnecting(false); onWriting(false) } }, [onConnecting, onWriting])
  useEffect(() => { const close = (event: KeyboardEvent) => { if (event.key === "Escape") { setImmersive(false); setTreeOpen(false) } }; window.addEventListener("keydown", close); return () => window.removeEventListener("keydown", close) }, [])

  const loadDirectory = useCallback(async (path: string): Promise<SandboxFileItem[] | undefined> => {
    if (!target || !source) return
    const key = source.key, sequence = (requests.current.get(path) ?? 0) + 1
    requests.current.set(path, sequence)
    const active = () => mounted.current && accessKey.current === key && requests.current.get(path) === sequence
    setPending(old => new Set(old).add(path)); setErrors(old => ({ ...old, [path]: "" }))
    try {
      const result = await filesApi.list(target, path)
      if (!active()) return
      const items = sortFiles(result.items)
      setDirectories(old => ({ ...old, [path]: items }))
      return items
    } catch (error) { if (active()) setErrors(old => ({ ...old, [path]: errorMessage(error) })); throw error }
    finally { if (active()) setPending(old => { const next = new Set(old); next.delete(path); return next }) }
  }, [source, target])
  useEffect(() => {
    let active = true
    setDirectories({}); setErrors({}); setPending(new Set()); setReady(false)
    if (!source) { latestCallbacks.current.onConnecting(false); return }
    latestCallbacks.current.onConnecting(true)
    void loadDirectory(".").then(async items => {
      if (!active || !items) return
      setReady(true); latestCallbacks.current.onAccessReady()
      const paths = new Set([...latestLocation.current.expanded, ...fileAncestors(latestLocation.current.directory)])
      await Promise.allSettled([...paths].filter(path => path !== ".").map(loadDirectory))
    }).catch(error => { if (active) latestCallbacks.current.onAccessError(error) }).finally(() => { if (active) latestCallbacks.current.onConnecting(false) })
    return () => { active = false }
  }, [source, loadDirectory])

  function navigate(item: SandboxFileItem) {
    if (busy) return
    const directory = item.type === "dir" ? item.path : fileParent(item.path)
    const expanded = [...new Set([...location.expanded, ...fileAncestors(directory)])]
    onLocation({ directory, selected: item.type === "dir" ? null : item, expanded })
    for (const path of fileAncestors(directory)) if (!directories[path] && !pending.has(path)) void loadDirectory(path).catch(() => {})
    setTreeOpen(false)
  }
  function toggleDirectory(path: string) {
    const open = location.expanded.includes(path)
    onLocation({ ...location, expanded: open ? location.expanded.filter(x => x !== path) : [...location.expanded, path] })
    if (!open && !directories[path] && !pending.has(path)) void loadDirectory(path).catch(() => {})
  }
  async function refresh() {
    if (!source || busy) return
    const key = source.key, old = latestLocation.current
    onConnecting(true)
    const result = await Promise.allSettled([...new Set([".", old.directory, ...old.expanded])].map(loadDirectory))
    if (accessKey.current !== key || !mounted.current) return
    onConnecting(false)
    if (result[0].status === "fulfilled" && result[0].value) { setReady(true); onAccessReady() }
    else if (result[0].status === "rejected") { setReady(false); onAccessError(result[0].reason) }
    setRevision(n => n + 1)
  }
  async function openLinkedFile(path: string) {
    const key = source?.key, origin = currentPath
    try {
      const items = await loadDirectory(fileParent(path))
      if (!items || key !== accessKey.current || origin !== (latestLocation.current.selected?.path ?? latestLocation.current.directory)) return
      const item = items.find(x => x.path === path)
      if (!item) { toast.error("链接目标不存在"); return }
      navigate(item)
    } catch (e) { toast.error(errorMessage(e)) }
  }
  async function download(item: SandboxFileItem) { try { await source?.download(item.path, item.type === "dir") } catch (e) { toast.error(errorMessage(e)) } }
  async function upload(files: FileList | File[]) {
    if (!target || !source || !ready || writes.current || !files.length) return
    const destination = location.directory, key = source.key
    writes.current = true; setUploading(true); onWriting(true)
    const notification = toast.loading(`正在上传到 ${destination === "." ? rootName : destination}`)
    let done = 0
    try {
      for (const file of Array.from(files)) {
        try { if (file.size > 32 * 1024 * 1024) throw new Error("单个文件不能超过 32 MiB"); await filesApi.upload(target, file, destination); done++ }
        catch (e) { toast.error(`${file.name}：${errorMessage(e)}`) }
      }
      if (mounted.current && accessKey.current === key) await loadDirectory(destination).catch(() => {})
      if (done) toast.success(`已上传 ${done} 个文件`)
    } finally { toast.dismiss(notification); writes.current = false; if (mounted.current) { setUploading(false); onWriting(false) } if (uploadInput.current) uploadInput.current.value = "" }
  }
  function newFolder() { setFolderName(""); setFolderError(""); setFolderOpen(true) }
  async function createFolder() {
    if (!target || writes.current) return
    const name = folderName.trim()
    if (!validFolderName(name)) { setFolderError("名称不能为空，且不能包含斜杠、父目录或控制字符。"); return }
    writes.current = true; setCreating(true); onWriting(true)
    try { await filesApi.mkdir(target, location.directory === "." ? name : `${location.directory}/${name}`); setFolderOpen(false); await loadDirectory(location.directory); toast.success("文件夹已创建") }
    catch (e) { setFolderError(errorMessage(e)) }
    finally { writes.current = false; if (mounted.current) { setCreating(false); onWriting(false) } }
  }
  function directoryRow(item: SandboxFileItem, depth: number): ReactNode {
    const isDir = item.type === "dir", open = location.expanded.includes(item.path), active = currentPath === item.path
    return <div key={item.path}>
      <div className={cn("group flex min-h-9 items-center rounded-md pr-1 hover:bg-accent/60", active && (isDir ? "bg-selected" : "bg-primary-soft text-link"))} style={{ paddingLeft: depth * 15 }}>
        {isDir ? <button type="button" aria-expanded={open} aria-label={`${open ? "折叠" : "展开"} ${item.name}`} className="flex size-6 shrink-0 items-center justify-center rounded text-muted-foreground" disabled={busy} onClick={() => toggleDirectory(item.path)}><ChevronRight className={cn("size-3 transition-transform", open && "rotate-90")} /></button> : <span className="w-6 shrink-0" />}
        <button type="button" disabled={busy} aria-current={active ? "true" : undefined} className="flex min-w-0 flex-1 items-center gap-2 py-1.5 text-left text-xs" title={item.path === "." ? rootName : item.path} onClick={() => navigate(item)}><FileIcon name={item.name} directory={isDir} className="size-4 shrink-0" /><span className="truncate">{item.name}</span></button>
        {isDir && <Button variant="ghost" size="icon" className="size-6 shrink-0 opacity-50 group-hover:opacity-100 focus-visible:opacity-100" disabled={!ready} title={`打包下载 ${item.name}`} aria-label={`打包下载 ${item.name}`} onClick={() => void download(item)}><FolderDown className="size-3.5" /></Button>}
      </div>
      {isDir && open && !query && <>{pending.has(item.path) && !directories[item.path] && <p role="status" className="py-2 text-xs text-muted-foreground" style={{ paddingLeft: depth * 15 + 28 }}>正在读取…</p>}{errors[item.path] && <div role="alert" className="px-3 py-2 text-xs text-destructive">{errors[item.path]}<button type="button" className="ml-2 underline" onClick={() => void (item.path === "." ? refresh() : loadDirectory(item.path)).catch(() => {})}>重试</button></div>}{directories[item.path]?.map(child => directoryRow(child, depth + 1))}</>}
    </div>
  }
  const filtered = query ? [...new Map(Object.values(directories).flat().filter(item => item.name.toLowerCase().includes(query.toLowerCase())).map(item => [item.path, item])).values()] : []
  const folderActions = <div className="flex flex-wrap gap-2"><Button variant="outline" size="sm" onClick={() => void download({ ...rootItem, path: location.directory })}><FolderDown className="mr-1.5 size-4" />打包下载</Button><Button variant="outline" size="sm" disabled={busy} onClick={newFolder}><FolderPlus className="mr-1.5 size-4" />新建文件夹</Button><Button size="sm" disabled={busy} onClick={() => uploadInput.current?.click()}>{uploading ? <Loader2 className="mr-1.5 size-4 animate-spin" /> : <Upload className="mr-1.5 size-4" />}上传文件</Button></div>

  return <div className={cn("relative flex min-h-0 flex-1 overflow-hidden bg-background", immersive && "fixed inset-0 z-40")} onDragOver={e => { if (ready && e.dataTransfer.types.includes("Files")) { e.preventDefault(); setDragging(true) } }} onDragLeave={e => { if (!e.currentTarget.contains(e.relatedTarget as Node | null)) setDragging(false) }} onDrop={e => { e.preventDefault(); setDragging(false); void upload(e.dataTransfer.files) }}>
    {treeOpen && <button type="button" className="absolute inset-0 z-10 bg-black/20 lg:hidden" aria-label="关闭目录" onClick={() => setTreeOpen(false)} />}
    <aside ref={tree} aria-label="文件导航" className={cn("absolute inset-y-0 left-0 z-20 flex w-[min(300px,88vw)] shrink-0 flex-col border-r bg-background lg:relative lg:z-auto lg:w-[var(--tree-width)]", !treeOpen && "hidden lg:flex", immersive && "!hidden")} style={{ "--tree-width": `${width}px` } as CSSProperties}>
      <div className="shrink-0 px-4 pb-3 pt-4">{sourceSelector}<div className="relative mt-4"><Search className="absolute left-2.5 top-2.5 size-3.5 text-muted-foreground" /><Input aria-label="筛选已加载文件" placeholder="筛选已加载文件" className="h-9 border-0 bg-muted/60 pl-8 text-xs" value={query} onChange={e => setQuery(e.target.value)} /></div></div>
      <div className="flex shrink-0 items-center justify-between px-4 pb-1 text-xs text-muted-foreground"><span>{query ? "已加载文件中的匹配结果" : "目录"}</span><div className="flex items-center"><Button size="icon" variant="ghost" className="size-7" disabled={!ready || busy} title="上传到当前目录" aria-label="上传到当前目录" onClick={() => uploadInput.current?.click()}>{uploading ? <Loader2 className="size-3.5 animate-spin" /> : <Upload className="size-3.5" />}</Button><Button size="icon" variant="ghost" className="size-7" disabled={!ready || busy} title="新建文件夹" aria-label="新建文件夹" onClick={newFolder}><FolderPlus className="size-3.5" /></Button><Button size="icon" variant="ghost" className="size-7" disabled={busy} title="折叠目录" aria-label="折叠目录" onClick={() => onLocation({ ...location, expanded: ["."] })}><ListTree className="size-3.5" /></Button></div></div>
      <div className="min-h-0 flex-1 overflow-auto px-2 pb-3">{target && (query ? (filtered.length ? filtered.map(item => directoryRow(item, 0)) : <p className="p-4 text-center text-xs text-muted-foreground">没有匹配的已加载文件</p>) : directoryRow(rootItem, 0))}</div>
      <div className="shrink-0 border-t p-2">{computerSelector}</div>
    </aside>
    {!immersive && <div role="separator" aria-label="调整目录栏宽度" aria-orientation="vertical" aria-valuemin={230} aria-valuemax={480} aria-valuenow={width} tabIndex={0} className="z-20 -ml-1 hidden w-1 shrink-0 cursor-col-resize hover:bg-primary/20 focus-visible:bg-primary/20 lg:block"
      onKeyDown={e => { if (["ArrowLeft", "ArrowRight"].includes(e.key)) { e.preventDefault(); const next = Math.max(230, Math.min(480, width + (e.key === "ArrowLeft" ? -20 : 20))); setWidth(next); localStorage.setItem("wispjar-file-tree-width", String(next)) } }}
      onPointerDown={e => { if (e.button !== 0) return; e.preventDefault(); e.currentTarget.setPointerCapture(e.pointerId); setResizing(true) }} onPointerMove={e => { if (resizing && tree.current) setWidth(Math.max(230, Math.min(480, e.clientX - tree.current.getBoundingClientRect().left))) }} onPointerUp={() => { setResizing(false); localStorage.setItem("wispjar-file-tree-width", String(width)) }} onPointerCancel={() => setResizing(false)} onLostPointerCapture={() => setResizing(false)} />}
    <section aria-label="文件预览" className={cn("flex min-w-0 flex-1 flex-col", resizing && "select-none [&_iframe]:pointer-events-none")}>
      <div className="flex h-12 shrink-0 items-center gap-1 border-b px-3 sm:px-5">
        {!immersive && <Button variant="ghost" size="icon" className="size-7 shrink-0 lg:hidden" aria-label="显示目录" onClick={() => setTreeOpen(true)}><PanelLeft className="size-4" /></Button>}
        <nav aria-label="目录路径" className="flex min-w-0 flex-1 items-center gap-1 overflow-x-auto whitespace-nowrap text-xs text-muted-foreground">{fileAncestors(location.directory).map((path, index) => <span key={path} className="flex items-center gap-1">{index > 0 && <ChevronRight className="size-3 shrink-0 opacity-60" />}<button type="button" disabled={busy} className="max-w-48 truncate rounded px-1 py-1 hover:bg-accent" title={path === "." ? rootName : path} onClick={() => navigate({ ...rootItem, path, name: path === "." ? rootName : path.split("/").pop()! })}>{path === "." ? rootName : path.split("/").pop()}</button></span>)}{selected && <><ChevronRight className="size-3 shrink-0 opacity-60" /><span className="px-1 text-foreground">{selected.name}</span></>}</nav>
        <RefreshButton variant="ghost" size="icon" className="size-7 shrink-0" disabled={!target || busy} refreshing={pending.size > 0} aria-label="刷新文件" title="刷新文件" onClick={() => void refresh()}>{null}</RefreshButton><Button variant="ghost" size="icon" className="size-7 shrink-0" aria-label={immersive ? "退出专注视图" : "展开预览"} onClick={() => setImmersive(v => !v)}>{immersive ? <Minimize2 className="size-3.5" /> : <Maximize2 className="size-3.5" />}</Button>
      </div>
      {!target ? unavailable : !ready ? <div className="flex min-h-0 flex-1 items-center justify-center p-6">{errors["."] ? <div role="alert" className="max-w-md space-y-3 text-center"><p className="text-sm text-destructive">{errors["."]}</p><p className="text-xs text-muted-foreground">可在目录树底部切换访问电脑。</p><Button variant="outline" size="sm" onClick={() => void refresh()}>重试</Button></div> : <p role="status" className="flex items-center gap-2 text-sm text-muted-foreground"><Loader2 className="size-4 animate-spin" />正在连接并读取文件…</p>}</div> : selected && source ? <FilePreview key={selected.path} sessionId="" scope={target.scope} source={source} fullPage item={selected} reloadToken={revision} onNavigate={path => void openLinkedFile(path)} /> : <div className="min-h-0 flex-1 overflow-auto px-4 py-6 sm:px-7">
        <div className="mb-6 flex flex-wrap items-center justify-between gap-3"><div><h2 className="text-lg font-medium">{location.directory === "." ? rootName : location.directory.split("/").pop()}</h2><p className="mt-1 text-xs text-muted-foreground">{currentItems.length} 个项目</p></div>{folderActions}</div>
        {errors[location.directory] ? <div role="alert" className="text-sm text-destructive">{errors[location.directory]}<Button variant="ghost" onClick={() => void loadDirectory(location.directory).catch(() => {})}>重试</Button></div> : pending.has(location.directory) && !directories[location.directory] ? <p role="status" className="py-8 text-sm text-muted-foreground">正在读取目录…</p> : currentItems.length ? <table className="w-full table-fixed text-left text-xs"><thead className="border-b text-muted-foreground"><tr><th className="w-[55%] py-3 font-normal"><button className="flex items-center gap-1" onClick={() => setAscending(v => !v)}>名称{ascending ? <ArrowUp className="size-3" /> : <ArrowDown className="size-3" />}</button></th><th className="py-3 font-normal">大小</th><th className="hidden py-3 font-normal xl:table-cell">修改时间</th><th className="w-8"><span className="sr-only">下载</span></th></tr></thead><tbody>{sortFiles(currentItems, ascending).map(item => <tr key={item.path} className="border-b hover:bg-accent/40"><td className="py-3 pr-3"><button disabled={busy} className="flex w-full min-w-0 items-center gap-2 text-left" onClick={() => navigate(item)}><FileIcon name={item.name} directory={item.type === "dir"} className="size-4 shrink-0" /><span className="truncate" title={item.name}>{item.name}</span></button></td><td className="text-muted-foreground">{item.type === "dir" ? "—" : formatBytes(item.size)}</td><td className="hidden truncate text-muted-foreground xl:table-cell" title={formatDateTime(item.modified_at)}>{formatDateTime(item.modified_at)}</td><td><Button variant="ghost" size="icon" className="size-7" title={`${item.type === "dir" ? "打包下载" : "下载"} ${item.name}`} aria-label={`${item.type === "dir" ? "打包下载" : "下载"} ${item.name}`} onClick={() => void download(item)}>{item.type === "dir" ? <FolderDown className="size-3.5" /> : <Download className="size-3.5" />}</Button></td></tr>)}</tbody></table> : <div className="flex flex-col items-center gap-3 py-12 text-muted-foreground"><FolderOpen className="size-8" /><p className="text-sm">这个文件夹还是空的</p></div>}
        <div className="mt-7 flex items-center justify-center gap-2 rounded-lg border border-dashed px-3 py-6 text-xs text-muted-foreground"><Upload className="size-4" /><span>拖入文件到当前目录，或 <button disabled={busy} className="text-link" onClick={() => uploadInput.current?.click()}>选择文件上传</button></span></div>
      </div>}
    </section>
    {dragging && <div className="pointer-events-none absolute inset-1 z-30 flex items-center justify-center rounded-lg border-2 border-dashed border-primary bg-background/90"><p className="text-sm">松开以上传到当前目录</p></div>}
    <input ref={uploadInput} type="file" multiple className="hidden" onChange={e => { if (e.target.files) void upload(e.target.files) }} />
    <Dialog open={folderOpen} onOpenChange={open => { if (!creating) setFolderOpen(open) }}><DialogContent className="max-w-md"><DialogHeader><DialogTitle>新建文件夹</DialogTitle><DialogDescription>创建位置：{rootName}{location.directory === "." ? "" : ` / ${location.directory}`}</DialogDescription></DialogHeader><form onSubmit={e => { e.preventDefault(); void createFolder() }} className="space-y-4"><Label htmlFor="new-directory-name">文件夹名称</Label><Input id="new-directory-name" maxLength={80} value={folderName} onChange={e => setFolderName(e.target.value)} autoComplete="off" autoFocus disabled={creating} />{folderError && <p role="alert" className="text-sm text-destructive">{folderError}</p>}<div className="flex justify-end gap-2"><Button type="button" variant="outline" disabled={creating} onClick={() => setFolderOpen(false)}>取消</Button><Button type="submit" disabled={creating || !folderName.trim()}>{creating ? "正在创建…" : "创建文件夹"}</Button></div></form></DialogContent></Dialog>
  </div>
}
