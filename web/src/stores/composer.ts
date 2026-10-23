import { create } from "zustand"
import type { ApprovalMode } from "@/lib/types"

interface OptimisticMessage {
  requestId: string
  content: string
  attachments?: Array<{ url: string; name: string }>
}

interface ComposerState {
  draft: string
  contextKey: string
  drafts: Record<string, string>
  approvalMode: ApprovalMode
  sending: boolean
  optimistic: OptimisticMessage | null
  setDraft: (draft: string) => void
  setApprovalMode: (mode: ApprovalMode) => void
  setSending: (sending: boolean) => void
  setOptimistic: (optimistic: OptimisticMessage) => void
  clearOptimistic: () => void
  reset: () => void
  enterContext: (key: string) => void
}

export const useComposerStore = create<ComposerState>()((set) => ({
  draft: "",
  contextKey: "",
  drafts: {},
  approvalMode: "auto",
  sending: false,
  optimistic: null,
  setDraft: (draft) => set({ draft }),
  setApprovalMode: (approvalMode) => set({ approvalMode }),
  setSending: (sending) => set({ sending }),
  setOptimistic: (optimistic) => set({ optimistic }),
  clearOptimistic: () => set({ optimistic: null }),
  enterContext: (contextKey) => set((state) => {
    if (state.contextKey === contextKey) return {}
    const drafts = { ...state.drafts }
    if (state.contextKey) drafts[state.contextKey] = state.draft
    return { contextKey, drafts, draft: drafts[contextKey] ?? "", approvalMode: "auto", sending: false, optimistic: null }
  }),
  reset: () => set({ draft: "", contextKey: "", drafts: {}, approvalMode: "auto", sending: false, optimistic: null }),
}))
