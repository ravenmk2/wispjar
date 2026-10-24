import assert from "node:assert/strict"
import test from "node:test"
import { previewDirectory, resolvePreviewReference } from "./file-preview"

const origin = "https://localhost"
const files = "/api/v1/previews/token/files/"
const entry = `${files}docs/readme.md`
const resolve = (url: string) => resolvePreviewReference(url, entry, files, origin)

test("preview resolves nested and parent references inside the granted directory", () => {
  assert.equal(resolve("./images/a.png")?.url, `${origin}${files}docs/images/a.png`)
  assert.equal(resolve("../assets/中文 图.svg?v=2#diagram")?.path, "assets/中文 图.svg")
  assert.equal(resolve("../assets/a.svg?v=2#diagram")?.url, `${origin}${files}assets/a.svg?v=2#diagram`)
  assert.equal(resolve("other.md#section")?.fragment, "#section")
})

test("preview rejects escapes, application-root URLs, malformed encodings and unsafe protocols", () => {
  for (const value of ["../../secret", "../../../files/secret", "/api/v1/auth/me", "..\\secret", "javascript:alert(1)", "data:text/html,x", "file:///etc/passwd", "%zz.png", "%2e%2e/%2e%2e/secret", "a%00.png", "a%5c.png"]) assert.equal(resolve(value), undefined, value)
  assert.equal(resolve("https://example.com/a.png")?.url, "https://example.com/a.png")
  assert.equal(resolve("//example.com/a.png")?.url, "//example.com/a.png")
  assert.equal(resolve("#section")?.url, "#section")
})

test("scope-root preview resolves parent references and keeps local navigation paths", () => {
  const nestedEntry = `${files}outputs/site/docs/readme.md`
  const resolveNested = (url: string) => resolvePreviewReference(url, nestedEntry, files, origin)
  assert.equal(resolveNested("../assets/check.svg")?.path, "outputs/site/assets/check.svg")
  assert.equal(resolveNested("../../../shared/check.svg")?.path, "shared/check.svg")
  assert.equal(resolveNested("next.md")?.path, "outputs/site/docs/next.md")
  assert.equal(resolveNested("../../../../other-scope/secret"), undefined)
  assert.equal(previewDirectory("readme.md"), ".")
  assert.equal(previewDirectory("outputs/site/docs/readme.md"), "outputs/site/docs")
})
