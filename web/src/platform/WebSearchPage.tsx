import { useCallback, useEffect, useState } from "react"
import { toast } from "sonner"
import { errorMessage } from "@/api/client"
import { SEARCH_TYPES, webSearchApi, type SearchProvider, type SearchProviderBody, type SearchProviderType, type SearchResponse } from "@/api/web-search"
import { ConfirmDialog, EmptyState, PageHeader, TableSkeleton } from "@/components/common"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardFooter, CardHeader, CardTitle } from "@/components/ui/card"
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { WebSearchResults } from "@/features/chat/WebSearchResults"

export default function WebSearchPage() {
  const [items, setItems] = useState<SearchProvider[] | null>(null)
  const [defaultId, setDefaultId] = useState("none")
  const [savedDefault, setSavedDefault] = useState("none")
  const [editing, setEditing] = useState<SearchProvider | "new" | null>(null)
  const [testing, setTesting] = useState<SearchProvider | null>(null)
  const [deleting, setDeleting] = useState<SearchProvider | null>(null)
  const [busy, setBusy] = useState(false)
  const [loadError, setLoadError] = useState<string | null>(null)
  const load = useCallback(async () => {
    try {
      const [list, config] = await Promise.all([webSearchApi.list(), webSearchApi.config()])
      setItems(list.items); setDefaultId(config.default_provider_id ?? "none"); setSavedDefault(config.default_provider_id ?? "none"); setLoadError(null)
    } catch (e) { setLoadError(errorMessage(e)) }
  }, [])
  useEffect(() => { void load() }, [load])
  async function saveDefault() {
    setBusy(true)
    try { await webSearchApi.setDefault(defaultId === "none" ? null : defaultId); setSavedDefault(defaultId); toast.success("全局默认已保存，下次搜索生效") }
    catch (e) { toast.error(errorMessage(e)) }
    finally { setBusy(false) }
  }
  async function remove() {
    if (!deleting) return
    setBusy(true)
    try { await webSearchApi.remove(deleting.id); setDeleting(null); toast.success("搜索实例已删除"); await load() }
    catch (e) { toast.error(errorMessage(e)) }
    finally { setBusy(false) }
  }
  return <div className="space-y-5">
    <PageHeader title="网络搜索" description="管理搜索供应商实例和全局默认；调用失败时明确报错，不自动切换实例。">
      <Button size="sm" onClick={() => setEditing("new")}>新建搜索实例</Button>
    </PageHeader>
    {loadError ? <div role="alert" className="text-sm text-destructive">{loadError}<Button variant="ghost" onClick={() => void load()}>重新加载</Button></div> : items === null ? <TableSkeleton rows={3} /> : <>
      <Card>
        <CardHeader><CardTitle className="text-base">全局默认搜索实例</CardTitle></CardHeader>
        <CardContent className="space-y-2">
          <div className="flex flex-wrap gap-2">
            <Select value={defaultId} onValueChange={setDefaultId} disabled={busy}>
              <SelectTrigger aria-label="全局默认搜索实例" className="w-full sm:w-80"><SelectValue /></SelectTrigger>
              <SelectContent><SelectItem value="none">不设置默认实例</SelectItem>{items.map((p) => <SelectItem key={p.id} value={p.id} disabled={p.status !== "active"}>{p.name} · {SEARCH_TYPES[p.type].label}{p.status !== "active" ? "（已停用）" : ""}</SelectItem>)}</SelectContent>
            </Select>
            <Button disabled={busy || defaultId === savedDefault} onClick={() => void saveDefault()}>{busy ? "保存中…" : "保存默认"}</Button>
          </div>
          <p className="text-xs text-muted-foreground">智能体可跟随全局默认，也可指定实例。未设置默认或默认实例停用时，跟随默认的搜索会明确报错。</p>
        </CardContent>
      </Card>
      {items.length === 0 ? <EmptyState title="暂无搜索实例" description="添加 DeepSeek、Tavily 或博查实例；同一类型可配置多个账号。" /> : <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-3">{items.map((p) => <Card key={p.id}>
        <CardHeader className="pb-3"><CardTitle className="flex flex-wrap items-center gap-2 text-base"><span className="min-w-0 break-words">{p.name}</span><Badge variant="secondary">{SEARCH_TYPES[p.type].label}</Badge>{savedDefault === p.id && <Badge variant="outline">全局默认</Badge>}</CardTitle></CardHeader>
        <CardContent className="space-y-2 text-sm"><div className="truncate text-muted-foreground" title={p.base_url}>{p.base_url}</div><div className="font-mono text-xs">{p.masked_key}</div><Badge variant={p.status === "active" ? "secondary" : "outline"}>{p.status === "active" ? "已启用" : "已停用"}</Badge></CardContent>
        <CardFooter className="flex-wrap gap-2"><Button variant="outline" size="sm" disabled={p.status !== "active"} onClick={() => setTesting(p)}>测试搜索</Button><Button variant="ghost" size="sm" onClick={() => setEditing(p)}>编辑</Button><Button variant="ghost" size="sm" onClick={() => setDeleting(p)}>删除</Button></CardFooter>
      </Card>)}</div>}
    </>}
    {editing && <ProviderEditor provider={editing === "new" ? null : editing} onClose={(saved) => { setEditing(null); if (saved) void load() }} />}
    {testing && <SearchCheck provider={testing} onClose={() => setTesting(null)} />}
    <ConfirmDialog open={!!deleting} onOpenChange={(open) => { if (!open && !busy) setDeleting(null) }} title={`删除「${deleting?.name ?? ""}」？`} description="全局默认或智能体仍在引用的实例无法删除。已有会话快照指定此实例时，删除后搜索将报错。" confirmText="删除" destructive loading={busy} onConfirm={() => void remove()} />
  </div>
}

function ProviderEditor({ provider, onClose }: { provider: SearchProvider | null; onClose: (saved: boolean) => void }) {
  const [form, setForm] = useState<SearchProviderBody>({ name: provider?.name ?? "", type: provider?.type ?? "deepseek", base_url: provider?.base_url ?? SEARCH_TYPES.deepseek.url, api_key: "", status: provider?.status ?? "active" })
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  async function save(event: React.FormEvent) {
    event.preventDefault(); setBusy(true); setError(null)
    try {
      const body = { ...form, name: form.name.trim(), base_url: form.base_url.trim() }
      if (provider) await webSearchApi.update(provider.id, body); else await webSearchApi.create(body)
      toast.success("搜索实例已保存"); onClose(true)
    } catch (e) { setError(errorMessage(e)) } finally { setBusy(false) }
  }
  return <Dialog open onOpenChange={(open) => { if (!open && !busy) onClose(false) }}><DialogContent>
    <DialogHeader><DialogTitle>{provider ? "编辑搜索实例" : "新建搜索实例"}</DialogTitle></DialogHeader>
    <form onSubmit={save} className="space-y-4">
      <div className="space-y-1.5"><Label htmlFor="search-name">实例名称</Label><Input id="search-name" required maxLength={128} value={form.name} onChange={(e) => setForm({ ...form, name: e.target.value })} placeholder="例如：DeepSeek 主账号" /></div>
      <div className="space-y-1.5"><Label>供应商类型</Label><Select value={form.type} disabled={!!provider} onValueChange={(v: SearchProviderType) => setForm({ ...form, type: v, base_url: SEARCH_TYPES[v].url })}><SelectTrigger aria-label="供应商类型"><SelectValue /></SelectTrigger><SelectContent>{Object.entries(SEARCH_TYPES).map(([type, meta]) => <SelectItem key={type} value={type}>{meta.label}</SelectItem>)}</SelectContent></Select></div>
      <div className="space-y-1.5"><Label htmlFor="search-url">Base URL</Label><Input id="search-url" required type="url" maxLength={512} value={form.base_url} onChange={(e) => setForm({ ...form, base_url: e.target.value })} /></div>
      <div className="space-y-1.5"><Label htmlFor="search-key">API Key</Label><Input id="search-key" type="password" autoComplete="new-password" required={!provider} value={form.api_key} onChange={(e) => setForm({ ...form, api_key: e.target.value })} placeholder={provider ? "留空保留现有密钥" : "输入供应商 API Key"} /><p className="text-xs text-muted-foreground">密钥加密保存，只供服务端调用。</p></div>
      <div className="space-y-1.5"><Label>状态</Label><Select value={form.status} onValueChange={(v: "active" | "disabled") => setForm({ ...form, status: v })}><SelectTrigger aria-label="实例状态"><SelectValue /></SelectTrigger><SelectContent><SelectItem value="active">启用</SelectItem><SelectItem value="disabled">停用</SelectItem></SelectContent></Select></div>
      {error && <p role="alert" className="text-sm text-destructive">{error}</p>}
      <DialogFooter><Button variant="outline" type="button" disabled={busy} onClick={() => onClose(false)}>取消</Button><Button type="submit" disabled={busy}>{busy ? "保存中…" : "保存"}</Button></DialogFooter>
    </form>
  </DialogContent></Dialog>
}

function SearchCheck({ provider, onClose }: { provider: SearchProvider; onClose: () => void }) {
  const [query, setQuery] = useState("Go release notes")
  const [busy, setBusy] = useState(false)
  const [result, setResult] = useState<SearchResponse | null>(null)
  const [error, setError] = useState<string | null>(null)
  async function check(event: React.FormEvent) {
    event.preventDefault(); setBusy(true); setResult(null); setError(null)
    try { setResult(await webSearchApi.check(provider.id, query)) } catch (e) { setError(errorMessage(e)) } finally { setBusy(false) }
  }
  return <Dialog open onOpenChange={(open) => { if (!open && !busy) onClose() }}><DialogContent className="sm:max-w-2xl">
    <DialogHeader><DialogTitle>测试搜索 · {provider.name}</DialogTitle></DialogHeader>
    <form onSubmit={check} className="space-y-3"><Label htmlFor="test-search-query">搜索词</Label><div className="flex gap-2"><Input id="test-search-query" value={query} maxLength={provider.type === "deepseek" ? 128 : 512} required onChange={(e) => setQuery(e.target.value)} /><Button type="submit" disabled={busy || !query.trim()}>{busy ? "搜索中…" : "测试搜索"}</Button></div><p className="text-xs text-muted-foreground">仅调用此实例一次，可能产生调用费用。</p></form>
    {error && <p role="alert" className="text-sm text-destructive">{error}</p>}
    {result && <WebSearchResults result={result} />}
  </DialogContent></Dialog>
}
