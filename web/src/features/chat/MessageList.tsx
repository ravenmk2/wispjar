import { ChevronDown } from "lucide-react"
import { useEffect, useMemo, useRef, useState } from "react"
import { NodeList, OptimisticBubble } from "@/features/chat/NodeList"
import { deriveConversationNodes } from "@/lib/conversation"
import { useComposerStore } from "@/stores/composer"
import { useSessionStore } from "@/stores/session"

const STICK_THRESHOLD = 80

export function MessageList({ sessionId }: { sessionId: string }) {
  const events = useSessionStore((s) => s.events)
  const liveAttempt = useSessionStore((s) => s.liveAttempt)
  const header = useSessionStore((s) => s.header)
  const optimistic = useComposerStore((s) => s.optimistic)
  const containerRef = useRef<HTMLDivElement>(null)
  const atBottomRef = useRef(true)
  const [atBottom, setAtBottom] = useState(true)

  const nodes = useMemo(() => deriveConversationNodes(events, liveAttempt), [events, liveAttempt])

  const running = (header?.running ?? false) || liveAttempt !== null

  // 吸底跟随：已在底部或自己刚发消息时跟随新内容；上翻阅读时不拉底
  useEffect(() => {
    const el = containerRef.current
    if (!el) return
    if (optimistic || atBottomRef.current) el.scrollTop = el.scrollHeight
  }, [nodes, optimistic, running])

  function handleScroll() {
    const el = containerRef.current
    if (!el) return
    const at = el.scrollHeight - el.scrollTop - el.clientHeight < STICK_THRESHOLD
    atBottomRef.current = at
    setAtBottom(at)
  }

  function scrollToBottom() {
    const el = containerRef.current
    if (!el) return
    el.scrollTo({ top: el.scrollHeight, behavior: "smooth" })
  }

  return (
    <div className="relative min-h-0 flex-1">
      <div ref={containerRef} data-chat-scroll onScroll={handleScroll} className="h-full overflow-y-auto">
        <div className="mx-auto max-w-chat space-y-5 px-4 py-6">
          <NodeList nodes={nodes} sessionId={sessionId} />
          {optimistic && <OptimisticBubble content={optimistic.content} attachments={optimistic.attachments} />}
        </div>
      </div>
      {!atBottom && (
        <button
          type="button"
          title="回到底部"
          onClick={scrollToBottom}
          className="absolute bottom-4 right-5 flex size-8 items-center justify-center rounded-full border bg-background text-fg-secondary shadow-md transition-colors hover:text-foreground"
        >
          <ChevronDown className="size-4" />
        </button>
      )}
    </div>
  )
}
