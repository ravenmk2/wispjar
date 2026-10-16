import { build } from "esbuild"
import { mkdtemp, rm } from "node:fs/promises"
import { tmpdir } from "node:os"
import { join, resolve, sep } from "node:path"
import { spawnSync } from "node:child_process"

const dir = await mkdtemp(join(tmpdir(), "wispjar-web-test-"))
try {
  const tests = ["conversation", "utils"]
  await build({ entryPoints: tests.map((name) => `src/lib/${name}.test.ts`), bundle: true, platform: "node", format: "cjs", outdir: dir, outExtension: { ".js": ".cjs" } })
  const result = spawnSync(process.execPath, ["--test", ...tests.map((name) => join(dir, `${name}.test.cjs`))], { stdio: "inherit" })
  process.exitCode = result.status ?? 1
} finally {
  if (!resolve(dir).startsWith(resolve(tmpdir()) + sep)) throw new Error("Unexpected test output path")
  await rm(dir, { recursive: true, force: true })
}
