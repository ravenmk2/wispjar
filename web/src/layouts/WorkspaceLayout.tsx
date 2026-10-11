import { Bot, MessagesSquare, Settings2, ShieldCheck } from "lucide-react"
import { useEffect } from "react"
import { Link, Outlet } from "react-router"
import { OrgSwitcher, UserMenu } from "@/layouts/AdminLayout"
import { Separator } from "@/components/ui/separator"
import { AgentPicker } from "@/features/agents/AgentPicker"
import { SessionList } from "@/features/sessions/SessionList"
import { followControl } from "@/mux/control"
import { isDark, useThemeMode } from "@/lib/theme"
import { useCurrentOrg, useIdentityStore } from "@/stores/identity"

export default function WorkspaceLayout() {
  const currentOrg = useCurrentOrg()
  const token = useIdentityStore((s) => s.token)
  const isAdmin = currentOrg?.role === "admin"
  const isPlatformAdmin = isAdmin && currentOrg?.is_platform
  const logoSrc = isDark(useThemeMode()) ? "/logo-dark.svg" : "/logo.svg"

  useEffect(() => {
    if (!currentOrg || !token) return
    const handle = followControl(currentOrg.id)
    return () => handle.close()
  }, [currentOrg, token])

  return (
    <div className="flex h-screen">
      <aside className="flex w-[280px] shrink-0 flex-col border-r bg-sidebar">
        <div className="flex h-12 shrink-0 items-center gap-2 px-4 text-sm font-semibold">
          <img src={logoSrc} alt="" className="size-5" />
          Wispjar
        </div>
        <Separator />
        <div className="shrink-0 p-2">
          <OrgSwitcher variant="sidebar" />
        </div>
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
        {(isAdmin || isPlatformAdmin) && (
          <>
            <Separator />
            <div className="shrink-0 space-y-0.5 p-2">
              {isAdmin && (
                <Link
                  to="/admin/members"
                  className="flex items-center gap-2 rounded-md px-3 py-1.5 text-sm text-muted-foreground hover:bg-accent hover:text-foreground"
                >
                  <Settings2 className="size-4" />
                  机构管理
                </Link>
              )}
              {isPlatformAdmin && (
                <Link
                  to="/platform/orgs"
                  className="flex items-center gap-2 rounded-md px-3 py-1.5 text-sm text-muted-foreground hover:bg-accent hover:text-foreground"
                >
                  <ShieldCheck className="size-4" />
                  平台管理
                </Link>
              )}
            </div>
          </>
        )}
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
