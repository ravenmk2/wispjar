import { useState } from "react"
import { Navigate, useLocation, useNavigate } from "react-router"
import { toast } from "sonner"
import { authApi } from "@/api/auth"
import { ApiError, errorMessage } from "@/api/client"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { useIdentityStore } from "@/stores/identity"

export default function LoginPage() {
  const token = useIdentityStore((s) => s.token)
  const user = useIdentityStore((s) => s.user)
  const setAuth = useIdentityStore((s) => s.setAuth)
  const navigate = useNavigate()
  const location = useLocation()
  const [username, setUsername] = useState("")
  const [password, setPassword] = useState("")
  const [error, setError] = useState("")
  const [loading, setLoading] = useState(false)

  if (token && user && !user.must_change_password) {
    const from = (location.state as { from?: string } | null)?.from ?? "/"
    return <Navigate to={from} replace />
  }
  if (token && user?.must_change_password) {
    return <Navigate to="/change-password" replace />
  }

  async function onSubmit(e: React.FormEvent) {
    e.preventDefault()
    if (!username || !password) {
      setError("请输入用户名和密码")
      return
    }
    setLoading(true)
    setError("")
    try {
      const res = await authApi.login(username.trim(), password)
      setAuth(res)
      if (res.user.must_change_password) {
        navigate("/change-password", { replace: true })
      } else {
        navigate("/", { replace: true })
      }
    } catch (err) {
      if (err instanceof ApiError && err.code === "auth/rate_limited") {
        setError("尝试次数过多，请稍后再试")
      } else {
        setError(errorMessage(err))
      }
      toast.error("登录失败")
    } finally {
      setLoading(false)
    }
  }

  return (
    <div className="flex min-h-screen items-center justify-center bg-muted/40 p-4">
      <Card className="w-full max-w-sm">
        <CardHeader>
          <CardTitle className="text-lg">登录 Wispjar</CardTitle>
        </CardHeader>
        <CardContent>
          <form onSubmit={onSubmit} className="auth-form space-y-4">
            <div className="space-y-1.5">
              <Label htmlFor="username">用户名</Label>
              <Input
                id="username"
                autoComplete="username"
                value={username}
                onChange={(e) => setUsername(e.target.value)}
                autoFocus
              />
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="password">密码</Label>
              <Input
                id="password"
                type="password"
                autoComplete="current-password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
              />
            </div>
            {error && <p className="text-sm text-destructive">{error}</p>}
            <Button type="submit" className="w-full" disabled={loading}>
              {loading ? "登录中…" : "登录"}
            </Button>
          </form>
          <p className="mt-4 text-center text-xs text-fg-secondary">
            本软件使用 MiSans 字体 · <a className="underline" href={new URL("../generated/fonts/MiSans-License.pdf", import.meta.url).href} target="_blank" rel="noreferrer">字体许可</a>
          </p>
        </CardContent>
      </Card>
    </div>
  )
}
