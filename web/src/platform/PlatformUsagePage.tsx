import { platformUsageApi } from "@/api/reports"
import { UsageReport, type UsageGroupOption } from "@/features/usage/UsageReport"

const GROUP_OPTIONS: UsageGroupOption[] = [
  { value: "org", label: "按机构" },
  { value: "user", label: "按用户" },
  { value: "agent", label: "按智能体" },
  { value: "model", label: "按模型" },
  { value: "day", label: "按日" },
]

export default function PlatformUsagePage() {
  return (
    <UsageReport
      title="用量"
      description="全平台 token 用量统计"
      groupOptions={GROUP_OPTIONS}
      defaultGroupBy="org"
      query={platformUsageApi.query}
    />
  )
}
