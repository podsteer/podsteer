/**
 * A Kubernetes kind, in the number there are of it.
 *
 * "Pod 1" and "Pod 4" are both wrong in the same place: a count and a noun
 * that disagree read as a label somebody forgot to finish. The API server has
 * a canonical plural for every kind — the `resource` name, "pods",
 * "ingresses", "networkpolicies" — but it is lowercase, and a heading reading
 * "networkpolicies 3" trades one wrong thing for another.
 *
 * So the rules are English's, applied to the CamelCase kind. They are correct
 * for every kind this is used on today and for the great majority of the
 * ones it could be: Kubernetes names its kinds in ordinary singular English
 * nouns, which is why "Ingresses" and "NetworkPolicies" come out right
 * without a table.
 *
 * The exception worth knowing is a kind that is ALREADY plural — Endpoints is
 * the one in the built-in set — which is left alone rather than made into
 * "Endpointses".
 */

/** Kinds whose name is already plural, so nothing is added. */
const ALREADY_PLURAL = new Set(['Endpoints'])

/** The plural of a kind name. */
export function pluralKind(kind: string): string {
  if (ALREADY_PLURAL.has(kind)) return kind

  // A consonant before a final `y` becomes `ies`: NetworkPolicy →
  // NetworkPolicies. A vowel before it does not: Gateway → Gateways.
  if (/[^aeiou]y$/.test(kind)) return `${kind.slice(0, -1)}ies`

  // A sibilant ending takes `es`: Ingress → Ingresses, Class → Classes.
  if (/(s|x|z|ch|sh)$/.test(kind)) return `${kind}es`

  return `${kind}s`
}

/** A kind named for how many there are: "Pod" for one, "Pods" for any other. */
export function countedKind(kind: string, count: number): string {
  return count === 1 ? kind : pluralKind(kind)
}
