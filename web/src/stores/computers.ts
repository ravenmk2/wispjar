import { create } from "zustand"
import type { Computer } from "@/lib/computers"

interface ComputerState {
  orgId: string
  items: Computer[]
  reset: (org: string) => void
  baseline: (org: string, items: Computer[]) => void
  upsert: (org: string, item: Computer) => void
  remove: (org: string, id: string) => void
}
export const useComputerStore = create<ComputerState>((set) => ({
  orgId: "", items: [],
  reset: (orgId) => set({ orgId, items: [] }),
  baseline: (org, items) => set((s) => s.orgId === org ? { items: items.filter((c) => c.org_id === org) } : {}),
  upsert: (org, item) => set((s) => s.orgId === org && item.org_id === org ? { items: [...s.items.filter((c) => c.id !== item.id), item] } : {}),
  remove: (org, id) => set((s) => s.orgId === org ? { items: s.items.filter((c) => c.id !== id) } : {}),
}))
