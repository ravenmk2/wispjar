import { useMemo } from "react"
import { foldChunks } from "@/lib/conversation"
import { formatCompactNumber } from "@/lib/format"
import { useSessionStore } from "@/stores/session"
import type { Usage } from "@/lib/types"

interface Stats {
  rounds: number
  steps: number
  tokens: number
  cacheHit: number | null
}

function computeStats(events: ReturnType<typeof useSessionStore.getState>["events"], liveUsage?: Usage): Stats {
  let rounds = 0
  let steps = 0
  let input = 0
  let output = 0
  let cacheRead = 0
  let cacheWrite = 0
  const add = (u?: Usage) => {
    if (!u) return
    input += u.input_tokens
    output += u.output_tokens
    cacheRead += u.cache_read_tokens
    cacheWrite += u.cache_write_tokens
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
  let cacheHit: number | null = null
  if (billed > 0 && cacheRead > 0) {
    const hit = Math.round((cacheRead / billed) * 100)
    // 非全中时不显示 100%
    cacheHit = hit >= 100 && cacheRead < billed ? 99 : hit
  }
  return { rounds, steps, tokens, cacheHit }
}

export function StatsPills() {
  const events = useSessionStore((s) => s.events)
  const liveAttempt = useSessionStore((s) => s.liveAttempt)

  const stats = useMemo(
    () => computeStats(events, liveAttempt ? foldChunks(liveAttempt.chunks).usage : undefined),
    [events, liveAttempt],
  )

  if (stats.rounds === 0 && stats.tokens === 0) return null

  return (
    <div className="flex shrink-0 items-center justify-center gap-3 pb-2.5 text-[13px] text-fg-tertiary">
      <span className="rounded-full bg-muted/70 px-2.5 py-0.5">
        {stats.rounds} 轮 · {stats.steps} 步
      </span>
      <span className="rounded-full bg-muted/70 px-2.5 py-0.5">
        {formatCompactNumber(stats.tokens)} tok
        {stats.cacheHit !== null && ` · 缓存命中 ${stats.cacheHit}%`}
      </span>
    </div>
  )
}
