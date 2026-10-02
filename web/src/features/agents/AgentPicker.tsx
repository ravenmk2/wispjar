import { Bot, Loader2 } from "lucide-react"
import { useEffect, useState } from "react"
import { useNavigate } from "react-router"
import { toast } from "sonner"
import { errorMessage } from "@/api/client"
import { sessionsApi } from "@/api/sessions"
import { workspaceApi } from "@/api/workspace"
import { Avatar, AvatarFallback } from "@/components/ui/avatar"
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

export function useCreateSession() {
  const currentOrg = useCurrentOrg()
  const navigate = useNavigate()
  const [creatingId, setCreatingId] = useState<string | null>(null)

  async function createSession(agentId: string) {
    if (!currentOrg || creatingId) return
    setCreatingId(agentId)
    try {
      const session = await sessionsApi.create(currentOrg.id, { agent_id: agentId })
      navigate(`/c/${session.id}`)
    } catch (e) {
      toast.error(errorMessage(e))
    } finally {
      setCreatingId(null)
    }
  }

  return { createSession, creatingId }
}

export function AgentPicker() {
  const agents = useOrgAgents()
  const { createSession, creatingId } = useCreateSession()

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
    return <div className="px-2 py-1 text-xs text-muted-foreground">暂无可用智能体</div>
  }
  return (
    <div className="space-y-0.5 px-1">
      {agents.map((agent) => (
        <button
          key={agent.id}
          type="button"
          onClick={() => void createSession(agent.id)}
          disabled={creatingId !== null}
          className="flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left text-sm hover:bg-accent disabled:opacity-60"
        >
          <Avatar className="size-6 shrink-0">
            <AvatarFallback className="text-xs">
              {agent.avatar || <Bot className="size-3.5" />}
            </AvatarFallback>
          </Avatar>
          <span className="min-w-0 flex-1 truncate">{agent.name}</span>
          {creatingId === agent.id && <Loader2 className="size-3.5 animate-spin text-muted-foreground" />}
        </button>
      ))}
    </div>
  )
}
