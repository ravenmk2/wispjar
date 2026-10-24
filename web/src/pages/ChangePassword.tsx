import { useEffect, useRef, useState } from "react"
import { Navigate, useNavigate } from "react-router"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { PasswordForm } from "@/features/personal/PasswordForm"
import { useIdentityStore } from "@/stores/identity"

export default function ChangePasswordPage({ embedded = false }: { embedded?: boolean }) {
  const token = useIdentityStore((s) => s.token)
  const mustChange = useIdentityStore((s) => s.user?.must_change_password)
  const navigate = useNavigate()
  const [expanded, setExpanded] = useState(false)
  const trigger = useRef<HTMLButtonElement>(null)
  const wasExpanded = useRef(false)

  useEffect(() => {
    if (!expanded && wasExpanded.current) trigger.current?.focus()
    wasExpanded.current = expanded
  }, [expanded])

  if (!token) return <Navigate to="/login" replace />

  if (embedded) return <section className="rounded-xl border bg-card" aria-labelledby="security-title">
    <div className="p-6">
      <h2 id="security-title" className="text-base font-semibold">账号安全</h2>
      <div className="mt-5 flex flex-wrap items-center justify-between gap-4">
        <div><div className="text-sm font-medium">登录密码</div><p className="mt-1 text-xs text-muted-foreground">使用独立密码，保护你的账号。</p></div>
        {!expanded && <Button ref={trigger} variant="outline" aria-expanded={false} aria-controls="password-form" onClick={() => setExpanded(true)}>修改密码</Button>}
      </div>
    </div>
    {expanded && <PasswordForm embedded onSuccess={() => setExpanded(false)} onCancel={() => setExpanded(false)} />}
  </section>

  return <div className="flex min-h-screen items-center justify-center bg-muted/40 p-4">
    <Card className="w-full max-w-sm">
      <CardHeader>
        <CardTitle className="text-lg">修改密码</CardTitle>
        <CardDescription>{mustChange ? "首次登录需要先修改密码" : "设置新的登录密码，保护你的账号。"}</CardDescription>
      </CardHeader>
      <CardContent><PasswordForm onSuccess={() => navigate("/", { replace: true })} /></CardContent>
    </Card>
  </div>
}
