import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, expect, it } from 'vitest'

/**
 * The light theme's text colours, held to WCAG AA by measurement.
 *
 * The light scheme shipped with warning text at 2.3:1 and the primary at 4.3:1
 * and nothing noticed, because a palette looks fine in the file it is written
 * in. This reads the values out of app.css and computes the ratios, so a later
 * retune that drifts back under 4.5:1 fails here rather than on somebody's
 * screen. Dark is not measured: its values were not the problem, and its
 * greys at the same opacities sit well clear.
 */

const CSS = readFileSync(join(dirname(fileURLToPath(import.meta.url)), '..', 'app.css'), 'utf8')

function block(selector: string): Record<string, string> {
  const start = CSS.indexOf(`${selector} {`)
  expect(start, `${selector} block`).toBeGreaterThan(-1)
  const body = CSS.slice(start, CSS.indexOf('\n}', start))
  const out: Record<string, string> = {}
  for (const match of body.matchAll(/(--[\w-]+):\s*(#[0-9a-fA-F]{6})\s*;/g)) out[match[1]] = match[2]
  return out
}

const light = block(":root[data-theme='light']")

function channel(value: number): number {
  const c = value / 255
  return c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4
}
function rgb(hex: string): [number, number, number] {
  return [1, 3, 5].map((i) => parseInt(hex.slice(i, i + 2), 16)) as [number, number, number]
}
function luminance(hex: string): number {
  const [r, g, b] = rgb(hex).map(channel)
  return 0.2126 * r + 0.7152 * g + 0.0722 * b
}
/** `fg` at `alpha` over `bg`, the way an opacity modifier composites. */
function over(fg: string, bg: string, alpha: number): string {
  const [f, b] = [rgb(fg), rgb(bg)]
  return '#' + f.map((c, i) => Math.round(c * alpha + b[i] * (1 - alpha)).toString(16).padStart(2, '0')).join('')
}
function contrast(a: string, b: string): number {
  const [x, y] = [luminance(a), luminance(b)].sort((p, q) => q - p)
  return (x + 0.05) / (y + 0.05)
}

describe('the light theme, measured', () => {
  const grounds = ['--surface', '--surface-container-low', '--surface-container']

  it.each(['--on-surface', '--on-surface-variant', '--primary', '--error', '--warning', '--success'])(
    '%s reads as body text on every ground text sits on',
    (token) => {
      for (const ground of grounds) {
        expect(contrast(light[token], light[ground]), `${token} on ${ground}`).toBeGreaterThanOrEqual(4.5)
      }
    },
  )

  it.each(['--gauge-warn-ink', '--gauge-critical-ink', '--gauge-normal-ink'])(
    '%s reads as text, which the fixed gauge colour cannot',
    (token) => {
      for (const ground of grounds) {
        expect(contrast(light[token], light[ground]), `${token} on ${ground}`).toBeGreaterThanOrEqual(4.5)
      }
    },
  )

  it('keeps faded secondary text readable at the opacity it is most faded to', () => {
    // /70 is the commonest fade in the interface; it has to pass as body text.
    for (const ground of grounds) {
      const faded = over(light['--on-surface-variant'], light[ground], 0.7)
      expect(contrast(faded, light[ground]), `/70 on ${ground}`).toBeGreaterThanOrEqual(4.5)
    }
  })

  it('draws a text field boundary somebody can find', () => {
    // WCAG 1.4.11: 3:1 for the edge of a control.
    expect(light['--outline']).toBeDefined()
    expect(contrast(light['--outline'], light['--surface'])).toBeGreaterThanOrEqual(3)
    expect(CSS).toMatch(/:root\[data-theme='light'\][\s\S]*--field-border: var\(--outline\)/)
  })

  it('has no purple cast left in its surfaces', () => {
    // The baseline MD3 neutrals were lavender; a surface whose red channel
    // exceeds its green is the tell.
    for (const ground of [...grounds, '--surface-container-high', '--surface-container-highest']) {
      const [r, g] = rgb(light[ground])
      expect(r, ground).toBeLessThanOrEqual(g)
    }
  })
})
