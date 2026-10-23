import { MoreHorizontal, Pause, Pencil, Play, Plus, Square, Trash2 } from "lucide-react"
import { useCallback, useEffect, useRef, useState } from "react"
import { Link } from "react-router"
import { toast } from "sonner"
import { computersApi } from "@/api/computers"
import { errorMessage } from "@/api/client"
import { ConfirmDialog, EmptyState, PageHeader, TableSkeleton } from "@/components/common"
import { Button } from "@/components/ui/button"
import { CloudComputerIcon } from "@/components/CloudComputerIcon"
import { RefreshButton } from "@/components/RefreshButton"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription } from "@/components/ui/dialog"
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuTrigger } from "@/components/ui/dropdown-menu"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { useOrgAgents } from "@/features/agents/AgentPicker"
import { canMutateComputer, computerOperationError, computerStatus, computerTransitioning, type Computer, type ComputerConfig, type ComputerRun, type OperationAccepted, type UserVolume } from "@/lib/computers"
import { ComputerFailure } from "./ComputerFailure"
import { EnvEditor } from "@/components/EnvEditor"
import { envPatch, envRowsError, type EnvRow } from "@/lib/env"
import { cn, uuid } from "@/lib/utils"
import { useCurrentOrg, useIdentityStore } from "@/stores/identity"
import { useComputerStore } from "@/stores/computers"

const selectClass = "h-9 w-full rounded-md border bg-background px-3 text-sm"

function ComputerStatusIndicator({ status, pending }: { status: string; pending: boolean }) {
  const transitioning = computerTransitioning(status)
  const processing = transitioning || pending
  const label = pending && !transitioning ? `${computerStatus(status)} · 操作处理中` : computerStatus(status)
  const color = processing ? "text-primary"
    : status === "running" ? "text-success"
    : status === "paused" ? "text-warning"
    : ["failed", "error"].includes(status) ? "text-destructive"
    : "text-muted-foreground"

  return <Tooltip>
    <TooltipTrigger asChild>
      <span tabIndex={0} role="img" aria-label={`电脑状态：${label}`} className={cn("inline-flex size-7 shrink-0 items-center justify-center rounded-full focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring", color)}>
        <span aria-hidden="true" className="relative flex size-2">
          {processing && <span className="absolute inset-0 rounded-full bg-current opacity-40 animate-ping" />}
          <span className="relative size-2 rounded-full bg-current transition-colors motion-reduce:transition-none" />
        </span>
      </span>
    </TooltipTrigger>
    <TooltipContent>{label}</TooltipContent>
  </Tooltip>
}

function VolumeStatusIndicator({ status }: { status?: string }) {
  const preparing = status === "pending" || status === "provisioning"
  const failed = status === "missing" || status === "error" || status === "failed"
  const label = status ? computerStatus(status) : "首次创建电脑时准备"
  return <Tooltip>
    <TooltipTrigger asChild>
      <span tabIndex={0} role="img" aria-label={`存储状态：${label}`} className={cn("inline-flex size-6 shrink-0 items-center justify-center rounded-full focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring", status === "ready" ? "text-success" : preparing ? "text-primary" : failed ? "text-destructive" : "text-muted-foreground")}>
        <span aria-hidden="true" className="relative flex size-2">
          {preparing && <span className="absolute inset-0 animate-ping rounded-full bg-current opacity-40" />}
          <span className="relative size-2 rounded-full bg-current" />
        </span>
      </span>
    </TooltipTrigger>
    <TooltipContent>{label}</TooltipContent>
  </Tooltip>
}

export default function ComputersPage() {
  const org = useCurrentOrg()
  const computers = useComputerStore((s) => s.items)
  const [loaded, setLoaded] = useState(false)
  const [refreshing, setRefreshing] = useState(false)
  const [error, setError] = useState("")
  const [volumes, setVolumes] = useState<UserVolume[]>([])
  const [runs, setRuns] = useState<Record<string, ComputerRun[]>>({})
  const [operations, setOperations] = useState<OperationAccepted[]>([])
  const [busy, setBusy] = useState(false)
  const [editor, setEditor] = useState<"create" | Computer | null>(null)
  const [deleting, setDeleting] = useState<Computer | null>(null)
  const [recovering, setRecovering] = useState<Computer | null>(null)
  const deleteKey = useRef("")
  const recoverKey = useRef("")
  const load = useCallback(async () => {
    if (!org) return
    const orgId = org.id
    const token = useIdentityStore.getState().token
    try {
      const [cs, vs] = await Promise.all([computersApi.list(orgId), computersApi.volumes(orgId)])
      if (useIdentityStore.getState().currentOrgId !== orgId || useIdentityStore.getState().token !== token) return
      useComputerStore.getState().baseline(orgId, cs.items)
      setVolumes(vs.items); setError(""); setLoaded(true)
      const entries = await Promise.all(cs.items.map(async (c) => [c.id, (await computersApi.runs(c.id)).items] as const))
      if (useIdentityStore.getState().currentOrgId === orgId && useIdentityStore.getState().token === token) setRuns(Object.fromEntries(entries))
    } catch (e) {
      if (useIdentityStore.getState().currentOrgId === orgId && useIdentityStore.getState().token === token) { setError(errorMessage(e)); setLoaded(true) }
    }
  }, [org?.id])
  async function refresh() {
    setRefreshing(true)
    try { await load() } finally { setRefreshing(false) }
  }
  useEffect(() => {
    setLoaded(false); setOperations([]); setRuns({}); setVolumes([]); setEditor(null); setDeleting(null); setRecovering(null)
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
  async function recover(c: Computer) {
    setBusy(true)
    try { track(await computersApi.recover(c.id, recoverKey.current)); setRecovering(null) }
    catch (e) { toast.error(errorMessage(e)) } finally { setBusy(false) }
  }
  return <div className="mx-auto max-w-5xl space-y-6">
    <PageHeader title="云电脑" description="多台电脑共享你的长期文件、智能体文件和会话工作空间。">
      <Tooltip>
        <TooltipTrigger asChild>
          <span className="relative inline-flex">
            <RefreshButton variant="outline" size="sm" refreshing={refreshing} onClick={() => void refresh()} />
            {operations.length > 0 && <span aria-hidden="true" className="pointer-events-none absolute -right-1 -top-1 flex h-4 min-w-4 items-center justify-center rounded-full bg-primary px-1 text-[10px] font-medium leading-none text-primary-foreground">{operations.length}</span>}
          </span>
        </TooltipTrigger>
        <TooltipContent>{operations.length > 0 ? `${operations.length} 个操作正在处理，页面会自动更新。` : "刷新云电脑"}</TooltipContent>
      </Tooltip>
      <Button size="sm" onClick={() => setEditor("create")} disabled={!org}><Plus className="size-4" />创建电脑</Button>
    </PageHeader>
    {error && <div role="alert" className="rounded-md border border-destructive/30 p-3 text-sm text-destructive">{error}</div>}
    <span role="status" className="sr-only">{operations.length > 0 ? `${operations.length} 个操作正在处理，页面会自动更新。` : ""}</span>
    {!loaded ? <TableSkeleton /> : computers.length === 0 ? <EmptyState icon={<CloudComputerIcon className="size-10" />} title="还没有云电脑" description="创建第一台电脑后，即可为智能体选择运行环境。" /> :
      <div className="grid grid-cols-1 gap-4 md:grid-cols-2">{computers.map((c) => {
        const activeRuns = runs[c.id]
        const pending = operations.some((op) => op.resource_id === c.id)
        const mutable = activeRuns !== undefined && canMutateComputer(c, activeRuns) && !pending
        const running = c.status === "running"
        const powerAction = running ? "暂停" : "启动"
        const powerDisabled = busy || pending || computerTransitioning(c.status) || c.status === "deleted" || (running && !mutable)
        const powerHint = busy || pending ? "电脑操作处理中"
          : computerTransitioning(c.status) || c.status === "deleted" ? computerStatus(c.status)
          : running && activeRuns === undefined ? "正在检查活动任务，暂不可暂停"
          : running && (activeRuns?.length ?? 0) > 0 ? "有活动任务，暂不可暂停"
          : `${powerAction}电脑`
        const imageName = c.config.image.split("/").pop() || c.config.image
        return <section key={c.id} className="min-w-0 space-y-3 rounded-xl border bg-card p-4">
          <div className="flex items-center gap-2">
            <CloudComputerIcon className="size-4 shrink-0" />
            <h2 className="min-w-0 flex-1 truncate font-medium" title={c.name}>{c.name}</h2>
            <ComputerStatusIndicator status={c.status} pending={pending} />
            <Tooltip>
              <TooltipTrigger asChild>
                <span tabIndex={powerDisabled ? 0 : undefined} className="inline-flex shrink-0 rounded-md focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring">
                  <Button variant="ghost" size="icon" className="size-8 text-muted-foreground" aria-label={`${powerAction}电脑：${c.name}`} disabled={powerDisabled} onClick={() => void act(c, running ? "pause" : "start")}>
                    {running ? <Pause aria-hidden="true" className="size-4" /> : <Play aria-hidden="true" className="size-4" />}
                  </Button>
                </span>
              </TooltipTrigger>
              <TooltipContent>{powerHint}</TooltipContent>
            </Tooltip>
            <DropdownMenu>
              <DropdownMenuTrigger asChild>
                <Button variant="ghost" size="icon" className="size-8 shrink-0 text-muted-foreground" aria-label={`${c.name}的更多操作`}><MoreHorizontal className="size-4" /></Button>
              </DropdownMenuTrigger>
              <DropdownMenuContent align="end">
                <DropdownMenuItem disabled={busy || pending || computerTransitioning(c.status)} onSelect={() => setEditor(c)}><Pencil />编辑 / 重建</DropdownMenuItem>
                <DropdownMenuSeparator />
                <DropdownMenuItem className="text-destructive focus:text-destructive" disabled={busy || pending || computerTransitioning(c.status)} onSelect={() => { recoverKey.current = uuid(); setRecovering(c) }}><Square />强制停止</DropdownMenuItem>
                <DropdownMenuItem className="text-destructive focus:text-destructive" disabled={busy || !mutable} onSelect={() => { deleteKey.current = uuid(); setDeleting(c) }}><Trash2 />删除电脑</DropdownMenuItem>
              </DropdownMenuContent>
            </DropdownMenu>
          </div>
          <div className="flex min-w-0 items-center gap-1 text-xs text-muted-foreground">
            <span className="shrink-0 whitespace-nowrap">{c.config.resources?.cpu ?? "默认"} CPU · {c.config.resources?.memory ?? "默认内存"}</span>
            <span aria-hidden="true" className="shrink-0">·</span>
            <Tooltip>
              <TooltipTrigger asChild>
                <span tabIndex={0} aria-label={`镜像：${c.config.image}`} className="min-w-0 truncate rounded-sm focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring">{imageName}</span>
              </TooltipTrigger>
              <TooltipContent className="max-w-sm break-all">{c.config.image}</TooltipContent>
            </Tooltip>
          </div>
          <ComputerFailure computer={c} />
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
        return <div key={kind} className="rounded-lg border p-3 text-sm"><div className="flex items-center justify-between gap-2"><code>/{kind}</code><VolumeStatusIndicator status={v?.status} /></div>
          <p className="mt-1 text-xs text-muted-foreground">{kind === "storage" ? "长期资产，最高保留优先级" : kind === "agents" ? "智能体长期文件与技能" : "各会话的输入、工作文件与成果"}</p>
          {v?.last_error && <p className="mt-2 text-destructive">{v.last_error}</p>}</div>
      })}</div>
      <p className="text-xs text-muted-foreground">暂停、重建或删除电脑均保留这三类存储。会话和智能体目录按各自的数据保留策略管理。</p>
    </section>
    <p className="text-sm text-muted-foreground">为智能体配置默认云电脑，请前往 <Link className="text-link hover:underline" to="/personal/agents">个人中心 / 智能体</Link>。</p>
    {editor && org && <ComputerEditor key={editor === "create" ? "create" : editor.id} orgId={org.id} computer={editor === "create" ? undefined : editor} onClose={() => setEditor(null)} onSaved={track} onRenamed={() => void load()} />}
    <ConfirmDialog open={!!deleting} onOpenChange={(open) => !open && setDeleting(null)} title={`删除「${deleting?.name ?? ""}」`} description="将停止并删除这台电脑，三类持久存储继续保留。有会话或默认设置引用时，需要先切换绑定。" destructive confirmText="删除电脑" loading={busy} onConfirm={() => deleting && void act(deleting, "delete")} />
    <ConfirmDialog open={!!recovering} onOpenChange={(open) => !open && !busy && setRecovering(null)} title={`强制停止「${recovering?.name ?? ""}」`} description="将强制终止这台电脑所有会话、子任务和维护进程（包括正常运行的任务），停止并移除当前实例，清理遗留占用。完成后电脑处于未启动状态，下次启动会创建新实例，中断的任务不会自动续跑；系统临时文件会丢失，storage、agents、sessions 三类持久文件及会话和智能体绑定保留。此操作无法撤销。" destructive confirmText="强制停止" loading={busy} onConfirm={() => recovering && void recover(recovering)} />
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
    <DialogHeader><DialogTitle>{computer ? "编辑云电脑" : "创建云电脑"}</DialogTitle><DialogDescription>{computer ? "名称和变量可直接保存；修改镜像或资源需要空闲后重建，三类持久存储保留。" : "镜像和资源可使用智能体模板；环境变量以系统默认值为基础。"}</DialogDescription></DialogHeader>
    <div className="space-y-3">
      <div><Label htmlFor="computer-name">名称</Label><Input id="computer-name" maxLength={128} value={name} onChange={(e) => changed(() => setName(e.target.value))} /></div>
      {!computer && <div><Label htmlFor="computer-template">环境模板</Label><select id="computer-template" className={selectClass} value={agent} onChange={(e) => changed(() => setAgent(e.target.value))}><option value="">系统默认</option>{agents?.map((a) => <option key={a.id} value={a.id}>{a.name}</option>)}</select></div>}
      <div><Label htmlFor="computer-image">镜像（系统已启用）</Label><Input id="computer-image" value={image} placeholder="使用默认镜像" onChange={(e) => changed(() => setImage(e.target.value))} /></div>
      <div className="grid grid-cols-2 gap-3"><div><Label htmlFor="computer-cpu">CPU</Label><Input id="computer-cpu" value={cpu} placeholder="默认" onChange={(e) => changed(() => setCPU(e.target.value))} /></div><div><Label htmlFor="computer-memory">内存</Label><Input id="computer-memory" value={memory} placeholder="默认，例如 1Gi" onChange={(e) => changed(() => setMemory(e.target.value))} /></div></div>
      <div className="space-y-2"><Label>电脑环境变量</Label><EnvEditor rows={envRows} savedKeys={envKeys} disabled={busy || !envLoaded} onChange={(rows) => changed(() => setEnvRows(rows))} /><p className="text-xs text-muted-foreground">下一轮任务生效。同名变量可由智能体和单次命令覆盖。</p></div>
      {error && <p role="alert" className="text-sm text-destructive">{error}</p>}
      <div className="flex justify-end gap-2"><Button variant="outline" disabled={busy} onClick={onClose}>取消</Button>{computer && <Button variant="outline" disabled={busy || !name.trim() || name === computer.name} onClick={() => void renameOnly()}>仅保存名称</Button>}<Button disabled={busy || !envLoaded || !name.trim()} onClick={() => void submit()}>{busy ? "正在提交…" : computer ? rebuild ? "保存并重建" : "保存" : "创建"}</Button></div>
    </div>
  </DialogContent></Dialog>
}
