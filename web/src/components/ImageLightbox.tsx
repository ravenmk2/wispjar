import * as Dialog from "@radix-ui/react-dialog"
import { Maximize, Minus, Plus, X } from "lucide-react"
import { useEffect, useLayoutEffect, useRef, useState } from "react"
import { clampImageView, fitImageScale, MAX_IMAGE_SCALE, zoomImageView, type ImageSize, type ImageView } from "@/lib/image-view"
import { cn } from "@/lib/utils"

export function ImageLightbox({ src, alt, size, pixelRatio, onClose, onRestoreFocus }: {
  src: string; alt: string; size?: ImageSize; pixelRatio: number
  onClose: () => void; onRestoreFocus: () => void
}) {
  const [loaded, setLoaded] = useState<ImageSize>()
  const original = size ?? loaded
  const image = { width: (original?.width ?? 1) / pixelRatio, height: (original?.height ?? 1) / pixelRatio }
  const [viewport, setViewport] = useState<ImageSize>({ width: 0, height: 0 })
  const [view, setView] = useState<ImageView>({ scale: 1, x: 0, y: 0 })
  const [dragging, setDragging] = useState(false)
  const [stage, setStage] = useState<HTMLDivElement | null>(null)
  const fitLocked = useRef(true)
  const suppressClick = useRef(false)
  const drag = useRef<{ pointer: number; startX: number; startY: number; x: number; y: number; target: HTMLElement }>()
  const fit = fitImageScale(image, viewport)
  const minScale = Math.min(0.1, fit)
  const canPan = image.width * view.scale > viewport.width || image.height * view.scale > viewport.height

  useLayoutEffect(() => {
    if (!stage) return
    const measure = () => {
      const next = { width: stage.clientWidth, height: stage.clientHeight }
      setViewport(next)
      setView((current) => fitLocked.current
        ? { scale: fitImageScale(image, next), x: 0, y: 0 }
        : clampImageView(current, image, next))
    }
    measure()
    const observer = new ResizeObserver(measure)
    observer.observe(stage)
    return () => observer.disconnect()
  }, [stage, image.width, image.height])

  useEffect(() => {
    if (!stage) return
    const wheel = (event: WheelEvent) => {
      event.preventDefault()
      event.stopPropagation()
      const rect = stage.getBoundingClientRect()
      const units = event.deltaMode === 1 ? 16 : event.deltaMode === 2 ? viewport.height : 1
      const delta = Math.max(-240, Math.min(240, event.deltaY * units))
      fitLocked.current = false
      setView((current) => zoomImageView(current, current.scale * Math.exp(-delta * 0.002), image, viewport,
        { x: event.clientX - rect.left - rect.width / 2, y: event.clientY - rect.top - rect.height / 2 }))
    }
    // A passive React wheel listener cannot prevent the underlying page from scrolling.
    stage.addEventListener("wheel", wheel, { passive: false })
    return () => stage.removeEventListener("wheel", wheel)
  }, [stage, image.width, image.height, viewport.width, viewport.height])

  function zoom(factor: number) {
    fitLocked.current = false
    setView((current) => zoomImageView(current, current.scale * factor, image, viewport))
  }
  function reset(fitWindow: boolean) {
    fitLocked.current = fitWindow
    setView(clampImageView({ scale: fitWindow ? fit : 1, x: 0, y: 0 }, image, viewport))
  }
  function endDrag() {
    const current = drag.current
    drag.current = undefined
    setDragging(false)
    if (current?.target.hasPointerCapture(current.pointer)) current.target.releasePointerCapture(current.pointer)
  }
  const control = "flex size-9 shrink-0 items-center justify-center rounded-full hover:bg-white/15 focus-visible:outline focus-visible:outline-2 focus-visible:outline-white disabled:opacity-30"

  return <Dialog.Portal>
    <Dialog.Overlay className="fixed inset-0 z-[100] bg-black/80" />
    <Dialog.Content aria-describedby={undefined}
      className="fixed inset-4 z-[101] outline-none sm:inset-8"
      onClick={(event) => { if (event.target === event.currentTarget) onClose() }}
      onCloseAutoFocus={(event) => { event.preventDefault(); onRestoreFocus() }}
      onKeyDown={(event) => {
        if (event.ctrlKey || event.metaKey || event.altKey) return
        if (["+", "=", "-", "0", "f", "F"].includes(event.key)) {
          event.preventDefault()
          if (event.key === "+" || event.key === "=") zoom(1.25)
          else if (event.key === "-") zoom(0.8)
          else reset(event.key !== "0")
        }
      }}>
      <Dialog.Title className="sr-only">{alt}</Dialog.Title>
      <div ref={setStage} className="absolute inset-0 bottom-14 select-none overflow-hidden"
        onClickCapture={(event) => {
          if (suppressClick.current) { event.preventDefault(); event.stopPropagation(); suppressClick.current = false }
        }}
        onClick={(event) => { if (event.target === event.currentTarget) onClose() }}
        onPointerDown={(event) => {
          suppressClick.current = false
          if (event.button !== 0 || !canPan) return
          event.preventDefault()
          const target = event.target as HTMLElement
          target.setPointerCapture(event.pointerId)
          drag.current = { pointer: event.pointerId, startX: event.clientX, startY: event.clientY, x: view.x, y: view.y, target }
        }}
        onPointerMove={(event) => {
          const current = drag.current
          if (!current || current.pointer !== event.pointerId) return
          const dx = event.clientX - current.startX, dy = event.clientY - current.startY
          if (Math.hypot(dx, dy) <= 3 && !suppressClick.current) return
          suppressClick.current = true
          setDragging(true)
          setView((previous) => clampImageView({ ...previous, x: current.x + dx, y: current.y + dy }, image, viewport))
        }}
        onPointerUp={endDrag} onPointerCancel={endDrag} onLostPointerCapture={endDrag}>
        <div className="absolute left-1/2 top-1/2" style={{ width: image.width, height: image.height,
          visibility: original ? undefined : "hidden",
          transform: `translate(-50%, -50%) translate(${view.x}px, ${view.y}px) scale(${view.scale})` }}>
          <img src={src} alt={alt} draggable={false}
            onLoad={(event) => setLoaded({ width: event.currentTarget.naturalWidth, height: event.currentTarget.naturalHeight })}
            className={cn("block size-full max-w-none rounded object-contain", canPan ? dragging ? "cursor-grabbing" : "cursor-grab" : "cursor-default")} />
        </div>
      </div>
      <div role="group" aria-label="图片缩放工具" className="absolute bottom-0 left-1/2 flex -translate-x-1/2 items-center gap-1 rounded-full border border-white/15 bg-black/70 px-2 py-1 text-white">
        <button type="button" aria-label="缩小图片" title="缩小 (-)" className={control} disabled={view.scale <= minScale} onClick={() => zoom(0.8)}><Minus className="size-4" /></button>
        <span aria-label="缩放比例" className="w-14 text-center text-sm tabular-nums">{Math.round(view.scale * 100)}%</span>
        <button type="button" aria-label="放大图片" title="放大 (+)" className={control} disabled={view.scale >= MAX_IMAGE_SCALE} onClick={() => zoom(1.25)}><Plus className="size-4" /></button>
        <button type="button" aria-label="原图 1:1" title="原图 1:1 (0)" className={cn(control, "w-11 text-sm")} onClick={() => reset(false)}>1:1</button>
        <button type="button" aria-label="适应窗口" title="适应窗口 (F)" className={control} onClick={() => reset(true)}><Maximize className="size-4" /></button>
      </div>
      <Dialog.Close aria-label="关闭图片预览" className="absolute right-0 top-0 rounded-full bg-black/50 p-2 text-white hover:bg-black/70 focus-visible:outline focus-visible:outline-2 focus-visible:outline-white"><X className="size-5" /></Dialog.Close>
    </Dialog.Content>
  </Dialog.Portal>
}
