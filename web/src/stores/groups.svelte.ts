/**
 * Cluster groups: the operator's own organisation of the kubeconfig.
 *
 * Every context the kubeconfig offers starts in the implicit "Default" group.
 * Groups created here are a way to give dozens of contexts meaning — "prod
 * eu", "edge", "qa" — and a kubeconfig does not offer, so it is a PodSteer
 * concept rather than anything read back from kubectl.
 *
 * Group membership is keyed by context name, the handle every other API
 * already uses, and persists in localStorage for the same reasons the display
 * preferences do: it is per-machine, carries no credentials, and must not
 * stall startup on an IPC round trip.
 */

import type { Cluster } from '$lib/api/client'

/** One operator-created group. The default group is implicit, never stored. */
export interface Group {
  id: string
  name: string
}

/** One labelled row of the cluster picker. */
export interface GroupSection {
  /** The group id, or DEFAULT_GROUP_ID for the default group. */
  id: string
  name: string
  /** True for the default group, which cannot be renamed or deleted. */
  isDefault: boolean
  clusters: Cluster[]
}

/** Id of the group every unassigned cluster belongs to. */
export const DEFAULT_GROUP_ID = 'default'
/** Display name of the default group. */
export const DEFAULT_GROUP_NAME = 'Default'

const STORAGE_KEY = 'podsteer.groups.v1'

interface PersistedShape {
  /** Operator-created groups, in creation order. */
  groups: Group[]
  /** Cluster context name -> group id. Absent means default. */
  assignments: Record<string, string>
}

const DEFAULTS: PersistedShape = {
  groups: [],
  assignments: {},
}

class Groups {
  groups = $state<Group[]>(DEFAULTS.groups)

  /** Cluster context name -> group id, for clusters moved out of default. */
  assignments = $state<Record<string, string>>(DEFAULTS.assignments)

  constructor() {
    this.#load()
  }

  /**
   * Splits the kubeconfig's clusters into labelled picker sections.
   *
   * The default group comes first, then the operator's groups in creation
   * order. Empty sections are dropped: a group with nothing in it is clutter
   * in the picker, though it still appears in the management dialog.
   */
  sections = (clusters: Cluster[]): GroupSection[] => {
    const buckets = new Map<string, Cluster[]>()
    for (const cluster of clusters) {
      const groupId = this.groupIdOf(cluster.id)
      const bucket = buckets.get(groupId)
      if (bucket) {
        bucket.push(cluster)
      } else {
        buckets.set(groupId, [cluster])
      }
    }

    const out: GroupSection[] = []
    const defaults = buckets.get(DEFAULT_GROUP_ID)
    if (defaults?.length) {
      out.push({ id: DEFAULT_GROUP_ID, name: DEFAULT_GROUP_NAME, isDefault: true, clusters: defaults })
    }
    for (const group of this.groups) {
      const bucket = buckets.get(group.id)
      if (bucket?.length) {
        out.push({ id: group.id, name: group.name, isDefault: false, clusters: bucket })
      }
    }
    return out
  }

  /** The group id a cluster belongs to, or DEFAULT_GROUP_ID. */
  groupIdOf = (clusterId: string): string => {
    const assigned = this.assignments[clusterId]
    if (assigned && this.groups.some((group) => group.id === assigned)) {
      return assigned
    }
    return DEFAULT_GROUP_ID
  }

  /**
   * Creates a group. Returns an error message, or null on success.
   *
   * Names are unique ignoring case: two groups that look alike are how a
   * cluster gets opened in the wrong environment.
   */
  create = (name: string): string | null => {
    const problem = this.#validate(name)
    if (problem) return problem

    this.groups = [...this.groups, { id: newGroupId(), name: name.trim() }]
    this.#save()
    return null
  }

  /** Renames a group. Returns an error message, or null on success. */
  rename = (id: string, name: string): string | null => {
    const problem = this.#validate(name, id)
    if (problem) return problem

    this.groups = this.groups.map((group) =>
      group.id === id ? { ...group, name: name.trim() } : group,
    )
    this.#save()
    return null
  }

  /**
   * Deletes a group. Its clusters fall back to the default group rather than
   * becoming unfindable.
   */
  remove = (id: string): void => {
    this.groups = this.groups.filter((group) => group.id !== id)

    const assignments = { ...this.assignments }
    for (const [clusterId, groupId] of Object.entries(assignments)) {
      if (groupId === id) delete assignments[clusterId]
    }
    this.assignments = assignments
    this.#save()
  }

  /** Moves a cluster into a group; the default group is implicit. */
  assign = (clusterId: string, groupId: string): void => {
    const assignments = { ...this.assignments }
    if (groupId === DEFAULT_GROUP_ID) {
      delete assignments[clusterId]
    } else {
      assignments[clusterId] = groupId
    }
    this.assignments = assignments
    this.#save()
  }

  /**
   * Shared validation for create and rename: a non-empty, unique name.
   * `ignoreId` lets a group keep its own name on rename.
   */
  #validate(name: string, ignoreId?: string): string | null {
    const trimmed = name.trim()
    if (!trimmed) return 'Enter a group name.'
    if (trimmed.toLowerCase() === DEFAULT_GROUP_NAME.toLowerCase()) {
      return `"${DEFAULT_GROUP_NAME}" is reserved for the default group.`
    }
    const taken = this.groups.some(
      (group) => group.id !== ignoreId && group.name.toLowerCase() === trimmed.toLowerCase(),
    )
    if (taken) return 'A group with that name already exists.'
    return null
  }

  // --- Persistence ----------------------------------------------------------

  #load(): void {
    try {
      const raw = localStorage.getItem(STORAGE_KEY)
      if (!raw) return

      const stored = JSON.parse(raw) as Partial<PersistedShape>

      const groups = Array.isArray(stored.groups)
        ? stored.groups.filter(
            (group): group is Group =>
              typeof group === 'object' &&
              group !== null &&
              typeof group.id === 'string' &&
              typeof group.name === 'string',
          )
        : []
      this.groups = groups

      // Drop assignments pointing at groups that no longer exist, so a
      // cluster always resolves to somewhere.
      const known = new Set(groups.map((group) => group.id))
      const assignments: Record<string, string> = {}
      if (stored.assignments && typeof stored.assignments === 'object') {
        for (const [clusterId, groupId] of Object.entries(stored.assignments)) {
          if (typeof groupId === 'string' && known.has(groupId)) {
            assignments[clusterId] = groupId
          }
        }
      }
      this.assignments = assignments
    } catch {
      // Corrupt storage must not stop the app starting; the default group
      // catching every cluster is a perfectly usable state.
    }
  }

  #save(): void {
    try {
      const payload: PersistedShape = {
        groups: this.groups,
        assignments: this.assignments,
      }
      localStorage.setItem(STORAGE_KEY, JSON.stringify(payload))
    } catch {
      // The organisation still applies to this session; only persistence
      // across restarts is lost.
    }
  }
}

/** Group ids that do not collide with a cluster context name. */
function newGroupId(): string {
  if (typeof crypto !== 'undefined' && 'randomUUID' in crypto) {
    return crypto.randomUUID()
  }
  return `group-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 10)}`
}

/** The application-wide group organisation, shared by the picker and dialog. */
export const groups = new Groups()
