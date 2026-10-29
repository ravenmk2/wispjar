import { test } from "node:test"
import assert from "node:assert/strict"
import { createHash } from "node:crypto"
import { mkdtemp, readFile, rm, writeFile } from "node:fs/promises"
import { tmpdir } from "node:os"
import { join } from "node:path"
import { zipSync } from "fflate"
import { prepareFonts } from "./prepare-fonts.mjs"

const sha = (bytes) => createHash("sha256").update(bytes).digest("hex")

test("downloads once, rebuilds offline, rejects damaged cache and font entries", async () => {
  const root = await mkdtemp(join(tmpdir(), "wispjar-fonts-"))
  try {
    const font = Buffer.from("fixture font bytes")
    const license = Buffer.from("fixture license")
    const archive = zipSync({ "fonts/Regular.woff2": font, "unneeded.ttf": Buffer.from("unused") })
    const manifest = {
      snapshot: "test",
      archive: { url: "https://localhost/fonts.zip", sha256: sha(archive) },
      license: { url: "https://localhost/license.pdf", sha256: sha(license) },
      fonts: [{ entry: "fonts/Regular.woff2", file: "Regular.woff2", weight: 400, sha256: sha(font) }],
    }
    const options = { manifest, cacheDir: join(root, "cache"), outputDir: join(root, "output"), offline: false }
    let requests = 0
    const fetchImpl = async (url) => {
      requests++
      return new Response(url === manifest.archive.url ? archive : license)
    }
    await prepareFonts({ ...options, fetchImpl })
    assert.equal(requests, 2)
    assert.deepEqual(await readFile(join(options.outputDir, "Regular.woff2")), font)
    assert.deepEqual(await readFile(join(options.outputDir, "MiSans-License.pdf")), license)
    const noNetwork = () => { throw new Error("Unexpected network access") }
    await rm(options.outputDir, { recursive: true })
    await prepareFonts({ ...options, offline: true, fetchImpl: noNetwork })
    assert.match(await readFile(join(options.outputDir, "fonts.css"), "utf8"), /font-display: swap/)
    await prepareFonts({ ...options, offline: false, fetchImpl: noNetwork })
    const badManifest = { ...manifest, fonts: [{ ...manifest.fonts[0], sha256: "0".repeat(64) }] }
    await assert.rejects(prepareFonts({ ...options, manifest: badManifest, offline: true }), /font missing or corrupt/)
    await writeFile(join(options.cacheDir, manifest.archive.sha256), "corrupt")
    await assert.rejects(prepareFonts({ ...options, offline: true }), /offline cache missing or corrupt/)
    await prepareFonts({ ...options, fetchImpl })
    assert.equal(requests, 3)
    await rm(join(options.cacheDir, manifest.license.sha256))
    await assert.rejects(prepareFonts({ ...options, offline: true }), /offline cache missing or corrupt/)
  } finally {
    await rm(root, { recursive: true, force: true })
  }
})
