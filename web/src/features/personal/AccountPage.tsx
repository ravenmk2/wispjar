import { useRef, useState } from "react"
import { toast } from "sonner"
import { authApi } from "@/api/auth"
import { ApiError, errorMessage } from "@/api/client"
import { PageHeader } from "@/components/common"
import { UserAvatar } from "@/components/UserAvatar"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import ChangePasswordPage from "@/pages/ChangePassword"
import { useIdentityStore } from "@/stores/identity"

export default function AccountPage() {
  const user = useIdentityStore((s) => s.user)
  const [draft, setDraft] = useState<string | null>(null)
  const [error, setError] = useState("")
  const [saving, setSaving] = useState(false)
  const submitting = useRef(false)
  const input = useRef<HTMLInputElement>(null)
  const savedName = user?.display_name || user?.username || ""
  const value = draft ?? savedName
  const dirty = value !== savedName

  async function save(e: React.FormEvent) {
    e.preventDefault()
    if (submitting.current || !dirty) return
    const name = value.trim()
    if (!name || [...name].length > 128) {
      setError("请输入 1～128 个字符的显示名称")
      input.current?.focus()
      return
    }
    const token = useIdentityStore.getState().token
    submitting.current = true
    setSaving(true)
    setError("")
    try {
      const res = await authApi.updateMe(name)
      const identity = useIdentityStore.getState()
      if (identity.token !== token) return
      identity.updateUser(res.user)
      setDraft(null)
      toast.success("个人资料已更新")
    } catch (err) {
      const detail = err instanceof ApiError ? err.details?.display_name : undefined
      setError(typeof detail === "string" ? detail : errorMessage(err))
    } finally {
      submitting.current = false
      setSaving(false)
    }
  }

  return <div className="mx-auto max-w-3xl space-y-6">
    <PageHeader title="账号与安全" description="账号设置适用于你加入的所有组织。" />
    <section className="rounded-xl border bg-card" aria-labelledby="profile-title">
      <div className="p-6 pb-5">
        <h2 id="profile-title" className="text-base font-semibold">个人资料</h2>
        <div className="mt-5 flex items-center gap-3"><UserAvatar size="profile" /><div className="min-w-0"><div className="truncate font-medium" title={savedName}>{savedName}</div><div className="mt-0.5 truncate text-xs text-muted-foreground" title={`@${user?.username}`}>@{user?.username}</div></div></div>
      </div>
      <form onSubmit={save} noValidate aria-busy={saving}>
        <div className="space-y-5 px-6 pb-6">
          <div className="max-w-md space-y-2">
            <Label htmlFor="display-name">显示名称</Label>
            <Input ref={input} id="display-name" name="display_name" autoComplete="nickname" value={value} disabled={saving} aria-invalid={!!error} aria-describedby={`display-name-hint${error ? " display-name-error" : ""}`} onChange={(e) => { setDraft(e.target.value); setError("") }} />
            <p id="display-name-hint" className="text-xs text-muted-foreground">在侧边栏及组织成员列表中显示。</p>
            {error && <p id="display-name-error" role="alert" className="text-xs text-destructive">{error}</p>}
          </div>
          <div className="space-y-1.5"><div className="text-sm font-medium">用户名</div><div className="break-all text-sm">{user?.username}</div><p className="text-xs text-muted-foreground">用于登录，暂不支持修改。</p></div>
        </div>
        <div className="flex flex-wrap items-center justify-end gap-2 border-t px-6 py-4">
          <Button type="button" variant="ghost" disabled={!dirty || saving} onClick={() => { setDraft(null); setError("") }}>取消</Button>
          <Button type="submit" disabled={!dirty || saving}>{saving ? "保存中…" : "保存更改"}</Button>
        </div>
      </form>
    </section>
    <ChangePasswordPage embedded />
  </div>
}
