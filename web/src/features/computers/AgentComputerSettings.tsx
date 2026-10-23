import { useEffect, useState } from "react"
import { Link } from "react-router"
import { toast } from "sonner"
import { computersApi } from "@/api/computers"
import { errorMessage } from "@/api/client"
import { Button } from "@/components/ui/button"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Label } from "@/components/ui/label"
import { computerStatus, resolveComputerSelection, selectableComputer, type Computer } from "@/lib/computers"
import type { AgentSummary } from "@/lib/types"
import { useCurrentOrg, useIdentityStore } from "@/stores/identity"

export function AgentComputerSettings({ agent, onClose, onSaved }: { agent: Pick<AgentSummary, "id" | "name">; onClose: () => void; onSaved?: () => void }) {
  const org = useCurrentOrg()
  const [computers, setComputers] = useState<Computer[] | null>(null)
  const [selected, setSelected] = useState("")
  const [error, setError] = useState("")
  const [saving, setSaving] = useState(false)
  const [binding, setBinding] = useState("")

  useEffect(() => {
    if (!org) return
    let active = true
    setComputers(null); setSelected(""); setError("")
    Promise.all([computersApi.list(org.id), computersApi.bindings(org.id)]).then(([cs, bs]) => {
      if (!active) return
      setComputers(cs.items.filter(selectableComputer))
      setBinding(bs.items.find((item) => item.agent_id === agent.id)?.computer_id ?? "")
      setSelected(resolveComputerSelection(cs.items, bs.items, agent.id))
    }).catch((e) => { if (active) setError(errorMessage(e)) })
    return () => { active = false }
  }, [org?.id, agent.id])

  async function save() {
    if (!org || !selected || saving) return
    const orgId = org.id
    const token = useIdentityStore.getState().token
    setSaving(true); setError("")
    try {
      await computersApi.bind(orgId, agent.id, selected)
      if (useIdentityStore.getState().currentOrgId !== orgId || useIdentityStore.getState().token !== token) return
      toast.success(`已设置「${agent.name}」的云电脑`)
      onSaved?.()
      onClose()
    } catch (e) { setError(errorMessage(e)) } finally { setSaving(false) }
  }

  async function clearBinding() {
    if (!org || saving) return
    const orgId = org.id
    const token = useIdentityStore.getState().token
    setSaving(true); setError("")
    try {
      await computersApi.unbind(orgId, agent.id)
      if (useIdentityStore.getState().currentOrgId !== orgId || useIdentityStore.getState().token !== token) return
      toast.success("已清除默认云电脑，已有会话不受影响")
      onSaved?.(); onClose()
    } catch (e) { setError(errorMessage(e)) } finally { setSaving(false) }
  }

  return <Dialog open onOpenChange={(open) => { if (!open && !saving) onClose() }}>
    <DialogContent>
      <DialogHeader><DialogTitle>{agent.name} · 个人配置</DialogTitle><DialogDescription>配置你使用这个智能体时的默认云电脑，新聊天自动使用。已有聊天继续使用原电脑。</DialogDescription></DialogHeader>
      <div className="space-y-2">
        <Label htmlFor="agent-computer">绑定云电脑</Label>
        <select id="agent-computer" className="h-9 w-full rounded-md border bg-background px-3 text-sm" value={selected} disabled={!computers || saving} onChange={(event) => setSelected(event.target.value)}>
          <option value="" disabled>{computers ? "选择一台云电脑" : "正在加载…"}</option>
          {computers?.map((computer) => <option key={computer.id} value={computer.id}>{computer.name} · {computerStatus(computer.status)}</option>)}
        </select>
        {computers?.length === 0 && <p className="text-sm text-muted-foreground">还没有可用云电脑，请先创建一台。</p>}
        {error && <p role="alert" className="text-sm text-destructive">{error}</p>}
      </div>
      <DialogFooter className="gap-2 sm:justify-between">
        <Button variant="ghost" asChild disabled={saving}><Link to="/personal/computers" onClick={(event) => { if (saving) event.preventDefault(); else onClose() }}>管理云电脑</Link></Button>
        <div className="flex gap-2">{binding && <Button variant="outline" disabled={saving || !computers} onClick={() => void clearBinding()}>清除绑定</Button>}<Button disabled={!selected || saving || !computers} onClick={() => void save()}>{saving ? "正在保存…" : "保存"}</Button></div>
      </DialogFooter>
    </DialogContent>
  </Dialog>
}
