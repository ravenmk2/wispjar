import { Bot } from "lucide-react"
import { EmptyState } from "@/components/common"

export default function AgentsPlaceholderPage() {
  return (
    <EmptyState
      className="border-none py-24"
      icon={<Bot className="size-10" />}
      title="智能体管理即将上线"
      description="该功能将在里程碑 M3 提供"
    />
  )
}
