import { useEffect } from "react"
import { RouterProvider } from "react-router"
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

export default function App() {
  useMuxLifecycle()
  useAutoHideScrollbars()
  return (
    <TooltipProvider delayDuration={300}>
      <RouterProvider router={router} />
      <Toaster position="top-center" richColors closeButton />
    </TooltipProvider>
  )
}
