import { Bot, MessagesSquare, PanelLeftClose, PanelLeftOpen } from "lucide-react"
import { useEffect, useState } from "react"
import { Outlet } from "react-router"
import { UserMenu } from "@/layouts/AdminLayout"
import { Button } from "@/components/ui/button"
import { Separator } from "@/components/ui/separator"
import { AgentPicker } from "@/features/agents/AgentPicker"
import { SessionList } from "@/features/sessions/SessionList"
import { followControl } from "@/mux/control"
import { isDark, useThemeMode } from "@/lib/theme"
import { cn } from "@/lib/utils"
import { useCurrentOrg, useIdentityStore } from "@/stores/identity"

const COLLAPSED_KEY = "wispjar-sidebar-collapsed"

export default function WorkspaceLayout() {
  const currentOrg = useCurrentOrg()
  const token = useIdentityStore((s) => s.token)
  const logoSrc = isDark(useThemeMode()) ? "/logo-dark.svg" : "/logo.svg"
  const [collapsed, setCollapsed] = useState(() => localStorage.getItem(COLLAPSED_KEY) === "1")

  useEffect(() => {
    if (!currentOrg || !token) return
    const handle = followControl(currentOrg.id)
    return () => handle.close()
  }, [currentOrg, token])

  function toggleSidebar() {
    setCollapsed((v) => {
      localStorage.setItem(COLLAPSED_KEY, v ? "0" : "1")
      return !v
    })
  }

  return (
    <div className="flex h-screen">
      <aside
        className={cn(
          "flex shrink-0 flex-col border-r bg-sidebar transition-[width] duration-200",
          collapsed ? "w-12" : "w-[280px]",
        )}
      >
        {collapsed ? (
          <>
            <div className="flex h-14 shrink-0 items-center justify-center">
              <Button variant="ghost" size="icon" className="size-8" title="展开侧边栏" onClick={toggleSidebar}>
                <PanelLeftOpen className="size-4" />
              </Button>
            </div>
            <Separator />
            <div className="min-h-0 flex-1" />
            <Separator />
            <div className="flex shrink-0 justify-center p-2">
              <UserMenu variant="icon" />
            </div>
          </>
        ) : (
          <>
            <div className="flex h-14 shrink-0 items-center gap-2.5 px-4 text-base font-semibold tracking-wider">
              <img src={logoSrc} alt="" className="size-6" />
              WISPJAR
              <Button
                variant="ghost"
                size="icon"
                className="ml-auto size-8"
                title="折叠侧边栏"
                onClick={toggleSidebar}
              >
                <PanelLeftClose className="size-4" />
              </Button>
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
          </>
        )}
      </aside>
      <main className="min-h-0 min-w-0 flex-1 overflow-y-auto">
        <Outlet />
      </main>
    </div>
  )
}
