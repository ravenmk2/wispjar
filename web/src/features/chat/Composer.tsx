import { Send, Square } from "lucide-react"
import { useEffect, useRef, useState } from "react"
import { toast } from "sonner"
import { errorMessage } from "@/api/client"
import { sessionsApi } from "@/api/sessions"
import { workspaceApi } from "@/api/workspace"
import { Button } from "@/components/ui/button"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { TokenBadge } from "@/features/chat/AssistantMessage"
import { cn, uuid } from "@/lib/utils"
import type { ApprovalMode, OrgModel } from "@/lib/types"
import { useComposerStore } from "@/stores/composer"
import { useCurrentOrg } from "@/stores/identity"
import { useSessionStore } from "@/stores/session"

const APPROVAL_MODES: Array<{ value: ApprovalMode; label: string; hint: string }> = [
  { value: "strict", label: "严格", hint: "全部确认" },
  { value: "default", label: "默认", hint: "仅命令确认" },
  { value: "auto", label: "自动", hint: "全部放行" },
]

export function Composer({ sessionId }: { sessionId: string }) {
  const currentOrg = useCurrentOrg()
  const header = useSessionStore((s) => s.header)
  const liveAttempt = useSessionStore((s) => s.liveAttempt)
  const draft = useComposerStore((s) => s.draft)
  const sending = useComposerStore((s) => s.sending)
  const approvalMode = useComposerStore((s) => s.approvalMode)
  const setDraft = useComposerStore((s) => s.setDraft)
  const setSending = useComposerStore((s) => s.setSending)
  const setApprovalMode = useComposerStore((s) => s.setApprovalMode)
  const setOptimistic = useComposerStore((s) => s.setOptimistic)
  const clearOptimistic = useComposerStore((s) => s.clearOptimistic)

  const [models, setModels] = useState<OrgModel[]>([])
  const [modelOverride, setModelOverride] = useState<string | null>(null)
  const textareaRef = useRef<HTMLTextAreaElement>(null)

  const running = header?.running ?? false
  const busy = running || liveAttempt !== null
  const modelId = modelOverride ?? header?.modelId ?? ""

  useEffect(() => {
    if (header?.approvalMode) setApprovalMode(header.approvalMode)
  }, [header?.approvalMode, setApprovalMode])

  useEffect(() => {
    setModelOverride(null)
  }, [header?.modelId])

  useEffect(() => {
    if (!currentOrg) return
    workspaceApi
      .models(currentOrg.id)
      .then((res) => setModels(res.items))
      .catch(() => {})
  }, [currentOrg])

  useEffect(() => {
    const el = textareaRef.current
    if (!el) return
    el.style.height = "auto"
    el.style.height = `${Math.min(el.scrollHeight, 200)}px`
  }, [draft])

  async function send() {
    const content = draft.trim()
    if (!content || sending || busy) return
    const requestId = uuid()
    setOptimistic({ requestId, content })
    setSending(true)
    try {
      await sessionsApi.prompt(sessionId, { content, request_id: requestId })
      setDraft("")
    } catch (e) {
      clearOptimistic()
      toast.error(errorMessage(e))
    } finally {
      setSending(false)
    }
  }

  async function stop() {
    try {
      await sessionsApi.cancel(sessionId)
    } catch (e) {
      toast.error(errorMessage(e))
    }
  }

  async function changeApprovalMode(mode: ApprovalMode) {
    if (mode === approvalMode) return
    setApprovalMode(mode)
    try {
      await sessionsApi.update(sessionId, { approval_mode: mode })
    } catch (e) {
      toast.error(errorMessage(e))
      if (header?.approvalMode) setApprovalMode(header.approvalMode)
    }
  }

  async function changeModel(next: string) {
    if (!next || next === modelId) return
    setModelOverride(next)
    try {
      await sessionsApi.update(sessionId, { model_id: next })
    } catch (e) {
      toast.error(errorMessage(e))
      setModelOverride(null)
    }
  }

  const modelName = models.find((m) => m.id === modelId)?.display_name

  return (
    <div className="shrink-0 border-t">
      <div className="mx-auto max-w-3xl px-4 py-3">
        <div className="rounded-lg border bg-background focus-within:ring-1 focus-within:ring-ring">
          <textarea
            ref={textareaRef}
            value={draft}
            onChange={(e) => setDraft(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter" && !e.shiftKey && !e.nativeEvent.isComposing) {
                e.preventDefault()
                void send()
              }
            }}
            disabled={busy}
            rows={2}
            placeholder={busy ? "会话运行中，可点击右侧按钮停止…" : "输入消息，Enter 发送，Shift+Enter 换行"}
            className="max-h-[200px] w-full resize-none rounded-t-lg bg-transparent px-3 py-2.5 text-sm outline-none placeholder:text-muted-foreground disabled:cursor-not-allowed disabled:opacity-60"
          />
          <div className="flex items-center gap-2 border-t px-2 py-1.5">
            <div className="flex items-center rounded-md border p-0.5">
              {APPROVAL_MODES.map((m) => (
                <button
                  key={m.value}
                  type="button"
                  title={m.hint}
                  onClick={() => void changeApprovalMode(m.value)}
                  className={cn(
                    "rounded px-2 py-0.5 text-xs text-muted-foreground hover:text-foreground",
                    approvalMode === m.value && "bg-accent font-medium text-foreground",
                  )}
                >
                  {m.label}
                </button>
              ))}
            </div>
            <div className="ml-auto flex items-center gap-2">
              {header?.usageTotal && <TokenBadge usage={header.usageTotal} />}
              <Select value={modelId} onValueChange={(v) => void changeModel(v)}>
                <SelectTrigger className="h-7 w-auto gap-1.5 border-none px-2 text-xs shadow-none focus:ring-0">
                  <SelectValue placeholder="选择模型">{modelName ?? modelId}</SelectValue>
                </SelectTrigger>
                <SelectContent>
                  {models.map((m) => (
                    <SelectItem key={m.id} value={m.id} className="text-xs">
                      {m.display_name}
                      <span className="ml-1.5 text-muted-foreground">{m.provider_name}</span>
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
              {busy ? (
                <Button size="sm" variant="destructive" className="h-7 gap-1.5 px-2.5" onClick={() => void stop()}>
                  <Square className="size-3.5" />
                  停止
                </Button>
              ) : (
                <Button
                  size="sm"
                  className="h-7 gap-1.5 px-2.5"
                  onClick={() => void send()}
                  disabled={!draft.trim() || sending}
                >
                  <Send className="size-3.5" />
                  发送
                </Button>
              )}
            </div>
          </div>
        </div>
      </div>
    </div>
  )
}
