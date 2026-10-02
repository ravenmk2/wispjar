import { AlertTriangle, CheckCircle2, ShieldQuestion, XCircle } from "lucide-react"
import { useEffect, useState } from "react"
import { toast } from "sonner"
import { errorMessage } from "@/api/client"
import { sessionsApi } from "@/api/sessions"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { DiffBlock } from "@/components/Markdown"
import { safeParseJson } from "@/lib/format"
import type { ApprovalNode } from "@/lib/conversation"

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

function ApprovalParams({ node }: { node: ApprovalNode }) {
  const args = safeParseJson(node.arguments)
  if (node.name === "exec" && typeof args?.command === "string") {
    return (
      <div className="rounded-md border bg-zinc-950 px-3 py-2">
        <code className="font-mono text-xs text-zinc-100">$ {args.command}</code>
      </div>
    )
  }
  if ((node.name === "write" || node.name === "edit") && args) {
    const path = typeof args.path === "string" ? args.path : ""
    const oldStr = typeof args.old_string === "string" ? args.old_string : null
    const newStr = typeof args.new_string === "string" ? args.new_string : null
    const content = typeof args.content === "string" ? args.content : null
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
            {node.arguments}
          </pre>
        )}
      </div>
    )
  }
  return (
    <pre className="max-h-40 overflow-auto whitespace-pre-wrap rounded-md border bg-muted/40 p-3 font-mono text-xs">
      {node.arguments}
    </pre>
  )
}

export function ApprovalCard({ node, sessionId }: { node: ApprovalNode; sessionId: string }) {
  const [acting, setActing] = useState<"allow" | "deny" | null>(null)
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

  if (!pending) {
    return (
      <div className="flex items-center gap-2 rounded-md border bg-muted/30 px-3 py-2 text-xs text-muted-foreground">
        {node.status === "allow" ? (
          <CheckCircle2 className="size-3.5 text-green-600" />
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
        <ShieldQuestion className="size-4 text-amber-600" />
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
        <ApprovalParams node={node} />
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
