import * as Dialog from "@radix-ui/react-dialog"
import { useEffect, useRef, useState } from "react"
import { ImageLightbox } from "./ImageLightbox"
import { Button } from "./ui/button"

export function PixelImage({ src, alt }: { src: string; alt: string }) {
  const [size, setSize] = useState<{ width: number; height: number }>()
  const [ratio, setRatio] = useState(() => Math.max(1, window.devicePixelRatio || 1))
  const [open, setOpen] = useState(false)
  const image = useRef<HTMLButtonElement>(null)
  useEffect(() => {
    setSize(undefined); setOpen(false)
  }, [src])
  useEffect(() => {
    let query: MediaQueryList
    const update = () => {
      query?.removeEventListener("change", update)
      setRatio(Math.max(1, window.devicePixelRatio || 1))
      query = matchMedia(`(resolution: ${window.devicePixelRatio}dppx)`)
      query.addEventListener("change", update)
    }
    update(); window.addEventListener("resize", update)
    return () => { query.removeEventListener("change", update); window.removeEventListener("resize", update) }
  }, [])
  return <Dialog.Root open={open} onOpenChange={setOpen}>
    <div className="min-h-0 flex-1 overflow-auto bg-muted/30">
      <div className="flex min-h-full w-full items-center justify-center p-4">
        <button ref={image} type="button" aria-label={`查看大图：${alt}`} aria-haspopup="dialog" className="block shrink-0 cursor-zoom-in rounded" onClick={() => setOpen(true)}
          style={{ width: size ? size.width / ratio : undefined, maxWidth: "100%" }}>
          <img src={src} alt={alt} className="block h-auto w-full max-w-none rounded" onLoad={(event) => setSize({ width: event.currentTarget.naturalWidth, height: event.currentTarget.naturalHeight })} />
        </button>
      </div>
    </div>
    <div className="flex shrink-0 items-center gap-1 border-t px-3 py-1 text-[11px] text-muted-foreground">
      <span className="mr-auto">{size ? `${size.width} × ${size.height}` : "图片"}</span>
      <Button variant="ghost" size="sm" className="h-6 px-2 text-[11px]" onClick={() => setOpen(true)}>查看原图</Button>
    </div>
    {open && <ImageLightbox src={src} alt={alt} size={size} pixelRatio={ratio} onClose={() => setOpen(false)} onRestoreFocus={() => image.current?.focus({ preventScroll: true })} />}
  </Dialog.Root>
}
