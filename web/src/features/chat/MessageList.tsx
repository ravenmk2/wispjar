import { useEffect, useMemo, useRef } from "react"
import { NodeList, OptimisticBubble } from "@/features/chat/NodeList"
import { deriveConversationNodes } from "@/lib/conversation"
import { useComposerStore } from "@/stores/composer"
import { useSessionStore } from "@/stores/session"

export function MessageList({ sessionId }: { sessionId: string }) {
  const events = useSessionStore((s) => s.events)
  const liveAttempt = useSessionStore((s) => s.liveAttempt)
  const optimistic = useComposerStore((s) => s.optimistic)
  const containerRef = useRef<HTMLDivElement>(null)

  const nodes = useMemo(() => deriveConversationNodes(events, liveAttempt), [events, liveAttempt])

  useEffect(() => {
    const el = containerRef.current
    if (el) el.scrollTop = el.scrollHeight
  }, [nodes, optimistic])

  return (
    <div ref={containerRef} className="min-h-0 flex-1 overflow-y-auto">
      <div className="mx-auto max-w-3xl space-y-3 px-4 py-6">
        <NodeList nodes={nodes} sessionId={sessionId} />
        {optimistic && <OptimisticBubble content={optimistic.content} />}
      </div>
    </div>
  )
}
