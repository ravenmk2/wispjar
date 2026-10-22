import { Loader2 } from "lucide-react"
import { useId, useRef, useState } from "react"
import { useNavigate } from "react-router"
import { toast } from "sonner"
import { errorMessage } from "@/api/client"
import { sessionsApi } from "@/api/sessions"
import { Button } from "@/components/ui/button"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"

export function ForkDialog({
  sessionId,
  seq,
  sourceTitle,
  onClose,
}: {
  sessionId: string
  seq?: number
  sourceTitle: string
  onClose: () => void
}) {
  const navigate = useNavigate()
  const titleId = useId()
  const defaultTitle = `${Array.from(sourceTitle.trim()).slice(0, 252).join("")}（副本）`
  const [title, setTitle] = useState(defaultTitle)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const submitting = useRef(false)
  const tooLong = Array.from(title.trim()).length > 256

  async function fork() {
    if (submitting.current || tooLong) return
    submitting.current = true
    setBusy(true)
    setError(null)
    try {
      const atSeq = seq ?? (await sessionsApi.events(sessionId, Number.MAX_SAFE_INTEGER)).last_seq
      const res = await sessionsApi.fork(sessionId, atSeq, title.trim() || undefined)
      toast.success("已创建聊天副本")
      onClose()
      navigate(`/c/${res.id}`)
    } catch (e) {
      setError(errorMessage(e))
    } finally {
      submitting.current = false
      setBusy(false)
    }
  }

  return (
    <Dialog open onOpenChange={(open) => !open && !submitting.current && onClose()}>
      <DialogContent className="sm:max-w-md" aria-busy={busy}>
        <form className="grid gap-5" onSubmit={(e) => { e.preventDefault(); void fork() }}>
          <DialogHeader>
            <DialogTitle>{seq === undefined ? "创建聊天副本" : "从这里开始新聊天"}</DialogTitle>
            <DialogDescription>
              {seq === undefined ? "复制当前聊天的全部记录，在新聊天中继续。" : "保留截至这条消息的聊天记录，在新聊天中继续。"}
            </DialogDescription>
          </DialogHeader>
          <div className="grid gap-2">
            <Label htmlFor={titleId}>新聊天标题</Label>
            <Input id={titleId} value={title} placeholder={defaultTitle} disabled={busy}
              aria-invalid={tooLong} aria-describedby={tooLong || error ? `${titleId}-error` : undefined}
              onChange={(e) => setTitle(e.target.value)}
              onFocus={(e) => e.target.select()}
              onKeyDown={(e) => { if (e.key === "Enter" && e.nativeEvent.isComposing) e.preventDefault() }} />
            {(tooLong || error) && <p id={`${titleId}-error`} role="alert" className="text-sm text-destructive">{tooLong ? "标题不能超过 256 个字符" : error}</p>}
          </div>
          <DialogFooter>
            <Button type="button" variant="outline" disabled={busy} onClick={onClose}>取消</Button>
            <Button type="submit" disabled={busy || tooLong}>
              {busy && <Loader2 className="animate-spin" />}
              {busy ? "创建中…" : "创建新聊天"}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
