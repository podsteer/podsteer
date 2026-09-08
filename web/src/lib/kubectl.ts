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
 * The wire type (`$bindings/models`) carries `id` and `group`
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

/**
 * `kubectl --context c -n ns set image <kind>/<name> <container>=<image>`.
 *
 * Always namespaced, for the same reason `scale` and `rolloutRestart` are —
 * every kind this applies to (Deployment, StatefulSet, DaemonSet) is
 * namespaced. `container` is not quoted: like a kind or an object name, it is
 * a Kubernetes identifier and never contains shell-special characters —
 * `image` is the one argument here an operator could paste something
 * surprising into, which is what `shellQuote` is for.
 */
export function setImage(
  ctx: string,
  kind: string,
  name: string,
  ns: string,
  container: string,
  image: string,
): string {
  return [
    ...base(ctx, ns),
    'set',
    'image',
    `${kind.toLowerCase()}/${name}`,
    `${container}=${shellQuote(image)}`,
  ].join(' ')
}

/**
 * `kubectl --context c -n ns rollout undo <kind>/<name> --to-revision=N`.
 *
 * Always namespaced, for the same reason `scale` and `rolloutRestart` are —
 * every kind this applies to (Deployment, StatefulSet, DaemonSet) is
 * namespaced. `toRevision` is never zero here: RollbackDialog only offers
 * this once a specific revision has been picked, unlike kubectl's own CLI
 * where `--to-revision=0` means "the previous one".
 */
export function rolloutUndo(ctx: string, kind: string, name: string, ns: string, toRevision: number): string {
  return [
    ...base(ctx, ns),
    'rollout',
    'undo',
    `${kind.toLowerCase()}/${name}`,
    `--to-revision=${toRevision}`,
  ].join(' ')
}

/** `kubectl --context c [-n ns] delete <resource> <name>`. */
export function del(ctx: string, resource: string, name: string, ns?: string): string {
  return [...base(ctx, ns), 'delete', resource, name].join(' ')
}

/** One object a bulk command names. `ns` is omitted for a cluster-scoped object. */
export interface BulkTarget {
  name: string
  ns?: string
}

/**
 * Targets grouped by namespace, in the order each namespace first appears.
 *
 * kubectl takes ONE `-n` per invocation, so a selection made under "All
 * namespaces" that spans three of them is three commands, not one with three
 * flags. The builders below emit one line per group; a selection within a
 * single namespace — the common case — stays one line.
 */
function byNamespace(targets: BulkTarget[]): [string | undefined, string[]][] {
  const groups = new Map<string | undefined, string[]>()
  for (const target of targets) {
    const names = groups.get(target.ns)
    if (names) names.push(target.name)
    else groups.set(target.ns, [target.name])
  }
  return [...groups.entries()]
}

/**
 * `kubectl --context c [-n ns] delete <resource> <a> <b> <c>` — one line per
 * namespace the selection spans. The equivalent of a bulk delete, which is
 * what the review dialog shows before running one.
 */
export function delMany(ctx: string, resource: string, targets: BulkTarget[]): string {
  return byNamespace(targets)
    .map(([ns, names]) => [...base(ctx, ns), 'delete', resource, ...names].join(' '))
    .join('\n')
}

/**
 * `kubectl --context c -n ns scale <kind>/<a> <kind>/<b> --replicas=N` — one
 * line per namespace. Always namespaced, for the same reason `scale` is.
 */
export function scaleMany(ctx: string, kind: string, targets: BulkTarget[], replicas: number): string {
  return byNamespace(targets)
    .map(([ns, names]) =>
      [
        ...base(ctx, ns),
        'scale',
        ...names.map((name) => `${kind.toLowerCase()}/${name}`),
        `--replicas=${replicas}`,
      ].join(' '),
    )
    .join('\n')
}

/**
 * `kubectl --context c -n ns rollout restart <kind>/<a> <kind>/<b>` — one
 * line per namespace. Always namespaced, for the same reason `scale` is.
 */
export function rolloutRestartMany(ctx: string, kind: string, targets: BulkTarget[]): string {
  return byNamespace(targets)
    .map(([ns, names]) =>
      [...base(ctx, ns), 'rollout', 'restart', ...names.map((name) => `${kind.toLowerCase()}/${name}`)].join(
        ' ',
      ),
    )
    .join('\n')
}

/**
 * `kubectl --context c cordon <a> <b>` or `... uncordon <a> <b>`.
 *
 * Nodes are cluster-scoped, so there is never a `-n` and never more than one
 * line. kubectl accepts several nodes in one invocation, which is what makes
 * this the honest equivalent of a bulk cordon rather than a shorthand.
 */
export function cordon(ctx: string, names: string[], on: boolean): string {
  return [...base(ctx), on ? 'cordon' : 'uncordon', ...names].join(' ')
}

/** What a drain was asked to do, as the flags kubectl would need. */
export interface DrainOptions {
  /** Evict pods no controller owns, which are not recreated. */
  force?: boolean
  /** Evict pods with emptyDir volumes, losing what is in them. */
  deleteEmptyDirData?: boolean
  /** Blank means the pod's own terminationGracePeriodSeconds. */
  gracePeriodSeconds?: number | null
}

/**
 * `kubectl --context c drain <node> --ignore-daemonsets [--force] …`
 *
 * `--ignore-daemonsets` is unconditional because PodSteer's drain never
 * evicts a DaemonSet pod — their controller would put them straight back —
 * so a command without it would describe a different act from the one the
 * button performs.
 *
 * The two optional flags are the dialog's own two ticks, and the grace period
 * is emitted only when the operator set one: kubectl's default of -1 means
 * "each pod's own", which is what a blank field already means here.
 */
export function drain(ctx: string, node: string, options: DrainOptions = {}): string {
  const parts = [...base(ctx), 'drain', node, '--ignore-daemonsets']
  if (options.force) parts.push('--force')
  if (options.deleteEmptyDirData) parts.push('--delete-emptydir-data')
  if (typeof options.gracePeriodSeconds === 'number' && options.gracePeriodSeconds >= 0) {
    parts.push(`--grace-period=${options.gracePeriodSeconds}`)
  }
  return parts.join(' ')
}

/**
 * `kubectl --context c -n ns create job <name> --from=cronjob/<cronjob>`
 *
 * THE NAME IS THE ONE PODSTEER WILL USE, not a placeholder: the dialog knows
 * what it is about to create, and a transcript that says `<name>` teaches
 * somebody to invent one rather than showing them what happened.
 */
export function createJobFromCronJob(ctx: string, cronJob: string, ns: string, jobName: string): string {
  return [...base(ctx, ns), 'create', 'job', jobName, `--from=cronjob/${cronJob}`].join(' ')
}

/**
 * `kubectl --context c -n ns patch <kind> <name> -p '{"spec":{"suspend":true}}'`
 *
 * A patch rather than `kubectl suspend`, because there is no such verb —
 * suspending is a field, and this is the command that sets it. The same shape
 * resumes, with false, which is why `suspend` is a parameter and not two
 * functions.
 */
export function suspend(ctx: string, kind: string, name: string, ns: string, on: boolean): string {
  const patch = `{"spec":{"suspend":${on}}}`
  return [...base(ctx, ns), 'patch', kind.toLowerCase(), name, '-p', shellQuote(patch)].join(' ')
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

/**
 * `kubectl --context c -n ns debug -it <pod> --image=<image> [--target=<container>] [-- <command>]`.
 *
 * The ephemeral debug container. `--target` shares the named container's
 * process namespace, and is omitted when nothing is targeted. `image` is the
 * one argument an operator can paste anything into, so it runs through
 * `shellQuote`; the container name is a Kubernetes identifier and does not.
 * PodSteer performs this through the pods/ephemeralcontainers subresource, not
 * by shelling out — this only shows the equivalent invocation.
 */
export function debug(
  ctx: string,
  pod: string,
  ns: string,
  image: string,
  target?: string,
  command: string[] = ['sh'],
): string {
  const parts = [...base(ctx, ns), 'debug', '-it', pod, `--image=${shellQuote(image)}`]
  if (target) parts.push(`--target=${target}`)
  parts.push('--', ...command)
  return parts.join(' ')
}

/**
 * `kubectl --context c -n ns cp <pod>:<remote> <local> [-c container]`.
 *
 * `local` is the FULL destination path — `~/Downloads/nginx`, not
 * `~/Downloads` — because that is what kubectl's second argument means and
 * what PodSteer's own download produces: the remote entry lands under the
 * chosen folder by its own name. Both paths are an operator's to type, so
 * both go through `shellQuote`; the pod name is a DNS label and needs
 * nothing.
 */
export function cpFromPod(
  ctx: string,
  pod: string,
  ns: string,
  remotePath: string,
  localPath: string,
  container?: string,
): string {
  const parts = [...base(ctx, ns), 'cp', shellQuote(`${pod}:${remotePath}`), shellQuote(localPath)]
  if (container) parts.push('-c', container)
  return parts.join(' ')
}

/**
 * `kubectl debug node/<node> -it --image=<image> --profile=sysadmin`.
 *
 * What the node shell APPROXIMATES — it is not exactly this. `kubectl debug
 * node/NAME` runs a debugging pod that mounts the host's filesystem at /host;
 * PodSteer instead runs a privileged pod that enters the node's namespaces
 * with nsenter, which is what `kubectl node-shell` and Lens do and what lands
 * a true root shell on the node. The `--profile=sysadmin` flag is the closest
 * kubectl has to the privileges this needs. Deliberately NOT namespaced: the
 * node is cluster-scoped, and PodSteer's own pod lands in a namespace the
 * dialog chooses rather than kubectl's default.
 */
export function debugNode(ctx: string, node: string, image: string): string {
  return [
    ...base(ctx),
    'debug',
    `node/${node}`,
    '-it',
    `--image=${shellQuote(image)}`,
    '--profile=sysadmin',
  ].join(' ')
}

/**
 * `kubectl --context c -n ns run podsteer-shell --rm -it --image=<image> --restart=Never`.
 *
 * What the IN-CLUSTER shell approximates, and the approximation is close: this
 * really is an ordinary pod in a namespace with a shell attached. Two
 * differences worth knowing rather than glossing. `--rm` deletes the pod when
 * kubectl's own session ends and PodSteer deletes it from the Go side instead,
 * so a crashed client cannot strand one — which is also why PodSteer's pod
 * carries an `activeDeadlineSeconds` backstop this line has no equivalent for.
 * And PodSteer's pod carries a security context built to satisfy Pod Security's
 * `restricted` profile; a bare `kubectl run` does not, so this command is
 * refused in exactly the namespaces the feature exists to work in. The
 * `--overrides` needed to add one would make the line unreadable, so the
 * command stays the recognisable one and this comment carries the difference.
 */
export function runShellPod(ctx: string, ns: string, image: string): string {
  return [
    ...base(ctx, ns),
    'run',
    'podsteer-shell',
    '--rm',
    '-it',
    `--image=${shellQuote(image)}`,
    '--restart=Never',
  ].join(' ')
}

/**
 * `kubectl --context c -n ns cp <local> <pod>:<remote> [-c container]`.
 *
 * `remote` is again the full destination — `/app/config`, not `/app` —
 * for the same reason as `cpFromPod`: kubectl names the result, and an
 * upload lands inside the chosen directory under the local entry's name.
 */
export function cpToPod(
  ctx: string,
  pod: string,
  ns: string,
  localPath: string,
  remotePath: string,
  container?: string,
): string {
  const parts = [...base(ctx, ns), 'cp', shellQuote(localPath), shellQuote(`${pod}:${remotePath}`)]
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
 * `kubectl --context c [-n ns] apply -f - --dry-run=server`.
 *
 * What Validate actually sends: the same manifest apply asks the API server
 * to run every admission check against (schema validation, webhooks)
 * without persisting anything. `--dry-run=server` rather than `--dry-run=client`
 * deliberately — a client-side dry run only checks the manifest parses, and
 * PodSteer's own Validate hits the server exactly like this hint says.
 */
export function applyDryRun(ctx: string, ns?: string): string {
  return [...base(ctx, ns), 'apply', '-f', '-', '--dry-run=server'].join(' ')
}

/**
 * `kubectl argo rollouts <verb> <name> -n <ns> --context <ctx>`.
 *
 * THE ONE BUILDER THAT DOES NOT USE `base`, and the exception is not a style
 * choice. `kubectl argo rollouts` is a PLUGIN: kubectl resolves it from the
 * first non-flag arguments and hands everything after them to
 * `kubectl-argo-rollouts`, so the global flags have to come after the
 * subcommand rather than before it. `kubectl --context c argo rollouts …`
 * is not the same command and is not what the plugin's own documentation
 * shows — and a hint that teaches an invocation which does not run is worse
 * than no hint.
 *
 * PodSteer performs the promotion through the API itself, as it does every
 * other write; this is the transcript, never a thing that is executed.
 */
export function argoRollouts(verb: 'promote' | 'abort', ctx: string, name: string, ns: string): string {
  return ['kubectl', 'argo', 'rollouts', verb, name, '-n', ns, '--context', shellQuote(ctx)].join(' ')
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

// --- helm --------------------------------------------------------------------
//
// THE THREE BUILDERS THAT ARE NOT KUBECTL, AND TWO OF THEM ARE NOT A
// TRANSCRIPT EITHER.
//
// Every builder above shows the kubectl command equivalent to a request
// PodSteer is about to make itself. `helm rollback` and `helm uninstall` are
// different in kind, and the difference is the decision recorded in ADR 6
// ("Helm releases are listed from labels and read one release at a time"):
// PodSteer deliberately does NOT perform either. Re-implementing what Helm
// does — re-render, diff, apply, prune, write a new release Secret — means
// re-implementing Helm, and getting it subtly wrong deletes production
// objects; shelling out to the operator's own `helm` was refused because
// starting a program on somebody's machine as a side effect of opening a page
// is a commitment this application makes only through the terminal pane they
// opened themselves and can see.
//
// So the command is COPIED, never run, and the operator runs it in their own
// shell. That keeps the read-only guard coherent rather than eroding it: the
// guard governs PodSteer's own writes to a cluster, and a command somebody
// types is theirs — which is precisely why `StartLocalSession` sits outside
// the guard and says so.
//
// `helm history` is the exception among the three and is a genuine read. It is
// offered beside the other two so the strip shows the whole sequence an
// operator would actually type, rather than only the destructive half.
//
// The flag order follows helm's own documentation: the positional arguments
// first, then `-n`, then `--kube-context`. helm is not a kubectl plugin — it
// is its own binary with its own flag parser — so there is no `base()` to
// share here, and kubectl's `--context` spelling would simply be rejected. As
// everywhere above, `ctx` is a kubeconfig context name and is the one argument
// an operator can put anything into, so it is the one that runs through
// `shellQuote`; a release name and a namespace are DNS-1123 labels and need
// nothing.

/**
 * `helm rollback <release> <revision> -n <ns> --kube-context <ctx>`.
 *
 * NOT EXECUTED BY PODSTEER — see the note above. `revision` is always a
 * specific one here rather than helm's own `0` shorthand for "the previous
 * release", because the strip is shown beside a revision somebody selected and
 * a command that silently means something else is worse than no command.
 */
export function helmRollback(ctx: string, release: string, ns: string, revision: number): string {
  return [
    'helm',
    'rollback',
    release,
    String(revision),
    '-n',
    ns,
    '--kube-context',
    shellQuote(ctx),
  ].join(' ')
}

/**
 * `helm uninstall <release> -n <ns> --kube-context <ctx>`.
 *
 * NOT EXECUTED BY PODSTEER — see the note above. No `--keep-history`: what the
 * strip shows has to be the plain command, and a flag PodSteer chose on the
 * operator's behalf would teach an invocation they never asked for.
 */
export function helmUninstall(ctx: string, release: string, ns: string): string {
  return ['helm', 'uninstall', release, '-n', ns, '--kube-context', shellQuote(ctx)].join(' ')
}

/**
 * `helm get values <release> -n <ns> --kube-context <ctx> > values.yaml`.
 *
 * THE READ THAT MAKES THE UPGRADE BESIDE IT SAFE, and the reason both are
 * offered rather than the upgrade alone. `helm upgrade` in Helm 3 does NOT
 * carry the previous release's values forward: an upgrade run without them
 * reverts every value the operator ever set, quietly, in one command that
 * looks like it only changed a version. Capturing them to a file first is the
 * sequence Helm's own documentation teaches, and it is the sequence PodSteer
 * shows.
 *
 * The redirect is part of the command rather than an instruction beside it,
 * because the file it writes is what the next command reads by name — two
 * halves that have to agree, and the operator should not have to make them
 * agree by hand. It writes into whatever directory their shell is in, which
 * is theirs; PodSteer runs none of this.
 *
 * `helm get values` returns only what was SUPPLIED, not the chart's defaults
 * merged in — which is exactly what belongs in a `-f` file. `--all` would
 * pin every default the chart happens to ship today, so it is deliberately
 * not here.
 */
export function helmGetValues(ctx: string, release: string, ns: string): string {
  return [
    'helm',
    'get',
    'values',
    release,
    '-n',
    ns,
    '--kube-context',
    shellQuote(ctx),
    '>',
    'values.yaml',
  ].join(' ')
}

/**
 * `helm upgrade <release> REPO/<chart> --version VERSION -n <ns> --kube-context <ctx> -f values.yaml`.
 *
 * NOT EXECUTED BY PODSTEER, for the reason the note above gives — an upgrade
 * is the same re-render, diff, apply and prune a rollback is, and doing it
 * approximately deletes production objects.
 *
 * TWO PLACEHOLDERS, IN CAPITALS AND WITHOUT ANGLE BRACKETS. PodSteer cannot
 * know either value and will not invent one:
 *
 *   - REPO is the operator's own repository alias. A release Secret records
 *     the chart's NAME, never where it was fetched from, and PodSteer reads
 *     no chart repositories at all.
 *   - VERSION is the version to move to. Reading an index to offer one is a
 *     network call to a third party this application deliberately does not
 *     make.
 *
 * They are capitals rather than `<repo>` because a copied command is pasted:
 * `<repo>` is a shell redirection and fails with a syntax error about a file,
 * where `REPO/ingress-nginx` reaches helm and fails saying the repository is
 * not found — which names the thing to fill in.
 *
 * The chart name is filled in when a revision has been read and empty
 * otherwise, in which case it too becomes a placeholder: the release list
 * carries no chart name, because it is not a label. See HelmChartIdentity.
 */
export function helmUpgrade(ctx: string, release: string, ns: string, chart: string): string {
  return [
    'helm',
    'upgrade',
    release,
    `REPO/${chart || 'CHART'}`,
    '--version',
    'VERSION',
    '-n',
    ns,
    '--kube-context',
    shellQuote(ctx),
    '-f',
    'values.yaml',
  ].join(' ')
}

/**
 * `helm history <release> -n <ns> --kube-context <ctx>`.
 *
 * The read of the same thing PodSteer's own revision list already shows, from
 * Helm's own storage — offered so the strip carries the harmless command
 * beside the two destructive ones rather than only teaching the dangerous
 * half.
 */
export function helmHistory(ctx: string, release: string, ns: string): string {
  return ['helm', 'history', release, '-n', ns, '--kube-context', shellQuote(ctx)].join(' ')
}
