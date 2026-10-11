/** @type {import('tailwindcss').Config} */
export default {
  darkMode: ["class"],
  content: ["./index.html", "./src/**/*.{ts,tsx}"],
  theme: {
    container: {
      center: true,
      padding: "2rem",
      screens: {
        "2xl": "1400px",
      },
    },
    extend: {
      colors: {
        border: "var(--border)",
        divider: "var(--divider)",
        input: "hsl(var(--input))",
        ring: "hsl(var(--ring))",
        background: "hsl(var(--background))",
        foreground: "var(--foreground)",
        primary: {
          DEFAULT: "hsl(var(--primary))",
          foreground: "hsl(var(--primary-foreground))",
        },
        secondary: {
          DEFAULT: "hsl(var(--secondary))",
          foreground: "var(--foreground)",
        },
        destructive: {
          DEFAULT: "hsl(var(--destructive))",
          foreground: "hsl(var(--destructive-foreground))",
        },
        muted: {
          DEFAULT: "hsl(var(--muted))",
          foreground: "var(--text-secondary)",
        },
        accent: {
          DEFAULT: "var(--accent)",
          foreground: "var(--foreground)",
        },
        selected: "var(--selected)",
        popover: {
          DEFAULT: "var(--popover)",
          foreground: "var(--foreground)",
        },
        card: {
          DEFAULT: "hsl(var(--card))",
          foreground: "var(--foreground)",
        },
        sidebar: "var(--sidebar)",
        bubble: "var(--bubble)",
        success: "var(--success)",
        warning: "var(--warning)",
        "fg-secondary": "var(--text-secondary)",
        "fg-tertiary": "var(--text-tertiary)",
        "fg-caption": "var(--text-caption)",
        tooltip: "var(--tooltip-bg)",
        toast: "var(--toast-bg)",
      },
      boxShadow: {
        input: "var(--shadow-input)",
        menu: "var(--shadow-menu)",
      },
      borderRadius: {
        lg: "var(--radius)",
        md: "calc(var(--radius) - 2px)",
        sm: "calc(var(--radius) - 4px)",
      },
      fontFamily: {
        sans: [
          "-apple-system",
          "BlinkMacSystemFont",
          "Segoe UI",
          "system-ui",
          "Roboto",
          "PingFang SC",
          "Source Han Sans SC",
          "Microsoft YaHei UI",
          "Microsoft YaHei",
          "Noto Sans CJK SC",
          "Helvetica Neue",
          "Arial",
          "sans-serif",
        ],
        mono: ["Geist Mono", "SF Mono", "Menlo", "JetBrains Mono", "Fira Code", "Consolas", "DejaVu Sans Mono", "monospace"],
      },
    },
  },
  plugins: [require("tailwindcss-animate")],
}
