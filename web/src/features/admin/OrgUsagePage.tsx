import { useCallback } from "react"
import { orgsApi } from "@/api/auth"
import type { UsageQueryParams } from "@/api/reports"
import { UsageReport, type UsageGroupOption } from "@/features/usage/UsageReport"
import { useIdentityStore } from "@/stores/identity"

const GROUP_OPTIONS: UsageGroupOption[] = [
  { value: "user", label: "按用户" },
  { value: "agent", label: "按智能体" },
  { value: "model", label: "按模型" },
  { value: "day", label: "按日" },
]

export default function OrgUsagePage() {
  const currentOrgId = useIdentityStore((s) => s.currentOrgId)

  const query = useCallback(
    (params: UsageQueryParams) => orgsApi.usage(currentOrgId ?? "", params),
    [currentOrgId],
  )

  if (!currentOrgId) return null

  return <UsageReport title="用量" description="本机构 token 用量统计" groupOptions={GROUP_OPTIONS} query={query} />
}
