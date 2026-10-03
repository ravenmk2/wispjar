import { GitFork, Loader2 } from "lucide-react"
import { useState } from "react"
import { useNavigate } from "react-router"
import { toast } from "sonner"
import { errorMessage } from "@/api/client"
import { sessionsApi } from "@/api/sessions"
import { Button } from "@/components/ui/button"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { cn } from "@/lib/utils"

export function ForkButton({
  sessionId,
  seq,
  className,
}: {
  sessionId: string
  seq: number
  className?: string
}) {
  const navigate = useNavigate()
  const [busy, setBusy] = useState(false)

  async function fork() {
    if (busy) return
    setBusy(true)
    try {
      const res = await sessionsApi.fork(sessionId, seq)
      toast.success("已创建分叉会话")
      navigate(`/c/${res.id}`)
    } catch (e) {
      toast.error(errorMessage(e))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <Button
          variant="ghost"
          size="icon"
          disabled={busy}
          onClick={() => void fork()}
          className={cn(
            "size-7 shrink-0 text-fg-tertiary hover:text-foreground",
            className,
          )}
        >
          {busy ? <Loader2 className="size-3.5 animate-spin" /> : <GitFork className="size-3.5" />}
        </Button>
      </TooltipTrigger>
      <TooltipContent>从这条消息分叉出新会话</TooltipContent>
    </Tooltip>
  )
}
