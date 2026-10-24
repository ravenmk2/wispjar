// Browser URL resolution preserves CSS/module-relative paths on the backend
// resource route. Keep local references inside the granted files/ subtree.
export function resolvePreviewReference(reference: string, entryUrl: string, filesUrl: string, origin: string): { url: string; path?: string; fragment?: string } | undefined {
  if (!reference) return undefined
  if (reference.startsWith("#")) return { url: reference }
  if (/^(?:https?:)?\/\//i.test(reference) || /^(?:mailto|tel):/i.test(reference)) return { url: reference }
  // Site-root URLs do not carry the resource token. Do not send them to app APIs.
  if (reference.startsWith("/") || reference.includes("\\") || /^[a-z][a-z\d+.-]*:/i.test(reference)) return undefined
  try {
    const base = new URL(filesUrl, origin)
    const url = new URL(reference, new URL(entryUrl, origin))
    if (url.origin !== base.origin || !url.pathname.startsWith(base.pathname)) return undefined
    const encoded = url.pathname.slice(base.pathname.length)
    const path = encoded.split("/").map(decodeURIComponent).join("/")
    if (path.split("/").some((part) => part === "..") || path.includes("\\") || path.includes("\0")) return undefined
    return { url: url.href, path, fragment: url.hash }
  } catch { return undefined }
}

export function previewDirectory(path: string): string {
  const index = path.lastIndexOf("/")
  return index < 0 ? "." : path.slice(0, index)
}

export function previewRoots(path: string): string[] {
  const parts = path.split("/").slice(0, -1)
  return [...parts.map((_, index) => parts.slice(0, parts.length - index).join("/")), "."]
}

export function previewEntry(path: string, root: string): string {
  return root === "." ? path : path.slice(root.length + 1)
}
