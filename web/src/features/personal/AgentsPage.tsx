import { Bot } from "lucide-react"
import { useCallback, useEffect, useState } from "react"
import { computersApi } from "@/api/computers"
import { workspaceApi } from "@/api/workspace"
import { errorMessage } from "@/api/client"
import { EmptyState, PageHeader, TableSkeleton } from "@/components/common"
import { Button } from "@/components/ui/button"
import { RefreshButton } from "@/components/RefreshButton"
import { Badge } from "@/components/ui/badge"
import { AgentComputerSettings } from "@/features/computers/AgentComputerSettings"
import type { AgentBinding, Computer } from "@/lib/computers"
import type { AgentSummary } from "@/lib/types"
import { useCurrentOrg, useIdentityStore } from "@/stores/identity"

export default function PersonalAgentsPage() {
  const org = useCurrentOrg()
  const token = useIdentityStore((s) => s.token)
  const [agents, setAgents] = useState<Array<Pick<AgentSummary, "id" | "name" | "description">>>([])
  const [computers, setComputers] = useState<Computer[]>([])
  const [bindings, setBindings] = useState<AgentBinding[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState("")
  const [editing, setEditing] = useState<Pick<AgentSummary, "id" | "name"> | null>(null)
  const [revision, setRevision] = useState(0)
  const refresh = useCallback(() => setRevision((value) => value + 1), [])

  useEffect(() => {
    let active = true
    setAgents([]); setBindings([]); setComputers([]); setEditing(null); setLoading(true); setError("")
    if (!org) { setLoading(false); return }
    Promise.all([workspaceApi.agents(org.id), computersApi.list(org.id), computersApi.bindings(org.id)]).then(([as, cs, bs]) => {
      if (!active) return
      const retired = bs.items.filter((binding) => !as.items.some((agent) => agent.id === binding.agent_id)).map((binding) => ({ id: binding.agent_id, name: `已下线智能体 ${binding.agent_id.slice(0, 8)}`, description: "可清除保留的个人配置" }))
      setAgents([...as.items, ...retired]); setComputers(cs.items); setBindings(bs.items)
    }).catch((e) => { if (active) setError(errorMessage(e)) }).finally(() => { if (active) setLoading(false) })
    return () => { active = false }
  }, [org?.id, token, revision])

  return <div className="mx-auto max-w-5xl space-y-5">
    <PageHeader title="智能体" description="管理你在当前组织中使用智能体的个人配置。"><RefreshButton variant="outline" size="sm" onClick={refresh} refreshing={loading} /></PageHeader>
    {error && <p role="alert" className="text-sm text-destructive">{error}</p>}
    {loading ? <TableSkeleton /> : !error && agents.length === 0 ? <EmptyState icon={<Bot className="size-8" />} title="暂无可用智能体" description="组织发布智能体后，可在这里配置个人使用偏好。" /> : !error && <div className="overflow-x-auto rounded-xl border">
      <table className="w-full text-left text-sm"><thead className="bg-muted/40 text-muted-foreground"><tr><th className="p-4 font-normal">智能体</th><th className="p-4 font-normal">默认云电脑</th><th className="p-4 font-normal">电脑绑定状态</th><th className="p-4"><span className="sr-only">操作</span></th></tr></thead>
        <tbody className="divide-y">{agents.map((agent) => {
          const binding = bindings.find((item) => item.agent_id === agent.id)
          const computer = computers.find((item) => item.id === binding?.computer_id)
          return <tr key={agent.id}><td className="p-4"><div className="flex items-center gap-3"><Bot className="size-4 shrink-0 text-muted-foreground" /><div><p className="font-medium">{agent.name}</p><p className="mt-1 text-xs text-muted-foreground">{agent.description}</p></div></div></td>
            <td className="whitespace-nowrap p-4">{computer?.name ?? (binding ? "原云电脑不可用" : "未绑定")}</td><td className="whitespace-nowrap p-4"><Badge variant="secondary">{computer ? "已绑定" : binding ? "需重新配置" : "待配置"}</Badge></td>
            <td className="p-4 text-right"><Button size="sm" variant="outline" onClick={() => setEditing(agent)}>配置</Button></td></tr>
        })}</tbody>
      </table>
    </div>}
    <p className="text-xs text-muted-foreground">这些配置仅对你本人在当前组织中生效。默认云电脑只影响新建会话，已有会话继续使用原电脑。</p>
    {editing && <AgentComputerSettings key={`${org?.id}-${editing.id}`} agent={editing} onClose={() => setEditing(null)} onSaved={refresh} />}
  </div>
}
