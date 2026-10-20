import test from "node:test"
import assert from "node:assert/strict"
import { envPatch, envRowsError } from "./env"

test("masked variables are preserved, deletion and explicit empty values are distinct", () => {
  assert.deepEqual(envPatch([{ key: "SECRET" }, { key: "EMPTY", value: "" }, { key: "NEW", value: "value" }], ["SECRET", "EMPTY", "OLD"]), {
    set: { EMPTY: "", NEW: "value" }, unset: ["OLD"],
  })
})

test("environment form rejects duplicate and reserved keys, NUL and oversized UTF-8 values", () => {
  assert.ok(envRowsError([{ key: "A" }, { key: " A " }]))
  for (const key of ["HOME", "WISPJAR", "EXECD_ACCESS_TOKEN", "1BAD"]) assert.ok(envRowsError([{ key, value: "x" }]))
  assert.ok(envRowsError([{ key: "A", value: "\0" }]))
  assert.ok(envRowsError([{ key: "A", value: "中".repeat(1400) }]))
  assert.equal(envRowsError([{ key: "__proto__", value: "value" }]), null)
  assert.equal(JSON.stringify(envPatch([{ key: "__proto__", value: "value" }], []).set), '{"__proto__":"value"}')
})
