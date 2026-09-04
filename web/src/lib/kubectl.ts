/**
 * kubectl command composition.
 *
 * PodSteer performs writes — scale, restart, delete, apply — through the
 * Kubernetes API directly, not by shelling out to kubectl. This file exists
 * so the operator can still SEE the kubectl command that does the same thing,
 * next to the button that is about to do it. The GUI is meant to teach
 * kubectl, not hide it, so every function here is a pure string builder: no
 * I/O, nothing executed, nothing that could drift from what is actually
 * shown on screen.
 *
 * `ctx` is a kubeconfig context name, always. A PodSteer cluster's ID IS that
 * name — see the doc comment on `Cluster.ID` in
 * `app/adapters/wails/dto.go` ("ID is the kubeconfig context name") and
 * `domain.ClusterID` in `app/domain/cluster.go`, which is constructed from it
 * verbatim (trimmed, and rejected only if it contains PodSteer's own internal
 * cache-key separator). So callers pass `session.cluster.id` straight
 * through as `ctx`.
 */

/**
 * Quotes a shell argument, but only when the shell would otherwise
 * misread it.
 *
 * Object names and namespaces are DNS-1123 labels — lowercase alphanumerics
 * and '-', nothing a POSIX shell treats specially — so quoting them would add
 * only noise to a command that exists to be read. A kubeconfig context name
 * has no such restriction: an operator can name one anything, including a
 * space or a single quote, and kubectl accepts it quoted either way. This is
 * therefore the one argument every builder below runs through `shellQuote`.
 */
export function shellQuote(value: string): string {
  if (/^[A-Za-z0-9_.\/:@%+=-]+$/.test(value)) return value
  // Standard POSIX single-quote escaping: close the quote, emit an escaped
  // single quote, reopen it. There is no character that needs escaping
  // *inside* single quotes other than the single quote itself.
  return `'${value.replace(/'/g, "'\\''")}'`
}

/**
 * The API resource argument kubectl expects for a kind: the lowercase
 * plural, qualified with its API group whenever it has one.
 *
 * kubectl will usually resolve "deployments" on its own, but PodSteer's own
 * catalog carries kinds that collide across groups — Events exist in both the
 * core group and `events.k8s.io` — so the group is appended whenever the kind
 * has one rather than only when a caller judges it necessary. The core group
 * is empty, per `domain.ResourceKind.Group`, so nothing is appended for it.
 */
export function resourceArg(kind: { group: string; resource: string }): string {
  return kind.group ? `${kind.resource}.${kind.group}` : kind.resource
}

/**
 * Recovers the `resource` segment for `resourceArg` from a `ResourceKind` as
 * the frontend actually has it.
 *
 * The wire type (`web/src/lib/wailsjs/go/models.ts`) carries `id` and `group`
 * but not `resource` on its own — `id` is `domain.ResourceKind.ID()`, i.e.
 * "group/version/resource" with the core group rendered as "core". This is
 * the one place that knows that wire format, so every call site can pass the
 * `ResourceKind` it already has instead of re-deriving the split itself.
 */
export function resourceArgForKind(kind: { group: string; id: string }): string {
  const resource = kind.id.split('/').pop() ?? ''
  return resourceArg({ group: kind.group, resource })
}

/**
 * The part every command opens with: `kubectl --context <ctx>`, and, for a
 * namespaced object, `-n <ns>`. Shared so the two can never drift apart —
 * `ns` is only ever omitted for a genuinely cluster-scoped object, never
 * forgotten for a namespaced one.
 */
function base(ctx: string, ns?: string): string[] {
  const parts = ['kubectl', '--context', shellQuote(ctx)]
  if (ns) parts.push('-n', ns)
  return parts
}

/** `kubectl --context c [-n ns] get <resource> <name>`. */
export function get(ctx: string, resource: string, name: string, ns?: string): string {
  return [...base(ctx, ns), 'get', resource, name].join(' ')
}

/** The same read, asking for the full manifest rather than the table view. */
export function getYaml(ctx: string, resource: string, name: string, ns?: string): string {
  return [...base(ctx, ns), 'get', resource, name, '-o', 'yaml'].join(' ')
}

/** `kubectl --context c [-n ns] describe <resource> <name>`. */
export function describe(ctx: string, resource: string, name: string, ns?: string): string {
  return [...base(ctx, ns), 'describe', resource, name].join(' ')
}

/**
 * `kubectl --context c -n ns scale <kind>/<name> --replicas=N`.
 *
 * Always namespaced: every scalable kind (Deployment, StatefulSet) is.
 */
export function scale(ctx: string, kind: string, name: string, ns: string, replicas: number): string {
  return [...base(ctx, ns), 'scale', `${kind.toLowerCase()}/${name}`, `--replicas=${replicas}`].join(
    ' ',
  )
}

/**
 * `kubectl --context c -n ns rollout restart <kind>/<name>`.
 *
 * Always namespaced, for the same reason `scale` is.
 */
export function rolloutRestart(ctx: string, kind: string, name: string, ns: string): string {
  return [...base(ctx, ns), 'rollout', 'restart', `${kind.toLowerCase()}/${name}`].join(' ')
}

/** `kubectl --context c [-n ns] delete <resource> <name>`. */
export function del(ctx: string, resource: string, name: string, ns?: string): string {
  return [...base(ctx, ns), 'delete', resource, name].join(' ')
}

/** Options `logs` accepts, each contributing one flag only when it is set. */
export interface LogsOptions {
  container?: string
  follow?: boolean
  tail?: number
  previous?: boolean
}

/**
 * `kubectl --context c -n ns logs <pod> [-c container] [--tail=N] [-f] [-p]`.
 *
 * Only the flags that apply are emitted — a container name is not always
 * known, and `-f`/`-p`/`--tail` are each independent choices LogViewer's own
 * toolbar makes.
 */
export function logs(ctx: string, pod: string, ns: string, options: LogsOptions = {}): string {
  const parts = [...base(ctx, ns), 'logs', pod]
  if (options.container) parts.push('-c', options.container)
  if (options.tail !== undefined) parts.push(`--tail=${options.tail}`)
  if (options.follow) parts.push('-f')
  if (options.previous) parts.push('-p')
  return parts.join(' ')
}

/**
 * `kubectl --context c -n ns exec -it <pod> [-c container] -- <command>`.
 *
 * Defaults to `/bin/sh`, which is what Terminal.svelte itself opens.
 */
export function exec(
  ctx: string,
  pod: string,
  ns: string,
  container?: string,
  command: string[] = ['/bin/sh'],
): string {
  const parts = [...base(ctx, ns), 'exec', '-it', pod]
  if (container) parts.push('-c', container)
  parts.push('--', ...command)
  return parts.join(' ')
}

/**
 * `kubectl --context c -n ns attach -it <pod> [-c container]`.
 *
 * Distinct from `exec`: attach connects to the container's own main
 * process — whatever its ENTRYPOINT/CMD started — rather than spawning a
 * new one, so there is no command to append. See Terminal.svelte's Attach
 * mode, which does the same thing over the Kubernetes API directly.
 */
export function attach(ctx: string, pod: string, ns: string, container?: string): string {
  const parts = [...base(ctx, ns), 'attach', '-it', pod]
  if (container) parts.push('-c', container)
  return parts.join(' ')
}

/** `kubectl --context c -n ns port-forward pod/<pod> <local>:<remote>`. */
export function portForward(
  ctx: string,
  pod: string,
  ns: string,
  localPort: number,
  remotePort: number,
): string {
  return [...base(ctx, ns), 'port-forward', `pod/${pod}`, `${localPort}:${remotePort}`].join(' ')
}

/**
 * `kubectl --context c [-n ns] apply -f -`.
 *
 * The manifest itself comes from the editor and is never part of the
 * command string — this only shows the invocation that would read it from
 * stdin, the way `git commit -F -` reads a message.
 */
export function apply(ctx: string, ns?: string): string {
  return [...base(ctx, ns), 'apply', '-f', '-'].join(' ')
}

/**
 * `kubectl --context c -n ns get secret <name> -o jsonpath='{.data.<key>}' |
 * base64 -d`.
 *
 * The pipe is for display only — PodSteer's own `RevealSecretKey` reads and
 * decodes the value itself (see `app/adapters/wails`), and this is never
 * executed. It exists so the hint teaches the two commands an operator would
 * actually need to type to get the same answer at a shell, in the order they
 * would type them.
 */
export function revealSecretKey(ctx: string, name: string, ns: string, key: string): string {
  // A dot separates path segments in jsonpath, and Secret keys are full of
  // them — tls.crt, ca.crt, .dockerconfigjson. Unescaped, `{.data.tls.crt}`
  // asks for a field called "crt" inside "tls" and prints nothing, which is
  // the kind of hint that teaches somebody the wrong command.
  const escapedKey = key.replace(/\./g, '\\.')
  return [
    ...base(ctx, ns),
    'get',
    'secret',
    name,
    '-o',
    `jsonpath='{.data.${escapedKey}}'`,
    '|',
    'base64',
    '-d',
  ].join(' ')
}
