import { useEffect } from "react"
import { RouterProvider } from "react-router"
import { authApi } from "@/api/auth"
import { Toaster } from "@/components/ui/sonner"
import { TooltipProvider } from "@/components/ui/tooltip"
import { mux } from "@/mux/connection"
import { router } from "@/routes"
import { useIdentityStore } from "@/stores/identity"
import { useAutoHideScrollbars } from "@/hooks/useAutoHideScrollbars"

function useMuxLifecycle() {
  useEffect(() => {
    const apply = (token: string | null) => {
      if (token) mux.connect(token)
      else mux.disconnect()
    }
    apply(useIdentityStore.getState().token)
    return useIdentityStore.subscribe((s) => apply(s.token))
  }, [])
}

function useRefreshIdentity() {
  const token = useIdentityStore((s) => s.token)
  useEffect(() => {
    if (!token) return
    let cancelled = false
    void authApi.me().then((me) => {
      const identity = useIdentityStore.getState()
      if (!cancelled && identity.token === token) identity.setIdentity(me.user, me.orgs)
    }).catch(() => {
      // Keep cached identity on network failures; the API client handles expired tokens.
    })
    return () => { cancelled = true }
  }, [token])
}

export default function App() {
  useRefreshIdentity()
  useMuxLifecycle()
  useAutoHideScrollbars()
  return (
    <TooltipProvider delayDuration={300}>
      <RouterProvider router={router} />
      <Toaster position="top-center" richColors closeButton />
    </TooltipProvider>
  )
}
