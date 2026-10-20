import { create } from "zustand"

interface ChatPresentationState {
  sessionId: string | null
  expanded: Record<string, boolean>
  preferences: Record<string, boolean>
  enterSession: (sessionId: string) => void
  setExpanded: (key: string, expanded: boolean) => void
  setPreference: (key: string, value: boolean) => void
}

// Presentation only: reconnects and tab changes retain overrides; another session starts fresh.
export const useChatPresentationStore = create<ChatPresentationState>()((set) => ({
  sessionId: null,
  expanded: {},
  preferences: {},
  enterSession: (sessionId) => set((s) => s.sessionId === sessionId ? s : { sessionId, expanded: {}, preferences: {} }),
  setExpanded: (key, expanded) => set((s) => ({ expanded: { ...s.expanded, [key]: expanded } })),
  setPreference: (key, value) => set((s) => ({ preferences: { ...s.preferences, [key]: value } })),
}))

export function useChatDisclosure(sessionId: string, key: string, defaultOpen = false) {
  const override = useChatPresentationStore((s) => !sessionId || s.sessionId === sessionId ? s.expanded[key] : undefined)
  const setExpanded = useChatPresentationStore((s) => s.setExpanded)
  return [override ?? defaultOpen, (open: boolean) => setExpanded(key, open)] as const
}

export function useChatPreference(sessionId: string, key: string) {
  const value = useChatPresentationStore((s) => s.sessionId === sessionId ? s.preferences[key] : undefined)
  const setPreference = useChatPresentationStore((s) => s.setPreference)
  return [value ?? false, (value: boolean) => setPreference(key, value)] as const
}
