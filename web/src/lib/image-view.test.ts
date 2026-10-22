import assert from "node:assert/strict"
import test from "node:test"
import { clampImageView, fitImageScale, zoomImageView } from "./image-view"

test("opening fits oversized images but never enlarges small images", () => {
  assert.equal(fitImageScale({ width: 512, height: 288 }, { width: 1000, height: 600 }), 1)
  assert.equal(fitImageScale({ width: 240, height: 1440 }, { width: 1000, height: 600 }), 600 / 1440)
})

test("wheel zoom keeps the image point under the pointer stationary", () => {
  const original = { scale: 1, x: 30, y: -20 }
  const anchor = { x: 120, y: 80 }
  const next = zoomImageView(original, 2, { width: 1600, height: 1200 }, { width: 800, height: 600 }, anchor)
  assert.equal((anchor.x - next.x) / next.scale, (anchor.x - original.x) / original.scale)
  assert.equal((anchor.y - next.y) / next.scale, (anchor.y - original.y) / original.scale)
})

test("panning reaches image edges without losing the image outside the viewport", () => {
  const image = { width: 640, height: 360 }
  const viewport = { width: 800, height: 600 }
  assert.deepEqual(clampImageView({ scale: 2, x: 9999, y: -9999 }, image, viewport), { scale: 2, x: 240, y: -60 })
  assert.deepEqual(clampImageView({ scale: 1, x: 9999, y: -9999 }, image, viewport), { scale: 1, x: 0, y: 0 })
})

test("extreme wheel input respects zoom limits and very tall images can still fit", () => {
  const image = { width: 1000, height: 1000 }, viewport = { width: 800, height: 600 }
  assert.equal(zoomImageView({ scale: 1, x: 0, y: 0 }, 1e9, image, viewport).scale, 8)
  assert.equal(zoomImageView({ scale: 1, x: 0, y: 0 }, 1e-9, image, viewport).scale, 0.1)
  const tall = { width: 1000, height: 100000 }
  assert.equal(clampImageView({ scale: 0.006, x: 0, y: 0 }, tall, viewport).scale, 0.006)
})
