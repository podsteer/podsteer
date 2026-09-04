/**
 * Launches the two special terminal sessions — an ephemeral debug container in
 * a pod, and a node shell on a node — from anywhere in the app.
 *
 * WHY A GLOBAL LAUNCHER. A node shell can be started from the node drawer OR
 * from the node row menu in the list, and a debug container mutates a pod that
 * outlives the drawer it was opened from. Both need a warning dialog first and
 * then a terminal that is not tied to the surface that launched it, so the
 * dialog and the terminal live here and are rendered once, by SessionOverlay,
 * mounted at the workspace level.
 *
 * TWO PHASES. `pending` is the dialog — collecting an image, a namespace, a
 * confirmation. `running` is the terminal, opened once the dialog is
 * confirmed. Only one of each exists at a time: these are deliberate, attended
 * actions, not something to have several of open at once.
 */

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

type Pending = PendingDebug | PendingNodeShell

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

type Running = RunningDebug | RunningNodeShell

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

  /** Closes the terminal. The Terminal component's own teardown stops the
   * session — which, for a node shell, deletes its pod. */
  close(): void {
    this.running = null
  }
}

/** The shared launcher for debug and node-shell sessions. */
export const sessionLauncher = new SessionLauncher()
