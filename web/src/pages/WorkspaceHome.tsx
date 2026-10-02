import { Bot } from "lucide-react"
import { EmptyState } from "@/components/common"

export default function WorkspaceHome() {
  return (
    <div className="flex h-full items-center justify-center p-6">
      <EmptyState
        className="w-full max-w-lg border-none"
        icon={<Bot className="size-10" />}
        title="选择智能体开始对话"
        description="对话功能即将上线"
      />
    </div>
  )
}
