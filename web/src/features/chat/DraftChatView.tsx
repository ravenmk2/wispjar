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

// 草稿会话页（/c/new?agent=<id>）：不创建会话、不 follow 流，首条消息发出时才建会话（见 Composer draftAgent 分支）
export default function DraftChatView() {
  const [searchParams] = useSearchParams()
  const agentId = searchParams.get("agent") ?? ""
  const agents = useOrgAgents()
  const agent = agents?.find((a) => a.id === agentId) ?? null
  const resetSession = useSessionStore((s) => s.reset)
  const composerReset = useComposerStore((s) => s.reset)
  const [ready, setReady] = useState(false)

  // 清空上一会话残留的事件流与输入草稿，保证消息区为空；reset 同步生效，ready 后再渲染
  useEffect(() => {
    resetSession()
    composerReset()
    setReady(true)
  }, [resetSession, composerReset])

  if (!agents || !ready) {
    return (
      <div className="mx-auto w-full max-w-3xl flex-1 px-4 py-6">
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
      <div className="flex h-11 shrink-0 items-center gap-2 border-b px-4">
        <Bot className="size-4 shrink-0 text-muted-foreground" />
        <span className="truncate text-sm font-medium">新会话</span>
        <span className="shrink-0 rounded bg-muted px-1.5 py-0.5 text-[11px] text-muted-foreground">
          {agent.name}
        </span>
      </div>
      <MessageList sessionId="" />
      <Composer key={agent.id} sessionId="" draftAgent={agent} />
    </div>
  )
}
