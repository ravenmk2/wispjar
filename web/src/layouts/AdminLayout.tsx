import { ArrowLeft, Building2, ChevronsUpDown, KeyRound, LogOut, ShieldCheck } from "lucide-react"
import { useState } from "react"
import { Link, NavLink, Outlet, useNavigate } from "react-router"
import { toast } from "sonner"
import { authApi } from "@/api/auth"
import { errorMessage } from "@/api/client"
import { Avatar, AvatarFallback } from "@/components/ui/avatar"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { Separator } from "@/components/ui/separator"
import { cn } from "@/lib/utils"
import { useCurrentOrg, useIdentityStore } from "@/stores/identity"

export function OrgSwitcher() {
  const orgs = useIdentityStore((s) => s.orgs)
  const setOrg = useIdentityStore((s) => s.setOrg)
  const setIdentity = useIdentityStore((s) => s.setIdentity)
  const currentOrg = useCurrentOrg()
  const [switching, setSwitching] = useState(false)

  async function switchOrg(orgId: string) {
    if (orgId === currentOrg?.id || switching) return
    setSwitching(true)
    setOrg(orgId)
    try {
      const me = await authApi.me()
      setIdentity(me.user, me.orgs)
    } catch (e) {
      toast.error(errorMessage(e))
    } finally {
      setSwitching(false)
    }
  }

  if (!currentOrg) return null

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button variant="outline" size="sm" className="max-w-56 gap-1.5">
          <Building2 className="size-3.5 shrink-0 text-muted-foreground" />
          <span className="truncate">{currentOrg.name}</span>
          {currentOrg.is_platform && <Badge variant="secondary">平台</Badge>}
          <ChevronsUpDown className="size-3.5 shrink-0 text-muted-foreground" />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start" className="w-56">
        <DropdownMenuLabel>切换机构</DropdownMenuLabel>
        <DropdownMenuSeparator />
        {orgs.map((org) => (
          <DropdownMenuItem key={org.id} onClick={() => switchOrg(org.id)} className="gap-2">
            <span className={cn("size-1.5 rounded-full", org.id === currentOrg.id ? "bg-primary" : "bg-transparent")} />
            <span className="flex-1 truncate">{org.name}</span>
            {org.is_platform && <Badge variant="secondary">平台</Badge>}
            {org.role === "admin" && <span className="text-xs text-muted-foreground">管理员</span>}
          </DropdownMenuItem>
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

export function UserMenu() {
  const user = useIdentityStore((s) => s.user)
  const clear = useIdentityStore((s) => s.clear)
  const navigate = useNavigate()

  if (!user) return null
  const initial = (user.display_name || user.username).slice(0, 1).toUpperCase()

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button variant="ghost" size="sm" className="gap-2 px-2">
          <Avatar className="size-6">
            <AvatarFallback className="text-xs">{initial}</AvatarFallback>
          </Avatar>
          <span className="max-w-32 truncate">{user.display_name || user.username}</span>
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-48">
        <DropdownMenuLabel>
          <div className="flex flex-col">
            <span>{user.display_name || user.username}</span>
            <span className="text-xs font-normal text-muted-foreground">@{user.username}</span>
          </div>
        </DropdownMenuLabel>
        <DropdownMenuSeparator />
        <DropdownMenuItem onClick={() => navigate("/change-password")}>
          <KeyRound className="size-4" />
          修改密码
        </DropdownMenuItem>
        <DropdownMenuSeparator />
        <DropdownMenuItem
          onClick={() => {
            clear()
            navigate("/login", { replace: true })
          }}
        >
          <LogOut className="size-4" />
          退出登录
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

export function SecondaryLayout({
  title,
  icon,
  items,
}: {
  title: string
  icon?: React.ReactNode
  items: Array<{ to: string; label: string }>
}) {
  return (
    <div className="flex min-h-screen flex-col">
      <header className="flex h-12 shrink-0 items-center gap-3 border-b px-4">
        <Button variant="ghost" size="sm" asChild className="gap-1.5 px-2">
          <Link to="/">
            <ArrowLeft className="size-4" />
            工作区
          </Link>
        </Button>
        <Separator orientation="vertical" className="h-5" />
        <div className="flex items-center gap-2 text-sm font-medium">
          {icon}
          {title}
        </div>
        <div className="ml-auto flex items-center gap-2">
          <OrgSwitcher />
          <UserMenu />
        </div>
      </header>
      <div className="flex flex-1">
        <nav className="w-44 shrink-0 border-r p-2">
          {items.map((item) => (
            <NavLink
              key={item.to}
              to={item.to}
              className={({ isActive }) =>
                cn(
                  "block rounded-md px-3 py-1.5 text-sm text-muted-foreground hover:bg-accent hover:text-foreground",
                  isActive && "bg-accent font-medium text-foreground",
                )
              }
            >
              {item.label}
            </NavLink>
          ))}
        </nav>
        <main className="min-w-0 flex-1 p-6">
          <Outlet />
        </main>
      </div>
    </div>
  )
}

export function AdminLayout() {
  const currentOrg = useCurrentOrg()
  return (
    <SecondaryLayout
      title={`机构管理 · ${currentOrg?.name ?? ""}`}
      items={[
        { to: "/admin/agents", label: "智能体" },
        { to: "/admin/skills", label: "Skills" },
        { to: "/admin/members", label: "成员" },
        { to: "/admin/usage", label: "用量" },
      ]}
    />
  )
}

export function PlatformLayout() {
  return (
    <SecondaryLayout
      title="平台管理"
      icon={<ShieldCheck className="size-4" />}
      items={[
        { to: "/platform/orgs", label: "机构" },
        { to: "/platform/users", label: "用户" },
        { to: "/platform/providers", label: "供应商" },
        { to: "/platform/models", label: "模型" },
        { to: "/platform/settings", label: "设置" },
      ]}
    />
  )
}
