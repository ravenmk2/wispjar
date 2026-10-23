import { useEffect, useRef, useState, type CSSProperties, type ReactNode } from "react"
import { cn } from "@/lib/utils"

const WIDTH_KEY = "wispjar-sandbox-panel-w"
const DEFAULT_WIDTH = 420

// Both current workspaces and legacy sandboxes share the same panel preference.
export function FilePanel({ children, embedded = false }: { children: ReactNode; embedded?: boolean }) {
  const panel = useRef<HTMLElement>(null)
  const [available, setAvailable] = useState(window.innerWidth)
  const [width, setWidth] = useState(() => {
    const stored = Number(localStorage.getItem(WIDTH_KEY))
    return Number.isFinite(stored) && stored > 0 ? stored : DEFAULT_WIDTH
  })
  const [dragging, setDragging] = useState(false)
  const drag = useRef<{ x: number; width: number } | null>(null)
  const max = Math.max(1, Math.round(available * 0.6))
  const min = Math.min(320, max)
  const clamp = (value: number) => Math.min(max, Math.max(min, value))
  const actual = clamp(width)

  useEffect(() => {
    const parent = panel.current?.parentElement
    if (!parent) return
    const observer = new ResizeObserver(() => setAvailable(parent.clientWidth))
    observer.observe(parent)
    setAvailable(parent.clientWidth)
    return () => observer.disconnect()
  }, [embedded])

  if (embedded) return <section aria-label="存储文件" className="flex h-[calc(100dvh-260px)] min-h-[360px] flex-col overflow-hidden rounded-xl border bg-background">{children}</section>

  function save(value: number) {
    const next = clamp(value)
    setWidth(next)
    localStorage.setItem(WIDTH_KEY, String(next))
  }

  return <aside ref={panel} aria-label="会话文件" className={cn(
    "absolute inset-y-0 right-0 z-20 flex w-[420px] max-w-[90vw] shrink-0 flex-col border-l bg-background shadow-lg lg:relative lg:inset-auto lg:z-auto lg:w-[var(--file-panel-width)] lg:max-w-none lg:shadow-none",
    dragging && "select-none [&_iframe]:pointer-events-none",
  )} style={{ "--file-panel-width": `${actual}px` } as CSSProperties}>
    <div role="separator" aria-label="调整文件面板宽度" aria-orientation="vertical" aria-valuemin={min} aria-valuemax={max} aria-valuenow={actual} tabIndex={0}
      className="absolute inset-y-0 -left-1 z-30 hidden w-2 touch-none cursor-col-resize rounded transition-colors hover:bg-primary/15 focus-visible:bg-primary/20 focus-visible:outline-none lg:block"
      title="拖拽调整宽度，双击恢复默认宽度"
      onDoubleClick={() => save(DEFAULT_WIDTH)}
      onKeyDown={(event) => {
        const next = event.key === "ArrowLeft" ? actual + 20 : event.key === "ArrowRight" ? actual - 20 : event.key === "Home" ? min : event.key === "End" ? max : null
        if (next === null) return
        event.preventDefault()
        save(next)
      }}
      onPointerDown={(event) => {
        if (event.button !== 0) return
        event.preventDefault()
        drag.current = { x: event.clientX, width: actual }
        event.currentTarget.setPointerCapture(event.pointerId)
        setDragging(true)
      }}
      onPointerMove={(event) => {
        if (drag.current) setWidth(clamp(drag.current.width + drag.current.x - event.clientX))
      }}
      onPointerUp={(event) => {
        if (drag.current) save(drag.current.width + drag.current.x - event.clientX)
        drag.current = null
        setDragging(false)
        if (event.currentTarget.hasPointerCapture(event.pointerId)) event.currentTarget.releasePointerCapture(event.pointerId)
      }}
      onLostPointerCapture={() => { drag.current = null; setDragging(false) }}
      onPointerCancel={() => { drag.current = null; setDragging(false) }}
    />
    {children}
  </aside>
}
