import { useState } from "react"
import { Navigate, useNavigate } from "react-router"
import { toast } from "sonner"
import { authApi } from "@/api/auth"
import { errorMessage } from "@/api/client"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { passwordValid } from "@/lib/format"
import { useIdentityStore } from "@/stores/identity"

export default function ChangePasswordPage({ embedded = false }: { embedded?: boolean }) {
  const token = useIdentityStore((s) => s.token)
  const user = useIdentityStore((s) => s.user)
  const setToken = useIdentityStore((s) => s.setToken)
  const setIdentity = useIdentityStore((s) => s.setIdentity)
  const navigate = useNavigate()
  const [oldPassword, setOldPassword] = useState("")
  const [newPassword, setNewPassword] = useState("")
  const [confirm, setConfirm] = useState("")
  const [error, setError] = useState("")
  const [loading, setLoading] = useState(false)

  if (!token) return <Navigate to="/login" replace />

  const mustChange = user?.must_change_password

  async function onSubmit(e: React.FormEvent) {
    e.preventDefault()
    if (!passwordValid(newPassword)) {
      setError("新密码至少 8 位，且需同时包含字母和数字")
      return
    }
    if (newPassword !== confirm) {
      setError("两次输入的新密码不一致")
      return
    }
    setLoading(true)
    setError("")
    try {
      const res = await authApi.changePassword(oldPassword, newPassword)
      if (res.token) setToken(res.token)
      const me = await authApi.me()
      setIdentity(me.user, me.orgs)
      toast.success("密码已更新")
      setOldPassword(""); setNewPassword(""); setConfirm("")
      if (!embedded) navigate("/", { replace: true })
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setLoading(false)
    }
  }

  return (
    <div className={embedded ? "" : "flex min-h-screen items-center justify-center bg-muted/40 p-4"}>
      <Card className={embedded ? "w-full" : "w-full max-w-sm"}>
        <CardHeader>
          <CardTitle className="text-lg">修改密码</CardTitle>
          <CardDescription>
            {mustChange ? "首次登录需要先修改密码" : "新密码至少 8 位，需包含字母和数字"}
          </CardDescription>
        </CardHeader>
        <CardContent>
          <form onSubmit={onSubmit} className="auth-form space-y-4">
            <div className="space-y-1.5">
              <Label htmlFor="old">旧密码</Label>
              <Input
                id="old"
                type="password"
                autoComplete="current-password"
                value={oldPassword}
                onChange={(e) => setOldPassword(e.target.value)}
                autoFocus
              />
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="new">新密码</Label>
              <Input
                id="new"
                type="password"
                autoComplete="new-password"
                value={newPassword}
                onChange={(e) => setNewPassword(e.target.value)}
              />
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="confirm">确认新密码</Label>
              <Input
                id="confirm"
                type="password"
                autoComplete="new-password"
                value={confirm}
                onChange={(e) => setConfirm(e.target.value)}
              />
            </div>
            {error && <p className="text-sm text-destructive">{error}</p>}
            <Button type="submit" className="w-full" disabled={loading}>
              {loading ? "提交中…" : "确认修改"}
            </Button>
          </form>
        </CardContent>
      </Card>
    </div>
  )
}
