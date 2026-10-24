import { Bot, Check, ChevronUp, Loader2, MessageSquare, Monitor, Search, Sparkles } from "lucide-react"
import { useEffect, useState } from "react"
import { filesApi } from "@/api/files"
import { sessionsApi, type FileScope } from "@/api/sessions"
import { errorMessage } from "@/api/client"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuLabel, DropdownMenuSeparator, DropdownMenuTrigger } from "@/components/ui/dropdown-menu"
import { computerStatus, type Computer } from "@/lib/computers"
import { cn } from "@/lib/utils"

export interface FileObject { id: string; name: string; description?: string }

export function FileSpacePicker({ orgId, scope, selected, open, onOpenChange, onSelect }: { orgId: string; scope: FileScope; selected?: string; open: boolean; onOpenChange: (open: boolean) => void; onSelect: (object: FileObject) => void }) {
  const [query, setQuery] = useState("")
  const [status, setStatus] = useState<"active" | "archived">("active")
  const [rows, setRows] = useState<FileObject[]>([])
  const [cursor, setCursor] = useState<string>()
  const [page, setPage] = useState<string>()
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState("")
  const [retry, setRetry] = useState(0)
  useEffect(() => { if (open) { setQuery(""); setPage(undefined); setRows([]); setCursor(undefined) } }, [open, scope])
  useEffect(() => {
    if (!open) return
    let active = true
    setLoading(true); setError("")
    const timer = setTimeout(() => {
      const request = scope === "agent"
        ? filesApi.agents(orgId).then(result => ({ items: result.items.filter(a => a.name.toLowerCase().includes(query.toLowerCase())).map(a => ({ id: a.id, name: a.name, description: a.status === "archived" ? "已归档智能体的个人文件" : "智能体的个人长期文件" })), next_cursor: undefined }))
        : sessionsApi.list(orgId, { q: query, status, limit: 50, cursor: page }).then(result => ({ items: result.items.map(s => ({ id: s.id, name: s.title || "新聊天", description: s.agent_name })), next_cursor: result.next_cursor }))
      void request.then(result => { if (active) { setRows(old => page ? [...old, ...result.items] : result.items); setCursor(result.next_cursor) } }).catch(e => { if (active) setError(errorMessage(e)) }).finally(() => { if (active) setLoading(false) })
    }, query ? 250 : 0)
    return () => { active = false; clearTimeout(timer) }
  }, [orgId, scope, open, query, status, page, retry])
  return <Dialog open={open} onOpenChange={onOpenChange}><DialogContent className="max-w-md"><DialogHeader><DialogTitle>选择{scope === "agent" ? "智能体" : "会话"}</DialogTitle><DialogDescription>浏览当前组织中属于你的文件。</DialogDescription></DialogHeader>
    <div className="relative"><Search className="absolute left-3 top-2.5 size-4 text-muted-foreground" /><Input aria-label="搜索智能体或会话" className="pl-9" value={query} placeholder="搜索名称" onChange={e => { setQuery(e.target.value); setPage(undefined); setCursor(undefined); setRows([]) }} /></div>
    {scope === "workspace" && <div className="flex gap-2">{(["active", "archived"] as const).map(value => <Button key={value} variant={status === value ? "secondary" : "ghost"} size="sm" onClick={() => { setStatus(value); setPage(undefined); setCursor(undefined); setRows([]) }}>{value === "active" ? "未归档" : "已归档"}</Button>)}</div>}
    <div className="max-h-80 space-y-1 overflow-y-auto">{rows.map(row => <button key={row.id} type="button" onClick={() => { onSelect(row); onOpenChange(false) }} className="flex w-full items-center gap-3 rounded-lg p-3 text-left hover:bg-accent">{scope === "agent" ? <Bot className="size-4 shrink-0 text-muted-foreground" /> : <MessageSquare className="size-4 shrink-0 text-muted-foreground" />}<span className="min-w-0 flex-1"><span className="block truncate text-sm">{row.name}</span><span className="text-xs text-muted-foreground">{row.description}</span></span>{selected === row.id && <Check className="size-4" />}</button>)}
      {error && <div role="alert" className="p-3 text-sm text-destructive">{error}<Button variant="ghost" size="sm" onClick={() => setRetry(n => n + 1)}>重试</Button></div>}
      {loading ? <p role="status" className="p-4 text-center text-sm text-muted-foreground">正在读取…</p> : !error && rows.length === 0 && <p className="p-4 text-center text-sm text-muted-foreground">没有找到{scope === "agent" ? "智能体" : "会话"}</p>}
      {cursor && <Button className="w-full" variant="ghost" size="sm" disabled={loading} onClick={() => setPage(cursor)}>加载更多</Button>}
    </div>
  </DialogContent></Dialog>
}

export function AccessComputerPicker({ computers, selected, resolved, loading, writing, error, onSelect, onRefresh }: { computers: Computer[]; selected: string; resolved?: Computer; loading: boolean; writing: boolean; error: boolean; onSelect: (id: string) => void; onRefresh: () => void }) {
  return <DropdownMenu><DropdownMenuTrigger asChild><button type="button" disabled={writing} aria-label="选择访问电脑" className="flex min-h-10 w-full items-center gap-2 rounded-lg px-2 text-left text-xs hover:bg-accent disabled:opacity-50">{loading ? <Loader2 className="size-4 shrink-0 animate-spin text-muted-foreground" /> : <Monitor className="size-4 shrink-0 text-muted-foreground" />}<span className="text-muted-foreground">{selected ? "手动" : "自动"}</span><span className={cn("size-1.5 shrink-0 rounded-full", error ? "bg-destructive" : loading ? "bg-primary animate-pulse" : resolved ? "bg-success" : "bg-muted-foreground")} /><span className="min-w-0 flex-1 truncate">{loading ? (resolved?.status === "running" ? "正在读取…" : "正在启动并连接…") : resolved?.name ?? "选择访问电脑"}</span><ChevronUp className="size-3.5 shrink-0 text-muted-foreground" /></button></DropdownMenuTrigger>
    <DropdownMenuContent side="top" align="start" className="max-h-[min(520px,80dvh)] w-80 max-w-[calc(100vw-24px)] overflow-y-auto">
      <DropdownMenuLabel>访问电脑</DropdownMenuLabel><p className="px-2 pb-2 text-xs text-muted-foreground">通过云电脑访问同一组个人文件。</p>
      <DropdownMenuItem onSelect={() => onSelect("")}><Sparkles /><span className="flex-1">自动选择<span className="mt-1 block text-xs text-muted-foreground">优先使用已运行的可用电脑</span></span>{!selected && <Check />}</DropdownMenuItem><DropdownMenuSeparator />
      {computers.map(computer => <DropdownMenuItem key={computer.id} disabled={!["running", "starting", "paused", "stopped"].includes(computer.status)} onSelect={() => onSelect(computer.id)}><Monitor /><div className="min-w-0 flex-1"><div className="flex items-center gap-2"><span className="truncate">{computer.name}</span><span className="shrink-0 text-xs text-muted-foreground">{computerStatus(computer.status)}</span></div><p className="mt-1 truncate text-xs text-muted-foreground" title={computer.config.image}>{computer.config.image || "默认镜像"} · {computer.config.resources.cpu || "默认"} CPU / {computer.config.resources.memory || "默认内存"}</p></div>{selected === computer.id && <Check />}</DropdownMenuItem>)}
      <DropdownMenuSeparator /><DropdownMenuItem onSelect={onRefresh}>刷新电脑列表</DropdownMenuItem><p className="px-2 pb-1 pt-2 text-xs text-muted-foreground">选择暂停的电脑会先启动。切换电脑保留当前目录。</p>
    </DropdownMenuContent>
  </DropdownMenu>
}
