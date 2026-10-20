import { computerFailure, type Computer } from "@/lib/computers"

export function ComputerFailure({ computer }: { computer: Pick<Computer, "last_error" | "error_detail" | "status"> }) {
  const detail = computerFailure(computer)
  if (!detail) return null
  return <div role="alert" className="w-full space-y-1 rounded-md border border-destructive/20 bg-destructive/5 p-3 text-sm">
    {computer.status === "recovering" && <p className="font-medium">恢复尚未完成，电脑暂不可用</p>}
    <p className="text-destructive">{detail.message}</p>
    <p className="text-muted-foreground">{detail.suggestion}</p>
    <details className="text-xs text-muted-foreground">
      <summary className="w-fit cursor-pointer py-1">排查信息</summary>
      <div className="space-y-1 break-all pt-1"><p>错误码：<code>{detail.code}</code></p>{detail.operation_id && <p>操作编号：<code>{detail.operation_id}</code></p>}</div>
    </details>
  </div>
}
