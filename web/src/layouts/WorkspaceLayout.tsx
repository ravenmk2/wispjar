import { Bot, MessagesSquare, Settings2, ShieldCheck } from "lucide-react"
import { Link, Outlet } from "react-router"
import { OrgSwitcher, UserMenu } from "@/layouts/AdminLayout"
import { Separator } from "@/components/ui/separator"
import { useCurrentOrg } from "@/stores/identity"

function SidebarPlaceholder({ icon, label }: { icon: React.ReactNode; label: string }) {
  return (
    <div className="px-3">
      <div className="mb-1.5 flex items-center gap-1.5 px-1 text-xs font-medium text-muted-foreground">
        {icon}
        {label}
      </div>
      <div className="rounded-md border border-dashed px-3 py-4 text-center text-xs text-muted-foreground">
        即将上线
      </div>
    </div>
  )
}

export default function WorkspaceLayout() {
  const currentOrg = useCurrentOrg()
  const isAdmin = currentOrg?.role === "admin"
  const isPlatformAdmin = isAdmin && currentOrg?.is_platform

  return (
    <div className="flex min-h-screen">
      <aside className="flex w-60 shrink-0 flex-col border-r">
        <div className="flex h-12 items-center px-4 text-sm font-semibold">WispJar</div>
        <Separator />
        <div className="flex-1 space-y-4 overflow-y-auto py-4">
          <SidebarPlaceholder icon={<Bot className="size-3.5" />} label="智能体" />
          <SidebarPlaceholder icon={<MessagesSquare className="size-3.5" />} label="会话" />
        </div>
        {(isAdmin || isPlatformAdmin) && (
          <>
            <Separator />
            <div className="space-y-0.5 p-2">
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
      </aside>
      <div className="flex min-w-0 flex-1 flex-col">
        <header className="flex h-12 shrink-0 items-center gap-2 border-b px-4">
          <OrgSwitcher />
          {isPlatformAdmin && (
            <Link
              to="/platform/orgs"
              className="rounded-md px-2 py-1 text-sm text-muted-foreground hover:bg-accent hover:text-foreground"
            >
              进入平台后台
            </Link>
          )}
          <div className="ml-auto">
            <UserMenu />
          </div>
        </header>
        <main className="min-h-0 flex-1 overflow-y-auto">
          <Outlet />
        </main>
      </div>
    </div>
  )
}
