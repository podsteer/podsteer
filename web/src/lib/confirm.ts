/**
 * Type-the-name confirmation, for a write PodSteer will not let happen by
 * accident.
 *
 * Production guardrails (SECURITY.md, CLAUDE.md's read-only section) add a
 * second gate beyond the ordinary "Are you sure?" dialog for a cluster whose
 * group is marked production: DeleteDialog requires typing the object's exact
 * name, and ScaleDialog requires it when the target is zero replicas. This is
 * the one comparison both make, kept here so the rule is argued over once
 * rather than reimplemented per dialog.
 */

/**
 * Whether what was typed confirms the named object.
 *
 * TRIMMED, because a name pasted from a list row or a terminal often carries
 * a stray newline or trailing space that means nothing about whether the
 * right object was named. CASE-SENSITIVE otherwise, deliberately not
 * forgiving beyond that: Kubernetes names are case-sensitive DNS labels, so
 * `my-app` and `My-App` can coexist as two different objects in the same
 * namespace, and treating a case mismatch as confirmation would let someone
 * type the wrong one and still be let through.
 */
export function nameConfirmed(typed: string, expected: string): boolean {
  return typed.trim() === expected
}
