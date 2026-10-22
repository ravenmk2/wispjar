import type { SVGProps } from "react"
import { cn } from "@/lib/utils"

export function CloudComputerIcon({ className, ...props }: SVGProps<SVGSVGElement>) {
  return (
    <svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.75" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true" className={cn("text-foreground", className)} {...props}>
      <path d="M7.5 11.5V9a5.75 5.75 0 0 1 11.5 0v.8A4.7 4.7 0 0 1 17.5 19H7a5 5 0 0 1 .6-9.96" />
    </svg>
  )
}
