import { UserRound } from "lucide-react"
import { Avatar, AvatarFallback } from "@/components/ui/avatar"
import { cn } from "@/lib/utils"

export function UserAvatar({ size = "sidebar" }: { size?: "sidebar" | "compact" | "profile" }) {
  return <Avatar aria-hidden="true" className={cn("shrink-0 border border-black/15 text-black/70 dark:border-white/20 dark:text-white/75", size === "profile" ? "size-12" : size === "compact" ? "size-8" : "size-9")}>
    <AvatarFallback><UserRound className={size === "profile" ? "size-6" : "size-5"} strokeWidth={1.75} /></AvatarFallback>
  </Avatar>
}
