export interface ImageSize { width: number; height: number }
export interface ImageView { scale: number; x: number; y: number }

export const MAX_IMAGE_SCALE = 8

export function fitImageScale(image: ImageSize, viewport: ImageSize): number {
  if (viewport.width <= 0 || viewport.height <= 0) return 1
  return Math.min(1, viewport.width / image.width, viewport.height / image.height)
}

export function clampImageView(view: ImageView, image: ImageSize, viewport: ImageSize): ImageView {
  const scale = Math.max(Math.min(0.1, fitImageScale(image, viewport)), Math.min(MAX_IMAGE_SCALE, view.scale))
  const maxX = Math.max(0, (image.width * scale - viewport.width) / 2)
  const maxY = Math.max(0, (image.height * scale - viewport.height) / 2)
  return { scale, x: maxX ? Math.max(-maxX, Math.min(maxX, view.x)) : 0,
    y: maxY ? Math.max(-maxY, Math.min(maxY, view.y)) : 0 }
}

export function zoomImageView(view: ImageView, scale: number, image: ImageSize, viewport: ImageSize,
  anchor = { x: 0, y: 0 }): ImageView {
  const nextScale = clampImageView({ ...view, scale }, image, viewport).scale
  const ratio = nextScale / view.scale
  return clampImageView({ scale: nextScale,
    x: anchor.x - (anchor.x - view.x) * ratio,
    y: anchor.y - (anchor.y - view.y) * ratio,
  }, image, viewport)
}
