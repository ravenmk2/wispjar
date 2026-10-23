import { Check, ChevronLeft, ChevronRight } from "lucide-react"
import { useEffect, useRef, useState, type KeyboardEvent } from "react"
import { toast } from "sonner"
import { ApiError, errorMessage } from "@/api/client"
import { sessionsApi } from "@/api/sessions"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { cn } from "@/lib/utils"
import type { Question, QuestionAnswer } from "@/lib/types"
import { useSessionStore, type PendingQuestion } from "@/stores/session"
import {
  EMPTY_ANSWER,
  questionDraftKey,
  useQuestionDraftStore,
  type QuestionAnswerDraft,
} from "@/stores/questionDraft"

// 选项 label 的 "(Recommended)"/"（推荐）" 后缀解析为徽标；提交仍回传原始 label（服务端按 label 校验）
const RECOMMENDED_RE = /\s*[(（](?:Recommended|推荐)[)）]\s*$/i

function parseRecommendedLabel(label: string): { text: string; recommended: boolean } {
  const m = label.match(RECOMMENDED_RE)
  if (!m || m.index === undefined) return { text: label, recommended: false }
  return { text: label.slice(0, m.index).trimEnd(), recommended: true }
}

function isDone(a: QuestionAnswerDraft): boolean {
  return a.skipped || a.selected.length > 0 || a.custom.trim() !== ""
}

function answerText(a: QuestionAnswer | undefined): { text: string; tone: "answer" | "skip" | "pending" } {
  if (!a) return { text: "待回答", tone: "pending" }
  const parts = [...a.selected]
  if (a.custom) parts.push(a.custom)
  if (parts.length === 0) return { text: "已跳过", tone: "skip" }
  return { text: parts.join("、"), tone: "answer" }
}

/** 只读问答对（settled 工具卡 / 迟到答复节点共用） */
export function QuestionAnswerList({
  questions,
  answers,
}: {
  questions: Question[]
  answers?: QuestionAnswer[]
}) {
  return (
    <dl className="divide-y divide-divider">
      {questions.map((q) => {
        const a = answers?.find((x) => x.id === q.id)
        const { text, tone } = answerText(a)
        return (
          <div key={q.id} className="space-y-2 py-3 first:pt-0 last:pb-0">
            <dt className="flex items-start gap-2 text-chat text-fg-secondary">
              <span className="mt-0.5 flex size-6 shrink-0 items-center justify-center rounded-md bg-accent text-meta">问</span>
              <span className="min-w-0 whitespace-pre-wrap [overflow-wrap:anywhere]">{q.question}</span>
            </dt>
            <dd
              className="flex items-start gap-2 text-chat"
            >
              <span className={cn(
                "mt-0.5 flex size-6 shrink-0 items-center justify-center rounded-md text-meta",
                tone === "answer" ? "bg-primary-soft text-link" : "bg-accent text-fg-caption",
              )}>答</span>
              <span className={cn(
                "min-w-0 whitespace-pre-wrap [overflow-wrap:anywhere]",
                tone === "answer" && "font-medium text-foreground",
                tone === "skip" && "text-fg-caption",
                tone === "pending" && "text-amber-600 dark:text-amber-400",
              )}>{text}</span>
            </dd>
          </div>
        )
      })}
    </dl>
  )
}

/** composer 区域整体接管的提问卡，中性卡片配合主色勾选和提交操作。 */
export function QuestionTakeover({
  question,
  sessionId,
}: {
  question: PendingQuestion
  sessionId: string
}) {
  const key = questionDraftKey(sessionId, question.callId)
  const draft = useQuestionDraftStore((s) => s.drafts[key])
  const setIndex = useQuestionDraftStore((s) => s.setIndex)
  const setAnswer = useQuestionDraftStore((s) => s.setAnswer)
  const clearDraft = useQuestionDraftStore((s) => s.clear)
  const settlePendingQuestion = useSessionStore((s) => s.settlePendingQuestion)
  const [submitting, setSubmitting] = useState(false)
  const bodyRef = useRef<HTMLDivElement>(null)

  const questions = question.questions
  const total = questions.length
  const index = Math.min(draft?.index ?? 0, total - 1)
  const answers = draft?.answers ?? {}
  const current = questions[index]
  const currentAnswer = answers[current.id] ?? EMPTY_ANSWER
  const isLast = index === total - 1

  useEffect(() => {
    bodyRef.current?.focus()
  }, [question.callId, index])

  function answerOf(id: string): QuestionAnswerDraft {
    return answers[id] ?? EMPTY_ANSWER
  }

  function goNext() {
    if (!isLast) setIndex(key, index + 1)
  }

  function goPrevious() {
    if (index > 0) setIndex(key, index - 1)
  }

  function choose(label: string) {
    setAnswer(key, current.id, { selected: [label], custom: "", skipped: false })
    // 单选选中自动翻下一题（末题停留等提交）
    goNext()
  }

  function toggle(label: string) {
    const a = answerOf(current.id)
    const selected = a.selected.includes(label)
      ? a.selected.filter((l) => l !== label)
      : [...a.selected, label]
    setAnswer(key, current.id, { ...a, selected, skipped: false })
  }

  function setCustom(value: string) {
    const a = answerOf(current.id)
    // 单选：填写"其他"清空已选；多选：与已选并存
    setAnswer(key, current.id, {
      selected: current.multi_select ? a.selected : [],
      custom: value,
      skipped: false,
    })
  }

  function skip() {
    setAnswer(key, current.id, { selected: [], custom: "", skipped: true })
    if (isLast) void submit()
    else goNext()
  }

  async function submit() {
    if (submitting) return
    // 读 store 现值（skip 末题直连 submit 时渲染闭包里的 answers 可能是旧的）
    const latest = useQuestionDraftStore.getState().drafts[key]?.answers ?? {}
    const done = (id: string) => isDone(latest[id] ?? EMPTY_ANSWER)
    const unfinished = questions.filter((q) => !done(q.id))
    if (unfinished.length > 0) {
      setIndex(key, questions.indexOf(unfinished[0]))
      toast.error(`还有 ${unfinished.length} 题未作答`)
      return
    }
    setSubmitting(true)
    try {
      await sessionsApi.answerQuestion(
        sessionId,
        question.callId,
        questions.map((q) => {
          const a = latest[q.id] ?? EMPTY_ANSWER
          if (a.skipped) return { id: q.id, selected: [] }
          return { id: q.id, selected: a.selected, ...(a.custom.trim() ? { custom: a.custom.trim() } : {}) }
        }),
      )
      clearDraft(key)
      // open 的 question/answered、continued 的 question/answered+合成 user/message 均随事件流撤卡
    } catch (e) {
      if (e instanceof ApiError && e.code === "session/running") {
        toast.error("聊天忙，稍后重试")
      } else if (e instanceof ApiError && e.code === "question/not_pending") {
        // 可能已被他端作答：撤卡，事件流随后对账
        toast.error("该提问已被作答")
        settlePendingQuestion(question.callId)
        clearDraft(key)
      } else {
        toast.error(errorMessage(e))
      }
    } finally {
      setSubmitting(false)
    }
  }

  function onKeyDown(e: KeyboardEvent<HTMLDivElement>) {
    if (e.defaultPrevented || e.nativeEvent.isComposing || e.nativeEvent.keyCode === 229) return
    const previous = e.altKey && !e.ctrlKey && !e.metaKey && !e.shiftKey && e.key === "ArrowLeft"
    const plain = !e.altKey && !e.ctrlKey && !e.metaKey && !e.shiftKey
    if (!previous && !(plain && (e.key === "Enter" || e.key === "Escape"))) return
    // Enter 保留按钮的原生激活行为；输入框和详情空白处执行下一题／提交。
    if (e.key === "Enter" && (e.target as HTMLElement).closest("button, a[href], [role=button]")) return
    e.preventDefault()
    e.stopPropagation()
    if (submitting || e.repeat) return
    if (previous) goPrevious()
    else if (e.key === "Escape") skip()
    else if (isLast) void submit()
    else goNext()
  }

  const hasOptions = !!current.options && current.options.length > 0
  const multi = !!current.multi_select
  const customActive = currentAnswer.custom.trim() !== ""

  return (
    <div className="shrink-0">
      <div className="mx-auto max-w-chat px-4 pb-4 pt-2">
        <div
          aria-busy={submitting}
          aria-keyshortcuts="Enter Escape Alt+ArrowLeft"
          onKeyDown={onKeyDown}
          className="chat-composer overflow-hidden rounded-[24px] border bg-composer"
        >
          <div className="flex items-center gap-2 bg-accent px-4 py-2.5 text-[13px] font-medium text-fg-secondary">
            <span className="size-2 rounded-full bg-fg-tertiary" />
            等待回答
            {total > 1 && (
              <span className="ml-auto flex items-center gap-1 text-xs font-normal tabular-nums">
                <button
                  type="button"
                  aria-label="上一题"
                  title="上一题（Alt+←）"
                  aria-keyshortcuts="Alt+ArrowLeft"
                  disabled={index === 0}
                  onClick={goPrevious}
                  className="flex size-5 items-center justify-center rounded hover:bg-selected disabled:opacity-40"
                >
                  <ChevronLeft className="size-3.5" />
                </button>
                {index + 1} / {total}
                <button
                  type="button"
                  aria-label="下一题"
                  disabled={isLast}
                  onClick={goNext}
                  className="flex size-5 items-center justify-center rounded hover:bg-selected disabled:opacity-40"
                >
                  <ChevronRight className="size-3.5" />
                </button>
              </span>
            )}
          </div>
          <div
            ref={bodyRef}
            tabIndex={0}
            role="group"
            aria-label="提问详情"
            className="max-h-[336px] overflow-y-auto px-4 pt-3 outline-none"
          >
            {current.header && <div className="text-xs font-medium text-fg-caption">{current.header}</div>}
            <div className="text-chat font-medium">{current.question}</div>
            <div className="pb-1 pt-2" key={current.id}>
              {hasOptions ? (
                <div role={multi ? "group" : "radiogroup"} className="space-y-1.5">
                  {current.options!.map((opt) => {
                    const { text, recommended } = parseRecommendedLabel(opt.label)
                    const checked = currentAnswer.selected.includes(opt.label)
                    return (
                      <button
                        key={opt.label}
                        type="button"
                        role={multi ? "checkbox" : "radio"}
                        aria-checked={checked}
                        data-focus-ring="none"
                        onClick={() => (multi ? toggle(opt.label) : choose(opt.label))}
                        className={cn(
                          "flex w-full items-start gap-2.5 rounded-xl border px-3 py-2 text-left text-chat transition-colors focus-visible:border-[color:var(--composer-border-focus)] focus-visible:bg-accent",
                          checked
                            ? "border-[color:var(--composer-border-focus)] bg-selected"
                            : "border-divider hover:border-[color:var(--composer-border-focus)] hover:bg-accent",
                        )}
                      >
                        <span
                          className={cn(
                            "mt-0.5 flex size-4 shrink-0 items-center justify-center border",
                            multi ? "rounded" : "rounded-full",
                            checked
                              ? "border-primary bg-primary text-primary-foreground"
                              : "border-black/20 dark:border-white/20",
                          )}
                        >
                          {checked && <Check className="size-3" />}
                        </span>
                        <span className="min-w-0 flex-1">
                          <span className="flex items-center gap-1.5">
                            <span>{text}</span>
                            {recommended && (
                              <Badge variant="secondary" className="text-meta font-normal">
                                推荐
                              </Badge>
                            )}
                          </span>
                          {opt.description && (
                            <span className="mt-0.5 block text-chat text-fg-secondary">{opt.description}</span>
                          )}
                        </span>
                      </button>
                    )
                  })}
                  <div
                    className={cn(
                      "flex items-center gap-2.5 rounded-xl border px-3 py-2 transition-colors focus-within:border-[color:var(--composer-border-focus)]",
                      customActive ? "border-[color:var(--composer-border-focus)] bg-selected" : "border-divider",
                    )}
                  >
                    <span
                      className={cn(
                        "flex size-4 shrink-0 items-center justify-center border",
                        multi ? "rounded" : "rounded-full",
                        customActive
                          ? "border-primary bg-primary text-primary-foreground"
                          : "border-black/20 dark:border-white/20",
                      )}
                    >
                      {customActive && <Check className="size-3" />}
                    </span>
                    <input
                      aria-label="其他回答"
                      data-focus-ring="none"
                      value={currentAnswer.custom}
                      onChange={(e) => setCustom(e.target.value)}
                      placeholder="其他（填写自定义回答）"
                      className="w-full bg-transparent text-chat outline-none placeholder:text-fg-placeholder"
                    />
                  </div>
                </div>
              ) : (
                <textarea
                  value={currentAnswer.custom}
                  onChange={(e) => setCustom(e.target.value)}
                  rows={3}
                  placeholder={`输入回答，Enter ${isLast ? "提交" : "下一题"}，Shift+Enter 换行`}
                  aria-label="回答"
                  className="block w-full resize-none rounded-xl border border-divider bg-transparent px-3 py-2 text-chat outline-none placeholder:text-fg-placeholder focus:border-[color:var(--composer-border-focus)]"
                />
              )}
            </div>
          </div>
          <div className="flex flex-wrap items-center justify-end gap-2 px-4 py-3.5">
            <span className="mr-auto text-meta text-fg-caption">
              Enter {isLast ? "提交" : "下一题"} · Esc 跳过{total > 1 && " · Alt+← 上一题"}
            </span>
            <Button size="sm" variant="outline" onClick={skip} disabled={submitting} aria-keyshortcuts="Escape">
              跳过
            </Button>
            <Button size="sm" onClick={() => (isLast ? void submit() : goNext())} disabled={submitting}>
              {submitting ? "提交中…" : isLast ? "提交" : "下一题"}
            </Button>
          </div>
        </div>
      </div>
    </div>
  )
}
