import { useNavigate } from "react-router"
import { safeWorkspaceLocation, workspaceLocationKey } from "@/lib/navigation"
import { useIdentityStore } from "@/stores/identity"

export function useReturnToWorkspace() {
  const navigate = useNavigate()

  return () => {
    const { user, orgs, currentOrgId, setOrg } = useIdentityStore.getState()
    if (!user) { navigate("/"); return }
    const currentOrg = orgs.find((org) => org.id === currentOrgId) ?? orgs[0]
    const previousOrgId = sessionStorage.getItem(`wispjar-workspace-org:${user.id}`)
    const previousOrg = orgs.find((org) => org.id === previousOrgId && !org.is_platform)
    const target = currentOrg?.is_platform && previousOrg ? previousOrg : currentOrg
    if (!target) { navigate("/"); return }
    if (target.id !== currentOrg?.id) setOrg(target.id)
    navigate(safeWorkspaceLocation(sessionStorage.getItem(workspaceLocationKey(user.id, target.id))))
  }
}
