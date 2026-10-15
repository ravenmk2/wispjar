import { build } from "esbuild"
import { mkdtemp, rm } from "node:fs/promises"
import { tmpdir } from "node:os"
import { join, resolve, sep } from "node:path"
import { spawnSync } from "node:child_process"

const dir = await mkdtemp(join(tmpdir(), "wispjar-web-test-"))
try {
  const outfile = join(dir, "conversation.test.cjs")
  await build({ entryPoints: ["src/lib/conversation.test.ts"], bundle: true, platform: "node", format: "cjs", outfile })
  const result = spawnSync(process.execPath, ["--test", outfile], { stdio: "inherit" })
  process.exitCode = result.status ?? 1
} finally {
  if (!resolve(dir).startsWith(resolve(tmpdir()) + sep)) throw new Error("Unexpected test output path")
  await rm(dir, { recursive: true, force: true })
}
