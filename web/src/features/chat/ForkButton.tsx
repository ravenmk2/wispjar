import { GitFork } from "lucide-react"
import { useState } from "react"
import { Button } from "@/components/ui/button"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { cn } from "@/lib/utils"
import { useSessionListStore } from "@/stores/sessionList"
import { useSessionStore } from "@/stores/session"
import { ForkDialog } from "./ForkDialog"

export function ForkButton({
  sessionId,
  seq,
  className,
}: {
  sessionId: string
  seq: number
  className?: string
}) {
  const [open, setOpen] = useState(false)
  const listTitle = useSessionListStore((s) => s.sessions[sessionId]?.title)
  const headerTitle = useSessionStore((s) => s.sessionId === sessionId ? s.header?.title : undefined)

  return (
    <>
      <Tooltip>
        <TooltipTrigger asChild>
          <Button
            variant="ghost"
            size="icon"
            aria-label="从这里开始新聊天"
            onClick={() => setOpen(true)}
            className={cn(
              "size-7 shrink-0 text-fg-tertiary hover:text-foreground [&_svg]:size-3.5",
              className,
            )}
          >
            <GitFork className="size-3.5" />
          </Button>
        </TooltipTrigger>
        <TooltipContent>从这里开始新聊天</TooltipContent>
      </Tooltip>
      {open && <ForkDialog sessionId={sessionId} seq={seq} sourceTitle={listTitle ?? headerTitle ?? ""} onClose={() => setOpen(false)} />}
    </>
  )
}
