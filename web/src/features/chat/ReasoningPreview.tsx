import { useLayoutEffect, useRef, useState } from "react"
import { advanceReasoningPreview, emptyReasoningPreview, PREVIEW_HOLD_MS, reasoningPreviewText } from "@/lib/reasoning-preview"

export function ReasoningPreview({ text }: { text: string }) {
  const ref = useRef<HTMLSpanElement>(null)
  const state = useRef(emptyReasoningPreview())
  const [preview, setPreview] = useState("")
  const [metrics, setMetrics] = useState({ width: 0, font: "", spacing: 0, revision: 0 })
  // Measure the actual flex slot, including after font loading and viewport changes.
  useLayoutEffect(() => {
    const element = ref.current
    if (!element) return
    const update = () => {
      const style = getComputedStyle(element)
      setMetrics(previous => ({ width: element.clientWidth, font: style.font,
        spacing: parseFloat(style.letterSpacing) || 0, revision: previous.revision + 1 }))
    }
    update()
    const observer = new ResizeObserver(update)
    observer.observe(element)
    document.fonts.addEventListener("loadingdone", update)
    return () => { observer.disconnect(); document.fonts.removeEventListener("loadingdone", update) }
  }, [])
  const normalized = text.replace(/\s/g, " ")
  useLayoutEffect(() => {
    if (!metrics.width) return
    const context = document.createElement("canvas").getContext("2d")
    if (!context) return
    context.font = metrics.font
    const measure = (value: string) => context.measureText(value).width + Array.from(value).length * metrics.spacing
    let timer: number | undefined
    function update() {
      state.current = advanceReasoningPreview(state.current, normalized, performance.now(), metrics.width, measure)
      setPreview(reasoningPreviewText(state.current, normalized))
      if (state.current.fullSince !== null) {
        const due = state.current.fullSince + PREVIEW_HOLD_MS
        timer = window.setTimeout(update, Math.max(1, due - performance.now()))
      }
    }
    update()
    return () => window.clearTimeout(timer)
  }, [normalized, metrics])
  return <span ref={ref} className="reasoning-preview-window block overflow-hidden whitespace-pre text-left" title={preview}>
    {preview || "正在思考…"}
  </span>
}
