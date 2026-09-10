import { readFileSync, readdirSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, expect, it } from 'vitest'

/**
 * The type scale, enforced across everything rather than across dialogs.
 *
 * DialogChrome.node.test.ts already forbids the caption size inside a dialog,
 * and states the reasoning: "`body-small` is 12px: the size for a caption
 * under a figure, not for the labels, values and paragraphs somebody is
 * reading in order to decide something." That test scans `lib/components` and
 * matches the string `text-body-small` — so fourteen pages and a hundred-odd
 * components were unchecked, and a size written as `text-[11px]` was invisible
 * to it entirely. Nine of those had accumulated in one menu, and one had
 * reached the inside of a dialog by the route the test cannot see.
 *
 * THIS TEST DOES THE HALF THAT NEEDS NO JUDGEMENT: every size must come from
 * the scale. Whether a particular string is a caption or a value is a reading
 * of what the text is FOR, and no test can make that call — but "11px written
 * as an arbitrary pixel value" is not a judgement, it is a bypass. The M3
 * scale in app.css already offers `label-small` at 11px and `body-small` at
 * 12px; anything reaching past it is either a size nobody chose deliberately
 * or a size that should be added to the scale and named.
 */
const HERE = dirname(fileURLToPath(import.meta.url))
const ROOTS = [join(HERE, '..', '..', 'pages'), join(HERE, '..')]

/** Files allowed an off-scale size, each for a reason stated beside it. */
const OFF_SCALE_ALLOWED = new Set([
  // SVG TEXT IS A DIFFERENT SYSTEM. A label inside a diagram is drawn into a
  // viewBox and scales with it, so it has no fixed relationship to the page's
  // type scale — a token there would be a number that means something else at
  // every zoom level.
  'DependencyMap.svelte',
  // THE SPLASH IS NOT THEMED AND NOT TOKENED, deliberately and for a reason
  // its own header gives: it is painted to match the window the Go side has
  // already drawn, so that there is no colour flip at launch. It uses literal
  // hex colours for the same reason it uses literal sizes — it is the one
  // screen that must not depend on the application's own design system,
  // because it is what shows while that system is still loading.
  'Splash.svelte',
])

/**
 * Sizes that are not on the scale: arbitrary pixels, and Tailwind's own.
 *
 * Matched anywhere rather than inside a `class="…"`, because a class
 * attribute here routinely spans several lines and carries an interpolation,
 * and an attribute-aware pattern silently matched two of thirty.
 *
 * THE TWO ALTERNATIVES ARE NOT TIDINESS EITHER. A trailing `\\b` after `]`
 * never matches — a bracket and the space after it are both non-word
 * characters — so one pattern for both shapes reported two offenders out of
 * thirty and looked like a clean sweep.
 */
const OFF_SCALE = /\btext-(\[\d+(?:\.\d+)?px\])|\btext-(xs|sm|base|lg|xl|\dxl)\b/g

function sources(dir: string, out: { name: string; path: string; text: string }[] = []) {
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    const path = join(dir, entry.name)
    if (entry.isDirectory()) {
      if (entry.name === 'bindings') continue
      sources(path, out)
      continue
    }
    if (entry.name.endsWith('.svelte')) out.push({ name: entry.name, path, text: readFileSync(path, 'utf8') })
  }
  return out
}

describe('every font size comes from the scale', () => {
  it('finds files to check at all', () => {
    const found = ROOTS.flatMap((root) => sources(root))
    expect(found.length).toBeGreaterThan(50)
  })

  it("uses no arbitrary pixel size and none of Tailwind's own", () => {
    const offenders: string[] = []

    for (const root of ROOTS) {
      for (const { name, path, text } of sources(root)) {
        if (OFF_SCALE_ALLOWED.has(name)) continue
        for (const [whole] of text.matchAll(OFF_SCALE)) {
          offenders.push(`${path}: ${whole}`)
        }
      }
    }

    expect(
      offenders,
      'these bypass the M3 scale in app.css — use label-small (11px), body-small (12px), ' +
        'label-medium (12px) or body-medium (14px), or add the size you need to the scale and name it',
    ).toEqual([])
  })
})
