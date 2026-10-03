import { create } from "zustand"
import type { ApprovalMode } from "@/lib/types"

interface OptimisticMessage {
  requestId: string
  content: string
}

interface ComposerState {
  draft: string
  approvalMode: ApprovalMode
  sending: boolean
  optimistic: OptimisticMessage | null
  setDraft: (draft: string) => void
  setApprovalMode: (mode: ApprovalMode) => void
  setSending: (sending: boolean) => void
  setOptimistic: (optimistic: OptimisticMessage) => void
  clearOptimistic: () => void
  reset: () => void
}

export const useComposerStore = create<ComposerState>()((set) => ({
  draft: "",
  approvalMode: "auto",
  sending: false,
  optimistic: null,
  setDraft: (draft) => set({ draft }),
  setApprovalMode: (approvalMode) => set({ approvalMode }),
  setSending: (sending) => set({ sending }),
  setOptimistic: (optimistic) => set({ optimistic }),
  clearOptimistic: () => set({ optimistic: null }),
  reset: () => set({ draft: "", approvalMode: "auto", sending: false, optimistic: null }),
}))
