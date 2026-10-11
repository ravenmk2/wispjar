import { Bot, MessagesSquare } from "lucide-react"
import { useEffect } from "react"
import { Outlet } from "react-router"
import { UserMenu } from "@/layouts/AdminLayout"
import { Separator } from "@/components/ui/separator"
import { AgentPicker } from "@/features/agents/AgentPicker"
import { SessionList } from "@/features/sessions/SessionList"
import { followControl } from "@/mux/control"
import { isDark, useThemeMode } from "@/lib/theme"
import { useCurrentOrg, useIdentityStore } from "@/stores/identity"

export default function WorkspaceLayout() {
  const currentOrg = useCurrentOrg()
  const token = useIdentityStore((s) => s.token)
  const logoSrc = isDark(useThemeMode()) ? "/logo-dark.svg" : "/logo.svg"

  useEffect(() => {
    if (!currentOrg || !token) return
    const handle = followControl(currentOrg.id)
    return () => handle.close()
  }, [currentOrg, token])

  return (
    <div className="flex h-screen">
      <aside className="flex w-[280px] shrink-0 flex-col border-r bg-sidebar">
        <div className="flex h-14 shrink-0 items-center gap-2.5 px-4 text-base font-semibold tracking-wider">
          <img src={logoSrc} alt="" className="size-6" />
          WISPJAR
        </div>
        <Separator />
        <div className="min-h-0 flex-1 space-y-4 overflow-y-auto py-4">
          <div className="px-3">
            <div className="mb-1.5 flex items-center gap-1.5 px-1 text-xs font-medium text-muted-foreground">
              <Bot className="size-3.5" />
              智能体
            </div>
            <AgentPicker />
          </div>
          <div className="px-3">
            <div className="mb-1.5 flex items-center gap-1.5 px-1 text-xs font-medium text-muted-foreground">
              <MessagesSquare className="size-3.5" />
              会话
            </div>
            <SessionList />
          </div>
        </div>
        <Separator />
        <div className="shrink-0 p-2">
          <UserMenu variant="sidebar" />
        </div>
      </aside>
      <main className="min-h-0 min-w-0 flex-1 overflow-y-auto">
        <Outlet />
      </main>
    </div>
  )
}
