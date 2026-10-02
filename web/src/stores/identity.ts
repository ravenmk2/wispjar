import { create } from "zustand"
import { persist } from "zustand/middleware"
import type { LoginResponse, User, UserOrg } from "@/lib/types"

interface IdentityState {
  token: string | null
  user: User | null
  orgs: UserOrg[]
  currentOrgId: string | null
  setAuth: (res: LoginResponse) => void
  setIdentity: (user: User, orgs: UserOrg[]) => void
  setToken: (token: string) => void
  setOrg: (orgId: string) => void
  clear: () => void
}

export const useIdentityStore = create<IdentityState>()(
  persist(
    (set, get) => ({
      token: null,
      user: null,
      orgs: [],
      currentOrgId: null,
      setAuth: (res) =>
        set({
          token: res.token,
          user: res.user,
          orgs: res.orgs,
          currentOrgId: pickOrgId(get().currentOrgId, res.orgs),
        }),
      setIdentity: (user, orgs) =>
        set({ user, orgs, currentOrgId: pickOrgId(get().currentOrgId, orgs) }),
      setToken: (token) => set({ token }),
      setOrg: (orgId) => set({ currentOrgId: orgId }),
      clear: () => set({ token: null, user: null, orgs: [], currentOrgId: null }),
    }),
    { name: "wispjar-identity" },
  ),
)

function pickOrgId(current: string | null, orgs: UserOrg[]): string | null {
  if (current && orgs.some((o) => o.id === current)) return current
  return orgs[0]?.id ?? null
}

export function useCurrentOrg(): UserOrg | null {
  const orgs = useIdentityStore((s) => s.orgs)
  const currentOrgId = useIdentityStore((s) => s.currentOrgId)
  return orgs.find((o) => o.id === currentOrgId) ?? orgs[0] ?? null
}
