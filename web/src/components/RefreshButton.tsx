import { RefreshCw } from "lucide-react"
import { useEffect, useRef, useState } from "react"
import { Button, type ButtonProps } from "@/components/ui/button"
import { cn } from "@/lib/utils"

interface RefreshButtonProps extends Omit<ButtonProps, "asChild"> {
  refreshing: boolean
  iconClassName?: string
}

export function RefreshButton({ refreshing, iconClassName, disabled, onClick, children = "刷新", ...props }: RefreshButtonProps) {
  const [feedback, setFeedback] = useState(false)
  const timer = useRef<ReturnType<typeof setTimeout>>()
  useEffect(() => () => clearTimeout(timer.current), [])
  const spinning = refreshing || feedback

  return <Button {...props} type={props.type ?? "button"} disabled={disabled || spinning} aria-busy={spinning} onClick={(event) => {
    setFeedback(true)
    clearTimeout(timer.current)
    timer.current = setTimeout(() => setFeedback(false), 600)
    onClick?.(event)
  }}>
    <RefreshCw aria-hidden="true" className={cn("size-4", spinning && "animate-spin", iconClassName)} />
    {children}
  </Button>
}
