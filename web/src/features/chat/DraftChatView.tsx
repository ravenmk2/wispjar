import { Bot, CircleAlert } from "lucide-react"
import { useEffect, useState } from "react"
import { Link, useSearchParams } from "react-router"
import { Button } from "@/components/ui/button"
import { EmptyState, TableSkeleton } from "@/components/common"
import { useOrgAgents } from "@/features/agents/AgentPicker"
import { Composer } from "@/features/chat/Composer"
import { MessageList } from "@/features/chat/MessageList"
import { useComposerStore } from "@/stores/composer"
import { useSessionStore } from "@/stores/session"
import { useCurrentOrg, useIdentityStore } from "@/stores/identity"

// 草稿聊天页（/c/new?agent=<id>）：不创建聊天、不 follow 流，首条消息发出时才建聊天（见 Composer draftAgent 分支）
export default function DraftChatView() {
  const [searchParams] = useSearchParams()
  const agentId = searchParams.get("agent") ?? ""
  const agents = useOrgAgents()
  const agent = agents?.find((a) => a.id === agentId) ?? null
  const resetSession = useSessionStore((s) => s.reset)
  const enterContext = useComposerStore((s) => s.enterContext)
  const org = useCurrentOrg()
  const userId = useIdentityStore((s) => s.user?.id)
  const [ready, setReady] = useState(false)

  // Clear events while preserving this agent's draft when returning from settings.
  useEffect(() => {
    resetSession()
    enterContext(`${userId}:${org?.id}:agent:${agentId}`)
    setReady(true)
  }, [resetSession, enterContext, userId, org?.id, agentId])

  if (!agents || !ready) {
    return (
      <div className="mx-auto w-full max-w-chat flex-1 px-4 py-6">
        <TableSkeleton rows={4} />
      </div>
    )
  }

  if (!agent) {
    return (
      <div className="flex h-full items-center justify-center p-6">
        <EmptyState
          className="w-full max-w-md border-none"
          icon={<CircleAlert className="size-10" />}
          title="智能体不存在或已下线"
          description="该智能体可能已被删除或归档，请返回工作区重新选择。"
          action={
            <Button asChild variant="outline" size="sm">
              <Link to="/">返回工作区</Link>
            </Button>
          }
        />
      </div>
    )
  }

  return (
    <div className="flex h-full min-w-0 flex-1 flex-col">
      <div className="flex h-12 shrink-0 items-center gap-2 border-b px-4">
        <Bot className="size-4 shrink-0 text-muted-foreground" />
        <span className="truncate text-base font-normal">新聊天</span>
        <span className="shrink-0 rounded bg-muted px-1.5 py-0.5 text-meta text-muted-foreground">
          {agent.name}
        </span>
      </div>
      <MessageList sessionId="" />
      <Composer key={agent.id} sessionId="" draftAgent={agent} />
    </div>
  )
}
