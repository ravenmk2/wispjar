import { Bot } from "lucide-react"
import { useEffect, useState } from "react"
import { useNavigate } from "react-router"
import { toast } from "sonner"
import { errorMessage } from "@/api/client"
import { workspaceApi } from "@/api/workspace"
import { Avatar, AvatarFallback } from "@/components/ui/avatar"
import { AgentAvatar, AVATAR_ICONS } from "@/features/agents/AgentAvatar"
import { useCurrentOrg } from "@/stores/identity"
import type { AgentSummary } from "@/lib/types"

export function useOrgAgents() {
  const currentOrg = useCurrentOrg()
  const [agents, setAgents] = useState<AgentSummary[] | null>(null)
  useEffect(() => {
    setAgents(null)
    if (!currentOrg) return
    let cancelled = false
    workspaceApi
      .agents(currentOrg.id)
      .then((res) => {
        if (!cancelled) setAgents(res.items)
      })
      .catch((e) => {
        if (!cancelled) {
          setAgents([])
          toast.error(errorMessage(e))
        }
      })
    return () => {
      cancelled = true
    }
  }, [currentOrg])
  return agents
}

export function AgentPicker() {
  const agents = useOrgAgents()
  const navigate = useNavigate()

  if (!agents) {
    return (
      <div className="space-y-1 px-1">
        {Array.from({ length: 2 }).map((_, i) => (
          <div key={i} className="h-9 animate-pulse rounded-md bg-muted" />
        ))}
      </div>
    )
  }
  if (agents.length === 0) {
    return <div className="px-2 py-1 text-sm text-muted-foreground">暂无可用智能体</div>
  }
  return (
    <div className="space-y-0.5 px-1">
      {agents.map((agent) => (
        <button
          key={agent.id}
          type="button"
          onClick={() => navigate(`/c/new?agent=${agent.id}`)}
          className="flex w-full items-center gap-2 min-h-9 rounded-[10px] px-2 py-1.5 text-left text-sm leading-5 hover:bg-accent"
        >
          <Avatar className="size-6 shrink-0">
            <AvatarFallback className="text-xs">
              {agent.avatar && AVATAR_ICONS[agent.avatar] ? (
                <AgentAvatar avatar={agent.avatar} className="size-3.5" />
              ) : (
                agent.avatar || <Bot className="size-3.5" />
              )}
            </AvatarFallback>
          </Avatar>
          <span className="min-w-0 flex-1 truncate">{agent.name}</span>
        </button>
      ))}
    </div>
  )
}
