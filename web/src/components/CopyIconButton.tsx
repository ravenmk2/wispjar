import { Check, Copy } from "lucide-react"
import { useEffect, useRef, useState } from "react"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { copyText } from "@/lib/format"
import { cn } from "@/lib/utils"

export function CopyIconButton({
  text,
  className,
  tooltip = "复制",
}: {
  text: string
  className?: string
  tooltip?: string
}) {
  const [copied, setCopied] = useState(false)
  const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null)

  useEffect(
    () => () => {
      if (timerRef.current) clearTimeout(timerRef.current)
    },
    [],
  )

  async function copy(e: React.MouseEvent) {
    e.stopPropagation()
    const ok = await copyText(text)
    if (!ok) return
    setCopied(true)
    if (timerRef.current) clearTimeout(timerRef.current)
    timerRef.current = setTimeout(() => setCopied(false), 1000)
  }

  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <button
          type="button"
          aria-label={copied ? "已复制" : tooltip}
          onClick={(e) => void copy(e)}
          className={cn(
            "flex size-7 shrink-0 items-center justify-center rounded-md text-fg-tertiary transition-colors hover:bg-accent hover:text-foreground",
            className,
          )}
        >
          {copied ? <Check className="size-3.5 text-success" /> : <Copy className="size-3.5" />}
        </button>
      </TooltipTrigger>
      <TooltipContent>{copied ? "已复制" : tooltip}</TooltipContent>
    </Tooltip>
  )
}
