import { Bot, MessagesSquare, PanelLeftClose, PanelLeftOpen } from "lucide-react"
import { useEffect, useState } from "react"
import { Outlet, useLocation } from "react-router"
import { UserMenu } from "@/layouts/AdminLayout"
import { SectionNavigation } from "@/layouts/SectionNavigation"
import { Button } from "@/components/ui/button"
import { Separator } from "@/components/ui/separator"
import { WispjarWordmark } from "@/components/WispjarWordmark"
import { AgentPicker } from "@/features/agents/AgentPicker"
import { SessionList } from "@/features/sessions/SessionList"
import { followControl } from "@/mux/control"
import { isDark, useThemeMode } from "@/lib/theme"
import { cn } from "@/lib/utils"
import { mainSection, workspaceLocationKey } from "@/lib/navigation"
import { useCurrentOrg, useIdentityStore } from "@/stores/identity"

const COLLAPSED_KEY = "wispjar-sidebar-collapsed"

export default function WorkspaceLayout() {
  const currentOrg = useCurrentOrg()
  const token = useIdentityStore((s) => s.token)
  const userId = useIdentityStore((s) => s.user?.id)
  const location = useLocation()
  const section = mainSection(location.pathname)
  const logoSrc = isDark(useThemeMode()) ? "/logo-dark.svg" : "/logo.svg"
  const [collapsed, setCollapsed] = useState(() => localStorage.getItem(COLLAPSED_KEY) === "1")

  useEffect(() => {
    if (!currentOrg || !token) return
    const handle = followControl(currentOrg.id)
    return () => handle.close()
  }, [currentOrg, token])

  useEffect(() => {
    if (section !== "workspace" || !userId || !currentOrg) return
    sessionStorage.setItem(workspaceLocationKey(userId, currentOrg.id), location.pathname + location.search)
    if (!currentOrg.is_platform) sessionStorage.setItem(`wispjar-workspace-org:${userId}`, currentOrg.id)
  }, [section, userId, currentOrg, location.pathname, location.search])

  function toggleSidebar() {
    setCollapsed((v) => {
      localStorage.setItem(COLLAPSED_KEY, v ? "0" : "1")
      return !v
    })
  }

  return (
    <div className="flex h-screen bg-sidebar">
      <aside
        className={cn(
          "flex shrink-0 flex-col bg-sidebar transition-[width] duration-200",
          collapsed ? "w-12" : "w-[240px]",
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
            <div className="flex h-14 shrink-0 items-center gap-2 px-4 text-base font-semibold">
              <img src={logoSrc} alt="" className="size-6" />
              <WispjarWordmark />
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
            {section === "workspace" ? <div className="flex min-h-0 flex-1 flex-col gap-6 overflow-hidden py-4">
              <div className="max-h-[45%] min-h-0 shrink-0 overflow-y-auto px-2">
                <div className="mb-2 flex items-center gap-2 px-2 text-sm leading-5 text-fg-tertiary">
                  <Bot className="size-4" />
                  智能体
                </div>
                <AgentPicker key={currentOrg?.id} />
              </div>
              <div className="flex min-h-0 flex-1 flex-col px-2">
                <div className="mb-2 flex shrink-0 items-center gap-2 px-2 text-sm leading-5 text-fg-tertiary">
                  <MessagesSquare className="size-4" />
                  聊天
                </div>
                <SessionList key={currentOrg?.id} />
              </div>
            </div> : <SectionNavigation section={section} />}
            <div className="shrink-0 p-2">
              <UserMenu variant="sidebar" />
            </div>
          </>
        )}
      </aside>
      <main className="min-h-0 min-w-0 flex-1 overflow-y-auto bg-background">
        <Outlet key={currentOrg?.id} />
      </main>
    </div>
  )
}
