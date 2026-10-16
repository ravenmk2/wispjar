import assert from "node:assert/strict"
import test from "node:test"
import { cn } from "./utils"

test("semantic reading sizes coexist with text colors in either order", () => {
  assert.equal(cn("text-chat text-foreground"), "text-chat text-foreground")
  assert.equal(cn("text-fg-caption text-meta"), "text-fg-caption text-meta")
})

test("reading sizes can override or be overridden without losing the text color", () => {
  assert.equal(cn("text-sm text-fg-secondary", "text-chat"), "text-fg-secondary text-chat")
  assert.equal(cn("text-meta text-fg-caption", "text-sm text-foreground"), "text-sm text-foreground")
})
