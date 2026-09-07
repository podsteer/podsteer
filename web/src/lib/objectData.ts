/**
 * The keys a Secret or a ConfigMap holds, as the panel lists them.
 *
 * SEPARATED FROM THE PANEL BECAUSE THE RULES ARE NOT OBVIOUS. Three kinds of
 * cell come out of two fields, a Secret's values are already masked by the
 * time they arrive, and a base64 length is not a byte count. Each of those is
 * a place to be quietly wrong on somebody's credentials, which is not a thing
 * to leave untested inside a component.
 *
 * WHAT THIS DOES NOT DO IS DECODE ANYTHING. A Secret's values reach the client
 * as `<hidden, N bytes>` — the adapter replaces them before they leave Go —
 * and reading one is a separate, audited act through RevealSecretKey. This
 * function only says which keys exist and what is safe to print about them.
 */

/** What kind of cell a key deserves. */
export type DataEntryKind =
  /** A Secret's value: masked here, revealed one key at a time on request. */
  | 'secret'
  /** A ConfigMap's text value, already in the clear in the manifest. */
  | 'text'
  /** Bytes. Listed by size, never offered a text editor. */
  | 'binary'

export interface DataEntry {
  key: string
  /** What to show before anybody asks for more. */
  display: string
  kind: DataEntryKind
}

/**
 * How many bytes a base64 string decodes to.
 *
 * `length * 3 / 4` is the wrong answer for anything padded, which is three
 * quarters of real keys: a 1,368-character certificate ending `==` is 1,024
 * bytes, not 1,026. Being two bytes out about a key nobody can see is exactly
 * the sort of small lie that makes somebody doubt the rest of the panel.
 */
export function base64Bytes(encoded: string): number {
  const clean = encoded.replace(/\s/g, '')
  if (clean.length === 0) return 0
  const padding = clean.endsWith('==') ? 2 : clean.endsWith('=') ? 1 : 0
  return Math.max(0, Math.floor((clean.length * 3) / 4) - padding)
}

/** A value that is not a string, printed without pretending to know its size. */
function quote(value: unknown): string {
  if (typeof value === 'string') return value
  return JSON.stringify(value) ?? ''
}

/**
 * The entries for one object, sorted by key.
 *
 * Sorted because the API server returns a map and a map has no order: an
 * unsorted list reshuffles under the operator on every poll, which makes a
 * panel somebody is reading a panel somebody is chasing.
 */
export function dataEntries(kind: string, manifest: Record<string, unknown> | null): DataEntry[] {
  if (!manifest) return []
  if (kind !== 'Secret' && kind !== 'ConfigMap') return []

  const entries: DataEntry[] = []
  const data = (manifest.data ?? {}) as Record<string, unknown>
  for (const key of Object.keys(data).sort()) {
    entries.push({
      key,
      display: quote(data[key]),
      kind: kind === 'Secret' ? 'secret' : 'text',
    })
  }

  // A Secret can carry stringData on an object that was never accepted — a
  // stored Helm manifest, a file somebody pasted. It is masked by the same
  // adapter and belongs in the list for the same reason: a key that exists
  // and is not listed is a key somebody will not know to change.
  const stringData = (manifest.stringData ?? {}) as Record<string, unknown>
  for (const key of Object.keys(stringData).sort()) {
    if (entries.some((entry) => entry.key === key)) continue
    entries.push({ key, display: quote(stringData[key]), kind: 'secret' })
  }

  const binary = (manifest.binaryData ?? {}) as Record<string, unknown>
  for (const key of Object.keys(binary).sort()) {
    const encoded = typeof binary[key] === 'string' ? (binary[key] as string) : ''
    entries.push({ key, display: `<binary, ${base64Bytes(encoded)} bytes>`, kind: 'binary' })
  }

  return entries
}
