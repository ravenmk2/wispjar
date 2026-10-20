import { useEffect, useState } from "react"
import { toast } from "sonner"
import { computersApi } from "@/api/computers"
import { sessionsApi } from "@/api/sessions"
import { errorMessage } from "@/api/client"
import { Button } from "@/components/ui/button"
import { ComputerPicker } from "./ComputerPicker"
import { computerStatus, computerTransitioning, workspaceRoot, type Computer } from "@/lib/computers"
import { uuid } from "@/lib/utils"
import type { SessionDetail } from "@/lib/types"

export function SessionComputer({ detail, busy, onChanged }: { detail: SessionDetail; busy: boolean; onChanged: (detail: SessionDetail) => void }) {
  const [computer, setComputer] = useState<Computer | null>(null)
  const [switching, setSwitching] = useState(false)
  useEffect(() => {
    let active = true
    setComputer(null)
    if (!detail.computer_id) return
    const refresh = () => computersApi.get(detail.computer_id!).then((c) => { if (active) setComputer(c) }).catch(() => {})
    void refresh()
    const timer = setInterval(() => void refresh(), 5000)
    return () => { active = false; clearInterval(timer) }
  }, [detail.computer_id])
  async function change(id: string) {
    if (!id || id === detail.computer_id) return
    setSwitching(true)
    try { onChanged(await sessionsApi.update(detail.id, { computer_id: id })); toast.success("已切换云电脑，工作空间保持不变") }
    catch (e) { toast.error(errorMessage(e)) } finally { setSwitching(false) }
  }
  async function start() {
    if (!detail.computer_id) return
    setSwitching(true)
    try { await computersApi.action(detail.computer_id, "start", uuid()); setComputer(await computersApi.get(detail.computer_id)); toast.info("启动请求已接收") }
    catch (e) { toast.error(errorMessage(e)) } finally { setSwitching(false) }
  }
  if (!detail.computer_id) return null
  return <div className="flex flex-wrap items-center gap-x-4 gap-y-1 border-b px-4 py-2">
    <ComputerPicker agentId={detail.agent_id} value={detail.computer_id} onChange={(id) => void change(id)} disabled={busy || switching || (!!computer && computerTransitioning(computer.status))} />
    {computer && <span className="text-xs text-muted-foreground">{computerStatus(computer.status)}</span>}
    {computer && ["paused", "stopped", "failed"].includes(computer.status) && <Button size="sm" variant="outline" onClick={() => void start()} disabled={switching}>启动电脑</Button>}
    <code className="text-xs text-muted-foreground" title="会话工作空间">{workspaceRoot(detail.id)}</code>
    {busy && <span className="text-xs text-muted-foreground">任务执行期间无法切换电脑</span>}
  </div>
}
