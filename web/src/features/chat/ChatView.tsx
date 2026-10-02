import { Bot, CircleAlert, WifiOff } from "lucide-react"
import { useEffect, useState } from "react"
import { Link, useParams } from "react-router"
import { ApiError, errorMessage } from "@/api/client"
import { sessionsApi } from "@/api/sessions"
import { Button } from "@/components/ui/button"
import { EmptyState, TableSkeleton } from "@/components/common"
import { Composer } from "@/features/chat/Composer"
import { MessageList } from "@/features/chat/MessageList"
import { followSession, type FollowHandle } from "@/mux/follow"
import { useComposerStore } from "@/stores/composer"
import { useCurrentOrg } from "@/stores/identity"
import { useSessionStore } from "@/stores/session"

interface LoadError {
  title: string
  description: string
}

function toLoadError(e: unknown): LoadError {
  if (e instanceof ApiError && (e.status === 403 || e.status === 404)) {
    return { title: "会话不存在或无权访问", description: "该会话可能已被删除，或不属于当前账号。" }
  }
  return { title: "会话加载失败", description: errorMessage(e) }
}

export default function ChatView() {
  const { sessionId = "" } = useParams()
  const currentOrg = useCurrentOrg()
  const header = useSessionStore((s) => s.header)
  const followStatus = useSessionStore((s) => s.followStatus)
  const composerReset = useComposerStore((s) => s.reset)
  const [loadError, setLoadError] = useState<LoadError | null>(null)

  useEffect(() => {
    setLoadError(null)
    if (!sessionId) return
    sessionsApi.detail(sessionId).catch((e) => setLoadError(toLoadError(e)))
  }, [sessionId])

  useEffect(() => {
    if (!sessionId || !currentOrg) return
    composerReset()
    const handle: FollowHandle = followSession(currentOrg.id, sessionId)
    return () => handle.close()
  }, [sessionId, currentOrg, composerReset])

  if (loadError) {
    return (
      <div className="flex h-full items-center justify-center p-6">
        <EmptyState
          className="w-full max-w-md border-none"
          icon={<CircleAlert className="size-10" />}
          title={loadError.title}
          description={loadError.description}
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
    <div className="flex h-full flex-col">
      <div className="flex h-11 shrink-0 items-center gap-2 border-b px-4">
        <Bot className="size-4 text-muted-foreground" />
        <span className="truncate text-sm font-medium">{header?.title || "新会话"}</span>
        {header?.agentName && (
          <span className="rounded bg-muted px-1.5 py-0.5 text-[11px] text-muted-foreground">{header.agentName}</span>
        )}
        {followStatus === "stalled" && (
          <span className="ml-auto flex items-center gap-1.5 rounded-md bg-amber-500/10 px-2 py-1 text-xs text-amber-700 dark:text-amber-400">
            <WifiOff className="size-3.5" />
            连接已断开，重连中…
          </span>
        )}
      </div>
      {followStatus === "connecting" && !header ? (
        <div className="mx-auto w-full max-w-3xl flex-1 px-4 py-6">
          <TableSkeleton rows={4} />
        </div>
      ) : (
        <MessageList sessionId={sessionId} />
      )}
      <Composer sessionId={sessionId} />
    </div>
  )
}
