/**
 * Trimming a manifest down to what an OPERATOR wrote, for duplicating it into
 * a new object.
 *
 * A manifest read back from the cluster carries a second layer nothing typed
 * by hand ever has: identity the server assigned (uid, resourceVersion,
 * generation), a record of the last apply, ownership it was given after the
 * fact, and status. Reapplying that layer under a new name is not a
 * duplicate, it is a request the server will refuse (a UID that belongs to
 * another object) or silently misreport (a status block describing the
 * object it was copied from). This is the counterpart to `withoutManagedFields`
 * in $lib/manifest — that one changes what is DISPLAYED, this one changes
 * what would be SENT.
 *
 * Uses the same `yaml` document API `$lib/manifest.ts` uses for its own
 * parse — see `web/package.json`. Unlike that module this one re-serialises
 * the whole document rather than splicing bytes: duplicating touches several
 * scattered fields rather than one contiguous block, and `yaml`'s `Document`
 * preserves every comment, key order and quoting style it did not touch —
 * which is what keeps the diff between the source object and this output to
 * exactly the fields listed below.
 */

import { parseDocument, type Document } from 'yaml'

/** Deletes a nested key, without `deleteIn`'s own behaviour of throwing when
    an ancestor is entirely absent rather than simply not holding the key.
    Every field this module removes is optional on a real object — a Secret
    with no `metadata.annotations` at all is ordinary, not malformed — so
    "nothing to remove" has to be as quiet as "removed it". */
function removeIn(doc: Document.Parsed, path: string[]): void {
  if (path.length === 1) {
    doc.delete(path[0])
    return
  }
  if (!doc.hasIn(path.slice(0, -1))) return
  doc.deleteIn(path)
}

/** Server-assigned identity and bookkeeping under `metadata`, on every kind. */
const METADATA_FIELDS = [
  'uid',
  'resourceVersion',
  'creationTimestamp',
  'generation',
  'managedFields',
  'selfLink',
  'ownerReferences',
]

/** kubectl's own record of the last manifest applied — a duplicate has not
    been applied yet, so carrying this forward would describe an apply that
    never happened. */
const LAST_APPLIED_ANNOTATION = 'kubectl.kubernetes.io/last-applied-configuration'

/** `spec` fields the server assigns, present only on the kind that has them —
    see CLAUDE.md's note that every edge and every field here has to be one
    Kubernetes actually draws, not one that merely looks similar. A ClusterIP
    is allocated FOR one Service and does not transfer to a second; a bound
    PVC's `volumeName` names the specific PersistentVolume the source claim
    was matched to; a Pod's `nodeName` is where the scheduler put THAT pod. */
const KIND_SPEC_FIELDS: Record<string, string[]> = {
  Service: ['clusterIP', 'clusterIPs'],
  PersistentVolumeClaim: ['volumeName'],
  Pod: ['nodeName'],
}

/**
 * A manifest with everything the server owns removed, and `metadata.name`
 * cleared for the operator to fill in.
 *
 * Labels, annotations (other than the one above), and the rest of `spec` are
 * preserved verbatim — a duplicate is meant to start as a copy, not a
 * skeleton, and the differences from `skeletonFor` are the point: this is
 * for somebody who already has a working object and wants another one like
 * it.
 *
 * Anything that fails to parse is returned unchanged, the same convention
 * `withoutManagedFields` follows — a manifest that cannot be trimmed should
 * still open in the editor rather than show nothing.
 */
export function stripForDuplicate(manifestYaml: string): string {
  try {
    const doc = parseDocument(manifestYaml)
    if (doc.errors.length > 0) return manifestYaml

    removeIn(doc, ['status'])
    for (const field of METADATA_FIELDS) removeIn(doc, ['metadata', field])
    removeIn(doc, ['metadata', 'annotations', LAST_APPLIED_ANNOTATION])

    const kind = doc.get('kind')
    if (typeof kind === 'string') {
      for (const field of KIND_SPEC_FIELDS[kind] ?? []) removeIn(doc, ['spec', field])
    }

    // Cleared rather than left as a starting point somebody might apply by
    // accident — a name collision is the one mistake here the server refuses
    // outright, which is exactly why it should never be a silent one.
    doc.setIn(['metadata', 'name'], '')
    const nameNode = doc.getIn(['metadata', 'name'], true) as { comment?: string } | null
    if (nameNode) nameNode.comment = ' name required'

    return doc.toString()
  } catch {
    return manifestYaml
  }
}
