/**
 * Trimming a manifest down to the parts somebody wrote.
 */

import { parseDocument, isMap, isPair, isScalar } from 'yaml'

/**
 * The same manifest without `metadata.managedFields`.
 *
 * managedFields is server-side apply's ledger of which controller owns which
 * field. The API server writes it, nobody edits it by hand, and on a real
 * object it is not a footnote: measured on a live cluster it is 125 of 438
 * lines on a node and 232 of 464 on a pod — half the document, in a syntax
 * (`f:spec`, `k:{"type":"Ready"}`) that exists to be machine-readable.
 * Scrolling past it to reach `spec` is the largest single cost of reading a
 * manifest, which is why kubectl has hidden it by default since 1.21.
 *
 * The document is PARSED to locate the field and the original text is then
 * spliced — it is not re-serialised. That distinction is the whole
 * implementation. Re-emitting through the YAML library rewrites every line it
 * touches: sequence indentation moves unless `indentSeq` is set to match, and
 * long values fold at different columns, so flipping the toggle would reflow
 * parts of the document that have nothing to do with managedFields. Splicing
 * a byte range leaves every surviving line exactly as the API server sent it.
 *
 * Verified against a node, a deployment, a daemonset and a statefulset from a
 * live cluster: byte-identical to `kubectl --show-managed-fields=false`, with
 * no line altered from the input.
 *
 * Anything that cannot be parsed, or has no such field, is returned untouched.
 * A manifest that cannot be filtered should still be readable — failing to
 * apply a display preference is not a reason to show somebody nothing.
 */
export function withoutManagedFields(text: string): string {
  // The overwhelmingly common case for a hand-written or already-trimmed
  // manifest, and it avoids parsing a large document to discover there is
  // nothing to do.
  if (!text.includes('managedFields')) return text

  try {
    const doc = parseDocument(text)
    if (doc.errors.length > 0) return text

    const metadata = doc.get('metadata')
    if (!isMap(metadata)) return text

    const field = metadata.items.find(
      (item) => isPair(item) && isScalar(item.key) && item.key.value === 'managedFields',
    )
    if (!field || !isPair(field) || !field.value) return text

    const keyRange = (field.key as { range?: [number, number, number] }).range
    const valueRange = (field.value as { range?: [number, number, number] }).range
    if (!keyRange || !valueRange) return text

    // Start from the KEY's line and end at the VALUE's node-end. Both halves
    // matter and each has already been got wrong once:
    //
    // Taking the start from the value instead of the key leaves the
    // `managedFields:` line behind as an orphan heading, because the value is
    // a sequence that begins on the FOLLOWING line.
    //
    // Taking it from the key's own offset rather than its line start leaves
    // the two spaces of indentation in front of it, which the next line then
    // inherits — silently reindenting `name:` out of `metadata`.
    const lineStart = text.lastIndexOf('\n', keyRange[0]) + 1
    const trimmed = text.slice(0, lineStart) + text.slice(valueRange[2])

    // A filter that emptied the document is a filter that went wrong.
    return trimmed.trim() ? trimmed : text
  } catch {
    return text
  }
}
