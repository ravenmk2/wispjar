import { useMemo } from "react"
import { HoverCard, HoverCardContent, HoverCardTrigger } from "@/components/ui/hover-card"
import { foldChunks } from "@/lib/conversation"
import { formatCompactNumber, formatNumber } from "@/lib/format"
import { cn } from "@/lib/utils"
import { useSessionStore } from "@/stores/session"
import type { Usage } from "@/lib/types"

interface Stats {
  rounds: number
  steps: number
  input: number
  output: number
  cacheRead: number
  cacheWrite: number
  tokens: number
  cacheHit: string | null
  cacheUnreported: boolean
  // 最近一次请求的 prompt tokens（≈当前上下文占用；input_tokens 已含缓存读）
  contextTokens: number
}

function computeStats(events: ReturnType<typeof useSessionStore.getState>["events"], liveUsage?: Usage): Stats {
  let rounds = 0
  let steps = 0
  let input = 0
  let output = 0
  let cacheRead = 0
  let cacheWrite = 0
  let hasUsage = false
  let cacheReported = false
  let contextTokens = 0
  const add = (u?: Usage) => {
    if (!u) return
    hasUsage = true
    if (u.cache_reported) cacheReported = true
    input += u.input_tokens
    output += u.output_tokens
    cacheRead += u.cache_read_tokens
    cacheWrite += u.cache_write_tokens
    contextTokens = u.input_tokens
  }
  for (const e of events) {
    if (e.type === "user/message") rounds++
    else if (e.type === "assistant/message") {
      steps++
      add(e.data.usage)
    }
  }
  add(liveUsage)
  // usage.input_tokens 即 OpenAI prompt_tokens，已含缓存命中部分；
  // 缓存读是其中的子集，合计时不得重复计入
  const tokens = input + output + cacheWrite
  const billed = input
  let cacheHit: string | null = null
  if (billed > 0 && cacheRead > 0) {
    const text = ((cacheRead / billed) * 100).toFixed(2)
    // 非全中时不显示 100.00%
    cacheHit = text === "100.00" && cacheRead < billed ? "99.99" : text
  }
  // 有 usage 记录但全部未上报缓存字段：显示「缓存命中 —」，不静默隐藏也不误报 0%
  const cacheUnreported = hasUsage && !cacheReported && cacheRead === 0
  return { rounds, steps, input, output, cacheRead, cacheWrite, tokens, cacheHit, cacheUnreported, contextTokens }
}

const R = 8
const CIRCUMFERENCE = 2 * Math.PI * R

function levelColor(ratio: number): { ring: string; bar: string; text: string } {
  if (ratio >= 0.9) return { ring: "stroke-destructive", bar: "bg-destructive", text: "text-destructive" }
  if (ratio >= 0.7) return { ring: "stroke-amber-500", bar: "bg-amber-500", text: "text-amber-600 dark:text-amber-400" }
  return { ring: "stroke-link", bar: "bg-link", text: "text-foreground" }
}

function StatRow({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex items-baseline justify-between gap-4">
      <span className="text-fg-tertiary">{label}</span>
      <span className="tabular-nums text-foreground">{value}</span>
    </div>
  )
}

export function ContextRing({ contextSize }: { contextSize?: number }) {
  const events = useSessionStore((s) => s.events)
  const liveAttempt = useSessionStore((s) => s.liveAttempt)

  const stats = useMemo(
    () => computeStats(events, liveAttempt ? foldChunks(liveAttempt.chunks).usage : undefined),
    [events, liveAttempt],
  )

  const hasLimit = !!contextSize && contextSize > 0
  const ratio = hasLimit ? Math.min(stats.contextTokens / contextSize, 1) : 0
  const pct = Math.round(ratio * 100)
  const color = levelColor(ratio)

  return (
    <HoverCard openDelay={150}>
      <HoverCardTrigger asChild>
        <button
          type="button"
          aria-label={hasLimit ? `上下文已用 ${pct}%` : "上下文用量未知"}
          className="flex size-7 items-center justify-center rounded-md text-fg-tertiary transition-colors hover:bg-accent"
        >
          <svg viewBox="0 0 20 20" className="size-[18px] -rotate-90">
            <circle cx="10" cy="10" r={R} fill="none" strokeWidth="2.5" className="stroke-context-track" />
            {ratio > 0 && (
              <circle
                cx="10"
                cy="10"
                r={R}
                fill="none"
                strokeWidth="2.5"
                strokeLinecap="round"
                strokeDasharray={CIRCUMFERENCE}
                strokeDashoffset={CIRCUMFERENCE * (1 - ratio)}
                className={cn("transition-[stroke-dashoffset] duration-300", color.ring)}
              />
            )}
          </svg>
        </button>
      </HoverCardTrigger>
      <HoverCardContent
        side="top"
        align="end"
        sideOffset={8}
        className="w-56 rounded-lg border bg-popover p-3 text-popover-foreground shadow-menu popper-blur"
      >
        <div className="flex items-baseline justify-between">
          <span className="text-xs text-fg-tertiary">上下文</span>
          <span className={cn("text-lg font-semibold tabular-nums", color.text)}>{hasLimit ? `${pct}%` : "—"}</span>
        </div>
        <div className="mt-0.5 text-xs tabular-nums text-fg-secondary">
          {hasLimit
            ? `${formatCompactNumber(stats.contextTokens)} / ${formatCompactNumber(contextSize)}`
            : "模型未配置上下文上限"}
        </div>
        {hasLimit && (
          <div className="mt-2 h-1 overflow-hidden rounded-full bg-context-track">
            <div
              className={cn("h-full rounded-full transition-[width] duration-300", color.bar)}
              style={{ width: `${Math.max(ratio * 100, 0.5)}%` }}
            />
          </div>
        )}
        <div className="my-2.5 border-t border-border/50" />
        <div className="space-y-1.5 text-xs">
          <StatRow label="聊天" value={`${stats.rounds} 轮 · ${stats.steps} 步`} />
          <StatRow label="累计 tokens" value={formatCompactNumber(stats.tokens)} />
          <StatRow
            label="缓存命中"
            value={stats.cacheHit !== null ? `${stats.cacheHit}%` : stats.cacheUnreported ? "—" : "0%"}
          />
          <StatRow label="输入 / 输出" value={`${formatNumber(stats.input)} / ${formatNumber(stats.output)}`} />
        </div>
      </HoverCardContent>
    </HoverCard>
  )
}
