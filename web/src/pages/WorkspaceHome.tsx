import { Bot, Clock, PlusCircle } from "lucide-react"
import { Link, useNavigate } from "react-router"
import { EmptyState } from "@/components/common"
import { Avatar, AvatarFallback } from "@/components/ui/avatar"
import { Button } from "@/components/ui/button"
import { useOrgAgents } from "@/features/agents/AgentPicker"
import { formatRelativeTime } from "@/lib/format"
import { useCurrentOrg } from "@/stores/identity"
import { useSessionListStore } from "@/stores/sessionList"

export default function WorkspaceHome() {
  const currentOrg = useCurrentOrg()
  const agents = useOrgAgents()
  const navigate = useNavigate()
  const sessions = useSessionListStore((s) => s.sessions)
  const order = useSessionListStore((s) => s.order)
  const recent = order.slice(0, 5).map((id) => sessions[id]).filter(Boolean)

  if (!agents) {
    return (
      <div className="mx-auto grid w-full max-w-chat gap-3 p-8 sm:grid-cols-2">
        {Array.from({ length: 4 }).map((_, i) => (
          <div key={i} className="h-28 animate-pulse rounded-lg bg-muted" />
        ))}
      </div>
    )
  }

  if (agents.length === 0) {
    const isAdmin = currentOrg?.role === "admin"
    return (
      <div className="flex h-full items-center justify-center p-6">
        <EmptyState
          className="w-full max-w-lg border-none"
          icon={<Bot className="size-10" />}
          title="还没有可用的智能体"
          description={isAdmin ? "先创建一个智能体，即可开始对话。" : "当前机构暂无已发布的智能体，请联系机构管理员创建。"}
          action={
            isAdmin ? (
              <Button asChild size="sm">
                <Link to="/admin/agents">
                  <PlusCircle className="size-4" />
                  去创建智能体
                </Link>
              </Button>
            ) : undefined
          }
        />
      </div>
    )
  }

  return (
    <div className="mx-auto w-full max-w-chat px-6 py-16">
      <h1 className="text-2xl font-semibold tracking-tight">开始新的对话</h1>
      <p className="mt-2 text-chat text-fg-secondary">选择一个智能体，开始你的工作</p>
      <div className="mt-8 grid gap-3 sm:grid-cols-2">
        {agents.map((agent) => (
          <button
            key={agent.id}
            type="button"
            onClick={() => navigate(`/c/new?agent=${agent.id}`)}
            className="flex items-start gap-3 rounded-xl border bg-card p-4 text-left transition-colors hover:border-primary/30 hover:bg-accent/50"
          >
            <Avatar className="size-9 shrink-0">
              <AvatarFallback>{agent.avatar || <Bot className="size-4" />}</AvatarFallback>
            </Avatar>
            <div className="min-w-0 flex-1">
              <div className="flex items-center gap-2">
                <span className="truncate text-chat font-medium">{agent.name}</span>
              </div>
              {agent.description && (
                <p className="mt-0.5 line-clamp-2 text-xs text-fg-secondary">{agent.description}</p>
              )}
              {agent.model_display_name && (
                <p className="mt-1 text-meta text-fg-caption">{agent.model_display_name}</p>
              )}
            </div>
          </button>
        ))}
      </div>
      {recent.length > 0 && (
        <>
          <h2 className="mb-3 mt-10 flex items-center gap-1.5 text-base font-medium text-fg-tertiary">
            <Clock className="size-3.5" />
            最近的会话
          </h2>
          <div className="divide-y rounded-xl border bg-card">
            {recent.map((s) => (
              <Link
                key={s.sessionId}
                to={`/c/${s.sessionId}`}
                className="flex items-center gap-2 px-3.5 py-2.5 text-chat transition-colors hover:bg-accent/50"
              >
                <span
                  className={
                    s.running ? "size-1.5 shrink-0 animate-pulse rounded-full bg-primary" : "size-1.5 shrink-0 rounded-full bg-transparent"
                  }
                />
                <span className="min-w-0 flex-1 truncate">{s.title || "新会话"}</span>
                <span className="text-xs text-fg-caption">{formatRelativeTime(s.lastActiveAt)}</span>
              </Link>
            ))}
          </div>
        </>
      )}
    </div>
  )
}
