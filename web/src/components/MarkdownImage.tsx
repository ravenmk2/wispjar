import * as Dialog from "@radix-ui/react-dialog"
import { useEffect, useRef, useState, type ComponentProps } from "react"
import { ImageLightbox } from "@/components/ImageLightbox"
import { cn } from "@/lib/utils"

function displayPixelRatio() {
  return typeof window === "undefined" ? 1 : Math.max(1, window.devicePixelRatio || 1)
}

export function MarkdownImage({ className, alt, src, onLoad, style, ...props }: ComponentProps<"img">) {
  const [open, setOpen] = useState(false)
  const [loaded, setLoaded] = useState<{ src?: string; width: number; height: number }>()
  const [pixelRatio, setPixelRatio] = useState(displayPixelRatio)
  const imageRef = useRef<HTMLImageElement>(null)
  const label = alt || "图片"
  const size = loaded?.src === src ? loaded : undefined
  const naturalWidth = size ? size.width / pixelRatio : undefined
  const naturalHeight = size ? size.height / pixelRatio : undefined

  useEffect(() => {
    let query: MediaQueryList
    // DPI changes when moving between displays or changing browser zoom.
    const update = () => {
      query?.removeEventListener("change", update)
      setPixelRatio(displayPixelRatio())
      query = window.matchMedia(`(resolution: ${window.devicePixelRatio}dppx)`)
      query.addEventListener("change", update)
    }
    update()
    window.addEventListener("resize", update)
    return () => {
      query.removeEventListener("change", update)
      window.removeEventListener("resize", update)
    }
  }, [])

  return (
    <Dialog.Root open={open} onOpenChange={setOpen}>
      <img
        {...props}
        ref={imageRef}
        src={src}
        alt={alt}
        role="button"
        tabIndex={0}
        aria-label={`查看大图：${label}`}
        aria-haspopup="dialog"
        className={cn("markdown-image cursor-zoom-in rounded focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring", className)}
        style={{ ...style, maxWidth: `min(100%, ${Math.min(naturalWidth ?? 720, 720)}px)`, maxHeight: `min(${Math.min(naturalHeight ?? 480, 480)}px, 60vh)` }}
        onLoad={(event) => {
          const image = event.currentTarget
          setLoaded({ src, width: image.naturalWidth, height: image.naturalHeight })
          onLoad?.(event)
        }}
        onClick={(event) => {
          event.preventDefault()
          event.stopPropagation()
          if (src) setOpen(true)
        }}
        onKeyDown={(event) => {
          if (event.key === "Enter" || event.key === " ") {
            event.preventDefault()
            event.stopPropagation()
            if (src) setOpen(true)
          }
        }}
      />
      {open && src && <ImageLightbox src={src} alt={label} size={size} pixelRatio={pixelRatio}
        onClose={() => setOpen(false)} onRestoreFocus={() => imageRef.current?.focus({ preventScroll: true })} />}
    </Dialog.Root>
  )
}
