import { create } from "zustand"

interface ChatPresentationState {
  sessionId: string | null
  expanded: Record<string, boolean>
  enterSession: (sessionId: string) => void
  setExpanded: (key: string, expanded: boolean) => void
}

// Presentation only: reconnects and tab changes retain overrides; another session starts fresh.
export const useChatPresentationStore = create<ChatPresentationState>()((set) => ({
  sessionId: null,
  expanded: {},
  enterSession: (sessionId) => set((s) => s.sessionId === sessionId ? s : { sessionId, expanded: {} }),
  setExpanded: (key, expanded) => set((s) => ({ expanded: { ...s.expanded, [key]: expanded } })),
}))

export function useChatDisclosure(sessionId: string, key: string, defaultOpen = false) {
  const override = useChatPresentationStore((s) => !sessionId || s.sessionId === sessionId ? s.expanded[key] : undefined)
  const setExpanded = useChatPresentationStore((s) => s.setExpanded)
  return [override ?? defaultOpen, (open: boolean) => setExpanded(key, open)] as const
}
