// Fixed SVG geometry keeps the dots independent of text glyphs and font size.
export function ThinkingDots() {
  return <svg className="thinking-dots" width="18" height="18" viewBox="0 0 18 18" fill="currentColor" aria-hidden="true">
    <circle cx="3" cy="9" r="1.5" />
    <circle cx="9" cy="9" r="1.5" />
    <circle cx="15" cy="9" r="1.5" />
  </svg>
}
