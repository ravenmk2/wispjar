import { sessionsApi } from "@/api/sessions"
import { mux } from "@/mux/connection"
import { useSessionStore } from "@/stores/session"
import type { FollowFrame, SessionEvent } from "@/lib/types"

export interface FollowHandle {
  streamId: string
  close: () => void
}

export function followSession(orgId: string, sessionId: string): FollowHandle {
  const store = useSessionStore
  store.getState().reset()
  store.getState().setFollowStatus("connecting")

  let filling = false
  let closed = false

  async function fillGap() {
    if (filling || closed) return
    filling = true
    try {
      for (;;) {
        const local = store.getState().lastSeq
        const res = await sessionsApi.events(sessionId, local)
        if (closed) return
        if (res.items.length === 0) break
        store.getState().appendEvents(res.items)
        if (store.getState().lastSeq >= res.last_seq || res.items.length < 500) break
      }
    } catch {
      store.getState().setFollowStatus("stalled")
    } finally {
      filling = false
    }
  }

  function onFrame(value: unknown) {
    const frame = value as FollowFrame
    if (frame.type === "snapshot") {
      store.getState().applySnapshot(sessionId, frame.header, frame.records, frame.hasMore, frame.assistantStream)
      void store.getState().loadHistory()
      return
    }
    if (frame.type === "event") {
      const event = frame.event as SessionEvent
      const lastSeq = store.getState().lastSeq
      if (event.seq <= lastSeq) return
      if (event.seq !== lastSeq + 1) {
        void fillGap()
        return
      }
      store.getState().appendEvents([event])
      return
    }
    if (frame.type === "assistant-stream") {
      store.getState().applyStreamFrame(frame.frame)
      return
    }
    if (frame.type === "tool-output") {
      store.getState().appendToolOutput(frame.callId, frame.stream, frame.delta)
    }
  }

  const streamId = mux.openStream(
    "session.follow",
    () => ({ orgId, sessionId, afterSeq: store.getState().lastSeq }),
    {
      onItem: onFrame,
      onError: () => store.getState().setFollowStatus("stalled"),
      onEnd: () => {
        if (!closed) store.getState().setFollowStatus("stalled")
      },
    },
  )

  const offStatus = mux.onStatusChange((status) => {
    if (closed) return
    if (status === "reconnecting" || status === "closed") {
      store.getState().setFollowStatus("stalled")
    } else if (status === "connecting") {
      store.getState().setFollowStatus("connecting")
    }
  })

  return {
    streamId,
    close: () => {
      closed = true
      offStatus()
      mux.closeStream(streamId)
    },
  }
}
