import { useState } from "react"
import { Navigate, useLocation, useNavigate } from "react-router"
import { toast } from "sonner"
import { authApi } from "@/api/auth"
import { ApiError, errorMessage } from "@/api/client"
import { Button } from "@/components/ui/button"
import { ArrowRight, Eye, EyeOff } from "lucide-react"
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
  const [showPassword, setShowPassword] = useState(false)

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
    <main className="flex min-h-svh items-center justify-center bg-[#f7f8fa] px-4 py-8 dark:bg-background">
      <div className="grid w-full max-w-[440px] overflow-hidden rounded-[18px] border border-border bg-card shadow-[0_12px_48px_-24px_rgba(24,42,68,0.19)] min-[701px]:min-h-[560px] min-[701px]:max-w-[960px] min-[701px]:grid-cols-[0.95fr_1.05fr]">
        <section aria-labelledby="intro-title" className="flex flex-col border-b border-border bg-[#f3f5f8] p-6 dark:bg-muted min-[701px]:border-b-0 min-[701px]:border-r min-[701px]:p-11">
          <svg className="h-9 w-[42px] shrink-0 text-primary min-[701px]:h-11 min-[701px]:w-[52px]" viewBox="0 0 52 44" fill="none" aria-hidden="true">
            <path d="m19 13 4.4 11.6L35 29l-11.6 4.4L19 45l-4.4-11.6L3 29l11.6-4.4Z" transform="translate(0 -3)" fill="currentColor" />
            <path d="m39 1 2.7 7.3L49 11l-7.3 2.7L39 21l-2.7-7.3L29 11l7.3-2.7Z" fill="currentColor" opacity=".65" />
            <path d="m44 28 1.6 4.4L50 34l-4.4 1.6L44 40l-1.6-4.4L38 34l4.4-1.6Z" fill="currentColor" opacity=".4" />
          </svg>
          <div className="pt-5 min-[701px]:my-auto min-[701px]:py-[42px]">
            <p className="mb-[18px] hidden text-[11px] tracking-[2px] text-fg-secondary min-[701px]:block">你的智能体工作空间</p>
            <h1 id="intro-title" className="text-[22px] font-semibold leading-[1.55] tracking-[-0.7px] min-[701px]:text-[32px]">一起思考，<br />把事情做好。</h1>
            <p className="mt-5 hidden text-[13px] leading-[1.9] text-fg-secondary min-[701px]:block">查找资料、梳理思路、完成任务，<br />从这里开始。</p>
          </div>
        </section>
        <section aria-labelledby="login-title" className="flex min-w-0 items-center px-6 py-7 min-[701px]:p-12">
          <div className="w-full">
            <h2 id="login-title" className="mb-7 text-2xl font-semibold tracking-[-0.6px]">欢迎回来</h2>
            <form onSubmit={onSubmit} className="auth-form">
              <div className="mb-[18px] space-y-2">
                <Label htmlFor="username" className="text-[13px]">用户名</Label>
                <Input id="username" name="username" autoComplete="username" placeholder="请输入用户名" className="h-11 rounded-[9px] bg-card px-3.5 shadow-none" value={username} onChange={(e) => setUsername(e.target.value)} autoFocus />
              </div>
              <div className="mb-[18px] space-y-2">
                <Label htmlFor="password" className="text-[13px]">密码</Label>
                <div className="relative">
                  <Input id="password" name="password" type={showPassword ? "text" : "password"} autoComplete="current-password" placeholder="请输入密码" className="h-11 rounded-[9px] bg-card pl-3.5 pr-12 shadow-none" value={password} onChange={(e) => setPassword(e.target.value)} />
                  <button type="button" aria-label={showPassword ? "隐藏密码" : "显示密码"} aria-pressed={showPassword} onClick={() => setShowPassword((value) => !value)} className="absolute right-1.5 top-1 flex size-9 items-center justify-center rounded-md text-fg-secondary hover:bg-accent hover:text-foreground">
                    {showPassword ? <EyeOff size={18} aria-hidden="true" /> : <Eye size={18} aria-hidden="true" />}
                  </button>
                </div>
              </div>
              {error && <p role="alert" className="mb-3 text-sm text-destructive">{error}</p>}
              <Button type="submit" className="mt-1.5 h-11 w-full gap-2.5 rounded-[9px] font-semibold" disabled={loading}>
                {loading ? "登录中…" : "登录"}
                {!loading && <ArrowRight size={16} aria-hidden="true" />}
              </Button>
              <p className="mt-5 text-center text-xs text-fg-secondary">如需开通账户，请联系管理员</p>
            </form>
          </div>
        </section>
      </div>
    </main>
  )
}