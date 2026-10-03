import { Toaster as Sonner } from "sonner"

type ToasterProps = React.ComponentProps<typeof Sonner>

const Toaster = ({ ...props }: ToasterProps) => {
  return (
    <Sonner
      theme="light"
      duration={3000}
      className="toaster group"
      toastOptions={{
        style: {
          background: "var(--toast-bg)",
          color: "#fff",
          border: "1px solid rgba(255, 255, 255, 0.08)",
          borderRadius: 10,
        },
        classNames: {
          toast: "group toast",
          description: "group-[.toast]:text-white/70",
          closeButton: "group-[.toast]:bg-toast group-[.toast]:text-white group-[.toast]:border-white/20",
          actionButton: "group-[.toast]:bg-primary group-[.toast]:text-primary-foreground",
          cancelButton: "group-[.toast]:bg-white/10 group-[.toast]:text-white",
        },
      }}
      {...props}
    />
  )
}

export { Toaster }
