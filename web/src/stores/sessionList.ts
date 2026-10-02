import { create } from "zustand"
import type { SessionProjection } from "@/lib/types"

interface SessionListState {
  sessions: Record<string, SessionProjection>
  order: string[]
  applyBaseline: (sessions: SessionProjection[]) => void
  applyUpsert: (session: SessionProjection) => void
  remove: (sessionId: string) => void
  reset: () => void
}

function sortedOrder(sessions: Record<string, SessionProjection>): string[] {
  return Object.values(sessions)
    .sort((a, b) => (a.lastActiveAt < b.lastActiveAt ? 1 : a.lastActiveAt > b.lastActiveAt ? -1 : a.sessionId < b.sessionId ? 1 : -1))
    .map((s) => s.sessionId)
}

export const useSessionListStore = create<SessionListState>()((set) => ({
  sessions: {},
  order: [],
  applyBaseline: (list) => {
    const sessions: Record<string, SessionProjection> = {}
    for (const s of list) sessions[s.sessionId] = s
    set({ sessions, order: sortedOrder(sessions) })
  },
  applyUpsert: (session) =>
    set((state) => {
      const sessions = { ...state.sessions, [session.sessionId]: session }
      return { sessions, order: sortedOrder(sessions) }
    }),
  remove: (sessionId) =>
    set((state) => {
      if (!(sessionId in state.sessions)) return state
      const sessions = { ...state.sessions }
      delete sessions[sessionId]
      return { sessions, order: state.order.filter((id) => id !== sessionId) }
    }),
  reset: () => set({ sessions: {}, order: [] }),
}))
