import type { Highlighter } from "shiki"

let highlighterPromise: Promise<Highlighter> | null = null
const loadedLangs = new Set<string>()
let bundledLangs: Record<string, unknown> | null = null

async function getHighlighter(): Promise<Highlighter> {
  if (!highlighterPromise) {
    highlighterPromise = import("shiki").then((m) => {
      bundledLangs = m.bundledLanguages as Record<string, unknown>
      return m.createHighlighter({ themes: ["github-light", "github-dark"], langs: [] })
    })
  }
  return highlighterPromise
}

export async function highlightCode(code: string, lang?: string): Promise<string | null> {
  try {
    const highlighter = await getHighlighter()
    let language = "text"
    if (lang) {
      const normalized = lang.toLowerCase()
      if (bundledLangs && normalized in bundledLangs) language = normalized
    }
    if (language !== "text" && !loadedLangs.has(language)) {
      await highlighter.loadLanguage(language as Parameters<Highlighter["loadLanguage"]>[0])
      loadedLangs.add(language)
    }
    return highlighter.codeToHtml(code, {
      lang: language,
      themes: { light: "github-light", dark: "github-dark" },
    })
  } catch {
    return null
  }
}
