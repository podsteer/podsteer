/**
 * Turning what an operator typed into SetImageDialog into the calls actually
 * worth making.
 *
 * The dialog lists every container and init container from the pod template,
 * each with its current image beside a field pre-filled with it — so most
 * fields, most of the time, are never touched. Sending a write for every row
 * regardless would re-apply N images an operator never asked to change,
 * which for a strategic merge patch is a no-op on the cluster but still N
 * requests, N audit-log lines, and N chances for one of them to fail on a
 * value nobody meant to touch.
 */

import type { PodTemplate } from './podTemplate'

/** One image change worth sending: SetImage's own arguments, minus the object identity. */
export interface ImageChange {
  container: string
  /** Routes the patch to initContainers instead of containers. See ManagementPort.SetImage. */
  initContainer: boolean
  image: string
}

/** Pulls `name` and `image` out of a raw container entry, or null if either is missing or not a string. */
function nameAndImage(entry: Record<string, unknown>): { name: string; image: string } | null {
  const { name, image } = entry
  if (typeof name !== 'string' || typeof image !== 'string') return null
  return { name, image }
}

/**
 * The edits actually worth applying.
 *
 * `edits` is keyed by container name — safe across containers AND init
 * containers because Kubernetes itself requires every container name in a
 * pod to be unique regardless of which list it is in, so there is no
 * collision to disambiguate.
 *
 * A row is included only when its edit, TRIMMED, is non-empty and differs
 * from the image the template currently has — never from whatever the
 * dialog originally seeded the field with, so typing a value and then typing
 * the original back produces no change either. Leading/trailing whitespace
 * in what was typed is never sent: it is not a value anybody meant, and it
 * would make an unchanged field look changed after a trip through a form
 * control that trims nothing on its own.
 *
 * Returned in template order — containers, then init containers — which is
 * also the order SetImageDialog applies them in.
 */
export function changedImages(
  template: PodTemplate | null,
  edits: Record<string, string>,
): ImageChange[] {
  const changes: ImageChange[] = []

  function collect(entries: Record<string, unknown>[] | undefined, initContainer: boolean): void {
    for (const raw of entries ?? []) {
      const parsed = nameAndImage(raw)
      if (!parsed) continue

      const edited = edits[parsed.name]
      if (edited === undefined) continue

      const trimmed = edited.trim()
      if (trimmed === '' || trimmed === parsed.image) continue

      changes.push({ container: parsed.name, initContainer, image: trimmed })
    }
  }

  collect(template?.spec?.containers, false)
  collect(template?.spec?.initContainers, true)

  return changes
}
