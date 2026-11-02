import { useCallback, useEffect, useRef, useState } from "react"
import { ArrowLeft, Download, RefreshCw } from "lucide-react"
import { toast } from "sonner"
import { api, apiRaw, errorMessage } from "@/api/client"
import { modelsApi } from "@/api/models"
import { ConfirmDialog, CopyButton, EmptyState, TableSkeleton } from "@/components/common"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Switch } from "@/components/ui/switch"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { highlightCode } from "@/lib/highlight"
import type { Model } from "@/lib/types"

type Config = { enabled: boolean; retention_days: number; max_storage_bytes: number }
type State = { config: Config; models: Record<string, boolean>; stats: { pending_bytes: number; queue_length: number; saved: number; dropped_full: number; dropped_large: number; dropped_incomplete: number; failures: number; clearing: boolean } }
type Log = { id: string; started_at: string; status: string; payload_path: string; payload_bytes: number; payload_sha256: string; metadata: { model_name: string; model_code: string; provider_name: string; api_protocol: string; method: string; url: string; content_type: string }; error_detail?: { message?: string } }
type List = { items: Log[]; next_cursor: string }
const base = "/platform/request-logs"
const bytes = (n: number) => n < 1024 ? `${n} B` : n < 1024 ** 2 ? `${(n / 1024).toFixed(1)} KiB` : `${(n / 1024 ** 2).toFixed(1)} MiB`
const statusLabel: Record<string, string> = { writing: "保存中", ready: "已保存", failed: "保存失败", deleting: "清理中" }

export default function RequestLogsPage() {
  const [state, setState] = useState<State | null>(null)
  const [models, setModels] = useState<Model[]>([])
  const [list, setList] = useState<List | null>(null)
  const [model, setModel] = useState("")
  const [cursor, setCursor] = useState("")
  const [history, setHistory] = useState<string[]>([])
  const [selected, setSelected] = useState<Log | null>(null)
  const [payload, setPayload] = useState<string | null>(null)
  const [payloadError, setPayloadError] = useState("")
  const [busy, setBusy] = useState(false)
  const [clearOpen, setClearOpen] = useState(false)
  const [days, setDays] = useState(7)
  const [capacity, setCapacity] = useState(5)
  const [loadError, setLoadError] = useState("")
  const listVersion = useRef(0)
  const refreshState = useCallback(async () => {
    const result = await api<State>(`${base}/config`)
    setState(result)
    return result
  }, [])
  const load = useCallback(async () => {
    const version = ++listVersion.current
    setLoadError("")
    try {
      const result = await api<List>(base, { params: { model_id: model, before: cursor } })
      if (version === listVersion.current) setList(result)
    } catch (e) { if (version === listVersion.current) setLoadError(errorMessage(e)) }
  }, [model, cursor])
  useEffect(() => { void Promise.all([refreshState(), modelsApi.list()]).then(([s, m]) => { setModels(m.items); setDays(s.config.retention_days); setCapacity(s.config.max_storage_bytes / 1024 ** 3) }).catch(e => toast.error(errorMessage(e))) }, [refreshState])
  useEffect(() => { setList(null); void load(); return () => { listVersion.current++ } }, [load])
  useEffect(() => {
    const timer = window.setInterval(() => { void refreshState().catch(() => {}) }, 5000)
    return () => window.clearInterval(timer)
  }, [refreshState])
  useEffect(() => {
    if (!selected || selected.status !== "ready") return
    let active = true
    setPayload(null); setPayloadError("")
    void apiRaw(`${base}/${selected.id}/payload`).then(r => r.text()).then(text => { if (active) setPayload(text) }).catch(e => { if (active) setPayloadError(errorMessage(e)) })
    return () => { active = false }
  }, [selected])
  async function updateConfig(config: Config) {
    setBusy(true)
    try { setState(await api<State>(`${base}/config`, { method: "PUT", body: config })); toast.success("设置已生效") } catch (e) { toast.error(errorMessage(e)) } finally { setBusy(false) }
  }
  async function toggleModel(id: string, enabled: boolean) {
    setBusy(true)
    try { setState(await api<State>(`${base}/models/${id}`, { method: "PUT", body: { enabled } })) } catch (e) { toast.error(errorMessage(e)) } finally { setBusy(false) }
  }
  async function clearLogs() {
    setBusy(true)
    try { await api(base, { method: "DELETE" }); setClearOpen(false); setSelected(null); await refreshState(); await load(); toast.success("已安排后台清理；之后的新请求会保留") } catch (e) { toast.error(errorMessage(e)) } finally { setBusy(false) }
  }
  const enabledCount = state ? Object.values(state.models).filter(Boolean).length : 0
  const dropped = state ? state.stats.dropped_full + state.stats.dropped_large + state.stats.dropped_incomplete : 0
  return <div className="space-y-4">
    <header className="flex flex-wrap items-center justify-between gap-3">
      <div><h1 className="text-lg font-semibold">模型请求日志</h1><p className="mt-1 text-xs text-muted-foreground">查看实际发出的请求，定位模型输入问题。</p></div>
      <label className="flex items-center gap-2 text-sm">记录总开关<Switch aria-label="记录总开关" checked={state?.config.enabled ?? false} disabled={!state || busy} onCheckedChange={enabled => state && void updateConfig({ ...state.config, enabled })} /><span className="text-xs text-muted-foreground">{state?.config.enabled ? "已开启" : "已关闭"}</span></label>
    </header>
    <div className="flex flex-wrap items-center justify-between gap-2 text-xs text-muted-foreground"><span>{!state ? "加载配置…" : !state.config.enabled ? "总开关已关闭，已有记录仍可查看。" : enabledCount ? `已为 ${enabledCount} 个模型启用记录。` : "请在模型开关中选择需要调试的模型。"}</span><span>{state && `本进程已保存 ${state.stats.saved} 条 · 排队 ${state.stats.queue_length} 条`}</span></div>
    {!!state && (dropped > 0 || state.stats.failures > 0) && <p role="status" className="text-xs text-destructive">本进程跳过 {dropped} 条（队列满 {state.stats.dropped_full}、超限 {state.stats.dropped_large}、未完整读取 {state.stats.dropped_incomplete}），存储错误 {state.stats.failures} 次。模型请求不受影响。</p>}
    <Tabs defaultValue="logs">
      <TabsList><TabsTrigger value="logs">请求记录</TabsTrigger><TabsTrigger value="models">模型开关</TabsTrigger><TabsTrigger value="storage">存储与清理</TabsTrigger></TabsList>
      <TabsContent value="logs" className="space-y-4">
        {selected ? <>
          <Button variant="ghost" size="sm" onClick={() => { setSelected(null); setPayload(null) }}><ArrowLeft className="mr-2 size-4" />返回记录</Button>
          <div className="overflow-hidden rounded-lg border">
            <div className="flex flex-wrap items-center justify-between gap-3 border-b p-4"><div><h2 className="font-medium">{selected.metadata.model_name}</h2><p className="text-xs text-muted-foreground">{selected.started_at} · {statusLabel[selected.status]}</p></div>{payload !== null && <div className="flex gap-2"><CopyButton text={payload} onCopied={ok => ok ? toast.success("已复制原始请求体") : toast.error("复制失败")} /><Button variant="outline" size="sm" onClick={() => { const url = URL.createObjectURL(new Blob([payload], { type: "application/json" })); const a = document.createElement("a"); a.href = url; a.download = `${selected.id}.json`; a.click(); setTimeout(() => URL.revokeObjectURL(url), 1000) }}><Download className="mr-2 size-4" />下载</Button></div>}</div>
            <dl className="grid grid-cols-[90px_minmax(0,1fr)] gap-x-4 gap-y-2 bg-muted/30 p-4 text-xs">
              <dt className="text-muted-foreground">请求地址</dt><dd className="break-all">{selected.metadata.method} {selected.metadata.url}</dd><dt className="text-muted-foreground">协议</dt><dd>{selected.metadata.api_protocol}</dd><dt className="text-muted-foreground">记录 ID</dt><dd className="break-all">{selected.id}</dd><dt className="text-muted-foreground">文件</dt><dd className="break-all">{selected.payload_path} · {bytes(selected.payload_bytes)}</dd><dt className="text-muted-foreground">SHA-256</dt><dd className="break-all font-mono">{selected.payload_sha256 || "—"}</dd>
            </dl>
            {selected.status !== "ready" ? <p className="p-4 text-sm text-muted-foreground">{selected.error_detail?.message || statusLabel[selected.status]}</p> : payloadError ? <p role="alert" className="p-4 text-sm text-destructive">{payloadError}</p> : payload === null ? <div className="p-4"><TableSkeleton rows={3} /></div> : <Payload text={payload} />}
          </div>
        </> : <>
          <div className="flex flex-wrap items-center justify-between gap-3"><label className="flex items-center gap-2 text-xs text-muted-foreground">模型<select aria-label="筛选模型" className="h-9 max-w-64 rounded-md border bg-background px-3 text-sm text-foreground" value={model} onChange={e => { setModel(e.target.value); setCursor(""); setHistory([]) }}><option value="">全部模型</option>{models.map(m => <option key={m.id} value={m.id}>{m.display_name}</option>)}</select></label><Button variant="outline" size="sm" onClick={() => { void load(); void refreshState().catch(e => toast.error(errorMessage(e))) }}><RefreshCw className="mr-2 size-3.5" />刷新</Button></div>
          {loadError ? <p role="alert" className="text-sm text-destructive">{loadError}</p> : !list ? <TableSkeleton /> : list.items.length === 0 ? <EmptyState title="暂无请求记录" description="同时开启总开关和模型开关后，新请求会记录在这里。" /> : <div className="rounded-lg border"><Table><TableHeader><TableRow><TableHead>记录时间</TableHead><TableHead>模型 / 供应商</TableHead><TableHead>协议</TableHead><TableHead>请求体</TableHead><TableHead>保存状态</TableHead><TableHead /></TableRow></TableHeader><TableBody>{list.items.map(l => <TableRow key={l.id}><TableCell className="whitespace-nowrap text-xs">{new Date(l.started_at).toLocaleString("zh-CN", { timeZone: "Asia/Shanghai", hour12: false })}</TableCell><TableCell>{l.metadata.model_name}<div className="text-xs text-muted-foreground">{l.metadata.provider_name}</div></TableCell><TableCell className="text-xs text-muted-foreground">{l.metadata.api_protocol === "responses" ? "Responses" : "Chat Completions"}</TableCell><TableCell className="whitespace-nowrap text-xs">{bytes(l.payload_bytes)}</TableCell><TableCell className="text-xs">{statusLabel[l.status] || l.status}</TableCell><TableCell><Button variant="ghost" size="sm" onClick={() => { setPayload(null); setPayloadError(""); setSelected(l) }}>查看</Button></TableCell></TableRow>)}</TableBody></Table></div>}
          <div className="flex items-center justify-between text-xs text-muted-foreground"><span>时间为 UTC+08:00 · 每页最多 50 条{state?.stats.clearing ? " · 正在后台清理" : ""}</span><div className="flex gap-2"><Button variant="outline" size="sm" disabled={!history.length} onClick={() => { setCursor(history[history.length - 1]); setHistory(history.slice(0, -1)) }}>上一页</Button><Button variant="outline" size="sm" disabled={!list?.next_cursor} onClick={() => { setHistory([...history, cursor]); setCursor(list!.next_cursor) }}>下一页</Button></div></div>
        </>}
      </TabsContent>
      <TabsContent value="models" className="space-y-4"><p className="text-xs text-muted-foreground">开关即时生效，仅作用于后续请求。关闭总开关会暂停全部模型记录，并保留以下选择。</p><Table><TableHeader><TableRow><TableHead>模型</TableHead><TableHead>供应商</TableHead><TableHead>记录开关</TableHead><TableHead>实际状态</TableHead></TableRow></TableHeader><TableBody>{models.map(m => <TableRow key={m.id}><TableCell>{m.display_name}<div className="text-xs text-muted-foreground">{m.model_code}</div></TableCell><TableCell>{m.provider_name}</TableCell><TableCell><Switch aria-label={`记录 ${m.display_name}`} checked={state?.models[m.id] ?? false} disabled={busy || !state} onCheckedChange={v => void toggleModel(m.id, v)} /></TableCell><TableCell className="text-xs text-muted-foreground">{state?.models[m.id] ? state.config.enabled ? "已启用" : "总开关已关闭" : "未启用"}</TableCell></TableRow>)}</TableBody></Table></TabsContent>
      <TabsContent value="storage" className="space-y-6"><form onSubmit={e => { e.preventDefault(); if (state) void updateConfig({ ...state.config, retention_days: days, max_storage_bytes: Math.round(capacity * 1024 ** 3) }) }} className="space-y-5">
        <p className="text-sm text-muted-foreground">请求正文按日期保存到数据目录。到期或达到容量上限后，后台清理最早的记录。</p>
        <label className="flex items-center justify-between gap-4 border-b pb-4 text-sm"><span>保留时间<span className="mt-1 block text-xs text-muted-foreground">到期后删除索引和正文文件</span></span><span className="flex items-center gap-2"><Input className="w-24" type="number" min={1} max={90} required value={days} onChange={e => setDays(e.target.valueAsNumber)} />天</span></label>
        <label className="flex items-center justify-between gap-4 border-b pb-4 text-sm"><span>总容量上限</span><span className="flex items-center gap-2"><Input className="w-24" type="number" min={0.001} max={100} step={0.001} required value={capacity} onChange={e => setCapacity(e.target.valueAsNumber)} />GiB</span></label>
        <p className="text-xs text-muted-foreground">单个请求最多 32 MiB，捕获与排队内存最多 128 MiB、队列最多 128 条。超过限制会跳过日志，保留完整的模型请求。</p><Button type="submit" disabled={busy || !state}>保存设置</Button>
      </form><div className="flex flex-wrap items-center justify-between gap-4 border-t pt-5"><div><h2 className="text-sm font-medium">清空请求日志</h2><p className="text-xs text-muted-foreground">删除清理开始前的记录，不修改开关，不影响会话数据。</p></div><Button variant="outline" disabled={busy || state?.stats.clearing} onClick={() => setClearOpen(true)}>{state?.stats.clearing ? "后台清理中…" : "清空日志"}</Button></div></TabsContent>
    </Tabs><ConfirmDialog open={clearOpen} onOpenChange={setClearOpen} title="清空请求日志？" description="将删除已有请求索引和正文文件，无法恢复。之后的新请求会保留。" destructive confirmText="确认清空" loading={busy} onConfirm={() => void clearLogs()} />
  </div>
}

function Payload({ text }: { text: string }) {
  const [formatted, setFormatted] = useState(true)
  const [html, setHtml] = useState<string | null>(null)
  // Large media payloads remain downloadable; avoid locking the UI with syntax highlighting.
  const large = text.length > 200_000
  let shown = text
  if (formatted && !large) { try { shown = JSON.stringify(JSON.parse(text), null, 2) } catch { /* retain original bytes */ } }
  useEffect(() => { let active = true; setHtml(null); if (!large) void highlightCode(shown, "json").then(h => { if (active) setHtml(h) }); return () => { active = false } }, [shown, large])
  return <><div className="flex items-center justify-between border-t px-4 py-2 text-xs text-muted-foreground"><span>请求正文 · JSON{large ? " · 大文件请下载查看全文" : ""}</span>{!large && <Button variant="ghost" size="sm" onClick={() => setFormatted(!formatted)}>{formatted ? "查看原始格式" : "格式化"}</Button>}</div>{large ? <p className="border-t p-4 text-sm text-muted-foreground">正文已完整保存（{bytes(new TextEncoder().encode(text).length)}）。为避免浏览器卡顿，不在页面展开；可复制或下载完整原始文件。</p> : html ? <div className="max-h-[60vh] overflow-auto border-t p-4 text-xs [&_pre]:!bg-transparent [&_pre]:leading-6" dangerouslySetInnerHTML={{ __html: html }} /> : <pre className="max-h-[60vh] overflow-auto border-t p-4 text-xs leading-6">{shown}</pre>}</>
}
