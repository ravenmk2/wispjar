import { createHash, randomUUID } from "node:crypto"
import { createReadStream, createWriteStream } from "node:fs"
import { mkdir, readFile, rename, rm, writeFile } from "node:fs/promises"
import { resolve, join } from "node:path"
import { fileURLToPath } from "node:url"
import { Readable } from "node:stream"
import { pipeline } from "node:stream/promises"
import { setTimeout as delay } from "node:timers/promises"
import { unzipSync } from "fflate"

const webRoot = fileURLToPath(new URL("../", import.meta.url))
const digest = (data) => createHash("sha256").update(data).digest("hex")

async function validFile(path, expected) {
  try {
    const hash = createHash("sha256")
    for await (const chunk of createReadStream(path)) hash.update(chunk)
    return hash.digest("hex") === expected
  } catch (error) {
    if (error.code === "ENOENT") return false
    throw error
  }
}

async function cachedDownload(resource, cacheDir, offline, fetchImpl) {
  const target = join(cacheDir, resource.sha256)
  if (await validFile(target, resource.sha256)) return target
  if (offline) throw new Error(`MiSans offline cache missing or corrupt: ${target}`)
  for (let attempt = 1; attempt <= 3; attempt++) {
    const temporary = `${target}.${randomUUID()}.tmp`
    try {
      console.log(`Downloading MiSans resource (attempt ${attempt}): ${resource.url}`)
      const response = await fetchImpl(resource.url, { signal: AbortSignal.timeout(300_000) })
      if (!response.ok || !response.body) throw new Error(`HTTP ${response.status}`)
      await pipeline(Readable.fromWeb(response.body), createWriteStream(temporary, { flags: "wx" }))
      if (!await validFile(temporary, resource.sha256)) throw new Error("SHA-256 mismatch; check the pinned font source before updating fonts.lock.json")
      await rename(temporary, target)
      return target
    } catch (error) {
      if (attempt === 3) throw new Error(`Cannot prepare MiSans: ${resource.url}`, { cause: error })
      await delay(attempt * 1000)
    } finally {
      await rm(temporary, { force: true })
    }
  }
}

export async function prepareFonts({
  manifest,
  cacheDir = process.env.WISPJAR_FONT_CACHE ?? join(webRoot, ".cache/fonts"),
  outputDir = join(webRoot, "src/generated/fonts"),
  offline = process.env.WISPJAR_FONTS_OFFLINE === "1",
  fetchImpl = fetch,
} = {}) {
  manifest ??= JSON.parse(await readFile(join(webRoot, "scripts/fonts.lock.json"), "utf8"))
  await mkdir(cacheDir, { recursive: true })
  const archive = await cachedDownload(manifest.archive, cacheDir, offline, fetchImpl)
  const license = await cachedDownload(manifest.license, cacheDir, offline, fetchImpl)
  const entries = new Set(manifest.fonts.map((font) => font.entry))
  const files = unzipSync(await readFile(archive), { filter: (entry) => entries.has(entry.name) })
  // Validate every selected file before emitting any build inputs. Never modify the fonts.
  for (const font of manifest.fonts) {
    if (!files[font.entry] || digest(files[font.entry]) !== font.sha256) {
      throw new Error(`MiSans font missing or corrupt: ${font.entry}`)
    }
  }
  await mkdir(outputDir, { recursive: true })
  for (const font of manifest.fonts) await writeFile(join(outputDir, font.file), files[font.entry])
  await writeFile(join(outputDir, "MiSans-License.pdf"), await readFile(license))
  const css = manifest.fonts.map((font) => `@font-face {
  font-family: "MiSans";
  src: url("./${font.file}") format("woff2");
  font-style: normal;
  font-weight: ${font.weight};
  font-display: swap;
}`).join("\n\n")
  await writeFile(join(outputDir, "fonts.css"), `${css}\n`)
  console.log(`MiSans ready: ${manifest.snapshot} (${offline ? "offline cache" : "verified cache/download"})`)
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  await prepareFonts()
}
