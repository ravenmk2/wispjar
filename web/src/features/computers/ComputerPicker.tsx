import { useEffect, useState } from "react"
import { Link } from "react-router"
import { computersApi } from "@/api/computers"
import { errorMessage } from "@/api/client"
import { computerStatus, resolveComputerSelection, selectableComputer, type Computer } from "@/lib/computers"
import { useCurrentOrg } from "@/stores/identity"

export function ComputerPicker({ agentId, value, onChange, disabled = false, defaultBinding = false }: {
  agentId: string; value: string; onChange: (id: string) => void; disabled?: boolean; defaultBinding?: boolean
}) {
  const org = useCurrentOrg()
  const [items, setItems] = useState<Computer[] | null>(null)
  const [error, setError] = useState("")
  useEffect(() => {
    let active = true
    setItems(null); setError("")
    if (!org) return
    if (defaultBinding) onChange("")
    Promise.all([computersApi.list(org.id), computersApi.bindings(org.id)]).then(([cs, bs]) => {
      if (!active) return
      setItems(cs.items)
      if (defaultBinding) onChange(resolveComputerSelection(cs.items, bs.items, agentId))
    }).catch((e) => { if (active) setError(errorMessage(e)) })
    return () => { active = false }
  }, [org?.id, agentId, defaultBinding])
  return <div className="flex flex-wrap items-center gap-2 text-sm">
    <label htmlFor={`computer-picker-${agentId}`} className="text-muted-foreground">云电脑</label>
    <select id={`computer-picker-${agentId}`} className="h-8 max-w-64 rounded-md border bg-background px-2 text-sm" value={value} disabled={disabled || !items} onChange={(e) => onChange(e.target.value)}>
      <option value="">{items ? "选择电脑" : "正在加载…"}</option>
      {items?.filter(selectableComputer).map((c) => <option key={c.id} value={c.id}>{c.name} · {computerStatus(c.status)}</option>)}
    </select>
    <Link to="/computers" className="text-xs text-muted-foreground underline">管理电脑</Link>
    {error && <span role="alert" className="text-xs text-destructive">{error}</span>}
    {items?.length === 0 && <span className="text-xs text-muted-foreground">请先创建一台云电脑。</span>}
  </div>
}
