import { readdirSync, readFileSync } from 'node:fs'
import { join } from 'node:path'
import { describe, expect, it } from 'vitest'

/**
 * A SOURCE SCAN, because the defect it catches is invisible to a rendering
 * test that does not ask the one right question.
 *
 * Svelte decides an element's XML namespace from where its template is
 * WRITTEN. A snippet declared at a component's top level and rendered inside
 * an `<svg>` produces HTML elements called "rect" and "path" — present in the
 * DOM, correctly attributed, and painted by no browser. The dependency map
 * shipped that way for eight days: it drew its edges, which are written
 * inline, and none of its nodes, which are not.
 *
 * MapNamespace.test.ts asserts the map itself. This one exists so the next
 * component to draw an SVG cannot make the same mistake quietly — the whole
 * failure mode is that everything looks right except the thing nobody thinks
 * to check.
 */
const HERE = join(import.meta.dirname, '..')

/** Elements that only mean anything inside an `<svg>`. */
const SVG_ONLY = /<(rect|circle|ellipse|line|polyline|polygon|path|text|tspan|marker|defs|use)\b/

function svelteSources(dir: string): { name: string; text: string }[] {
  const out: { name: string; text: string }[] = []
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    const path = join(dir, entry.name)
    if (entry.isDirectory()) out.push(...svelteSources(path))
    else if (entry.name.endsWith('.svelte')) out.push({ name: path, text: readFileSync(path, 'utf8') })
  }
  return out
}

/** Whether an `<svg>` is still open at this offset. */
function insideSvg(text: string, at: number): boolean {
  const before = text.slice(0, at)
  const opened = before.match(/<svg\b/g)?.length ?? 0
  const closed = before.match(/<\/svg>/g)?.length ?? 0
  return opened > closed
}

describe('snippets that draw SVG', () => {
  it('are declared inside the svg they draw into', () => {
    const offenders: string[] = []

    for (const { name, text } of svelteSources(HERE)) {
      for (const match of text.matchAll(/\{#snippet\s+(\w+)/g)) {
        const start = match.index
        const end = text.indexOf('{/snippet}', start)
        const body = text.slice(start, end === -1 ? undefined : end)

        if (!SVG_ONLY.test(body)) continue
        if (insideSvg(text, start)) continue

        offenders.push(`${name}: {#snippet ${match[1]}}`)
      }
    }

    expect(offenders).toEqual([])
  })
})
