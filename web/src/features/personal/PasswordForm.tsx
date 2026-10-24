import { Eye, EyeOff } from "lucide-react"
import { useRef, useState } from "react"
import { toast } from "sonner"
import { authApi } from "@/api/auth"
import { ApiError, errorMessage } from "@/api/client"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { passwordValid } from "@/lib/format"
import { cn } from "@/lib/utils"
import { useIdentityStore } from "@/stores/identity"

const fields = [
  { key: "old_password", label: "当前密码", autoComplete: "current-password" },
  { key: "new_password", label: "新密码", autoComplete: "new-password" },
  { key: "confirm", label: "确认新密码", autoComplete: "new-password" },
] as const
type Field = typeof fields[number]["key"]

export function PasswordForm({ embedded = false, onSuccess, onCancel }: { embedded?: boolean; onSuccess: () => void; onCancel?: () => void }) {
  const [values, setValues] = useState<Record<Field, string>>({ old_password: "", new_password: "", confirm: "" })
  const [visible, setVisible] = useState<Partial<Record<Field, boolean>>>({})
  const [errors, setErrors] = useState<Partial<Record<Field, string>>>({})
  const [error, setError] = useState("")
  const [loading, setLoading] = useState(false)
  const submitting = useRef(false)
  const inputs = useRef<Partial<Record<Field, HTMLInputElement | null>>>({})

  async function submit(e: React.FormEvent) {
    e.preventDefault()
    if (submitting.current) return
    const nextErrors: Partial<Record<Field, string>> = {}
    if (!values.old_password) nextErrors.old_password = "请输入当前密码"
    if (!passwordValid(values.new_password)) nextErrors.new_password = "至少 8 位，需同时包含字母和数字"
    if (!values.confirm || values.new_password !== values.confirm) nextErrors.confirm = "两次输入的新密码不一致"
    setErrors(nextErrors)
    setError("")
    const first = fields.find(({ key }) => nextErrors[key])
    if (first) { inputs.current[first.key]?.focus(); return }
    const token = useIdentityStore.getState().token
    submitting.current = true
    setLoading(true)
    try {
      const res = await authApi.changePassword(values.old_password, values.new_password)
      const identity = useIdentityStore.getState()
      if (identity.token !== token || !identity.user || !token) return
      // The write has succeeded: don't turn a subsequent /me network error into a failed password change.
      identity.setAuth({ token: res.token ?? token, user: { ...identity.user, must_change_password: false }, orgs: identity.orgs })
      setValues({ old_password: "", new_password: "", confirm: "" })
      setVisible({})
      toast.success("密码已更新")
      onSuccess()
    } catch (err) {
      const fieldErrors: Partial<Record<Field, string>> = {}
      if (err instanceof ApiError) {
        for (const { key } of fields) {
          const detail = err.details?.[key]
          if (typeof detail === "string") fieldErrors[key] = detail
        }
      }
      const firstError = fields.find(({ key }) => fieldErrors[key])
      if (firstError) {
        setErrors(fieldErrors)
        // Focus after the disabled inputs are re-enabled.
        requestAnimationFrame(() => inputs.current[firstError.key]?.focus())
      } else setError(errorMessage(err))
    } finally {
      submitting.current = false
      setLoading(false)
    }
  }

  return <form id="password-form" onSubmit={submit} noValidate aria-busy={loading} className={cn("auth-form", embedded && "border-t")}>
    <div className={embedded ? "space-y-5 p-6" : "space-y-4"}>
      {fields.map(({ key, label, autoComplete }, index) => <div key={key} className="max-w-md space-y-2">
        <Label htmlFor={key}>{label}</Label>
        <div className="relative">
          <Input ref={(el) => { inputs.current[key] = el }} id={key} name={key} type={visible[key] ? "text" : "password"} autoComplete={autoComplete} autoFocus={index === 0} value={values[key]} disabled={loading} className="pr-10" aria-invalid={!!errors[key]} aria-describedby={[key === "new_password" && "password-hint", errors[key] && `${key}-error`].filter(Boolean).join(" ") || undefined} onChange={(e) => { setValues((v) => ({ ...v, [key]: e.target.value })); setErrors((v) => ({ ...v, [key]: undefined, ...(key === "new_password" ? { confirm: undefined } : {}) })); setError("") }} />
          <button type="button" disabled={loading} aria-label={`${visible[key] ? "隐藏" : "显示"}${label}`} aria-pressed={!!visible[key]} onClick={() => setVisible((v) => ({ ...v, [key]: !v[key] }))} className="absolute right-1 top-1 flex size-7 items-center justify-center rounded text-muted-foreground hover:bg-accent disabled:opacity-50">{visible[key] ? <EyeOff className="size-4" /> : <Eye className="size-4" />}</button>
        </div>
        {key === "new_password" && <p id="password-hint" className="text-xs text-muted-foreground">至少 8 位，需同时包含字母和数字。</p>}
        {errors[key] && <p id={`${key}-error`} role="alert" className="text-xs text-destructive">{errors[key]}</p>}
      </div>)}
      {error && <p role="alert" className="text-sm text-destructive">{error}</p>}
    </div>
    <div className={embedded ? "flex justify-end gap-2 border-t px-6 py-4" : "mt-4"}>
      {onCancel && <Button type="button" variant="ghost" disabled={loading} onClick={onCancel}>取消</Button>}
      <Button type="submit" className={embedded ? "" : "w-full"} disabled={loading}>{loading ? "更新中…" : "更新密码"}</Button>
    </div>
  </form>
}
