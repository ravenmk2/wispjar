import { useEffect } from "react"
import { RouterProvider } from "react-router"
import { Toaster } from "@/components/ui/sonner"
import { TooltipProvider } from "@/components/ui/tooltip"
import { mux } from "@/mux/connection"
import { router } from "@/routes"
import { useIdentityStore } from "@/stores/identity"

function useSystemTheme() {
  useEffect(() => {
    const mq = window.matchMedia("(prefers-color-scheme: dark)")
    const apply = () => document.documentElement.classList.toggle("dark", mq.matches)
    apply()
    mq.addEventListener("change", apply)
    return () => mq.removeEventListener("change", apply)
  }, [])
}

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
  useSystemTheme()
  useMuxLifecycle()
  return (
    <TooltipProvider delayDuration={300}>
      <RouterProvider router={router} />
      <Toaster position="top-center" richColors closeButton />
    </TooltipProvider>
  )
}
