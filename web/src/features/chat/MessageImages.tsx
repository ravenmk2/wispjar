import { ImageOff, Loader2 } from "lucide-react"
import { useEffect, useState } from "react"
import { attachmentsApi } from "@/api/attachments"
import { cn } from "@/lib/utils"
import type { AttachmentRef } from "@/lib/types"

const SINGLE_MAX_EDGE = 240
const TILE_SIZE = 64

// 单图长边 240px，宽高比钳制在 [0.25, 4]
function fitSize(a: AttachmentRef): { width: number; height: number } | null {
  if (!a.width || !a.height) return null
  const ratio = Math.min(4, Math.max(0.25, a.width / a.height))
  return ratio >= 1
    ? { width: SINGLE_MAX_EDGE, height: Math.round(SINGLE_MAX_EDGE / ratio) }
    : { width: Math.round(SINGLE_MAX_EDGE * ratio), height: SINGLE_MAX_EDGE }
}

function useAttachmentURL(attachmentId: string): { url: string | null; failed: boolean; retry: () => void } {
  const [url, setUrl] = useState<string | null>(null)
  const [failed, setFailed] = useState(false)
  const [attempt, setAttempt] = useState(0)
  useEffect(() => {
    let alive = true
    setFailed(false)
    attachmentsApi.getObjectURL(attachmentId).then(
      (u) => {
        if (alive) setUrl(u)
      },
      () => {
        if (alive) setFailed(true)
      },
    )
    return () => {
      alive = false
    }
  }, [attachmentId, attempt])
  return { url, failed, retry: () => setAttempt((n) => n + 1) }
}

function AttachmentImage({
  attachment,
  single,
  onOpen,
}: {
  attachment: AttachmentRef
  single: boolean
  onOpen: () => void
}) {
  const { url, failed, retry } = useAttachmentURL(attachment.id)
  const size = single ? fitSize(attachment) : null

  if (!single || size) {
    const box = size ?? { width: TILE_SIZE, height: TILE_SIZE }
    return (
      <button
        type="button"
        title={attachment.name}
        onClick={onOpen}
        style={{ width: box.width, height: box.height }}
        className="relative block shrink-0 overflow-hidden rounded-lg bg-accent"
      >
        {url && <img src={url} alt={attachment.name} className="size-full object-cover" />}
        {!url && !failed && (
          <span className="absolute inset-0 flex items-center justify-center">
            <Loader2 className="size-4 animate-spin text-fg-tertiary" />
          </span>
        )}
        {failed && (
          <span
            role="button"
            title="加载失败，点击重试"
            onClick={(e) => {
              e.stopPropagation()
              retry()
            }}
            className="absolute inset-0 flex flex-col items-center justify-center gap-0.5 text-fg-tertiary hover:text-foreground"
          >
            <ImageOff className="size-4" />
            <span className="text-meta">重试</span>
          </span>
        )}
      </button>
    )
  }

  // 单图且宽高未知：加载前给占位，加载后按自然尺寸（封顶 240px）展示
  if (!url) {
    return (
      <div
        className="flex items-center justify-center rounded-lg bg-accent"
        style={{ width: 160, height: 120 }}
      >
        {failed ? (
          <button
            type="button"
            title="加载失败，点击重试"
            onClick={retry}
            className="flex flex-col items-center gap-0.5 text-fg-tertiary hover:text-foreground"
          >
            <ImageOff className="size-4" />
            <span className="text-meta">重试</span>
          </button>
        ) : (
          <Loader2 className="size-4 animate-spin text-fg-tertiary" />
        )}
      </div>
    )
  }
  return (
    <button type="button" title={attachment.name} onClick={onOpen} className="block">
      <img
        src={url}
        alt={attachment.name}
        className="max-w-60 rounded-lg object-cover"
        style={{ maxHeight: SINGLE_MAX_EDGE }}
      />
    </button>
  )
}

function Lightbox({ attachment, onClose }: { attachment: AttachmentRef; onClose: () => void }) {
  const { url } = useAttachmentURL(attachment.id)
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose()
    }
    window.addEventListener("keydown", onKey)
    return () => window.removeEventListener("keydown", onKey)
  }, [onClose])
  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/80 p-8"
      onClick={onClose}
    >
      {url ? (
        <img
          src={url}
          alt={attachment.name}
          className="max-h-full max-w-full rounded-lg object-contain"
        />
      ) : (
        <Loader2 className="size-6 animate-spin text-white" />
      )}
    </div>
  )
}

export function MessageImages({
  attachments,
  className,
}: {
  attachments: AttachmentRef[]
  className?: string
}) {
  const [openId, setOpenId] = useState<string | null>(null)
  if (attachments.length === 0) return null
  const single = attachments.length === 1
  const opened = attachments.find((a) => a.id === openId)
  return (
    <div className={cn("flex flex-wrap gap-1", className)}>
      {attachments.map((a) => (
        <AttachmentImage key={a.id} attachment={a} single={single} onOpen={() => setOpenId(a.id)} />
      ))}
      {opened && <Lightbox attachment={opened} onClose={() => setOpenId(null)} />}
    </div>
  )
}
