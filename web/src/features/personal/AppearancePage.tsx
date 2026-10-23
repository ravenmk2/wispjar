import { Check, Monitor, Moon, Sun } from "lucide-react"
import { PageHeader } from "@/components/common"
import { setThemeMode, useThemeMode, type ThemeMode } from "@/lib/theme"
import { cn } from "@/lib/utils"

const options: Array<{ value: ThemeMode; label: string; description: string; icon: typeof Sun }> = [
  { value: "light", label: "浅色", description: "明亮清晰的界面", icon: Sun },
  { value: "dark", label: "深色", description: "适合较暗的使用环境", icon: Moon },
  { value: "system", label: "跟随系统", description: "与设备的外观保持一致", icon: Monitor },
]

export default function AppearancePage() {
  const mode = useThemeMode()
  return <div className="mx-auto max-w-3xl space-y-6">
    <PageHeader title="外观偏好" description="选择适合你的主题，修改后立即生效。" />
    <div className="grid gap-4 sm:grid-cols-3">{options.map((option) => <button key={option.value} type="button" aria-pressed={mode === option.value} onClick={() => setThemeMode(option.value)} className={cn("rounded-xl border bg-card p-4 text-left hover:bg-accent", mode === option.value && "border-primary ring-1 ring-primary")}>
      <div className={cn("mb-4 flex h-24 overflow-hidden rounded-md border", option.value === "dark" ? "border-white/10 bg-[#121212]" : "border-black/10 bg-white")}><div className={cn("w-1/4", option.value === "dark" ? "bg-[#202020]" : "bg-[#f4f4f2]")} /><div className="flex-1 space-y-3 p-4"><div className={cn("h-1.5 w-4/5 rounded", option.value === "dark" ? "bg-white/20" : "bg-black/10")} /><div className={cn("h-1.5 w-3/5 rounded", option.value === "dark" ? "bg-white/20" : "bg-black/10")} /></div></div>
      <div className="flex items-center gap-2 text-sm font-medium"><option.icon className="size-4" />{option.label}{mode === option.value && <Check className="ml-auto size-4 text-primary" />}</div><p className="mt-2 text-xs text-muted-foreground">{option.description}</p>
    </button>)}</div>
    <p className="text-xs text-muted-foreground">外观偏好保存在当前浏览器，切换组织时保持一致。</p>
  </div>
}
