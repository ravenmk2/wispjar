import { useEffect } from "react"
import { RouterProvider } from "react-router"
import { Toaster } from "@/components/ui/sonner"
import { TooltipProvider } from "@/components/ui/tooltip"
import { router } from "@/routes"

function useSystemTheme() {
  useEffect(() => {
    const mq = window.matchMedia("(prefers-color-scheme: dark)")
    const apply = () => document.documentElement.classList.toggle("dark", mq.matches)
    apply()
    mq.addEventListener("change", apply)
    return () => mq.removeEventListener("change", apply)
  }, [])
}

export default function App() {
  useSystemTheme()
  return (
    <TooltipProvider delayDuration={300}>
      <RouterProvider router={router} />
      <Toaster position="top-center" richColors closeButton />
    </TooltipProvider>
  )
}
