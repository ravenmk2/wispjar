import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import type { SandboxStatus } from "@/lib/types"
import { cn } from "@/lib/utils"

const STATUS_LABEL: Record<SandboxStatus, string> = {
  none: "未创建",
  creating: "创建中",
  running: "运行中",
  paused: "已暂停",
  recycled: "已回收",
}

export function SandboxStatusIndicator({ status }: { status: SandboxStatus }) {
  const label = `云电脑：${STATUS_LABEL[status]}`
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <span tabIndex={0} role="img" aria-label={label}
          className="inline-flex size-5 shrink-0 items-center justify-center rounded-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/50">
          <span aria-hidden className={cn("size-1.5 rounded-full",
            status === "running" || status === "creating" ? "bg-primary" : "bg-fg-tertiary",
            status === "creating" && "animate-pulse motion-reduce:animate-none",
          )} />
        </span>
      </TooltipTrigger>
      <TooltipContent>{label}</TooltipContent>
    </Tooltip>
  )
}
