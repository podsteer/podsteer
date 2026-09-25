/**
 * Launches the three special terminal sessions — an ephemeral debug container
 * in a pod, a node shell on a node, and a LOCAL shell on the operator's own
 * machine — from anywhere in the app.
 *
 * WHY A GLOBAL LAUNCHER. A node shell can be started from the node drawer OR
 * from the node row menu in the list, and a debug container mutates a pod that
 * outlives the drawer it was opened from. A local shell belongs to the cluster
 * TAB rather than to any object in it, so it has no drawer to live in at all.
 * All three need a dialog first and then a terminal that is not tied to the
 * surface that launched it, so the dialog and the terminal live here and are
 * rendered once, by SessionOverlay, mounted at the workspace level.
 *
 * TWO PHASES. `pending` is the dialog — collecting an image, a namespace, a
 * confirmation. `running` is the terminal, opened once the dialog is
 * confirmed. Only one of each exists at a time: these are deliberate, attended
 * actions, not something to have several of open at once.
 *
 * ONE CLUSTER AT A TIME, and `leave` is what keeps that true. This module is a
 * singleton, but SessionOverlay — the only thing that renders it — is mounted
 * inside the per-cluster workspace, which App.svelte destroys and rebuilds on
 * every tab switch. Nothing here may outlive that: both phases name a cluster,
 * and a phase shown under a tab it does not name is a lie about where the
 * operator is working.
 */

import { localShellRequest, localShellTitle, type CodingAgent } from '$lib/localShell'

/**
 * The object a local terminal was opened beside, named in an agent's first
 * prompt. Plain strings, because it is a label for a sentence rather than a
 * reference anything resolves.
 */
export interface TerminalSubject {
  kind: string
  namespace: string
  name: string
}

/** A pod debug container awaiting its dialog. */
export interface PendingDebug {
  kind: 'debug'
  clusterId: string
  namespace: string
  pod: string
  /** The pod's containers, so the dialog can offer a --target. */
  containers: string[]
  readOnly: boolean
  /** The group name when this cluster is marked production, else null. */
  productionGroup: string | null
}

/** A node shell awaiting its dialog. */
export interface PendingNodeShell {
  kind: 'nodeshell'
  clusterId: string
  node: string
  readOnly: boolean
  productionGroup: string | null
}

/**
 * A local terminal awaiting its dialog.
 *
 * NO readOnly FIELD, and its absence is the design. Every other pending
 * session here carries the cluster's read-only flag so the dialog can refuse.
 * This one cannot be refused on that basis: the guard governs PodSteer's own
 * writes, and a shell on the operator's machine with their own credentials is
 * outside it. The pane says so; see localShellNotice.
 */
export interface PendingLocal {
  kind: 'local'
  /** The kubeconfig context of the tab this was opened from. */
  clusterId: string
  /** The coding agents detected on this machine, possibly none. */
  agents: CodingAgent[]
  /** The object open in the drawer, named in an agent's first prompt. */
  subject: TerminalSubject
}

/**
 * An in-cluster shell awaiting its dialog.
 *
 * `namespace` is the TAB'S current namespace, or '' when the tab is on every
 * one — in which case the dialog asks rather than guessing, and never falls
 * back to a system namespace. See $lib/clusterShell.
 *
 * No readOnly field, and unlike PendingLocal's absence this one is not a
 * design statement: the guard applies in full (creating a pod is a write) and
 * it is enforced where every other write is, synchronously in the backend. The
 * toolbar entry is what carries the disabled state.
 */
export interface PendingClusterShell {
  kind: 'clustershell'
  clusterId: string
  namespace: string
}

type Pending = PendingDebug | PendingNodeShell | PendingLocal | PendingClusterShell

/** A debug terminal that is open. */
export interface RunningDebug {
  kind: 'debug'
  clusterId: string
  namespace: string
  pod: string
  target: string
  image: string
  command: string[]
}

/** A node-shell terminal that is open. */
export interface RunningNodeShell {
  kind: 'nodeshell'
  clusterId: string
  node: string
  namespace: string
  image: string
}

/** A local terminal that is open. */
export interface RunningLocal {
  kind: 'local'
  clusterId: string
  /** The agent to run, or null for the operator's own login shell. */
  agent: string | null
  /** Whether the agent was asked to keep to read-only kubectl. */
  readOnly: boolean
  subject: TerminalSubject
  /** What the pane is titled — the agent's label, or "Local shell". */
  title: string
}

/**
 * An in-cluster shell terminal that is open.
 *
 * `pod` distinguishes the two ways this pane opens: empty means CREATE a pod
 * from `image`, and a name means ATTACH to one PodSteer already has. Two
 * fields rather than two Running kinds, because everything else about the pane
 * — the title, the teardown, the namespace it names — is identical, and the
 * backend has a method for each.
 */
export interface RunningClusterShell {
  kind: 'clustershell'
  clusterId: string
  namespace: string
  image: string
  pod: string
}

type Running = RunningDebug | RunningNodeShell | RunningLocal | RunningClusterShell

class SessionLauncher {
  /** The dialog being shown, or null. */
  pending = $state.raw<Pending | null>(null)
  /** The terminal being shown, or null. */
  running = $state.raw<Running | null>(null)

  /** Opens the debug dialog for one pod. */
  requestDebug(request: Omit<PendingDebug, 'kind'>): void {
    this.pending = { kind: 'debug', ...request }
  }

  /** Opens the node-shell dialog for one node. */
  requestNodeShell(request: Omit<PendingNodeShell, 'kind'>): void {
    this.pending = { kind: 'nodeshell', ...request }
  }

  /** Opens the local-terminal dialog for the tab in front. */
  requestLocal(request: Omit<PendingLocal, 'kind'>): void {
    this.pending = { kind: 'local', ...request }
  }

  /** Opens the in-cluster shell dialog for the tab in front. */
  requestClusterShell(request: Omit<PendingClusterShell, 'kind'>): void {
    this.pending = { kind: 'clustershell', ...request }
  }

  /** Dismisses the dialog without starting anything. */
  cancel(): void {
    this.pending = null
  }

  /** Confirms the debug dialog: closes it and opens the debug terminal. */
  startDebug(target: string, image: string, command: string[]): void {
    if (this.pending?.kind !== 'debug') return
    const { clusterId, namespace, pod } = this.pending
    this.pending = null
    this.running = { kind: 'debug', clusterId, namespace, pod, target, image, command }
  }

  /** Confirms the node-shell dialog: closes it and opens the node-shell terminal. */
  startNodeShell(image: string, namespace: string): void {
    if (this.pending?.kind !== 'nodeshell') return
    const { clusterId, node } = this.pending
    this.pending = null
    this.running = { kind: 'nodeshell', clusterId, node, namespace, image }
  }

  /**
   * Confirms the in-cluster shell dialog by CREATING a pod.
   *
   * The namespace comes from the dialog rather than from the pending request:
   * the tab may have been on "All namespaces", in which case the operator
   * typed one, and it is theirs rather than the tab's.
   */
  startClusterShell(image: string, namespace: string): void {
    if (this.pending?.kind !== 'clustershell') return
    const { clusterId } = this.pending
    this.pending = null
    this.running = { kind: 'clustershell', clusterId, namespace, image, pod: '' }
  }

  /**
   * Confirms it by ATTACHING to a pod PodSteer already has.
   *
   * `image` is left empty: the pane never sends one on this path, because the
   * pod already exists and its image is whatever it was created with. Sending
   * the dialog's image would claim something about a pod nobody created here.
   */
  attachClusterShell(namespace: string, pod: string): void {
    if (this.pending?.kind !== 'clustershell') return
    const { clusterId } = this.pending
    this.pending = null
    this.running = { kind: 'clustershell', clusterId, namespace, image: '', pod }
  }

  /**
   * Confirms the local-terminal dialog: closes it and opens the terminal.
   *
   * The request is normalised through localShellRequest, so an agent that has
   * been uninstalled since the dialog opened falls back to a plain shell
   * rather than being sent for the backend to refuse.
   */
  startLocal(agentId: string, readOnly: boolean): void {
    if (this.pending?.kind !== 'local') return
    const { clusterId, agents, subject } = this.pending
    const request = localShellRequest(agentId, readOnly, agents)
    this.pending = null
    this.running = {
      kind: 'local',
      clusterId,
      agent: request.agent,
      readOnly: request.readOnly,
      subject,
      title: localShellTitle(request, agents),
    }
  }

  /** Closes the terminal. The Terminal component's own teardown stops the
   * session — which, for a node shell and an in-cluster shell, deletes its
   * pod, and for a local shell ends the process on this machine. */
  close(): void {
    this.running = null
  }

  /**
   * Drops both phases because the workspace that was rendering them has gone.
   *
   * SessionOverlay calls this when it is destroyed — on a tab switch, and when
   * the last tab closes. Without it the singleton kept its state across that
   * remount and two things followed, both of which name the WRONG CLUSTER:
   *
   *   - A dialog opened on production reappeared under the next tab, and
   *     confirming it created a privileged pod on production while the
   *     operator was looking at staging.
   *   - A terminal was torn down by the unmount — which for a node shell and
   *     an in-cluster shell DELETES its pod — and then silently started again,
   *     on its original cluster, the moment a workspace mounted. Re-opening
   *     that tab created a second privileged pod with no dialog in between.
   *
   * Dropping rather than restoring, because both are attended actions. A
   * dialog whose context has gone should be re-opened deliberately, and a
   * session whose pod has already been deleted cannot be resumed — only
   * replaced, which is a decision for the operator and not for a remount.
   */
  leave(): void {
    this.pending = null
    this.running = null
  }
}

/** The shared launcher for debug and node-shell sessions. */
export const sessionLauncher = new SessionLauncher()
