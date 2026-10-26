export const PREVIEW_HOLD_MS = 500
const PREVIEW_SEGMENT_LENGTH = 48
const PREVIEW_HARD_LIMIT = 72

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
    // Long sentences must keep advancing too. Prefer word/CJK boundaries;
    // cap unbroken tokens without splitting UTF-16 surrogate pairs.
    const length = i - state.candidate
    if (!/\s/.test(char) && length >= PREVIEW_SEGMENT_LENGTH &&
      (/\s/.test(text[i - 1]) || /[\u3000-\u9fff]/.test(char) ||
        (length >= PREVIEW_HARD_LIMIT && !/[\uDC00-\uDFFF]/.test(char)))) {
      state.candidate = i
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
