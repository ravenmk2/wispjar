import { Plus, X } from "lucide-react"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import type { EnvRow } from "@/lib/env"

export function EnvEditor({ rows, onChange, savedKeys, disabled }: {
  rows: EnvRow[]; onChange: (rows: EnvRow[]) => void; savedKeys: string[]; disabled?: boolean
}) {
  return <div className="space-y-2">
    {rows.map((row, index) => <div key={index} className="flex gap-2">
      <Input aria-label={`变量名 ${index + 1}`} className="min-w-0 flex-1 font-mono" placeholder="变量名" value={row.key} disabled={disabled || savedKeys.includes(row.key)} onChange={(e) => onChange(rows.map((r, i) => i === index ? { ...r, key: e.target.value } : r))} />
      <Input aria-label={`变量值 ${index + 1}`} className="min-w-0 flex-1 font-mono" type="password" autoComplete="new-password" placeholder={row.value === undefined ? "已配置，输入以替换" : "变量值（可为空）"} value={row.value ?? ""} disabled={disabled} onChange={(e) => onChange(rows.map((r, i) => i === index ? { ...r, value: e.target.value } : r))} />
      <Button variant="ghost" size="icon" aria-label={`删除变量 ${index + 1}`} disabled={disabled} onClick={() => onChange(rows.filter((_, i) => i !== index))}><X className="size-4" /></Button>
    </div>)}
    <Button variant="outline" size="sm" disabled={disabled} onClick={() => onChange([...rows, { key: "", value: "" }])}><Plus className="size-4" />添加变量</Button>
    <p className="text-xs text-muted-foreground">已有值不回显，未编辑则保留。删除变量后保存即可移除。</p>
  </div>
}
