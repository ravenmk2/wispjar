import { Bot, CircleAlert, ListTree, MessagesSquare, PanelRight, WifiOff } from "lucide-react"
import { useEffect, useMemo, useState } from "react"
import { Link, useParams } from "react-router"
import { ApiError, errorMessage } from "@/api/client"
import { sessionsApi } from "@/api/sessions"
import { Button } from "@/components/ui/button"
import { EmptyState, TableSkeleton } from "@/components/common"
import { ApprovalTakeover } from "@/features/chat/ApprovalCard"
import { Composer } from "@/features/chat/Composer"
import { MessageList } from "@/features/chat/MessageList"
import { QuestionTakeover } from "@/features/chat/QuestionCard"
import { StatsPills } from "@/features/chat/StatsPills"
import { SandboxPanel } from "@/features/sandbox/SandboxPanel"
import { TrajectoryView } from "@/features/trajectory/TrajectoryView"
import { followSession, type FollowHandle } from "@/mux/follow"
import { cn } from "@/lib/utils"
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
  const pendingApprovals = useSessionStore((s) => s.pendingApprovals)
  const pendingQuestions = useSessionStore((s) => s.pendingQuestions)
  const composerReset = useComposerStore((s) => s.reset)
  const [loadError, setLoadError] = useState<LoadError | null>(null)
  const [tab, setTab] = useState<"chat" | "trajectory">("chat")
  const [sandboxOpen, setSandboxOpen] = useState(false)

  // 顶层待审批队列（嵌套子智能体审批仍在 task 卡内处理），逐条接管 composer 区
  const pendingApproval = useMemo(
    () =>
      Object.values(pendingApprovals)
        .filter((a) => !a.parentCallId)
        .sort((a, b) => a.askedAt - b.askedAt)[0] ?? null,
    [pendingApprovals],
  )

  // 未决提问按 askedAt 排队；与审批同时 pending 时审批优先，答完审批提问卡接上
  const pendingQuestion = useMemo(
    () => Object.values(pendingQuestions).sort((a, b) => a.askedAt - b.askedAt)[0] ?? null,
    [pendingQuestions],
  )

  useEffect(() => {
    setLoadError(null)
    setTab("chat")
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
    <div className="flex h-full min-w-0 flex-1">
      <div className="flex h-full min-w-0 flex-1 flex-col">
        <div className="flex h-11 shrink-0 items-center gap-2 border-b px-4">
          <Bot className="size-4 shrink-0 text-muted-foreground" />
          <span className="truncate text-sm font-medium">{header?.title || "新会话"}</span>
          {header?.agentName && (
            <span className="shrink-0 rounded bg-muted px-1.5 py-0.5 text-[11px] text-muted-foreground">
              {header.agentName}
            </span>
          )}
          <div className="ml-4 flex shrink-0 items-center rounded-md border p-0.5">
            <button
              type="button"
              onClick={() => setTab("chat")}
              className={cn(
                "flex items-center gap-1 rounded-sm px-2 py-1 text-xs text-muted-foreground hover:text-foreground",
                tab === "chat" && "bg-accent font-medium text-foreground",
              )}
            >
              <MessagesSquare className="size-3.5" />
              聊天
            </button>
            <button
              type="button"
              onClick={() => setTab("trajectory")}
              className={cn(
                "flex items-center gap-1 rounded-sm px-2 py-1 text-xs text-muted-foreground hover:text-foreground",
                tab === "trajectory" && "bg-accent font-medium text-foreground",
              )}
            >
              <ListTree className="size-3.5" />
              轨迹
            </button>
          </div>
          {followStatus === "stalled" && (
            <span className="ml-auto flex shrink-0 items-center gap-1.5 rounded-md bg-amber-500/10 px-2 py-1 text-xs text-amber-700 dark:text-amber-400">
              <WifiOff className="size-3.5" />
              连接已断开，重连中…
            </span>
          )}
          <Button
            variant="ghost"
            size="icon"
            className={cn("size-8", followStatus !== "stalled" && "ml-auto", sandboxOpen && "bg-accent")}
            title="沙箱面板"
            onClick={() => setSandboxOpen((v) => !v)}
          >
            <PanelRight className="size-4" />
          </Button>
        </div>
        {followStatus === "connecting" && !header ? (
          <div className="mx-auto w-full max-w-3xl flex-1 px-4 py-6">
            <TableSkeleton rows={4} />
          </div>
        ) : tab === "chat" ? (
          <MessageList sessionId={sessionId} />
        ) : (
          <TrajectoryView sessionId={sessionId} />
        )}
        {tab === "chat" &&
          (pendingApproval ? (
            <ApprovalTakeover approval={pendingApproval} sessionId={sessionId} />
          ) : pendingQuestion ? (
            <QuestionTakeover question={pendingQuestion} sessionId={sessionId} />
          ) : (
            <Composer sessionId={sessionId} />
          ))}
        {tab === "chat" && <StatsPills />}
      </div>
      {sandboxOpen && <SandboxPanel sessionId={sessionId} onClose={() => setSandboxOpen(false)} />}
    </div>
  )
}
