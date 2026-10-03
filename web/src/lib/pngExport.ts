/**
 * The topology as a PNG, through the native save dialog.
 *
 * THE SVG ON SCREEN CANNOT BE SAVED AS IT IS. Its colours are Tailwind classes
 * resolved against the theme's custom properties, and an SVG serialised on its
 * own carries the class names and none of the stylesheet — so every element's
 * COMPUTED paint is written onto the copy before it is rasterised, which is
 * also what makes the export match the theme it was taken in.
 *
 * THE DRAWN BOUNDS, AND CAPPED. A canvas has a size limit — WebKit's is about
 * 16.7 million pixels, an area a 5,000-box topology passes at life size — and
 * a canvas over the limit does not fail, it draws nothing. So the scale is
 * chosen to fit under it, and when that scale is below life size the page
 * says so beside the saved path rather than leaving somebody to find their
 * labels unreadable later.
 *
 * Rasterised from a `data:` URL because the page's content security policy
 * allows images from `data:` and not from `blob:`.
 */

export interface ExportLimits {
  /** The longest side a canvas may have. */
  maxSide: number
  /** The most pixels a canvas may have in total. */
  maxPixels: number
}

/** WebKit's limit is the tighter of the three engines', so it is everyone's. */
export const EXPORT_LIMITS: ExportLimits = { maxSide: 16_384, maxPixels: 16_777_216 }

/**
 * The scale to render at: `preferred` (2 for a sharp image on a high-density
 * screen) unless the limits force it lower. `capped` is set when the result
 * is below life size — when the image is smaller than the map really is.
 */
export function exportScale(
  width: number,
  height: number,
  preferred = 2,
  limits: ExportLimits = EXPORT_LIMITS,
): { scale: number; capped: boolean; width: number; height: number } {
  const w = Math.max(1, width)
  const h = Math.max(1, height)
  const scale = Math.min(preferred, limits.maxSide / w, limits.maxSide / h, Math.sqrt(limits.maxPixels / (w * h)))
  return {
    scale,
    capped: scale < 1,
    width: Math.max(1, Math.floor(w * scale)),
    height: Math.max(1, Math.floor(h * scale)),
  }
}

/** The paint properties that carry a theme, copied from computed style. */
const PAINT = [
  'fill',
  'fill-opacity',
  'stroke',
  'stroke-opacity',
  'stroke-width',
  'stroke-dasharray',
  'opacity',
  'font-family',
  'font-size',
  'font-weight',
  'visibility',
] as const

/** Writes every element's computed paint onto its copy, in document order. */
function inlinePaint(source: Element, copy: Element): void {
  const style = getComputedStyle(source)
  const declarations = PAINT.map((name) => `${name}:${style.getPropertyValue(name)}`)
  // Animation off: the export is a still image, and a dash offset mid-flow
  // is not what anybody meant to save.
  declarations.push('animation:none')
  copy.setAttribute('style', declarations.join(';'))
  const sources = source.children
  const copies = copy.children
  for (let i = 0; i < sources.length && i < copies.length; i++) inlinePaint(sources[i], copies[i])
}

/**
 * Renders `content` — the transformed group holding the drawing — over the
 * given bounds, and resolves to base64 PNG bytes (no `data:` prefix).
 */
export async function renderPng(
  svg: SVGSVGElement,
  content: SVGGElement,
  bounds: { x: number; y: number; width: number; height: number },
  background: string,
  preferredScale = 2,
): Promise<{ base64: string; capped: boolean; scale: number }> {
  const size = exportScale(bounds.width, bounds.height, preferredScale)

  const copy = svg.cloneNode(true) as SVGSVGElement
  inlinePaint(svg, copy)
  copy.setAttribute('xmlns', 'http://www.w3.org/2000/svg')
  copy.setAttribute('width', String(size.width))
  copy.setAttribute('height', String(size.height))
  copy.setAttribute('viewBox', `${bounds.x} ${bounds.y} ${bounds.width} ${bounds.height}`)
  copy.removeAttribute('class')

  // The pan and zoom are the view's, not the picture's.
  const index = [...svg.querySelectorAll('g')].indexOf(content)
  const drawing = index >= 0 ? copy.querySelectorAll('g')[index] : null
  drawing?.removeAttribute('transform')

  const ground = document.createElementNS('http://www.w3.org/2000/svg', 'rect')
  ground.setAttribute('x', String(bounds.x))
  ground.setAttribute('y', String(bounds.y))
  ground.setAttribute('width', String(bounds.width))
  ground.setAttribute('height', String(bounds.height))
  ground.setAttribute('fill', background)
  copy.insertBefore(ground, copy.firstChild)

  const markup = new XMLSerializer().serializeToString(copy)
  const url = `data:image/svg+xml;base64,${toBase64(markup)}`

  const image = new Image()
  image.decoding = 'async'
  await new Promise<void>((resolve, reject) => {
    image.onload = () => resolve()
    image.onerror = () => reject(new Error('The drawing could not be rasterised.'))
    image.src = url
  })

  const canvas = document.createElement('canvas')
  canvas.width = size.width
  canvas.height = size.height
  const context = canvas.getContext('2d')
  if (!context) throw new Error('This webview would not give a canvas to draw the image on.')
  context.drawImage(image, 0, 0, size.width, size.height)
  const data = canvas.toDataURL('image/png')
  return { base64: data.slice(data.indexOf(',') + 1), capped: size.capped, scale: size.scale }
}

/** UTF-8 safe base64 — names in a cluster are not all ASCII. */
function toBase64(text: string): string {
  const bytes = new TextEncoder().encode(text)
  let binary = ''
  for (let i = 0; i < bytes.length; i += 0x8000) {
    binary += String.fromCharCode(...bytes.subarray(i, i + 0x8000))
  }
  return btoa(binary)
}

/** `<cluster>-topology-<scope>-<YYYYMMDD-HHMMSS>.png`, safe as a filename. */
export function topologyFilename(cluster: string, scope: { namespaces: string[]; all: boolean }, now = new Date()): string {
  const safe = (segment: string) => segment.replace(/[^A-Za-z0-9._-]+/g, '_')
  const pad = (n: number) => String(n).padStart(2, '0')
  const stamp = `${now.getFullYear()}${pad(now.getMonth() + 1)}${pad(now.getDate())}-${pad(now.getHours())}${pad(now.getMinutes())}${pad(now.getSeconds())}`
  const named = scope.all
    ? 'all'
    : scope.namespaces.length <= 3
      ? scope.namespaces.join('+')
      : `${scope.namespaces.length}-namespaces`
  return `${safe(cluster)}-topology-${safe(named || 'none')}-${stamp}.png`
}
