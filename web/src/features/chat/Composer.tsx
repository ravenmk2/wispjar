import { ArrowUp, Check, ChevronDown, ImageOff, Loader2, Paperclip, RotateCw, Shield, ShieldCheck, ShieldOff, Square, X } from "lucide-react"
import { useEffect, useMemo, useRef, useState } from "react"
import { useNavigate } from "react-router"
import { toast } from "sonner"
import { attachmentsApi } from "@/api/attachments"
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
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { ContextRing } from "@/features/chat/ContextRing"
import { cn, uuid } from "@/lib/utils"
import { REASONING_EFFORT_OPTIONS } from "@/lib/types"
import type { AgentSummary, ApprovalMode, AttachmentRef, OrgModel } from "@/lib/types"
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

const MAX_IMAGES = 9
const MAX_IMAGE_BYTES = 8 * 1024 * 1024
const IMAGE_MIMES = new Set(["image/png", "image/jpeg", "image/webp", "image/gif"])

interface PendingImage {
  key: string
  file: File
  previewUrl: string
  status: "uploading" | "ready" | "error"
  ref?: AttachmentRef
}

function effortLabel(effort: string): string {
  return effort.charAt(0).toUpperCase() + effort.slice(1)
}

export function Composer({ sessionId, draftAgent }: { sessionId: string; draftAgent?: AgentSummary }) {
  const currentOrg = useCurrentOrg()
  const navigate = useNavigate()
  const header = useSessionStore((s) => s.header)
  const liveAttempt = useSessionStore((s) => s.liveAttempt)
  const lastSeq = useSessionStore((s) => s.lastSeq)
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
  const [effort, setEffort] = useState<string | null>(null)
  const textareaRef = useRef<HTMLTextAreaElement>(null)
  const fileInputRef = useRef<HTMLInputElement>(null)
  const [images, setImages] = useState<PendingImage[]>([])
  const [dragging, setDragging] = useState(false)
  const uploadsRef = useRef(new Map<string, Promise<AttachmentRef>>())
  const imagesRef = useRef<PendingImage[]>([])
  imagesRef.current = images

  const running = header?.running ?? false
  const busy = running || liveAttempt !== null
  const modelId = modelOverride ?? header?.modelId ?? draftAgent?.model_id ?? ""
  const effortLocked = !draftAgent && lastSeq > 0

  useEffect(() => {
    if (header?.approvalMode) setApprovalMode(header.approvalMode)
  }, [header?.approvalMode, setApprovalMode])

  useEffect(() => {
    setModelOverride(null)
  }, [header?.modelId])

  useEffect(() => {
    setEffort(null)
    if (draftAgent || !sessionId) return
    let active = true
    sessionsApi
      .detail(sessionId)
      .then((d) => {
        if (!active) return
        const v = d.model_params?.reasoning_effort
        setEffort(typeof v === "string" && v ? v : null)
      })
      .catch(() => {})
    return () => {
      active = false
    }
  }, [sessionId, draftAgent?.id])

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

  // 切换会话/卸载时丢弃未发送附件并释放本地预览
  useEffect(() => {
    return () => {
      for (const img of imagesRef.current) URL.revokeObjectURL(img.previewUrl)
      imagesRef.current = []
      uploadsRef.current.clear()
      setImages([])
    }
  }, [sessionId])

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

  const currentModel = models.find((m) => m.id === modelId)
  const modelEfforts = REASONING_EFFORT_OPTIONS.filter((e) => currentModel?.capabilities.reasoning_efforts?.includes(e))
  const modelEffortDefault = currentModel?.capabilities.reasoning_effort_default ?? ""
  // 草稿未显式选档时展示模型默认值；正式会话以服务端保存值为准。
  const selectedEffort = effort ?? (draftAgent ? modelEffortDefault || null : null)
  const supportsEffort = modelEfforts.length > 0
  const visionSupported = currentModel?.capabilities.vision ?? true

  function startUpload(item: PendingImage) {
    // 草稿态会话尚未创建，附件延迟到首条消息发送时（建会话后）再上传
    if (draftAgent) {
      setImages((prev) => prev.map((i) => (i.key === item.key ? { ...i, status: "ready" } : i)))
      return
    }
    const promise = attachmentsApi.upload(sessionId, item.file)
    uploadsRef.current.set(item.key, promise)
    promise.then(
      (ref) => {
        setImages((prev) => prev.map((i) => (i.key === item.key ? { ...i, status: "ready", ref } : i)))
      },
      () => {
        setImages((prev) => prev.map((i) => (i.key === item.key ? { ...i, status: "error" } : i)))
      },
    )
  }

  function addFiles(files: File[]) {
    if (files.length === 0) return
    if (!visionSupported) {
      toast.error("当前模型不支持图片")
      return
    }
    const accepted: PendingImage[] = []
    for (const file of files) {
      if (!IMAGE_MIMES.has(file.type)) {
        toast.error(`「${file.name}」格式不支持，仅支持 PNG/JPEG/WebP/GIF`)
        continue
      }
      if (file.size > MAX_IMAGE_BYTES) {
        toast.error(`「${file.name}」超过 8MB 大小限制`)
        continue
      }
      accepted.push({ key: uuid(), file, previewUrl: URL.createObjectURL(file), status: "uploading" })
    }
    const capacity = MAX_IMAGES - imagesRef.current.length
    if (accepted.length > capacity) {
      toast.error(`最多添加 ${MAX_IMAGES} 张图片`)
      for (const extra of accepted.splice(capacity)) URL.revokeObjectURL(extra.previewUrl)
    }
    if (accepted.length === 0) return
    setImages((prev) => [...prev, ...accepted])
    for (const item of accepted) startUpload(item)
  }

  function removeImage(key: string) {
    uploadsRef.current.delete(key)
    setImages((prev) => {
      const target = prev.find((i) => i.key === key)
      if (target) URL.revokeObjectURL(target.previewUrl)
      return prev.filter((i) => i.key !== key)
    })
  }

  function retryImage(key: string) {
    const item = imagesRef.current.find((i) => i.key === key)
    if (!item || item.status !== "error") return
    setImages((prev) => prev.map((i) => (i.key === key ? { ...i, status: "uploading" } : i)))
    startUpload(item)
  }

  async function send() {
    const content = draft.trim()
    if (sending || busy || (!draftAgent && !sessionId)) return
    const pending = imagesRef.current
    if (!content && pending.length === 0) return
    if (pending.some((i) => i.status === "error")) {
      toast.error("有图片上传失败，请重试或移除后再发送")
      return
    }
    setSending(true)
    try {
      let targetSessionId = sessionId
      let refs: AttachmentRef[]
      if (draftAgent) {
        // 惰性建会话：首条消息先创建会话，再补传附件、发 prompt，最后切换到正式会话页；
        // 任一步失败都停留在草稿页，输入内容与附件保留
        if (!currentOrg) throw new Error("当前机构不可用")
        const session = await sessionsApi.create(currentOrg.id, {
          agent_id: draftAgent.id,
          ...(effort ? { reasoning_effort: effort } : {}),
        })
        targetSessionId = session.id
        refs = await Promise.all(
          pending.map((i) => i.ref ?? attachmentsApi.upload(targetSessionId, i.file)),
        )
      } else {
        // 等待在途上传全部就绪；任一失败则整条不发送（草稿与附件保留）
        refs = await Promise.all(
          pending.map((i) => {
            if (i.status === "ready" && i.ref) return Promise.resolve(i.ref)
            const p = uploadsRef.current.get(i.key)
            return p ?? Promise.reject(new Error("图片上传未完成"))
          }),
        )
      }
      const requestId = uuid()
      setOptimistic({
        requestId,
        content,
        attachments: pending.length
          ? pending.map((i) => ({ url: i.previewUrl, name: i.file.name }))
          : undefined,
      })
      await sessionsApi.prompt(targetSessionId, {
        content,
        request_id: requestId,
        ...(refs.length ? { attachments: refs.map((r) => r.id) } : {}),
      })
      setDraft("")
      setImages([])
      uploadsRef.current.clear()
      if (draftAgent) navigate(`/c/${targetSessionId}`, { replace: true })
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
    if (draftAgent || !sessionId || mode === approvalMode) return
    setApprovalMode(mode)
    try {
      await sessionsApi.update(sessionId, { approval_mode: mode })
    } catch (e) {
      toast.error(errorMessage(e))
      if (header?.approvalMode) setApprovalMode(header.approvalMode)
    }
  }

  async function changeModel(next: string) {
    if (draftAgent || !sessionId || !next || next === modelId) return
    const prevEffort = effort
    const nextModel = models.find((m) => m.id === next)
    const nextDefault = nextModel?.capabilities.reasoning_effort_default ?? ""
    setModelOverride(next)
    if (!effortLocked) setEffort(nextDefault || null)
    try {
      // 未锁定换模型时重置为新模型默认档；空串清除之前的会话档位覆盖。
      const res = await sessionsApi.update(sessionId, {
        model_id: next,
        ...(!effortLocked ? { reasoning_effort: nextDefault } : {}),
      })
      const v = res.model_params?.reasoning_effort
      if (!effortLocked) setEffort(typeof v === "string" && v ? v : null)
    } catch (e) {
      toast.error(errorMessage(e))
      setModelOverride(null)
      setEffort(prevEffort)
    }
  }

  async function changeEffort(next: string) {
    if (
      sending || effortLocked || (next !== "" && !modelEfforts.includes(next)) ||
      next === (selectedEffort ?? "") || (!draftAgent && !sessionId)
    ) return
    const prev = effort
    setEffort(next || null)
    // 新对话尚无会话 ID，选项留在本地，首条消息创建会话时一起提交。
    if (draftAgent) return
    try {
      const res = await sessionsApi.update(sessionId, { reasoning_effort: next })
      const value = res.model_params?.reasoning_effort
      setEffort(typeof value === "string" && value ? value : null)
    } catch (e) {
      toast.error(errorMessage(e))
      setEffort(prev)
    }
  }

  const modelName = currentModel?.display_name
  const currentMode = APPROVAL_MODES.find((m) => m.value === approvalMode) ?? APPROVAL_MODES[2]

  return (
    <div className="shrink-0">
      <div className="mx-auto max-w-3xl px-4 pb-4 pt-2">
        <div
          onDragOver={(e) => {
            if (e.dataTransfer.types.includes("Files")) {
              e.preventDefault()
              setDragging(true)
            }
          }}
          onDragLeave={(e) => {
            if (!e.currentTarget.contains(e.relatedTarget as Node)) setDragging(false)
          }}
          onDrop={(e) => {
            if (!e.dataTransfer.files.length) return
            e.preventDefault()
            setDragging(false)
            addFiles([...e.dataTransfer.files])
          }}
          className={cn(
            "rounded-[28px] border bg-card shadow-input focus-within:border-black/25 dark:focus-within:border-white/25",
            dragging && "border-primary ring-2 ring-primary/30",
          )}
        >
          {images.length > 0 && (
            <div className="flex flex-wrap gap-2 px-4 pt-3">
              {images.map((img) => (
                <div
                  key={img.key}
                  title={img.file.name}
                  className="group relative size-14 overflow-hidden rounded-lg bg-accent"
                >
                  <img src={img.previewUrl} alt={img.file.name} className="size-full object-cover" />
                  {img.status === "uploading" && (
                    <span className="absolute inset-0 flex items-center justify-center bg-black/30">
                      <Loader2 className="size-4 animate-spin text-white" />
                    </span>
                  )}
                  {img.status === "error" && (
                    <button
                      type="button"
                      title="上传失败，点击重试"
                      onClick={() => retryImage(img.key)}
                      className="absolute inset-0 flex flex-col items-center justify-center gap-0.5 bg-black/50 text-white"
                    >
                      <ImageOff className="size-3.5" />
                      <RotateCw className="size-3" />
                    </button>
                  )}
                  <button
                    type="button"
                    title="移除"
                    onClick={() => removeImage(img.key)}
                    className="absolute right-0.5 top-0.5 flex size-4 items-center justify-center rounded-full bg-black/60 text-white opacity-0 transition-opacity hover:bg-black/80 group-hover:opacity-100"
                  >
                    <X className="size-3" />
                  </button>
                </div>
              ))}
            </div>
          )}
          <textarea
            ref={textareaRef}
            value={draft}
            onChange={(e) => setDraft(e.target.value)}
            onPaste={(e) => {
              const files = [...e.clipboardData.files]
              if (files.length > 0) {
                e.preventDefault()
                addFiles(files)
              }
            }}
            onKeyDown={(e) => {
              if (e.key === "Enter" && !e.shiftKey && !e.nativeEvent.isComposing) {
                e.preventDefault()
                void send()
              }
            }}
            rows={2}
            placeholder={busy ? "会话运行中，可点击右侧按钮停止…" : "输入消息，Enter 发送，Shift+Enter 换行"}
            className="block w-full resize-none bg-transparent px-4 pb-1 pt-3.5 text-[15px] leading-6 outline-none placeholder:text-fg-caption"
          />
          <input
            ref={fileInputRef}
            type="file"
            accept="image/png,image/jpeg,image/webp,image/gif"
            multiple
            className="hidden"
            onChange={(e) => {
              addFiles([...(e.target.files ?? [])])
              e.target.value = ""
            }}
          />
          <div className="flex items-center gap-1 px-3 pb-3 pt-1.5">
            <Tooltip>
              <TooltipTrigger asChild>
                <span className="flex">
                  <button
                    type="button"
                    disabled={!visionSupported}
                    onClick={() => fileInputRef.current?.click()}
                    className="flex size-7 items-center justify-center rounded-md text-fg-tertiary transition-colors hover:bg-accent hover:text-foreground disabled:opacity-40 disabled:hover:bg-transparent disabled:hover:text-fg-tertiary"
                  >
                    <Paperclip className="size-4" />
                  </button>
                </span>
              </TooltipTrigger>
              <TooltipContent>{visionSupported ? "添加图片" : "当前模型不支持图片"}</TooltipContent>
            </Tooltip>
            <DropdownMenu>
              <DropdownMenuTrigger asChild>
                <button
                  type="button"
                  disabled={!!draftAgent}
                  title={draftAgent ? "发送首条消息后可调整审批模式" : undefined}
                  className="group flex h-7 items-center gap-1 rounded-md px-2 text-[13px] font-medium text-fg-secondary outline-none hover:bg-accent hover:text-foreground disabled:opacity-40 disabled:hover:bg-transparent disabled:hover:text-fg-secondary"
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
            <div className="ml-auto flex min-w-0 items-center gap-1.5">
              <ContextRing contextSize={currentModel?.context_size} />
              <DropdownMenu onOpenChange={(open) => {
                if (!open) setModelQuery("")
              }}>
                <DropdownMenuTrigger asChild>
                  <button
                    type="button"
                    aria-label="选择模型"
                    disabled={sending || !!draftAgent || !sessionId}
                    title={draftAgent ? "发送首条消息后可切换模型" : undefined}
                    className="group flex h-7 min-w-0 max-w-48 items-center gap-1 rounded-md px-2 text-[13px] font-medium text-fg-secondary outline-none hover:bg-accent hover:text-foreground disabled:opacity-40 disabled:hover:bg-transparent disabled:hover:text-fg-secondary"
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
              <DropdownMenu>
                <DropdownMenuTrigger asChild>
                  <button
                    type="button"
                    aria-label="选择推理等级"
                    disabled={sending || effortLocked || !supportsEffort || (!draftAgent && !sessionId)}
                    title={effortLocked ? "对话开始后不可更改思考强度" : !supportsEffort ? "当前模型不支持选择推理等级" : "推理等级"}
                    className="group flex h-7 shrink-0 items-center gap-1 rounded-md px-2 text-[13px] font-medium text-fg-secondary outline-none hover:bg-accent hover:text-foreground disabled:opacity-40 disabled:hover:bg-transparent disabled:hover:text-fg-secondary"
                  >
                    <span>{selectedEffort ? effortLabel(selectedEffort) : supportsEffort ? "默认" : "推理等级"}</span>
                    <ChevronDown className="size-3 text-fg-tertiary transition-transform group-data-[state=open]:rotate-180" />
                  </button>
                </DropdownMenuTrigger>
                <DropdownMenuContent side="top" align="end" sideOffset={8} className="w-40">
                  <DropdownMenuLabel className="px-2.5 text-xs font-normal text-fg-caption">推理等级</DropdownMenuLabel>
                  <DropdownMenuRadioGroup value={selectedEffort ?? ""} onValueChange={(v) => void changeEffort(v)}>
                    {!modelEffortDefault && (
                      <DropdownMenuRadioItem
                        value=""
                        disabled={sending || effortLocked}
                        title={effortLocked ? "对话开始后不可更改思考强度" : undefined}
                        className="px-2.5 text-[13px] [&>span:first-of-type]:hidden"
                      >
                        <span className={cn("truncate", selectedEffort === null && "font-semibold")}>默认</span>
                        {selectedEffort === null && (
                          <Check className="absolute right-2 top-1/2 size-3.5 -translate-y-1/2" />
                        )}
                      </DropdownMenuRadioItem>
                    )}
                    {modelEfforts.map((e) => (
                      <DropdownMenuRadioItem
                        key={e}
                        value={e}
                        disabled={sending || effortLocked}
                        title={effortLocked ? "对话开始后不可更改思考强度" : undefined}
                        className="px-2.5 text-[13px] [&>span:first-of-type]:hidden"
                      >
                        <span className={cn("truncate", e === selectedEffort && "font-semibold")}>{effortLabel(e)}</span>
                        {e === selectedEffort && (
                          <Check className="absolute right-2 top-1/2 size-3.5 -translate-y-1/2" />
                        )}
                      </DropdownMenuRadioItem>
                    ))}
                  </DropdownMenuRadioGroup>
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
                  disabled={(!draft.trim() && images.length === 0) || sending || busy}
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
