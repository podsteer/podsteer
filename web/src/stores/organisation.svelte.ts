/**
 * The operator's own organisation of the kubeconfig: projects, then groups,
 * then clusters.
 *
 * A kubeconfig is a flat list of contexts, which stops being navigable at
 * about a dozen. Two levels are needed rather than one because the two
 * questions people actually ask are different: WHICH SYSTEM is this
 * ("checkout", "the data platform") and WHICH ENVIRONMENT ("dev", "staging",
 * "prod"). One level forces those into a single name — "checkout-prod",
 * "checkout-dev" — which is exactly the flat list again with longer strings.
 *
 * Both levels have an implicit Default, so someone who never opens the
 * organiser sees one project containing one group containing everything, which
 * is the shape this had before projects existed.
 *
 * None of it comes from kubectl — it is a PodSteer concept keyed by context
 * name, the handle every other API already uses. It persists in localStorage
 * for the same reasons the display preferences do: per-machine, no
 * credentials, and it must not stall startup on an IPC round trip.
 */

import type { Cluster } from '$lib/api/client'

/** One operator-created project. The default project is implicit. */
export interface Project {
  id: string
  name: string
}

/**
 * What environment a group represents, for colour-coding and the production
 * guardrails (a banner on a write dialog, a name-typed confirmation). Empty
 * string means unset — deliberately not defaulted to anything, because
 * guessing "production" would be as wrong as guessing "development", and
 * guessing wrong here is worse than saying nothing: an unmarked group that
 * silently behaved like production would train someone to distrust the
 * marking, and one that behaved like development would fail to guard the
 * cluster it was protecting.
 */
export type Environment = 'production' | 'staging' | 'development' | 'other' | ''

/** Every choice the environment select offers, in the order it offers them. */
export const ENVIRONMENTS: Array<{ value: Environment; label: string }> = [
  { value: '', label: 'Not set' },
  { value: 'production', label: 'Production' },
  { value: 'staging', label: 'Staging' },
  { value: 'development', label: 'Development' },
  { value: 'other', label: 'Other' },
]

/**
 * The fixed palette a group's colour is chosen from.
 *
 * Named tokens, not free hex: a colour picker turns "what does blue mean on
 * this cluster" into a question with sixty answers across an organisation,
 * where six tokens keep it into one everyone can learn. The values live
 * beside the Material tokens in app.css, themed for light and dark.
 */
export const GROUP_COLOURS = ['red', 'orange', 'yellow', 'green', 'blue', 'purple'] as const
export type GroupColour = (typeof GROUP_COLOURS)[number]

/**
 * A group's guardrail settings: what it is, how it is marked, and whether
 * PodSteer will act on it.
 *
 * One shape for both kinds of group — see `settingsFor` and `setGroupSettings`
 * for how a project's Default group (which has no `Group` record of its own)
 * and an operator-created one are read and written through it alike.
 */
export interface GroupSettings {
  environment: Environment
  colour: GroupColour | ''
  readOnly: boolean
}

/** What an unmarked group's settings are — every field at its "not set". */
const NO_GROUP_SETTINGS: GroupSettings = { environment: '', colour: '', readOnly: false }

/** One operator-created group, always inside exactly one project. */
export interface Group extends GroupSettings {
  id: string
  name: string
  projectId: string
}

/**
 * Where a cluster sits.
 *
 * The project is stored alongside the group rather than derived from it,
 * because a cluster can sit directly in a project's Default group — which has
 * no record of its own to look the project up from.
 */
export interface Placement {
  project: string
  group: string
}

/** One group's row in the picker. */
export interface GroupSection {
  id: string
  name: string
  /** True for a project's Default group, which cannot be renamed or deleted. */
  isDefault: boolean
  projectId: string
  clusters: Cluster[]
  settings: GroupSettings
}

/** One project's block in the picker. */
export interface ProjectSection {
  id: string
  name: string
  /** True for the Default project, which cannot be renamed or deleted. */
  isDefault: boolean
  groups: GroupSection[]
  /** Clusters across every group in this project. */
  clusterCount: number
}

/**
 * The key a group's UI state is filed under.
 *
 * Not the group id: every project's Default group shares the id
 * DEFAULT_GROUP_ID, so collapsing one project's Default would collapse every
 * project's Default, and two of them would claim the same DOM id. The pair is
 * unique where the id alone is not.
 */
export const groupKey = (projectId: string, groupId: string): string => `${projectId}/${groupId}`

/** Ids and names of the two implicit containers. */
export const DEFAULT_PROJECT_ID = 'default-project'
export const DEFAULT_PROJECT_NAME = 'Default'
export const DEFAULT_GROUP_ID = 'default'
export const DEFAULT_GROUP_NAME = 'Default'

const STORAGE_KEY = 'podsteer.organisation.v1'
/** Read once, to carry forward organisation made before projects existed. */
const LEGACY_STORAGE_KEY = 'podsteer.groups.v1'

interface PersistedShape {
  projects: Project[]
  groups: Group[]
  /** The default project's name, when the operator has changed it. */
  defaultProjectName?: string
  /** Project id -> that project's default group name, where changed. */
  defaultGroupNames?: Record<string, string>
  /**
   * Project id -> that project's default group's guardrail settings, where
   * changed. The same "changed" shape as defaultGroupNames, and for the same
   * reason: the Default group has no Group record of its own to carry them
   * on, since every project's Default shares DEFAULT_GROUP_ID.
   */
  defaultGroupSettings?: Record<string, Partial<GroupSettings>>
  /** Cluster context name -> where it sits. Absent means both Defaults. */
  assignments: Record<string, Placement>
  /** Project and group ids the operator collapsed. Absent means expanded. */
  collapsed: string[]
}

const DEFAULTS: PersistedShape = {
  projects: [],
  groups: [],
  assignments: {},
  collapsed: [],
}

/** Where anything unassigned, or assigned to something deleted, ends up. */
const HOME: Placement = { project: DEFAULT_PROJECT_ID, group: DEFAULT_GROUP_ID }

class Organisation {
  projects = $state<Project[]>(DEFAULTS.projects)
  groups = $state<Group[]>(DEFAULTS.groups)

  /**
   * What the two implicit containers are CALLED, where that has been changed.
   *
   * Only the names are stored, never the containers. Keeping the default as
   * absence is what lets a fresh install have empty storage and lets
   * placementOf repair anything by falling back to "nothing recorded" — so a
   * rename must not turn it into a record. The ids stay DEFAULT_PROJECT_ID
   * and DEFAULT_GROUP_ID whatever it is called.
   *
   * Group names are per project: renaming one project's default group must
   * not rename another's.
   */
  defaultProjectName = $state<string>(DEFAULT_PROJECT_NAME)
  defaultGroupNames = $state<Record<string, string>>({})
  /** Project id -> that project's default group's guardrail settings. */
  defaultGroupSettings = $state<Record<string, GroupSettings>>({})
  assignments = $state<Record<string, Placement>>(DEFAULTS.assignments)

  /**
   * Project and group ids the operator has collapsed.
   *
   * Stored as the exception rather than the rule: a newly created container is
   * expanded, and a shape that only records collapses cannot accidentally hide
   * one it has never heard of.
   */
  collapsed = $state<Set<string>>(new Set())

  constructor() {
    this.#load()
  }

  // --- Reading --------------------------------------------------------------

  /**
   * Where a cluster sits, repaired against what actually exists.
   *
   * Every lookup validates rather than trusting the stored value, because the
   * alternative is a cluster that belongs to a deleted project and therefore
   * appears nowhere in the picker. Falling back is always better than
   * vanishing.
   */
  placementOf = (clusterId: string): Placement => {
    const stored = this.assignments[clusterId]
    if (!stored) return HOME

    const projectExists =
      stored.project === DEFAULT_PROJECT_ID ||
      this.projects.some((project) => project.id === stored.project)
    if (!projectExists) return HOME

    if (stored.group === DEFAULT_GROUP_ID) {
      return { project: stored.project, group: DEFAULT_GROUP_ID }
    }

    // A group that no longer exists, or was moved to another project, drops
    // the cluster to its project's Default rather than to the Default project:
    // the project is the coarser fact and the likelier one to still be right.
    const group = this.groups.find((candidate) => candidate.id === stored.group)
    if (!group || group.projectId !== stored.project) {
      return { project: stored.project, group: DEFAULT_GROUP_ID }
    }
    return stored
  }

  /** What a project's default group is called. */
  defaultGroupNameFor = (projectId: string): string =>
    this.defaultGroupNames[projectId] ?? DEFAULT_GROUP_NAME

  /**
   * A group's guardrail settings, repaired the same way `placementOf` repairs
   * a placement: a group that no longer exists reports the unmarked default
   * rather than throwing, since a stale caller — a session for a cluster
   * whose group was deleted a moment ago — is an ordinary race, not a bug.
   *
   * The Default group has no `Group` record of its own to hold these on,
   * because DEFAULT_GROUP_ID is shared by every project's Default — the same
   * reason `defaultGroupNameFor` reads a side table instead of a field.
   */
  settingsFor = (projectId: string, groupId: string): GroupSettings => {
    if (groupId === DEFAULT_GROUP_ID) {
      return this.defaultGroupSettings[projectId] ?? NO_GROUP_SETTINGS
    }
    const group = this.groups.find(
      (candidate) => candidate.id === groupId && candidate.projectId === projectId,
    )
    return group
      ? { environment: group.environment, colour: group.colour, readOnly: group.readOnly }
      : NO_GROUP_SETTINGS
  }

  /**
   * Changes one or more of a group's guardrail settings, leaving the rest.
   *
   * A custom group's settings live directly on its `Group` record, so they
   * travel for free when `moveGroupToProject` reparents it — the same way its
   * name already does. The Default group's live in `defaultGroupSettings`,
   * keyed by project, mirroring `defaultGroupNames`.
   */
  setGroupSettings = (projectId: string, groupId: string, patch: Partial<GroupSettings>): void => {
    if (groupId === DEFAULT_GROUP_ID) {
      const current = this.defaultGroupSettings[projectId] ?? NO_GROUP_SETTINGS
      this.defaultGroupSettings = {
        ...this.defaultGroupSettings,
        [projectId]: { ...current, ...patch },
      }
    } else {
      this.groups = this.groups.map((candidate) =>
        candidate.id === groupId ? { ...candidate, ...patch } : candidate,
      )
    }
    this.#save()
  }

  /** Every group in a project, its Default first. */
  groupsIn = (
    projectId: string,
  ): Array<{ id: string; name: string; isDefault: boolean; settings: GroupSettings }> => [
    {
      id: DEFAULT_GROUP_ID,
      name: this.defaultGroupNameFor(projectId),
      isDefault: true,
      settings: this.settingsFor(projectId, DEFAULT_GROUP_ID),
    },
    ...this.groups
      .filter((group) => group.projectId === projectId)
      .map((group) => ({
        id: group.id,
        name: group.name,
        isDefault: false,
        settings: this.settingsFor(projectId, group.id),
      })),
  ]

  /** Every project, the Default first, in the operator's order. */
  allProjects = (): Array<{ id: string; name: string; isDefault: boolean }> => [
    { id: DEFAULT_PROJECT_ID, name: this.defaultProjectName, isDefault: true },
    ...this.projects.map((project) => ({ id: project.id, name: project.name, isDefault: false })),
  ]

  /**
   * The picker's whole tree.
   *
   * `includeEmpty` keeps containers with nothing in them. The picker passes
   * true — an empty project the operator just created has to be visible, or
   * creating it looks like it failed — while callers that only want to report
   * what exists pass false.
   */
  sections = (clusters: Cluster[], includeEmpty = false): ProjectSection[] => {
    // One pass, keyed by the project/group pair, so the cost is linear in clusters
    // rather than clusters × groups.
    const buckets = new Map<string, Cluster[]>()
    for (const cluster of clusters) {
      const { project, group } = this.placementOf(cluster.id)
      const key = groupKey(project, group)
      const bucket = buckets.get(key)
      if (bucket) bucket.push(cluster)
      else buckets.set(key, [cluster])
    }

    const out: ProjectSection[] = []
    for (const project of this.allProjects()) {
      const groups: GroupSection[] = []
      for (const group of this.groupsIn(project.id)) {
        const members = buckets.get(groupKey(project.id, group.id)) ?? []
        if (members.length || includeEmpty) {
          groups.push({
            id: group.id,
            name: group.name,
            isDefault: group.isDefault,
            projectId: project.id,
            clusters: members,
            settings: group.settings,
          })
        }
      }

      const clusterCount = groups.reduce((total, group) => total + group.clusters.length, 0)
      if (clusterCount || includeEmpty) {
        out.push({
          id: project.id,
          name: project.name,
          isDefault: project.isDefault,
          groups,
          clusterCount,
        })
      }
    }
    return out
  }

  /** Counts clusters in one project, for the organiser's summaries. */
  countIn = (clusters: Cluster[], projectId: string, groupId?: string): number =>
    clusters.filter((cluster) => {
      const placement = this.placementOf(cluster.id)
      if (placement.project !== projectId) return false
      return groupId === undefined || placement.group === groupId
    }).length

  // --- Collapsing -----------------------------------------------------------

  isCollapsed = (id: string): boolean => this.collapsed.has(id)

  /**
   * Collapses or expands one project or group.
   *
   * A new Set rather than a mutation: Svelte tracks the assignment, and
   * mutating in place would leave the picker showing the previous state.
   */
  toggleCollapsed = (id: string): void => {
    const next = new Set(this.collapsed)
    if (!next.delete(id)) next.add(id)
    this.collapsed = next
    this.#save()
  }

  // --- Moving clusters ------------------------------------------------------

  /** Moves a cluster into a project and one of its groups. */
  place = (clusterId: string, project: string, group: string): void => {
    const assignments = { ...this.assignments }
    if (project === DEFAULT_PROJECT_ID && group === DEFAULT_GROUP_ID) {
      // The implicit home is stored as absence, so the file does not grow a
      // row for every cluster the operator never organised.
      delete assignments[clusterId]
    } else {
      assignments[clusterId] = { project, group }
    }
    this.assignments = assignments
    this.#save()
  }

  // --- Projects -------------------------------------------------------------

  /** Creates a project. Returns an error message, or null on success. */
  createProject = (name: string): string | null => {
    const problem = this.#validateProject(name)
    if (problem) return problem

    this.projects = [...this.projects, { id: newId('project'), name: name.trim() }]
    this.#save()
    return null
  }

  renameProject = (id: string, name: string): string | null => {
    const problem = this.#validateProject(name, id)
    if (problem) return problem

    if (id === DEFAULT_PROJECT_ID) {
      // The name, and only the name. Materialising a record here would undo
      // the "default is absence" invariant everything else depends on.
      this.defaultProjectName = name.trim()
    } else {
      this.projects = this.projects.map((project) =>
        project.id === id ? { ...project, name: name.trim() } : project,
      )
    }
    this.#save()
    return null
  }

  /**
   * Deletes a project, with its groups.
   *
   * Its clusters fall back to the Default project rather than becoming
   * unfindable — the same rule groups follow, for the same reason.
   */
  removeProject = (id: string): void => {
    this.projects = this.projects.filter((project) => project.id !== id)

    const orphanedKeys = this.groups
      .filter((group) => group.projectId === id)
      .map((group) => groupKey(id, group.id))
    this.groups = this.groups.filter((group) => group.projectId !== id)

    const assignments = { ...this.assignments }
    for (const [clusterId, placement] of Object.entries(assignments)) {
      if (placement.project === id) delete assignments[clusterId]
    }
    this.assignments = assignments

    if (id in this.defaultGroupNames) {
      const names = { ...this.defaultGroupNames }
      delete names[id]
      this.defaultGroupNames = names
    }
    if (id in this.defaultGroupSettings) {
      const settings = { ...this.defaultGroupSettings }
      delete settings[id]
      this.defaultGroupSettings = settings
    }

    this.#forgetCollapsed([id, groupKey(id, DEFAULT_GROUP_ID), ...orphanedKeys])
    this.#save()
  }

  // --- Groups ---------------------------------------------------------------

  /** Creates a group inside a project. Returns an error message, or null. */
  createGroup = (name: string, projectId: string): string | null => {
    const problem = this.#validateGroup(name, projectId)
    if (problem) return problem

    this.groups = [
      ...this.groups,
      { id: newId('group'), name: name.trim(), projectId, ...NO_GROUP_SETTINGS },
    ]
    this.#save()
    return null
  }

  /**
   * Renames a group.
   *
   * `projectId` is required rather than derived, because the default group has
   * no record to derive it from — and its id is the same in every project, so
   * without it there is no way to tell which project's default is meant.
   */
  renameGroup = (id: string, name: string, projectId: string): string | null => {
    if (id !== DEFAULT_GROUP_ID && !this.groups.some((group) => group.id === id)) {
      return 'That group no longer exists.'
    }

    const problem = this.#validateGroup(name, projectId, id)
    if (problem) return problem

    if (id === DEFAULT_GROUP_ID) {
      this.defaultGroupNames = { ...this.defaultGroupNames, [projectId]: name.trim() }
    } else {
      this.groups = this.groups.map((candidate) =>
        candidate.id === id ? { ...candidate, name: name.trim() } : candidate,
      )
    }
    this.#save()
    return null
  }

  /** Deletes a group. Its clusters fall back to the project's Default group. */
  removeGroup = (id: string): void => {
    const doomed = this.groups.find((group) => group.id === id)
    this.groups = this.groups.filter((group) => group.id !== id)

    const assignments = { ...this.assignments }
    for (const [clusterId, placement] of Object.entries(assignments)) {
      if (placement.group === id) {
        assignments[clusterId] = { project: placement.project, group: DEFAULT_GROUP_ID }
      }
    }
    this.assignments = assignments

    this.#forgetCollapsed(doomed ? [groupKey(doomed.projectId, id)] : [])
    this.#save()
  }

  // --- Ordering -------------------------------------------------------------

  /**
   * Moves a project or group one place earlier or later.
   *
   * Creation order is the wrong order the moment someone has six projects and
   * production is the one they open every morning. Ordering is by adjacent
   * swap rather than drag: the organiser is a list, the lists are short, and
   * two buttons work from a keyboard.
   *
   * The implicit Defaults are not in these arrays and therefore always lead —
   * which is the right place for the container everything starts in.
   */
  moveProject = (id: string, delta: -1 | 1): void => {
    this.projects = reorder(this.projects, id, delta)
    this.#save()
  }

  /**
   * Moves a group, with its clusters, into a different project.
   *
   * The clusters have to be carried explicitly. A placement records the
   * project alongside the group, so a group that changed project would leave
   * every member pointing at the project it came from — and placementOf, which
   * distrusts exactly that mismatch, would drop them all into the old
   * project's Default. The group would arrive empty and the clusters would
   * appear to have been left behind.
   */
  moveGroupToProject = (groupId: string, projectId: string): void => {
    const group = this.groups.find((candidate) => candidate.id === groupId)
    if (!group || group.projectId === projectId) return

    const from = group.projectId
    this.groups = this.groups.map((candidate) =>
      candidate.id === groupId ? { ...candidate, projectId } : candidate,
    )

    const assignments = { ...this.assignments }
    for (const [clusterId, placement] of Object.entries(assignments)) {
      if (placement.group === groupId) {
        assignments[clusterId] = { project: projectId, group: groupId }
      }
    }
    this.assignments = assignments

    // Collapse state is filed per project/group pair, so the old key now
    // names nothing. Carry it rather than dropping it: a group the operator
    // had shut should still be shut where it lands.
    if (this.collapsed.has(groupKey(from, groupId))) {
      const next = new Set(this.collapsed)
      next.delete(groupKey(from, groupId))
      next.add(groupKey(projectId, groupId))
      this.collapsed = next
    }

    this.#save()
  }

  /**
   * Moves a project so it sits where `beforeId` currently does.
   *
   * Index-based rather than by delta, because a drag lands on a row rather
   * than stepping one place. Passing null means "to the end".
   */
  placeProjectBefore = (id: string, beforeId: string | null): void => {
    this.projects = placeBefore(this.projects, id, beforeId)
    this.#save()
  }

  /** The same, for a group among its own project's groups. */
  placeGroupBefore = (id: string, beforeId: string | null): void => {
    const group = this.groups.find((candidate) => candidate.id === id)
    if (!group) return

    const siblings = this.groups.filter((candidate) => candidate.projectId === group.projectId)
    const moved = placeBefore(siblings, id, beforeId)

    const queue = [...moved]
    this.groups = this.groups.map((candidate) =>
      candidate.projectId === group.projectId ? (queue.shift() as Group) : candidate,
    )
    this.#save()
  }

  /** Moves a group within its own project. */
  moveGroup = (id: string, delta: -1 | 1): void => {
    const group = this.groups.find((candidate) => candidate.id === id)
    if (!group) return

    // Reordered within the project's own slice, then stitched back, so a group
    // cannot jump over one belonging to a different project.
    const siblings = this.groups.filter((candidate) => candidate.projectId === group.projectId)
    const moved = reorder(siblings, id, delta)

    const queue = [...moved]
    this.groups = this.groups.map((candidate) =>
      candidate.projectId === group.projectId ? (queue.shift() as Group) : candidate,
    )
    this.#save()
  }

  // --- Validation -----------------------------------------------------------

  #validateProject(name: string, ignoreId?: string): string | null {
    const trimmed = name.trim()
    if (!trimmed) return 'Enter a project name.'

    // The reservation follows the CURRENT name rather than the constant. Once
    // the default is called "Personal", "Default" is an ordinary name and
    // "Personal" is the one that would produce two indistinguishable rows.
    if (
      ignoreId !== DEFAULT_PROJECT_ID &&
      trimmed.toLowerCase() === this.defaultProjectName.toLowerCase()
    ) {
      return `"${this.defaultProjectName}" is the default project.`
    }

    const taken = this.projects.some(
      (project) => project.id !== ignoreId && project.name.toLowerCase() === trimmed.toLowerCase(),
    )
    return taken ? 'A project with that name already exists.' : null
  }

  /**
   * Group names are unique WITHIN a project, not globally: "staging" in two
   * different projects is the normal case, not a mistake.
   */
  #validateGroup(name: string, projectId: string, ignoreId?: string): string | null {
    const trimmed = name.trim()
    if (!trimmed) return 'Enter a group name.'

    // Scoped to this project: another project's default may well be called
    // the same thing, and that is not a collision.
    const defaultName = this.defaultGroupNameFor(projectId)
    if (ignoreId !== DEFAULT_GROUP_ID && trimmed.toLowerCase() === defaultName.toLowerCase()) {
      return `"${defaultName}" is this project's default group.`
    }

    const taken = this.groups.some(
      (group) =>
        group.id !== ignoreId &&
        group.projectId === projectId &&
        group.name.toLowerCase() === trimmed.toLowerCase(),
    )
    return taken ? 'That project already has a group with that name.' : null
  }

  #forgetCollapsed(ids: string[]): void {
    if (!ids.some((id) => this.collapsed.has(id))) return
    const next = new Set(this.collapsed)
    for (const id of ids) next.delete(id)
    this.collapsed = next
  }

  // --- Persistence ----------------------------------------------------------

  #load(): void {
    try {
      const raw = localStorage.getItem(STORAGE_KEY)
      if (raw) {
        this.#adopt(JSON.parse(raw) as Partial<PersistedShape>)
        return
      }
      this.#adoptLegacy()
    } catch {
      // Corrupt storage must not stop the app starting; one Default project
      // holding one Default group holding everything is a usable state.
    }
  }

  #adopt(stored: Partial<PersistedShape>): void {
    const projects = Array.isArray(stored.projects)
      ? stored.projects.filter(isNamedRecord)
      : []
    this.projects = projects

    const knownProjects = new Set(projects.map((project) => project.id))
    const groups = Array.isArray(stored.groups)
      ? stored.groups
          .filter(
            (group): group is Group =>
              isNamedRecord(group) &&
              typeof (group as Group).projectId === 'string' &&
              ((group as Group).projectId === DEFAULT_PROJECT_ID ||
                knownProjects.has((group as Group).projectId)),
          )
          // Backward-compatible read: a group persisted before guardrail
          // settings existed carries none of these three fields, and each
          // normalises to its "not set" value rather than being dropped —
          // the group itself is still perfectly valid, it simply predates the
          // feature.
          .map((group) => ({ ...group, ...normaliseSettings(group) }))
      : []
    this.groups = groups

    if (typeof stored.defaultProjectName === 'string' && stored.defaultProjectName.trim()) {
      this.defaultProjectName = stored.defaultProjectName
    }
    if (stored.defaultGroupNames && typeof stored.defaultGroupNames === 'object') {
      const names: Record<string, string> = {}
      for (const [projectId, name] of Object.entries(stored.defaultGroupNames)) {
        // Kept only for projects that still exist, since nothing else would
        // ever prune them; the Default project always does.
        const lives = projectId === DEFAULT_PROJECT_ID || knownProjects.has(projectId)
        if (lives && typeof name === 'string' && name.trim()) names[projectId] = name
      }
      this.defaultGroupNames = names
    }
    if (stored.defaultGroupSettings && typeof stored.defaultGroupSettings === 'object') {
      const settings: Record<string, GroupSettings> = {}
      for (const [projectId, raw] of Object.entries(stored.defaultGroupSettings)) {
        const lives = projectId === DEFAULT_PROJECT_ID || knownProjects.has(projectId)
        if (lives && raw && typeof raw === 'object') settings[projectId] = normaliseSettings(raw)
      }
      this.defaultGroupSettings = settings
    } else {
      this.defaultGroupSettings = {}
    }

    // Placements are repaired on read by placementOf, so storage only has to
    // drop what is malformed rather than what is stale.
    const assignments: Record<string, Placement> = {}
    if (stored.assignments && typeof stored.assignments === 'object') {
      for (const [clusterId, placement] of Object.entries(stored.assignments)) {
        if (
          placement &&
          typeof placement === 'object' &&
          typeof placement.project === 'string' &&
          typeof placement.group === 'string'
        ) {
          assignments[clusterId] = { project: placement.project, group: placement.group }
        }
      }
    }
    this.assignments = assignments

    // Not validated against what exists: group keys are project-scoped pairs,
    // and a key for something deleted collapses nothing rather than hiding
    // anything. Removal prunes, which is what keeps this from growing.
    if (Array.isArray(stored.collapsed)) {
      this.collapsed = new Set(
        stored.collapsed.filter((id): id is string => typeof id === 'string'),
      )
    }
  }

  /**
   * Carries forward organisation made before projects existed.
   *
   * Every old group becomes a group of the Default project, and every old
   * assignment keeps its group — so someone who had organised their contexts
   * finds them where they left them, one level deeper than before.
   *
   * Read once and then written under the new key. The old key is deliberately
   * left in place rather than deleted: it costs a few hundred bytes and it is
   * the only copy if this migration turns out to be wrong.
   */
  #adoptLegacy(): void {
    const raw = localStorage.getItem(LEGACY_STORAGE_KEY)
    if (!raw) return

    const stored = JSON.parse(raw) as {
      groups?: Array<{ id?: unknown; name?: unknown }>
      assignments?: Record<string, unknown>
      collapsed?: unknown[]
    }

    const groups = Array.isArray(stored.groups) ? stored.groups.filter(isNamedRecord) : []
    this.groups = groups.map((group) => ({
      ...group,
      projectId: DEFAULT_PROJECT_ID,
      ...NO_GROUP_SETTINGS,
    }))

    const known = new Set(groups.map((group) => group.id))
    const assignments: Record<string, Placement> = {}
    if (stored.assignments && typeof stored.assignments === 'object') {
      for (const [clusterId, groupId] of Object.entries(stored.assignments)) {
        if (typeof groupId === 'string' && known.has(groupId)) {
          assignments[clusterId] = { project: DEFAULT_PROJECT_ID, group: groupId }
        }
      }
    }
    this.assignments = assignments

    // Old collapse ids were bare group ids, all of them in what is now the
    // Default project, so they need the project prefix to still mean anything.
    if (Array.isArray(stored.collapsed)) {
      this.collapsed = new Set(
        stored.collapsed
          .filter((id): id is string => typeof id === 'string' && (id === DEFAULT_GROUP_ID || known.has(id)))
          .map((id) => groupKey(DEFAULT_PROJECT_ID, id)),
      )
    }

    this.#save()
  }

  #save(): void {
    try {
      const payload: PersistedShape = {
        projects: this.projects,
        groups: this.groups,
        defaultProjectName: this.defaultProjectName,
        defaultGroupNames: this.defaultGroupNames,
        defaultGroupSettings: this.defaultGroupSettings,
        assignments: this.assignments,
        collapsed: [...this.collapsed],
      }
      localStorage.setItem(STORAGE_KEY, JSON.stringify(payload))
    } catch {
      // The organisation still applies to this session; only persistence
      // across restarts is lost.
    }
  }
}

/** True for the `{ id, name }` shape both projects and groups start from. */
function isNamedRecord(value: unknown): value is { id: string; name: string } {
  return (
    typeof value === 'object' &&
    value !== null &&
    typeof (value as { id?: unknown }).id === 'string' &&
    typeof (value as { name?: unknown }).name === 'string'
  )
}

function isEnvironment(value: unknown): value is Environment {
  return (
    value === '' ||
    value === 'production' ||
    value === 'staging' ||
    value === 'development' ||
    value === 'other'
  )
}

function isGroupColour(value: unknown): value is GroupColour | '' {
  return value === '' || (GROUP_COLOURS as readonly string[]).includes(value as string)
}

/**
 * Reads a `GroupSettings` out of an arbitrary object, defaulting whatever is
 * missing or malformed to "not set" rather than dropping the whole record.
 *
 * The one function both read paths for guardrail settings go through — a
 * persisted `Group` (which carries these fields inline) and a persisted
 * `defaultGroupSettings` entry (which is exactly this shape on its own) — so
 * "what counts as a valid environment" is decided once.
 */
function normaliseSettings(raw: Partial<GroupSettings> | undefined): GroupSettings {
  return {
    environment: isEnvironment(raw?.environment) ? raw.environment : '',
    colour: isGroupColour(raw?.colour) ? raw.colour : '',
    readOnly: raw?.readOnly === true,
  }
}

/**
 * Returns a copy with `id` moved to sit immediately before `beforeId`.
 *
 * A null `beforeId` means the end. Removing before computing the insertion
 * point is what keeps the arithmetic honest when an item moves forwards: the
 * target's index shifts by one the moment the item leaves.
 */
function placeBefore<T extends { id: string }>(items: T[], id: string, beforeId: string | null): T[] {
  const from = items.findIndex((item) => item.id === id)
  if (from < 0 || id === beforeId) return items

  const next = [...items]
  const [moved] = next.splice(from, 1)

  const to = beforeId === null ? next.length : next.findIndex((item) => item.id === beforeId)
  if (to < 0) return items

  next.splice(to, 0, moved)
  return next
}

/** Returns a copy with one item moved by delta, or the original if it cannot. */
function reorder<T extends { id: string }>(items: T[], id: string, delta: number): T[] {
  const from = items.findIndex((item) => item.id === id)
  const to = from + delta
  if (from < 0 || to < 0 || to >= items.length) return items

  const next = [...items]
  const [moved] = next.splice(from, 1)
  next.splice(to, 0, moved)
  return next
}

/** Ids that cannot collide with a cluster context name or each other. */
function newId(kind: 'project' | 'group'): string {
  if (typeof crypto !== 'undefined' && 'randomUUID' in crypto) {
    return `${kind}-${crypto.randomUUID()}`
  }
  return `${kind}-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 10)}`
}

/** The application-wide organisation, shared by the picker and the organiser. */
export const organisation = new Organisation()
