/**
 * Live exec sessions, held outside the component that draws them.
 *
 * WHY THIS EXISTS. Maximising a pane mounts it inside a dialog, which destroys
 * the component in the tab and builds a new one — `PaneDialog` renders its
 * children under `{#if open}`. For logs that costs nothing: the stream is
 * re-read and the same lines come back.
 *
 * For a shell it would be destructive. A new component calls StartSession, and
 * a new session is a NEW `kubectl exec`: a fresh shell, in the container's
 * default directory, with none of the history, none of the environment somebody
 * had set up, and whatever they had running abandoned. Pressing "maximise" in
 * the middle of tailing a file would silently throw the work away.
 *
 * So the session id lives here rather than in the component. The Go side keys
 * its sessions in a map and does not care who is talking to it, so a remount
 * re-attaches to the session already running instead of opening another. The
 * serialised buffer rides along, which is what makes the change of size look
 * like a resize rather than a reconnect.
 *
 * A GRACE PERIOD RATHER THAN A FLAG, and that is what makes it correct in
 * every direction. Handing the session over explicitly would need the
 * component to know why it is being destroyed — maximise hands over, closing
 * the dialog hands over the other way, switching tabs does not, closing the
 * drawer does not — and it cannot tell those apart. So an unmount always
 * offers the session up, and it is reaped a few seconds later unless somebody
 * has claimed it. A remount claims it in milliseconds; a tab switch or a
 * closed drawer never does, and the exec is stopped.
 *
 * IN MEMORY, FOR THE LIFE OF THE WINDOW. Nothing here is written anywhere: a
 * shell's scrollback is the most sensitive text in the application — whatever
 * somebody typed into a production container — and it is not going to disk.
 */

/**
 * Identifies a session by what it is attached to, not by who opened it.
 *
 * `mode` defaults to 'shell' and is folded into the key only for 'attach', so
 * every existing shell key is unchanged. It has to be part of the key at
 * all: Shell and Attach are two different sessions against the same
 * container — one starts a new process, the other connects to the running
 * one — and without this a mode switch would either collide with, or
 * silently reattach to, a session opened in the other mode.
 */
export function sessionKey(
  clusterId: string,
  namespace: string,
  podName: string,
  container: string,
  mode: 'shell' | 'attach' = 'shell',
): string {
  const base = `${clusterId}/${namespace}/${podName}/${container}`
  return mode === 'attach' ? `${base}/attach` : base
}

interface Held {
  /** The Go-side session id, still live. */
  id: string
  /** The screen as it was, for restoring after a remount. */
  buffer: string
  /** The reaper, cancelled if the session is claimed in time. */
  reap: number
}

/**
 * How long an orphaned session waits to be claimed.
 *
 * Long enough for a remount, which is one frame, and short enough that a
 * closed drawer does not leave an exec running against somebody's production
 * container for any length of time.
 */
const GRACE_MS = 3000

class TerminalSessions {
  /**
   * Plain Map, not `$state`. Nothing renders from it — a component reads it
   * once on mount and writes to it once on destroy — so making it reactive
   * would put a session handover through the reactivity graph for no reader.
   */
  #held = new Map<string, Held>()

  /**
   * Claims the session running for this target, if one is still waiting.
   *
   * Claiming CANCELS THE REAPER — that is the whole handshake. A component
   * that takes a session owns it again, and is responsible for offering it
   * back when it goes.
   */
  take(key: string): { id: string; buffer: string } | null {
    const held = this.#held.get(key)
    if (!held) return null

    window.clearTimeout(held.reap)
    this.#held.delete(key)
    return { id: held.id, buffer: held.buffer }
  }

  /**
   * Offers a session up on unmount, to be reaped if nobody wants it.
   *
   * `stop` is how the session is actually ended, passed in rather than
   * imported so this store has no dependency on the Wails bridge and stays
   * testable without it.
   */
  offer(key: string, id: string, buffer: string, stop: (id: string) => void): void {
    const existing = this.#held.get(key)
    if (existing) window.clearTimeout(existing.reap)

    const reap = window.setTimeout(() => {
      this.#held.delete(key)
      stop(id)
    }, GRACE_MS)

    this.#held.set(key, { id, buffer, reap })
  }

  /**
   * Drops a session without stopping it, for when it has already ended.
   *
   * The far end hanging up, or a deliberate reconnect. Reaping it afterwards
   * would call StopSession on an id the Go side has already forgotten.
   */
  forget(key: string): void {
    const held = this.#held.get(key)
    if (!held) return

    window.clearTimeout(held.reap)
    this.#held.delete(key)
  }
}

export const terminalSessions = new TerminalSessions()
