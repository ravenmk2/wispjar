export const PREVIEW_HOLD_MS = 1800

export interface ReasoningPreviewState {
  scanned: number
  candidate: number
  boundary: boolean
  start: number
  end: number
  changedAt: number
}

export function emptyReasoningPreview(now: number): ReasoningPreviewState {
  return { scanned: 0, candidate: 0, boundary: false, start: 0, end: 0, changedAt: now }
}

// Scan only newly received characters. Timing affects this preview alone, never the stream.
export function advanceReasoningPreview(previous: ReasoningPreviewState, text: string, now: number) {
  const state = { ...(text.length < previous.scanned ? emptyReasoningPreview(now) : previous) }
  for (let i = state.scanned; i < text.length; i++) {
    const char = text[i]
    if (state.boundary && !/\s/.test(char)) {
      state.candidate = i
      state.boundary = false
    }
    if (state.candidate === state.start) state.end = i + 1
    // A period is a boundary only when followed by whitespace (not decimals/URLs).
    if (/[\n。！？!?]/.test(char) || (/\s/.test(char) && text[i - 1] === ".")) state.boundary = true
  }
  if (state.candidate !== state.start && now - state.changedAt >= PREVIEW_HOLD_MS) {
    state.start = state.candidate
    state.end = text.length
    state.changedAt = now
  }
  state.scanned = text.length
  return state
}

export function reasoningPreviewText(state: ReasoningPreviewState, text: string) {
  return text.slice(state.start, Math.min(state.end, state.start + 240)).trim()
}
