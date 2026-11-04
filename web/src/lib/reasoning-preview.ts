export const PREVIEW_HOLD_MS = 200
const graphemes = new Intl.Segmenter(undefined, { granularity: "grapheme" })

// Measure complete graphemes so mixed CJK, emoji and combining marks stay intact.
export function fitPreviewLine(text: string, width: number, measure: (text: string) => number, tail = false): string {
  const parts = Array.from(graphemes.segment(text), (part) => part.segment)
  let low = 0
  let high = parts.length
  while (low < high) {
    const count = Math.ceil((low + high) / 2)
    const candidate = (tail ? parts.slice(parts.length - count) : parts.slice(0, count)).join("")
    if (measure(candidate) <= width) low = count
    else high = count - 1
  }
  return (tail ? parts.slice(parts.length - low) : parts.slice(0, low)).join("")
}

export interface ReasoningPreviewState {
  start: number
  end: number
  length: number
  fullSince: number | null
}

export function emptyReasoningPreview(): ReasoningPreviewState {
  return { start: 0, end: 0, length: 0, fullSince: null }
}

export function advanceReasoningPreview(previous: ReasoningPreviewState, text: string, now: number,
  width: number, measure: (text: string) => number): ReasoningPreviewState {
  const state = { ...(text.length < previous.length ? emptyReasoningPreview() : previous), length: text.length }
  if (width <= 0) return state
  const line = fitPreviewLine(text.slice(state.start), width, measure)
  // An oversized grapheme waits for a resize instead of spinning a timer.
  if (!line && text.length > state.start) return { ...state, end: state.start, fullSince: null }
  state.end = state.start + line.length
  if (state.end === text.length) return { ...state, fullSince: null }
  state.fullSince ??= now
  if (now - state.fullSince < PREVIEW_HOLD_MS) return state
  const rest = text.slice(state.end)
  const next = fitPreviewLine(rest, width, measure)
  // Skip excess backlog only at a line boundary, without artificially pacing incoming text.
  state.start = next.length < rest.length ? text.length - fitPreviewLine(rest, width, measure, true).length : state.end
  state.end = text.length
  state.fullSince = null

  return state
}

export function reasoningPreviewText(state: ReasoningPreviewState, text: string) {
  return text.slice(state.start, state.end)
}
