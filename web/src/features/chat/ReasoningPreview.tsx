import { useEffect, useRef, useState } from "react"
import { advanceReasoningPreview, emptyReasoningPreview, PREVIEW_HOLD_MS, reasoningPreviewText } from "@/lib/reasoning-preview"

// Mounted only for a collapsed, live preview; unmounting cancels pending updates.
export function ReasoningPreview({ text }: { text: string }) {
  const state = useRef(emptyReasoningPreview(performance.now()))
  const [preview, setPreview] = useState({ text: "", start: 0, outgoing: "" })
  useEffect(() => {
    function update() {
      state.current = advanceReasoningPreview(state.current, text, performance.now())
      const next = { text: reasoningPreviewText(state.current, text), start: state.current.start }
      setPreview(previous => previous.text === next.text && previous.start === next.start ? previous : {
        ...next,
        outgoing: previous.start !== next.start ? previous.text : previous.outgoing,
      })
    }
    update()
    if (state.current.candidate === state.current.start) return
    const timer = window.setTimeout(update, Math.max(0, PREVIEW_HOLD_MS - (performance.now() - state.current.changedAt)))
    return () => window.clearTimeout(timer)
  }, [text])
  // Only segment changes restart the two-line roll. Token appends keep the same DOM.
  // At most one outgoing line is retained; stream updates never await an animation.
  return <span key={preview.start} className="reasoning-preview-window block overflow-hidden text-left" title={preview.text}>
    {preview.outgoing && <span aria-hidden="true" className="reasoning-preview-outgoing block truncate">{preview.outgoing}</span>}
    <span className={`block truncate${preview.outgoing ? " reasoning-preview-incoming" : ""}`}>{preview.text || "正在思考…"}</span>
  </span>
}
