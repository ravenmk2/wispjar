import { Bot, Boxes, Building2, FileClock, Folder, KeyRound, Palette, Plug, Settings2, Sparkles, Users, ChartNoAxesCombined } from "lucide-react"
import { NavLink } from "react-router"
import { CloudComputerIcon } from "@/components/CloudComputerIcon"
import { sectionNavigation, type MainSection } from "@/lib/navigation"
import { cn } from "@/lib/utils"

const icons = { computer: CloudComputerIcon, agent: Bot, storage: Folder, account: KeyRound, appearance: Palette, skill: Sparkles, members: Users, usage: ChartNoAxesCombined, organization: Building2, provider: Plug, model: Boxes, audit: FileClock, settings: Settings2 }

export function SectionNavigation({ section }: { section: Exclude<MainSection, "workspace"> }) {
  const navigation = sectionNavigation[section]
  return <nav aria-label={navigation.title} className="min-h-0 flex-1 overflow-y-auto px-2 py-4">
    <h2 className="mb-5 px-2 text-base font-semibold">{navigation.title}</h2>
    {navigation.groups.map((group, index) => <div key={index} className={cn(index > 0 && "mt-6")}>
      {group.label && <div className="mb-2 px-2 text-xs text-fg-tertiary">{group.label}</div>}
      <div className="space-y-0.5">{group.items.map((item) => {
        const Icon = icons[item.icon as keyof typeof icons]
        return <NavLink key={item.to} to={item.to} className={({ isActive }) => cn("flex min-h-9 items-center gap-2 rounded-[10px] px-2 py-1.5 text-sm text-muted-foreground hover:bg-accent hover:text-foreground", isActive && "bg-selected font-medium text-foreground")}>
          <Icon className="size-4 shrink-0" /><span className="truncate">{item.label}</span>
        </NavLink>
      })}</div>
    </div>)}
  </nav>
}
