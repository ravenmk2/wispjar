import { mux } from "@/mux/connection"
import { useSessionListStore } from "@/stores/sessionList"
import { useComputerStore } from "@/stores/computers"
import type { ControlFrame } from "@/lib/types"

export interface ControlHandle {
  streamId: string
  close: () => void
}

export function followControl(orgId: string): ControlHandle {
  const store = useSessionListStore
  store.getState().reset()
  useComputerStore.getState().reset(orgId)

  const streamId = mux.openStream(
    "control",
    () => ({ orgId }),
    {
      onItem: (value) => {
        const frame = value as ControlFrame
        if (frame.type === "baseline") {
          store.getState().applyBaseline(frame.sessions)
          if (frame.computers) useComputerStore.getState().baseline(orgId, frame.computers)
        } else if (frame.type === "upsert") {
          store.getState().applyUpsert(frame.session)
        } else if (frame.type === "remove") {
          store.getState().remove(frame.sessionId)
        } else if (frame.type === "computer/upsert") {
          useComputerStore.getState().upsert(orgId, frame.computer)
        } else if (frame.type === "computer/remove") {
          useComputerStore.getState().remove(orgId, frame.computerId)
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
