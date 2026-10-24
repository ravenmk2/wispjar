import { Building2, Check, CircleUserRound, LayoutDashboard, LogOut, Monitor, Moon, Settings2, ShieldCheck, Sun } from "lucide-react"
import { useState } from "react"
import { Outlet, useLocation, useNavigate } from "react-router"
import { toast } from "sonner"
import { authApi } from "@/api/auth"
import { errorMessage } from "@/api/client"
import { UserAvatar } from "@/components/UserAvatar"
import { Badge } from "@/components/ui/badge"
import {
  DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuLabel,
  DropdownMenuSeparator, DropdownMenuSub, DropdownMenuSubContent,
  DropdownMenuSubTrigger, DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { useReturnToWorkspace } from "@/hooks/useReturnToWorkspace"
import { mainSection, orgSwitchPath, platformOrganization, sectionNavigation } from "@/lib/navigation"
import { setThemeMode, useThemeMode, type ThemeMode } from "@/lib/theme"
import { cn } from "@/lib/utils"
import type { UserOrg } from "@/lib/types"
import { useCurrentOrg, useIdentityStore } from "@/stores/identity"

const THEME_OPTIONS: Array<{ value: ThemeMode; label: string; icon: typeof Sun }> = [
  { value: "light", label: "浅色", icon: Sun },
  { value: "dark", label: "深色", icon: Moon },
  { value: "system", label: "跟随系统", icon: Monitor },
]

function ThemeSubMenu() {
  const mode = useThemeMode()
  return <DropdownMenuSub>
    <DropdownMenuSubTrigger className="gap-2"><Sun className="size-4 dark:hidden" /><Moon className="hidden size-4 dark:block" />外观</DropdownMenuSubTrigger>
    <DropdownMenuSubContent className="w-36">{THEME_OPTIONS.map((option) => <DropdownMenuItem key={option.value} onClick={() => setThemeMode(option.value)} className="gap-2">
      <option.icon className="size-4" /><span className="flex-1">{option.label}</span>{mode === option.value && <Check className="size-3.5" />}
    </DropdownMenuItem>)}</DropdownMenuSubContent>
  </DropdownMenuSub>
}

export function UserMenu({ variant = "sidebar" }: { variant?: "sidebar" | "icon" }) {
  const user = useIdentityStore((s) => s.user)
  const orgs = useIdentityStore((s) => s.orgs)
  const setOrg = useIdentityStore((s) => s.setOrg)
  const clear = useIdentityStore((s) => s.clear)
  const currentOrg = useCurrentOrg()
  const navigate = useNavigate()
  const returnToWorkspace = useReturnToWorkspace()
  const location = useLocation()
  const section = mainSection(location.pathname)
  const [switching, setSwitching] = useState(false)
  const platformOrg = platformOrganization(orgs)
  if (!user) return null

  function openPlatform() {
    if (!platformOrg) return
    if (currentOrg && !currentOrg.is_platform) sessionStorage.setItem(`wispjar-workspace-org:${user!.id}`, currentOrg.id)
    setOrg(platformOrg.id)
    navigate("/platform/orgs")
  }

  async function switchOrg(org: UserOrg) {
    if (org.id === currentOrg?.id || switching) return
    const token = useIdentityStore.getState().token
    setSwitching(true)
    setOrg(org.id)
    navigate(orgSwitchPath(location.pathname, org), { replace: true })
    try {
      const me = await authApi.me()
      if (useIdentityStore.getState().token === token && useIdentityStore.getState().currentOrgId === org.id) useIdentityStore.getState().setIdentity(me.user, me.orgs)
    } catch (e) { toast.error(errorMessage(e)) }
    finally { setSwitching(false) }
  }

  const entry = (name: string, Icon: typeof Sun, active: boolean, onClick: () => void) => <DropdownMenuItem onClick={onClick} className={cn("gap-2", active && "bg-selected")}>
    <Icon className="size-4" /><span className="flex-1">{name}</span>{active && <Check className="size-3.5 text-muted-foreground" />}
  </DropdownMenuItem>

  return <DropdownMenu>
    <DropdownMenuTrigger asChild>
      <button type="button" title={`用户菜单 · ${user.display_name || user.username} · @${user.username}`} aria-label={`用户菜单 · ${user.display_name || user.username}`} className={variant === "icon" ? "flex size-8 items-center justify-center rounded-full hover:bg-accent" : "flex h-14 w-full items-center gap-3 rounded-lg px-2 text-sm hover:bg-accent"}>
        <UserAvatar size={variant === "icon" ? "compact" : "sidebar"} />
        {variant === "sidebar" && <span className="min-w-0 flex-1 text-left"><span className="block truncate font-medium">{user.display_name || user.username}</span><span className="block truncate text-meta text-muted-foreground">@{user.username}</span></span>}
      </button>
    </DropdownMenuTrigger>
    <DropdownMenuContent side="top" align="start" className="w-60">
      <DropdownMenuLabel><div className="flex min-w-0 flex-col"><span className="truncate" title={user.display_name || user.username}>{user.display_name || user.username}</span><span className="truncate text-xs font-normal text-muted-foreground" title={`@${user.username}`}>@{user.username}</span>
        {currentOrg && <span className="mt-1 flex items-center gap-1.5 text-xs font-normal text-muted-foreground"><span className="truncate">{currentOrg.name}</span>{currentOrg.is_platform && <Badge variant="secondary">系统</Badge>}</span>}
      </div></DropdownMenuLabel>
      <DropdownMenuSeparator />
      <DropdownMenuSub>
        <DropdownMenuSubTrigger className="gap-2" disabled={switching}><Building2 className="size-4" />切换组织</DropdownMenuSubTrigger>
        <DropdownMenuSubContent className="w-56">{orgs.map((org) => <DropdownMenuItem key={org.id} onClick={() => void switchOrg(org)} className="gap-2">
          <span className={cn("size-1.5 rounded-full", org.id === currentOrg?.id ? "bg-primary" : "bg-transparent")} /><span className="flex-1 truncate">{org.name}</span>
          {org.is_platform && <Badge variant="secondary">系统</Badge>}{org.role === "admin" && <span className="text-xs text-muted-foreground">管理员</span>}
        </DropdownMenuItem>)}</DropdownMenuSubContent>
      </DropdownMenuSub>
      <DropdownMenuSeparator />
      {entry("工作区", LayoutDashboard, section === "workspace", returnToWorkspace)}
      {entry("个人中心", CircleUserRound, section === "personal", () => navigate("/personal/computers"))}
      {currentOrg?.role === "admin" && entry("组织管理", Settings2, section === "organization", () => navigate("/admin/agents"))}
      {platformOrg && entry("系统管理", ShieldCheck, section === "system", openPlatform)}
      <DropdownMenuSeparator />
      <ThemeSubMenu />
      <DropdownMenuSeparator />
      <DropdownMenuItem onClick={() => { clear(); navigate("/login", { replace: true }) }}><LogOut className="size-4" />退出登录</DropdownMenuItem>
    </DropdownMenuContent>
  </DropdownMenu>
}

function SecondaryLayout({ title }: { title: string }) {
  const org = useCurrentOrg()
  const { pathname } = useLocation()
  const personalPage = title === "个人中心" ? sectionNavigation.personal.groups.flatMap((group) => group.items).find((item) => item.to === pathname)?.label : undefined
  const fileBrowser = pathname === "/personal/storage"
  return <div className="flex h-full min-h-0 flex-col">
    <header className="flex h-14 shrink-0 items-center gap-2 border-b px-6 text-sm">
      <span className="font-medium">{title}</span><span className="text-muted-foreground">/</span><span className="truncate text-muted-foreground">{personalPage ?? (title === "系统管理" ? "平台范围" : org?.name)}</span>
    </header>
    <div className={fileBrowser ? "flex min-h-0 flex-1 flex-col overflow-hidden" : "min-h-0 flex-1 overflow-y-auto p-4 sm:p-6"}><Outlet /></div>
  </div>
}

export function PersonalLayout() { return <SecondaryLayout title="个人中心" /> }
export function AdminLayout() { return <SecondaryLayout title="组织管理" /> }
export function PlatformLayout() { return <SecondaryLayout title="系统管理" /> }
