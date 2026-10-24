import { ChevronsUpDown, Folder, HardDrive } from "lucide-react"
import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import { Link } from "react-router"
import { toast } from "sonner"
import { ApiError, errorMessage } from "@/api/client"
import { computersApi } from "@/api/computers"
import type { FileSpaceTarget } from "@/api/files"
import type { FileScope } from "@/api/sessions"
import { EmptyState } from "@/components/common"
import { Button } from "@/components/ui/button"
import { accessComputers } from "@/lib/file-browser"
import type { Computer } from "@/lib/computers"
import { cn } from "@/lib/utils"
import { useCurrentOrg, useIdentityStore } from "@/stores/identity"
import { AccessComputerPicker, FileSpacePicker, type FileObject } from "./FileSpacePicker"
import { FileBrowser, type FileLocation } from "./FileBrowser"

const names: Record<FileScope, string> = { storage: "我的存储", agent: "智能体", workspace: "会话" }

export default function StoragePage() {
  const org = useCurrentOrg()
  const user = useIdentityStore(s => s.user)
  return org && user ? <PersonalFiles key={`${org.id}:${user.id}`} orgId={org.id} /> : null
}

function PersonalFiles({ orgId }: { orgId: string }) {
  const [scope, setScope] = useState<FileScope>("storage")
  const [objects, setObjects] = useState<Partial<Record<FileScope, FileObject>>>({})
  const [pickerOpen, setPickerOpen] = useState(false)
  const [computers, setComputers] = useState<Computer[]>([])
  const [computerId, setComputerId] = useState("")
  const [autoId, setAutoId] = useState("")
  const [computersLoading, setComputersLoading] = useState(true)
  const [computersError, setComputersError] = useState("")
  const [listRevision, setListRevision] = useState(0)
  const [connecting, setConnecting] = useState(false)
  const [connectionError, setConnectionError] = useState(false)
  const [writing, setWriting] = useState(false)
  const [locations, setLocations] = useState<Record<string, FileLocation>>({})
  const token = useIdentityStore(s => s.token)
  const attempted = useRef(new Set<string>())
  const choices = useMemo(() => accessComputers(computers), [computers])
  const resolved = computers.find(c => c.id === (computerId || autoId))
  const targetObject = objects[scope]
  const spaceKey = `${scope}:${targetObject?.id ?? ""}`
  const location = locations[spaceKey] ?? { directory: ".", selected: null, expanded: ["."] }
  const target = useMemo<FileSpaceTarget | undefined>(() => resolved && (scope === "storage" || targetObject) ? { orgId, computerId: resolved.id, scope, targetId: scope === "storage" ? undefined : targetObject?.id } : undefined, [orgId, resolved?.id, scope, targetObject?.id])
  useEffect(() => {
    let active = true
    setComputersLoading(true); setComputersError("")
    void computersApi.list(orgId).then(result => {
      if (!active) return
      setComputers(result.items)
      setAutoId(previous => accessComputers(result.items).some(c => c.id === previous) ? previous : accessComputers(result.items)[0]?.id ?? "")
    }).catch(e => { if (active) setComputersError(errorMessage(e)) }).finally(() => { if (active) setComputersLoading(false) })
    return () => { active = false }
  }, [orgId, token, listRevision])
  function selectComputer(id: string) { attempted.current.clear(); setComputerId(id); setConnectionError(false); if (!id) setAutoId(choices[0]?.id ?? "") }
  const accessError = useCallback((error: unknown): boolean => {
    if (!computerId && resolved && error instanceof ApiError && (error.code.startsWith("computer/") || error.code === "file/api_unavailable")) {
      attempted.current.add(resolved.id)
      const next = choices.find(c => !attempted.current.has(c.id))
      if (next) { setAutoId(next.id); toast.info(`无法通过「${resolved.name}」读取文件，正在尝试「${next.name}」`); return true }
    }
    setConnectionError(true); return false
  }, [computerId, resolved, choices])
  const accessReady = useCallback(() => setConnectionError(false), [])
  const updateLocation = useCallback((next: FileLocation) => setLocations(old => ({ ...old, [spaceKey]: next })), [spaceKey])
  return <>
    <FileBrowser key={spaceKey} target={target} rootName={scope === "storage" ? "我的存储" : targetObject?.name ?? names[scope]} location={location} onLocation={updateLocation} onConnecting={setConnecting} onWriting={setWriting} onAccessError={accessError} onAccessReady={accessReady}
      sourceSelector={<><div className="flex gap-0.5 rounded-lg bg-muted/60 p-1" aria-label="文件来源">{(["storage", "agent", "workspace"] as const).map(value => <button type="button" key={value} disabled={writing} aria-pressed={scope === value} className={cn("min-w-0 flex-1 rounded-md px-1 py-1.5 text-xs text-muted-foreground disabled:opacity-50", scope === value && "bg-background font-medium text-foreground shadow-sm")} onClick={() => { setScope(value); if (value !== "storage" && !objects[value]) setPickerOpen(true) }}>{names[value]}</button>)}</div>
        {scope === "storage" ? <div className="mt-4 flex items-center gap-2"><HardDrive className="size-4 text-muted-foreground" /><div><p className="text-sm">我的存储</p><p className="mt-0.5 text-xs text-muted-foreground">跨会话使用的资料与成果</p></div></div> : <><Button variant="outline" className="mt-4 w-full justify-between gap-2 text-sm" disabled={writing} onClick={() => setPickerOpen(true)}><span className="truncate">{targetObject?.name ?? `选择${names[scope]}`}</span><ChevronsUpDown className="size-3.5 shrink-0 text-muted-foreground" /></Button><p className="mt-2 text-xs text-muted-foreground">{scope === "agent" ? "这个智能体的个人长期文件" : "这个会话的输入、工作文件与成果"}</p></>}
      </>}
      computerSelector={<AccessComputerPicker computers={computers} selected={computerId} resolved={resolved} loading={connecting || computersLoading} writing={writing} error={connectionError || !!computersError} onSelect={selectComputer} onRefresh={() => { attempted.current.clear(); setListRevision(n => n + 1) }} />}
      unavailable={computersLoading ? <p role="status" className="m-auto p-6 text-sm text-muted-foreground">正在读取云电脑…</p> : computersError ? <EmptyState title="无法读取云电脑" description={computersError} action={<Button variant="outline" onClick={() => setListRevision(n => n + 1)}>重试</Button>} /> : !resolved ? <EmptyState icon={<HardDrive className="size-8" />} title={computerId ? "所选电脑不可用" : "没有可用的云电脑"} description="文件通过你的云电脑访问。请在目录树底部选择电脑，或前往云电脑页面创建。" action={<Button variant="outline" asChild><Link to="/personal/computers">管理云电脑</Link></Button>} /> : <EmptyState icon={<Folder className="size-8" />} title={`选择${names[scope]}`} description="选择后即可浏览对应文件，不会新建会话。" action={<Button onClick={() => setPickerOpen(true)}>选择{names[scope]}</Button>} />}
    />
    <FileSpacePicker orgId={orgId} scope={scope} selected={targetObject?.id} open={pickerOpen} onOpenChange={setPickerOpen} onSelect={object => setObjects(old => ({ ...old, [scope]: object }))} />
  </>
}
