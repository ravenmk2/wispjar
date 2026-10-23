import { Folder } from "lucide-react"
import { useEffect, useRef, useState } from "react"
import { Link } from "react-router"
import { errorMessage } from "@/api/client"
import { sessionsApi } from "@/api/sessions"
import { EmptyState, PageHeader } from "@/components/common"
import { Button } from "@/components/ui/button"
import { RefreshButton } from "@/components/RefreshButton"
import { Label } from "@/components/ui/label"
import { WorkspaceFiles } from "@/features/computers/WorkspaceFiles"
import type { SessionSummary } from "@/lib/types"
import { useCurrentOrg, useIdentityStore } from "@/stores/identity"

export default function StoragePage() {
  const org = useCurrentOrg()
  const token = useIdentityStore((s) => s.token)
  const [sessions, setSessions] = useState<SessionSummary[]>([])
  const [cursor, setCursor] = useState<string | undefined>()
  const [status, setStatus] = useState<"active" | "archived">("active")
  const [selected, setSelected] = useState("")
  const [opened, setOpened] = useState("")
  const [loading, setLoading] = useState(false)
  const [opening, setOpening] = useState(false)
  const [error, setError] = useState("")
  const [revision, setRevision] = useState(0)
  const request = useRef(0)

  useEffect(() => {
    let active = true
    request.current++
    setSessions([]); setSelected(""); setOpened(""); setCursor(undefined); setOpening(false); setError(""); setLoading(true)
    if (!org) { setLoading(false); return }
    sessionsApi.list(org.id, { limit: 50, status }).then((result) => {
      if (active) { setSessions(result.items); setCursor(result.next_cursor) }
    }).catch((e) => { if (active) setError(errorMessage(e)) }).finally(() => { if (active) setLoading(false) })
    return () => { active = false; request.current++ }
  }, [org?.id, token, status, revision])

  async function loadMore() {
    if (!org || !cursor || loading) return
    const version = request.current
    setLoading(true)
    try {
      const result = await sessionsApi.list(org.id, { limit: 50, cursor, status })
      if (version !== request.current) return
      setSessions((items) => [...items, ...result.items]); setCursor(result.next_cursor); setError("")
    } catch (e) { if (version === request.current) setError(errorMessage(e)) }
    finally { if (version === request.current) setLoading(false) }
  }

  async function open() {
    if (!selected || opening) return
    const version = ++request.current
    setOpening(true); setError("")
    try {
      const detail = await sessionsApi.detail(selected)
      if (version !== request.current) return
      if (!detail.computer_id) { setError("会话未绑定云电脑，请重新选择会话。"); return }
      setOpened(detail.id)
    } catch (e) { if (version === request.current) setError(errorMessage(e)) }
    finally { if (version === request.current) setOpening(false) }
  }

  return <div className="mx-auto max-w-5xl space-y-5">
    <PageHeader title="存储" description="浏览和管理当前组织下可在不同会话中共用的个人文件。"><RefreshButton variant="outline" size="sm" onClick={() => setRevision((value) => value + 1)} refreshing={loading} /></PageHeader>
    <div className="flex flex-wrap items-end gap-3 rounded-xl border p-4">
      <div className="min-w-0 flex-1 space-y-2"><Label htmlFor="storage-session">访问会话</Label><select id="storage-session" className="h-9 w-full rounded-md border bg-background px-2 text-sm" value={selected} disabled={loading || opening} onChange={(event) => { request.current++; setSelected(event.target.value); setOpened(""); setError("") }}><option value="">选择已绑定云电脑的会话</option>{sessions.map((session) => <option key={session.id} value={session.id}>{session.title || "新聊天"} · {session.agent_name}</option>)}</select></div>
      <select aria-label="会话状态" className="h-9 rounded-md border bg-background px-2 text-sm" value={status} onChange={(event) => setStatus(event.target.value as "active" | "archived")}><option value="active">未归档会话</option><option value="archived">已归档会话</option></select>
      {cursor && <Button variant="outline" size="sm" disabled={loading || opening} onClick={() => void loadMore()}>加载更多</Button>}
      <Button size="sm" disabled={!selected || opening || loading} onClick={() => void open()}>{opening ? "正在连接…" : "浏览文件"}</Button>
    </div>
    <p className="text-xs text-muted-foreground">存储仅本人可见，同一组织内的个人云电脑共用这些文件。浏览时会使用所选会话的云电脑，电脑已暂停时会自动启动。</p>
    {error && <p role="alert" className="text-sm text-destructive">{error}</p>}
    {opened ? <WorkspaceFiles key={`${org?.id}-${opened}`} sessionId={opened} running={false} fixedScope="storage" embedded /> : <EmptyState icon={<Folder className="size-8" />} title={loading ? "正在读取会话…" : "选择一个访问会话"} description="使用已有会话的云电脑浏览存储，不会新建会话或发送消息。" action={<Button variant="outline" size="sm" asChild><Link to="/">前往工作区</Link></Button>} />}
  </div>
}
