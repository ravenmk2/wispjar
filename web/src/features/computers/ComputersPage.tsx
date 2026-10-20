import { Monitor, Plus, RefreshCw } from "lucide-react"
import { useCallback, useEffect, useRef, useState } from "react"
import { Link } from "react-router"
import { toast } from "sonner"
import { computersApi } from "@/api/computers"
import { errorMessage } from "@/api/client"
import { ConfirmDialog, EmptyState, PageHeader, TableSkeleton } from "@/components/common"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription } from "@/components/ui/dialog"
import { useOrgAgents } from "@/features/agents/AgentPicker"
import { canMutateComputer, computerOperationError, computerStatus, computerTransitioning, selectableComputer, type AgentBinding, type Computer, type ComputerConfig, type ComputerRun, type OperationAccepted, type UserVolume } from "@/lib/computers"
import { ComputerFailure } from "./ComputerFailure"
import { EnvEditor } from "@/components/EnvEditor"
import { envPatch, envRowsError, type EnvRow } from "@/lib/env"
import { uuid } from "@/lib/utils"
import { useCurrentOrg, useIdentityStore } from "@/stores/identity"
import { useComputerStore } from "@/stores/computers"

const selectClass = "h-9 w-full rounded-md border bg-background px-3 text-sm"

export default function ComputersPage() {
  const org = useCurrentOrg()
  const agents = useOrgAgents()
  const computers = useComputerStore((s) => s.items)
  const [loaded, setLoaded] = useState(false)
  const [error, setError] = useState("")
  const [volumes, setVolumes] = useState<UserVolume[]>([])
  const [bindings, setBindings] = useState<AgentBinding[]>([])
  const [runs, setRuns] = useState<Record<string, ComputerRun[]>>({})
  const [operations, setOperations] = useState<OperationAccepted[]>([])
  const [busy, setBusy] = useState(false)
  const [editor, setEditor] = useState<"create" | Computer | null>(null)
  const [deleting, setDeleting] = useState<Computer | null>(null)
  const [recovering, setRecovering] = useState<Computer | null>(null)
  const [bindingBusy, setBindingBusy] = useState<string | null>(null)
  const deleteKey = useRef("")
  const recoverKey = useRef("")
  const bindingAgents = [...(agents ?? []), ...bindings.filter((b) => !agents?.some((a) => a.id === b.agent_id)).map((b) => ({ id: b.agent_id, name: `已下线智能体 ${b.agent_id.slice(0, 8)}` }))]
  const load = useCallback(async () => {
    if (!org) return
    const orgId = org.id
    const token = useIdentityStore.getState().token
    try {
      const [cs, vs, bs] = await Promise.all([computersApi.list(orgId), computersApi.volumes(orgId), computersApi.bindings(orgId)])
      if (useIdentityStore.getState().currentOrgId !== orgId || useIdentityStore.getState().token !== token) return
      useComputerStore.getState().baseline(orgId, cs.items)
      setVolumes(vs.items); setBindings(bs.items); setError(""); setLoaded(true)
      const entries = await Promise.all(cs.items.map(async (c) => [c.id, (await computersApi.runs(c.id)).items] as const))
      if (useIdentityStore.getState().currentOrgId === orgId && useIdentityStore.getState().token === token) setRuns(Object.fromEntries(entries))
    } catch (e) {
      if (useIdentityStore.getState().currentOrgId === orgId && useIdentityStore.getState().token === token) { setError(errorMessage(e)); setLoaded(true) }
    }
  }, [org?.id])
  useEffect(() => {
    setLoaded(false); setOperations([]); setRuns({}); setBindings([]); setVolumes([]); setEditor(null); setDeleting(null); setRecovering(null)
    if (org) useComputerStore.getState().reset(org.id)
    void load()
    const timer = setInterval(() => void load(), 5000)
    return () => clearInterval(timer)
  }, [load, org?.id])
  useEffect(() => {
    if (!operations.length) return
    let active = true
    let polling = false
    const timer = setInterval(async () => {
      if (polling) return
      polling = true
      try {
      for (const accepted of operations) {
        try {
          const result = await computersApi.operation(accepted.operation_id)
          if (!active) return
          if (["completed", "failed"].includes(result.status)) {
            setOperations((ops) => ops.filter((op) => op.operation_id !== accepted.operation_id))
            if (result.status === "failed") toast.error(computerOperationError(result))
            else toast.success("电脑操作已完成")
            void load()
          }
        } catch (e) { if (active) setError(errorMessage(e)) }
      }
      } finally { polling = false }
    }, 1500)
    return () => { active = false; clearInterval(timer) }
  }, [operations, load])
  function track(op: OperationAccepted) {
    if (op.status === "completed") toast.success("操作已完成")
    else setOperations((ops) => [...ops.filter((old) => old.operation_id !== op.operation_id), op])
    void load()
  }
  async function act(c: Computer, action: "start" | "pause" | "delete") {
    setBusy(true)
    try {
      track(await computersApi.action(c.id, action, action === "delete" ? deleteKey.current : uuid()))
      setDeleting(null)
    } catch (e) { toast.error(errorMessage(e)) } finally { setBusy(false) }
  }
  async function bind(agent: string, computer: string) {
    if (!org) return
    setBindingBusy(agent)
    try { if (computer) await computersApi.bind(org.id, agent, computer); else await computersApi.unbind(org.id, agent); await load(); toast.success(computer ? "已更新新会话的默认电脑" : "已清除默认电脑，已有会话不受影响") }
    catch (e) { toast.error(errorMessage(e)) } finally { setBindingBusy(null) }
  }
  async function recover(c: Computer) {
    setBusy(true)
    try { track(await computersApi.recover(c.id, recoverKey.current)); setRecovering(null) }
    catch (e) { toast.error(errorMessage(e)) } finally { setBusy(false) }
  }
  return <div className="mx-auto max-w-5xl space-y-6 p-6">
    <PageHeader title="我的云电脑" description="多台电脑共享你的长期文件、智能体文件和会话工作空间。">
      <Button variant="outline" size="sm" onClick={() => void load()}><RefreshCw className="size-4" />刷新</Button>
      <Button size="sm" onClick={() => setEditor("create")} disabled={!org}><Plus className="size-4" />创建电脑</Button>
    </PageHeader>
    {error && <div role="alert" className="rounded-md border border-destructive/30 p-3 text-sm text-destructive">{error}</div>}
    {operations.length > 0 && <p role="status" className="text-sm text-muted-foreground">{operations.length} 个操作正在处理，页面会自动更新。</p>}
    {!loaded ? <TableSkeleton /> : computers.length === 0 ? <EmptyState icon={<Monitor className="size-10" />} title="还没有云电脑" description="创建第一台电脑后，即可为智能体选择运行环境。" /> :
      <div className="grid gap-4 md:grid-cols-2">{computers.map((c) => {
        const activeRuns = runs[c.id]
        const pending = operations.some((op) => op.resource_id === c.id)
        const mutable = activeRuns !== undefined && canMutateComputer(c, activeRuns) && !pending
        return <section key={c.id} className="space-y-3 rounded-xl border bg-card p-4">
          <div className="flex items-center gap-2"><Monitor className="size-4" /><h2 className="min-w-0 flex-1 truncate font-medium">{c.name}</h2><span className="text-xs text-muted-foreground">{computerStatus(c.status)}</span></div>
          <p className="truncate text-xs text-muted-foreground" title={c.config.image}>{c.config.image} · {c.config.resources?.cpu ?? "默认"} CPU · {c.config.resources?.memory ?? "默认内存"}</p>
          <ComputerFailure computer={c} />
          <div className="flex flex-wrap gap-2">
            <Button size="sm" variant="outline" disabled={busy || pending || computerTransitioning(c.status) || c.status === "running"} onClick={() => void act(c, "start")}>启动</Button>
            <Button size="sm" variant="outline" disabled={busy || !mutable || c.status !== "running"} onClick={() => void act(c, "pause")}>暂停</Button>
            <Button size="sm" variant="outline" disabled={busy || pending || computerTransitioning(c.status)} onClick={() => setEditor(c)}>编辑 / 重建</Button>
            <Button size="sm" variant="ghost" className="text-destructive" disabled={busy || !mutable} onClick={() => { deleteKey.current = uuid(); setDeleting(c) }}>删除</Button>
            <Button size="sm" variant="ghost" className="text-destructive" disabled={busy || pending || computerTransitioning(c.status)} onClick={() => { recoverKey.current = uuid(); setRecovering(c) }}>终止任务并恢复</Button>
          </div>
          <div className="border-t pt-2 text-xs text-muted-foreground">
            {activeRuns === undefined ? "正在检查活动任务…" : activeRuns.length === 0 ? "没有活动任务" : <ul className="space-y-1">{activeRuns.map((run) => <li key={run.id}>
              {run.session_id ? <Link className="underline" to={`/c/${run.session_id}`}>查看{run.parent_run_id ? "子任务" : "会话任务"}</Link> : "环境维护"} · {computerStatus(run.status)}
              {run.lease_until && new Date(run.lease_until).getTime() < Date.now() && " · 等待确认执行状态"}
            </li>)}</ul>}
          </div>
        </section>
      })}</div>}
    <section className="space-y-3"><h2 className="font-medium">持久存储</h2>
      <div className="grid gap-3 sm:grid-cols-3">{["storage", "agents", "sessions"].map((kind) => {
        const v = volumes.find((v) => v.kind === kind)
        return <div key={kind} className="rounded-lg border p-3 text-sm"><code>/{kind}</code><p className="mt-1 text-muted-foreground">{v ? computerStatus(v.status) : "首次创建电脑时准备"}</p>
          <p className="mt-1 text-xs text-muted-foreground">{kind === "storage" ? "长期资产，最高保留优先级" : kind === "agents" ? "智能体长期文件与技能" : "各会话的输入、工作文件与成果"}</p>
          {v?.last_error && <p className="mt-2 text-destructive">{v.last_error}</p>}</div>
      })}</div>
      <p className="text-xs text-muted-foreground">暂停、重建或删除电脑均保留这三类存储。会话和智能体目录按各自的数据保留策略管理。</p>
    </section>
    <section className="space-y-3"><h2 className="font-medium">智能体默认电脑</h2><p className="text-sm text-muted-foreground">仅影响新会话，已有会话继续使用原电脑。</p>
      {bindingAgents.map((agent) => <div className="flex items-center gap-4" key={agent.id}>
        <Label className="w-40 truncate" htmlFor={`binding-${agent.id}`}>{agent.name}</Label>
        <select id={`binding-${agent.id}`} className={`${selectClass} max-w-sm`} value={bindings.find((b) => b.agent_id === agent.id)?.computer_id ?? ""} disabled={bindingBusy === agent.id} onChange={(e) => void bind(agent.id, e.target.value)}>
          <option value="">无默认电脑</option>{computers.filter(selectableComputer).map((c) => <option key={c.id} value={c.id}>{c.name}</option>)}
        </select>
      </div>)}
    </section>
    {editor && org && <ComputerEditor key={editor === "create" ? "create" : editor.id} orgId={org.id} computer={editor === "create" ? undefined : editor} onClose={() => setEditor(null)} onSaved={track} onRenamed={() => void load()} />}
    <ConfirmDialog open={!!deleting} onOpenChange={(open) => !open && setDeleting(null)} title={`删除「${deleting?.name ?? ""}」`} description="将停止并删除这台电脑，三类持久存储继续保留。有会话或默认设置引用时，需要先切换绑定。" destructive confirmText="删除电脑" loading={busy} onConfirm={() => deleting && void act(deleting, "delete")} />
    <ConfirmDialog open={!!recovering} onOpenChange={(open) => !open && !busy && setRecovering(null)} title={`终止「${recovering?.name ?? ""}」的全部任务并恢复`} description="将强制终止这台电脑所有会话、子任务和维护进程（包括正常运行的任务），停止并移除当前实例，清理遗留占用。确认停止后可重新启动；系统临时文件会丢失，storage、agents、sessions 三类持久文件及会话绑定保留。此操作无法撤销。" destructive confirmText="终止全部任务并恢复" loading={busy} onConfirm={() => recovering && void recover(recovering)} />
  </div>
}

function ComputerEditor({ orgId, computer, onClose, onSaved, onRenamed }: { orgId: string; computer?: Computer; onClose: () => void; onSaved: (op: OperationAccepted) => void; onRenamed: () => void }) {
  const agents = useOrgAgents()
  const [name, setName] = useState(computer?.name ?? "")
  const [image, setImage] = useState(computer?.config.image ?? "")
  const [cpu, setCPU] = useState(computer?.config.resources?.cpu ?? "")
  const [memory, setMemory] = useState(computer?.config.resources?.memory ?? "")
  const [agent, setAgent] = useState("")
  const [envRows, setEnvRows] = useState<EnvRow[]>([])
  const [envKeys, setEnvKeys] = useState<string[]>([])
  const [envLoaded, setEnvLoaded] = useState(!computer)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState("")
  const key = useRef(uuid())
  useEffect(() => {
    if (!computer) return
    let active = true
    computersApi.env(computer.id).then(({ keys }) => {
      if (active) { setEnvKeys(keys); setEnvRows(keys.map((key) => ({ key }))); setEnvLoaded(true) }
    }).catch((e) => { if (active) setError(errorMessage(e)) })
    return () => { active = false }
  }, [computer?.id])
  const rebuild = !!computer && (image.trim() !== computer.config.image || cpu.trim() !== (computer.config.resources?.cpu ?? "") || memory.trim() !== (computer.config.resources?.memory ?? ""))
  async function renameOnly() {
    if (!computer || !name.trim()) return
    setBusy(true); setError("")
    try { await computersApi.rename(computer.id, name.trim()); onRenamed(); toast.success("已保存名称"); onClose() }
    catch (e) { setError(errorMessage(e)) } finally { setBusy(false) }
  }
  async function submit() {
    if (!name.trim()) { setError("请输入电脑名称"); return }
    const envError = envRowsError(envRows)
    if (envError) { setError(envError); return }
    const variables = envPatch(envRows, envKeys)
    const config: ComputerConfig = {}
    if (image.trim()) config.image = image.trim()
    if (cpu.trim() || memory.trim()) config.resources = { ...(cpu.trim() ? { cpu: cpu.trim() } : {}), ...(memory.trim() ? { memory: memory.trim() } : {}) }
    setBusy(true); setError("")
    try {
      if (computer && !rebuild) {
        await computersApi.patch(computer.id, { name: name.trim(), env: variables })
        onRenamed(); toast.success("已保存，环境变量从下一轮任务生效"); onClose(); return
      }
      if (computer && name !== computer.name) await computersApi.rename(computer.id, name.trim())
      const op = computer
        ? await computersApi.action(computer.id, "rebuild", key.current, config, variables)
        : await computersApi.create(orgId, { name: name.trim(), agent_id: agent || undefined, config: { ...config, env: variables.set } }, key.current)
      onSaved(op); onClose()
    } catch (e) { setError(errorMessage(e)) } finally { setBusy(false) }
  }
  function changed(fn: () => void) { fn(); key.current = uuid() }
  return <Dialog open onOpenChange={(open) => !open && !busy && onClose()}><DialogContent className="max-h-[85vh] overflow-y-auto">
    <DialogHeader><DialogTitle>{computer ? "编辑云电脑" : "创建云电脑"}</DialogTitle><DialogDescription>{computer ? "名称和变量可直接保存；修改镜像或资源需要空闲后重建，三类持久存储保留。" : "镜像和资源可使用智能体模板；环境变量以平台默认值为基础。"}</DialogDescription></DialogHeader>
    <div className="space-y-3">
      <div><Label htmlFor="computer-name">名称</Label><Input id="computer-name" maxLength={128} value={name} onChange={(e) => changed(() => setName(e.target.value))} /></div>
      {!computer && <div><Label htmlFor="computer-template">环境模板</Label><select id="computer-template" className={selectClass} value={agent} onChange={(e) => changed(() => setAgent(e.target.value))}><option value="">平台默认</option>{agents?.map((a) => <option key={a.id} value={a.id}>{a.name}</option>)}</select></div>}
      <div><Label htmlFor="computer-image">镜像（平台已启用）</Label><Input id="computer-image" value={image} placeholder="使用默认镜像" onChange={(e) => changed(() => setImage(e.target.value))} /></div>
      <div className="grid grid-cols-2 gap-3"><div><Label htmlFor="computer-cpu">CPU</Label><Input id="computer-cpu" value={cpu} placeholder="默认" onChange={(e) => changed(() => setCPU(e.target.value))} /></div><div><Label htmlFor="computer-memory">内存</Label><Input id="computer-memory" value={memory} placeholder="默认，例如 1Gi" onChange={(e) => changed(() => setMemory(e.target.value))} /></div></div>
      <div className="space-y-2"><Label>电脑环境变量</Label><EnvEditor rows={envRows} savedKeys={envKeys} disabled={busy || !envLoaded} onChange={(rows) => changed(() => setEnvRows(rows))} /><p className="text-xs text-muted-foreground">下一轮任务生效。同名变量可由智能体和单次命令覆盖。</p></div>
      {error && <p role="alert" className="text-sm text-destructive">{error}</p>}
      <div className="flex justify-end gap-2"><Button variant="outline" disabled={busy} onClick={onClose}>取消</Button>{computer && <Button variant="outline" disabled={busy || !name.trim() || name === computer.name} onClick={() => void renameOnly()}>仅保存名称</Button>}<Button disabled={busy || !envLoaded || !name.trim()} onClick={() => void submit()}>{busy ? "正在提交…" : computer ? rebuild ? "保存并重建" : "保存" : "创建"}</Button></div>
    </div>
  </DialogContent></Dialog>
}
