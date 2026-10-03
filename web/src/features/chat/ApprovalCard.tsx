import { AlertTriangle, CheckCircle2, ShieldQuestion, XCircle } from "lucide-react"
import { useEffect, useRef, useState } from "react"
import { toast } from "sonner"
import { errorMessage } from "@/api/client"
import { sessionsApi } from "@/api/sessions"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { DiffBlock } from "@/components/Markdown"
import { safeParseJson } from "@/lib/format"
import type { ApprovalNode } from "@/lib/conversation"
import { useSessionStore, type PendingApproval } from "@/stores/session"

const APPROVAL_TTL_MS = 10 * 60 * 1000

function useCountdown(deadline: number, active: boolean): number {
  const [remaining, setRemaining] = useState(() => Math.max(0, deadline - Date.now()))
  useEffect(() => {
    if (!active) return
    const timer = setInterval(() => setRemaining(Math.max(0, deadline - Date.now())), 1000)
    return () => clearInterval(timer)
  }, [deadline, active])
  return active ? remaining : 0
}

function formatRemaining(ms: number): string {
  const total = Math.ceil(ms / 1000)
  const m = Math.floor(total / 60)
  const s = total % 60
  return `${m}:${String(s).padStart(2, "0")}`
}

function ApprovalParams({ name, args }: { name: string; args: string }) {
  const parsed = safeParseJson(args)
  if (name === "exec" && typeof parsed?.command === "string") {
    return (
      <div className="rounded-md border bg-zinc-950 px-3 py-2">
        <code className="font-mono text-xs text-zinc-100">$ {parsed.command}</code>
      </div>
    )
  }
  if ((name === "write" || name === "edit") && parsed) {
    const path = typeof parsed.path === "string" ? parsed.path : ""
    const oldStr = typeof parsed.old_string === "string" ? parsed.old_string : null
    const newStr = typeof parsed.new_string === "string" ? parsed.new_string : null
    const content = typeof parsed.content === "string" ? parsed.content : null
    const diff =
      oldStr !== null && newStr !== null
        ? `--- ${path}\n+++ ${path}\n${oldStr.split("\n").map((l) => `-${l}`).join("\n")}\n${newStr.split("\n").map((l) => `+${l}`).join("\n")}`
        : content !== null
          ? `--- /dev/null\n+++ ${path}\n${content.split("\n").map((l) => `+${l}`).join("\n")}`
          : null
    return (
      <div>
        {path && <div className="mb-1 font-mono text-xs text-muted-foreground">{path}</div>}
        {diff ? (
          <DiffBlock diff={diff} className="max-h-60 overflow-y-auto" />
        ) : (
          <pre className="max-h-40 overflow-auto whitespace-pre-wrap rounded-md border bg-muted/40 p-3 font-mono text-xs">
            {args}
          </pre>
        )}
      </div>
    )
  }
  return (
    <pre className="max-h-40 overflow-auto whitespace-pre-wrap rounded-md border bg-muted/40 p-3 font-mono text-xs">
      {args}
    </pre>
  )
}

export function ApprovalCard({ node, sessionId }: { node: ApprovalNode; sessionId: string }) {
  const [acting, setActing] = useState<"allow" | "deny" | null>(null)
  const pendingEntry = useSessionStore((s) => s.pendingApprovals[node.callId])
  const pending = node.status === "pending"
  const remaining = useCountdown(node.time + APPROVAL_TTL_MS, pending)

  async function decide(decision: "allow" | "deny") {
    if (acting) return
    setActing(decision)
    try {
      await sessionsApi.decideApproval(sessionId, node.callId, decision)
    } catch (e) {
      toast.error(errorMessage(e))
    } finally {
      setActing(null)
    }
  }

  // 顶层待审批由 composer 区的 ApprovalTakeover 接管，消息流内不再渲染小卡；
  // 嵌套子智能体审批（带 parentCallId）保持现状
  if (pending && pendingEntry && !pendingEntry.parentCallId) return null

  if (!pending) {
    return (
      <div className="flex items-center gap-2 rounded-md border bg-muted/30 px-3 py-2 text-xs text-muted-foreground">
        {node.status === "allow" ? (
          <CheckCircle2 className="size-3.5 text-green-600 dark:text-green-400" />
        ) : (
          <XCircle className="size-3.5 text-red-500" />
        )}
        <span className="font-medium">{node.name}</span>
        <span>
          {node.status === "allow" ? "已允许" : node.auto ? "已自动拒绝（超时或系统关闭）" : "已拒绝"}
        </span>
        {node.decidedBy && node.decidedBy !== "system" && <span>· {node.decidedBy}</span>}
      </div>
    )
  }

  return (
    <div className="rounded-md border border-amber-500/50 bg-amber-500/5">
      <div className="flex items-center gap-2 border-b border-amber-500/30 px-3 py-2">
        <ShieldQuestion className="size-4 text-amber-600 dark:text-amber-400" />
        <span className="text-sm font-medium">审批请求</span>
        <span className="text-xs text-muted-foreground">{node.name}</span>
        <Badge
          variant={node.risk === "high" ? "destructive" : "secondary"}
          className="text-[11px] font-normal"
        >
          {node.risk === "high" ? "高风险" : "中风险"}
        </Badge>
        <span className="ml-auto flex items-center gap-1 font-mono text-xs text-amber-700 dark:text-amber-400">
          <AlertTriangle className="size-3" />
          {formatRemaining(remaining)}
        </span>
      </div>
      <div className="space-y-3 px-3 py-2.5">
        <ApprovalParams name={node.name} args={node.arguments} />
        <div className="flex items-center gap-2">
          <Button size="sm" onClick={() => void decide("allow")} disabled={acting !== null}>
            {acting === "allow" ? "处理中…" : "允许"}
          </Button>
          <Button
            size="sm"
            variant="outline"
            onClick={() => void decide("deny")}
            disabled={acting !== null}
          >
            {acting === "deny" ? "处理中…" : "拒绝"}
          </Button>
          <span className="text-xs text-muted-foreground">超时未处理将自动拒绝</span>
        </div>
      </div>
    </div>
  )
}

/** composer 区域整体接管的审批卡（替换 Composer 胶囊卡位置） */
export function ApprovalTakeover({
  approval,
  sessionId,
}: {
  approval: PendingApproval
  sessionId: string
}) {
  const [acting, setActing] = useState<"allow" | "deny" | null>(null)
  const remaining = useCountdown(approval.askedAt + APPROVAL_TTL_MS, true)
  const bodyRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    bodyRef.current?.focus()
  }, [approval.callId])

  async function decide(decision: "allow" | "deny") {
    if (acting) return
    setActing(decision)
    try {
      await sessionsApi.decideApproval(sessionId, approval.callId, decision)
    } catch (e) {
      toast.error(errorMessage(e))
    } finally {
      setActing(null)
    }
  }

  function onKeyDown(e: React.KeyboardEvent<HTMLDivElement>) {
    if (e.defaultPrevented || e.nativeEvent.isComposing || e.repeat) return
    const target = e.target as HTMLElement
    if (target.closest("input, textarea, select, [contenteditable]")) return
    if (e.ctrlKey || e.metaKey || e.altKey || e.shiftKey) return
    if (e.key === "Enter") {
      if (target.closest("button, a[href], [role=button]")) return
      e.preventDefault()
      void decide("allow")
    } else if (e.key === "Escape") {
      e.preventDefault()
      void decide("deny")
    }
  }

  return (
    <div className="shrink-0">
      <div className="mx-auto max-w-3xl px-4 pb-4 pt-2">
        <div
          aria-busy={acting !== null}
          onKeyDown={onKeyDown}
          className="overflow-hidden rounded-[28px] border border-warning bg-card shadow-[0_4px_24px_rgba(15,17,21,0.08)]"
        >
          <div className="flex items-center gap-2 bg-warning/10 px-4 py-2.5 text-[13px] font-medium text-warning">
            <span className="size-2 rounded-full bg-warning" />
            等待审批
            <span className="ml-auto font-mono text-xs font-normal">{formatRemaining(remaining)}</span>
          </div>
          <div
            ref={bodyRef}
            tabIndex={0}
            role="group"
            aria-label="审批详情"
            className="max-h-[336px] overflow-y-auto px-4 pt-3 outline-none"
          >
            <div className="text-[15px] font-medium leading-6">工具 {approval.name} 请求执行</div>
            <div className="pb-1 pt-1.5">
              <ApprovalParams name={approval.name} args={approval.arguments} />
            </div>
          </div>
          <div className="flex items-center justify-end gap-2 px-4 py-3.5">
            <Button
              size="sm"
              variant="outline"
              onClick={() => void decide("deny")}
              disabled={acting !== null}
              className="hover:border-transparent hover:bg-destructive/10 hover:text-destructive"
            >
              {acting === "deny" ? "处理中…" : "拒绝"}
            </Button>
            <Button size="sm" onClick={() => void decide("allow")} disabled={acting !== null}>
              {acting === "allow" ? "处理中…" : "允许一次"}
            </Button>
          </div>
        </div>
      </div>
    </div>
  )
}
