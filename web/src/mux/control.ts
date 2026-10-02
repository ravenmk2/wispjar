import { mux } from "@/mux/connection"
import { useSessionListStore } from "@/stores/sessionList"
import type { ControlFrame } from "@/lib/types"

export interface ControlHandle {
  streamId: string
  close: () => void
}

export function followControl(orgId: string): ControlHandle {
  const store = useSessionListStore
  store.getState().reset()

  const streamId = mux.openStream(
    "control",
    () => ({ orgId }),
    {
      onItem: (value) => {
        const frame = value as ControlFrame
        if (frame.type === "baseline") {
          store.getState().applyBaseline(frame.sessions)
        } else if (frame.type === "upsert") {
          store.getState().applyUpsert(frame.session)
        } else if (frame.type === "remove") {
          store.getState().remove(frame.sessionId)
        }
      },
      onError: () => {},
      onEnd: () => {},
    },
  )

  return {
    streamId,
    close: () => mux.closeStream(streamId),
  }
}
