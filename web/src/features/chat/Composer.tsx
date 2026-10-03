import { ArrowUp, Check, ChevronDown, Shield, ShieldCheck, ShieldOff, Square } from "lucide-react"
import { useEffect, useMemo, useRef, useState } from "react"
import { toast } from "sonner"
import { errorMessage } from "@/api/client"
import { sessionsApi } from "@/api/sessions"
import { workspaceApi } from "@/api/workspace"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { Input } from "@/components/ui/input"
import { TokenBadge } from "@/features/chat/AssistantMessage"
import { cn, uuid } from "@/lib/utils"
import type { ApprovalMode, OrgModel } from "@/lib/types"
import { useComposerStore } from "@/stores/composer"
import { useCurrentOrg } from "@/stores/identity"
import { useSessionStore } from "@/stores/session"

const APPROVAL_MODES: Array<{
  value: ApprovalMode
  label: string
  hint: string
  icon: typeof Shield
}> = [
  { value: "strict", label: "严格", hint: "全部确认", icon: ShieldCheck },
  { value: "default", label: "默认", hint: "仅命令确认", icon: Shield },
  { value: "auto", label: "自动", hint: "全部放行", icon: ShieldOff },
]

// 内容自适应长高，约 14 行（leading-6）封顶后内部滚动
const MAX_TEXTAREA_HEIGHT = 14 * 24

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
  const [modelQuery, setModelQuery] = useState("")
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
    el.style.height = `${Math.min(el.scrollHeight, MAX_TEXTAREA_HEIGHT)}px`
    el.style.overflowY = el.scrollHeight > MAX_TEXTAREA_HEIGHT ? "auto" : "hidden"
  }, [draft])

  const filteredModels = useMemo(() => {
    const q = modelQuery.trim().toLowerCase()
    if (!q) return models
    return models.filter(
      (m) =>
        m.display_name.toLowerCase().includes(q) ||
        m.model_code.toLowerCase().includes(q) ||
        m.provider_name.toLowerCase().includes(q),
    )
  }, [models, modelQuery])

  const modelGroups = useMemo(() => {
    const groups: Array<{ provider: string; items: OrgModel[] }> = []
    for (const m of filteredModels) {
      const group = groups.find((g) => g.provider === m.provider_name)
      if (group) group.items.push(m)
      else groups.push({ provider: m.provider_name, items: [m] })
    }
    return groups
  }, [filteredModels])

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
  const currentMode = APPROVAL_MODES.find((m) => m.value === approvalMode) ?? APPROVAL_MODES[1]

  return (
    <div className="shrink-0">
      <div className="mx-auto max-w-3xl px-4 pb-4 pt-2">
        <div className="rounded-[28px] border border-black/10 bg-background shadow-[0_4px_24px_rgba(15,17,21,0.08)] focus-within:border-black/20 dark:border-white/10 dark:focus-within:border-white/20">
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
            rows={2}
            placeholder={busy ? "会话运行中，可点击右侧按钮停止…" : "输入消息，Enter 发送，Shift+Enter 换行"}
            className="block w-full resize-none bg-transparent px-4 pb-1 pt-3.5 text-sm leading-6 outline-none placeholder:text-fg-caption"
          />
          <div className="flex items-center gap-1 px-3 pb-3 pt-1.5">
            <DropdownMenu>
              <DropdownMenuTrigger asChild>
                <button
                  type="button"
                  className="group flex h-7 items-center gap-1 rounded-md px-2 text-[13px] font-medium text-fg-secondary outline-none hover:bg-accent hover:text-foreground"
                >
                  {currentMode.label}
                  <ChevronDown className="size-3 text-fg-tertiary transition-transform group-data-[state=open]:rotate-180" />
                </button>
              </DropdownMenuTrigger>
              <DropdownMenuContent side="top" align="start" sideOffset={8} className="w-52">
                {APPROVAL_MODES.map((m) => (
                  <DropdownMenuItem
                    key={m.value}
                    onSelect={() => void changeApprovalMode(m.value)}
                    className="items-start gap-2.5 px-2.5 py-2"
                  >
                    <m.icon className="mt-0.5 size-4 text-fg-tertiary" />
                    <span className="flex flex-col">
                      <span className={cn("text-[13px]", approvalMode === m.value && "font-semibold")}>
                        {m.label}
                      </span>
                      <span className="text-xs text-fg-caption">{m.hint}</span>
                    </span>
                    {approvalMode === m.value && <Check className="ml-auto mt-1 size-3.5" />}
                  </DropdownMenuItem>
                ))}
              </DropdownMenuContent>
            </DropdownMenu>
            <div className="ml-auto flex items-center gap-1.5">
              {header?.usageTotal && <TokenBadge usage={header.usageTotal} />}
              <DropdownMenu onOpenChange={(open) => !open && setModelQuery("")}>
                <DropdownMenuTrigger asChild>
                  <button
                    type="button"
                    className="group flex h-7 max-w-48 items-center gap-1 rounded-md px-2 text-[13px] font-medium text-fg-secondary outline-none hover:bg-accent hover:text-foreground"
                  >
                    <span className="truncate">{(modelName ?? modelId) || "选择模型"}</span>
                    <ChevronDown className="size-3 shrink-0 text-fg-tertiary transition-transform group-data-[state=open]:rotate-180" />
                  </button>
                </DropdownMenuTrigger>
                <DropdownMenuContent side="top" align="end" sideOffset={8} className="w-64">
                  {models.length > 4 && (
                    <div className="px-1 pb-1">
                      <Input
                        autoFocus
                        value={modelQuery}
                        onChange={(e) => setModelQuery(e.target.value)}
                        onKeyDown={(e) => {
                          if (e.key !== "Escape" && e.key !== "ArrowDown" && e.key !== "ArrowUp") {
                            e.stopPropagation()
                          }
                        }}
                        placeholder="搜索模型…"
                        className="h-8 px-2.5 text-xs shadow-none"
                      />
                    </div>
                  )}
                  <DropdownMenuRadioGroup value={modelId} onValueChange={(v) => void changeModel(v)}>
                    {modelGroups.map((group) => (
                      <div key={group.provider}>
                        <DropdownMenuLabel className="px-2.5 pb-1 pt-1.5 text-xs font-normal text-fg-caption">
                          {group.provider}
                        </DropdownMenuLabel>
                        {group.items.map((m) => (
                          <DropdownMenuRadioItem
                            key={m.id}
                            value={m.id}
                            className="px-2.5 text-[13px] [&>span:first-of-type]:hidden"
                          >
                            <span className={cn("truncate", m.id === modelId && "font-semibold")}>
                              {m.display_name}
                            </span>
                            {m.id === modelId && (
                              <Check className="absolute right-2 top-1/2 size-3.5 -translate-y-1/2" />
                            )}
                          </DropdownMenuRadioItem>
                        ))}
                      </div>
                    ))}
                  </DropdownMenuRadioGroup>
                  {filteredModels.length === 0 && (
                    <div className="px-2.5 py-2 text-xs text-fg-caption">无匹配模型</div>
                  )}
                </DropdownMenuContent>
              </DropdownMenu>
              {busy && !draft.trim() ? (
                <button
                  type="button"
                  title="停止"
                  onClick={() => void stop()}
                  className="ml-1 flex size-[34px] shrink-0 items-center justify-center rounded-full bg-primary text-primary-foreground transition-colors hover:bg-primary/90"
                >
                  <Square className="size-3 fill-current" />
                </button>
              ) : (
                <button
                  type="button"
                  title="发送"
                  onClick={() => void send()}
                  disabled={!draft.trim() || sending || busy}
                  className="ml-1 flex size-[34px] shrink-0 items-center justify-center rounded-full bg-primary text-primary-foreground transition-opacity hover:bg-primary/90 disabled:opacity-40"
                >
                  <ArrowUp className="size-4" />
                </button>
              )}
            </div>
          </div>
        </div>
      </div>
    </div>
  )
}
