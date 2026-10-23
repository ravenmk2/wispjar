import { PageHeader } from "@/components/common"
import ChangePasswordPage from "@/pages/ChangePassword"
import { useIdentityStore } from "@/stores/identity"

export default function AccountPage() {
  const user = useIdentityStore((s) => s.user)
  return <div className="mx-auto max-w-3xl space-y-6">
    <PageHeader title="账号与安全" description="账号设置适用于你加入的所有组织。" />
    <dl className="divide-y rounded-xl border px-5 text-sm">
      <div className="flex flex-wrap justify-between gap-3 py-4"><dt className="text-muted-foreground">显示名称</dt><dd>{user?.display_name || user?.username}</dd></div>
      <div className="flex flex-wrap justify-between gap-3 py-4"><dt className="text-muted-foreground">用户名</dt><dd>{user?.username}</dd></div>
    </dl>
    <ChangePasswordPage embedded />
  </div>
}
