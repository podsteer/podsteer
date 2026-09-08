import { beforeEach, describe, expect, it, vi } from 'vitest'

import {
  preferences,
  detailLabelBounds,
  detailLabelWidthCSS,
  detailWidthBounds,
  DETAIL_MAX_REM,
  DETAIL_MAX_SHARE,
  DETAIL_LABEL_MAX_REM,
  DETAIL_LABEL_MAX_SHARE,
  DETAIL_LABEL_MIN_REM,
  DETAIL_MIN_REM,
  DETAIL_WIDTHS,
  DEFAULT_DETAIL_LABEL_SHARE,
  DEFAULT_DEBUG_IMAGE,
  DEFAULT_NODE_SHELL_IMAGE,
  DEFAULT_NODE_SHELL_NAMESPACE,
  defaultExportedPreferences,
  mergeExportedPreferences,
} from './preferences.svelte'
import { LAST_APPLIED_ANNOTATION, type CustomColumnSpec } from '$lib/customColumns'
import { MAX_SAVED_VIEWS } from '$lib/savedViews'

const STORAGE_KEY = 'podsteer.preferences.v1'

/**
 * happy-dom, the environment this suite runs under, does not implement
 * `localStorage` at all (`window.localStorage` is `undefined`) — the store's
 * own #load/#save already treat that as "storage unavailable" and fall back
 * to defaults through a try/catch, which is why every other test in this
 * file never touches it directly. The persistence tests below are the
 * exception: they exist to verify what #load/#save actually read and write,
 * so they need a real (if minimal) Storage to read it back from.
 */
function memoryStorage(): Storage {
  const store = new Map<string, string>()
  return {
    getItem: (key: string) => store.get(key) ?? null,
    setItem: (key: string, value: string) => void store.set(key, value),
    removeItem: (key: string) => void store.delete(key),
    clear: () => store.clear(),
    key: (index: number) => [...store.keys()][index] ?? null,
    get length() {
      return store.size
    },
  } as Storage
}

vi.stubGlobal('localStorage', memoryStorage())

describe('detail panel bounds', () => {
  it('floors at a readable width and ceilings before the window is covered', () => {
    // A wide monitor: both rem bounds apply, and neither is anywhere near the
    // share of the window.
    const wide = detailWidthBounds(3440, 16)
    expect(wide.min).toBe(DETAIL_MIN_REM * 16)
    expect(wide.max).toBe(DETAIL_MAX_REM * 16)

    // A narrow one: the ceiling is the window, not the rem cap.
    const narrow = detailWidthBounds(1024, 16)
    expect(narrow.max).toBe(1024 * DETAIL_MAX_SHARE)
  })

  it('gives the floor up rather than covering the window', () => {
    // A window narrower than the floor. A panel wider than the window it is
    // in has hidden the list completely, which is the one thing the setting
    // exists to prevent — so the floor yields and min never exceeds max.
    const tiny = detailWidthBounds(360, 16)
    expect(tiny.min).toBeLessThanOrEqual(tiny.max)
    expect(tiny.max).toBeLessThanOrEqual(360)
  })

  it('scales with the interface, not with a hardcoded 16px', () => {
    // The floor exists to fit one row of a label and its value. Somebody who
    // has scaled their interface up has made that row taller AND wider, so a
    // floor measured in pixels would stop fitting it.
    const scaled = detailWidthBounds(3440, 20)
    expect(scaled.min).toBe(DETAIL_MIN_REM * 20)
  })
})

describe('detail panel width', () => {
  beforeEach(() => {
    preferences.setDetailWidth(DETAIL_WIDTHS[0].fraction)
  })

  it('keeps the stored share inside something sane', () => {
    // Stored preferences outlive the code that wrote them, and this one is a
    // raw number rather than one of three buttons — deliberately, so the drag
    // can write any width. The real limits are applied per window, in pixels;
    // this is only the guard against a value that is not a share at all.
    preferences.setDetailWidth(3)
    expect(preferences.detailWidthFraction).toBeLessThanOrEqual(0.95)

    preferences.setDetailWidth(0)
    expect(preferences.detailWidthFraction).toBeGreaterThan(0)
  })

  it('never lets a share store as one thing and render as another', () => {
    // THE FAILURE THIS GUARDS. The drag clamps in pixels and then divides by
    // the window width to store a share; if the setter clamped the share more
    // tightly than the pixel bounds do, a drag released at the edge of what
    // the panel allows would be stored as something else and the panel would
    // jump on release. On a small window 90% is a legitimate drag.
    const narrow = 1280
    const { max } = detailWidthBounds(narrow, 16)

    preferences.setDetailWidth(max / narrow)
    expect(preferences.detailWidthFraction * narrow).toBeCloseTo(max, 5)
  })

  it('offers three shares, all of them within what is allowed', () => {
    // A preset the setter would clamp is a button that does not do what it
    // says — it would set a width and then show a different one as selected.
    for (const choice of DETAIL_WIDTHS) {
      preferences.setDetailWidth(choice.fraction)
      expect(preferences.detailWidthFraction).toBeCloseTo(choice.fraction, 5)
    }
  })
})

describe('the column divider', () => {
  it('is one width every section reads, live while it is dragged', () => {
    // THE POINT OF THE GESTURE. Dragging the divider in Identity has to move
    // the one in Labels and in Annotations, so the width being dragged cannot
    // live in the list holding the pointer — every list reads this one value,
    // and it answers with the drag before the drag has been committed.
    preferences.setDetailLabelShare(DEFAULT_DETAIL_LABEL_SHARE)
    expect(preferences.detailLabelShare).toBeCloseTo(DEFAULT_DETAIL_LABEL_SHARE, 5)

    preferences.labelShareDrag = 0.4
    expect(preferences.detailLabelShare).toBeCloseTo(0.4, 5)

    preferences.setDetailLabelShare(0.4)
    expect(preferences.labelShareDrag).toBeNull()
    expect(preferences.detailLabelShare).toBeCloseTo(0.4, 5)
  })

  it('never lets a dragged divider store as one thing and render as another', () => {
    // The same failure the panel's own drag guards against: the drag clamps
    // in pixels and stores a share, so a divider dropped at the end of its
    // travel must come back as the pixel width it was dropped at.
    const pane = 688
    const { min, max } = detailLabelBounds(pane, 16)

    for (const edge of [min, max]) {
      preferences.setDetailLabelShare(edge / pane)
      expect(preferences.detailLabelShare * pane).toBeCloseTo(edge, 5)
    }
  })

  it('leaves the values more room than the labels', () => {
    // Past 60% the labels have more room than the values they describe, which
    // is the wrong way round whatever the pane is — so the ceiling is a share
    // of the pane rather than a length.
    const narrow = detailLabelBounds(400, 16)
    expect(narrow.max).toBeLessThanOrEqual(400 * DETAIL_LABEL_MAX_SHARE)

    const wide = detailLabelBounds(2000, 16)
    expect(wide.max).toBe(DETAIL_LABEL_MAX_REM * 16)
    expect(wide.min).toBe(DETAIL_LABEL_MIN_REM * 16)
  })

  it('renders the same bounds it drags against', () => {
    // The CSS and detailLabelBounds are two spellings of one rule. If they
    // drift, the divider follows the pointer to a width the resting style
    // refuses, and lets go somewhere else.
    const css = detailLabelWidthCSS(0.26)
    expect(css).toContain(`${DETAIL_LABEL_MIN_REM}rem`)
    expect(css).toContain(`${DETAIL_LABEL_MAX_REM}rem`)
    expect(css).toContain(`${DETAIL_LABEL_MAX_SHARE * 100}%`)
    expect(css).toContain('26%')
  })
})

describe('pinned kinds', () => {
  // Unique per test so one test's pins cannot leak into another's — the
  // store is a module singleton, shared for the life of the file.
  let clusterId: string
  let n = 0
  beforeEach(() => {
    clusterId = `pin-test-cluster-${n++}`
  })

  it('starts with nothing pinned', () => {
    expect(preferences.pinnedKindsFor(clusterId)).toEqual([])
    expect(preferences.isKindPinned(clusterId, 'apps/v1/deployments')).toBe(false)
  })

  it('pins in the order pinned, and unpins by id', () => {
    preferences.pinKind(clusterId, 'apps/v1/deployments')
    preferences.pinKind(clusterId, 'core/v1/services')
    preferences.pinKind(clusterId, 'batch/v1/cronjobs')

    expect(preferences.pinnedKindsFor(clusterId)).toEqual([
      'apps/v1/deployments',
      'core/v1/services',
      'batch/v1/cronjobs',
    ])
    expect(preferences.isKindPinned(clusterId, 'core/v1/services')).toBe(true)

    preferences.unpinKind(clusterId, 'core/v1/services')
    expect(preferences.pinnedKindsFor(clusterId)).toEqual([
      'apps/v1/deployments',
      'batch/v1/cronjobs',
    ])
    expect(preferences.isKindPinned(clusterId, 'core/v1/services')).toBe(false)
  })

  it('pinning an already-pinned kind does not duplicate or reorder it', () => {
    preferences.pinKind(clusterId, 'apps/v1/deployments')
    preferences.pinKind(clusterId, 'core/v1/services')
    // Pinning the first one again must not move it to the end — a second
    // click on an already-filled star should do nothing, not reorder the
    // section out from under the operator.
    preferences.pinKind(clusterId, 'apps/v1/deployments')

    expect(preferences.pinnedKindsFor(clusterId)).toEqual([
      'apps/v1/deployments',
      'core/v1/services',
    ])
  })

  it('unpinning something never pinned is a harmless no-op', () => {
    expect(() => preferences.unpinKind(clusterId, 'apps/v1/deployments')).not.toThrow()
    expect(preferences.pinnedKindsFor(clusterId)).toEqual([])
  })

  it('scopes pins to one cluster at a time', () => {
    const other = `${clusterId}-other`
    preferences.pinKind(clusterId, 'apps/v1/deployments')
    preferences.pinKind(other, 'core/v1/nodes')

    expect(preferences.pinnedKindsFor(clusterId)).toEqual(['apps/v1/deployments'])
    expect(preferences.pinnedKindsFor(other)).toEqual(['core/v1/nodes'])
  })

  it('persists kind ids only, keyed by cluster, so a restart keeps them', () => {
    preferences.pinKind(clusterId, 'apps/v1/deployments')
    preferences.pinKind(clusterId, 'core/v1/services')

    const stored = JSON.parse(localStorage.getItem(STORAGE_KEY) ?? '{}')
    expect(stored.pinnedKinds[clusterId]).toEqual(['apps/v1/deployments', 'core/v1/services'])
  })

  it('reads a missing pinnedKinds key as {} rather than failing', async () => {
    // BACKWARD COMPATIBILITY. A preferences blob written before this setting
    // existed has no pinnedKinds key at all — a fresh module load over that
    // blob must default to {} rather than throwing or leaving the field
    // undefined, exactly as every other field here already does.
    const raw = JSON.parse(localStorage.getItem(STORAGE_KEY) ?? '{}')
    delete raw.pinnedKinds
    localStorage.setItem(STORAGE_KEY, JSON.stringify(raw))

    const { preferences: reloaded } = await reimportPreferences()
    expect(reloaded.pinnedKindsFor(clusterId)).toEqual([])
  })

  it('drops anything in a stored entry that is not a kind id string', async () => {
    const raw = JSON.parse(localStorage.getItem(STORAGE_KEY) ?? '{}')
    raw.pinnedKinds = { [clusterId]: ['apps/v1/deployments', 42, null, 'core/v1/services'] }
    localStorage.setItem(STORAGE_KEY, JSON.stringify(raw))

    const { preferences: reloaded } = await reimportPreferences()
    expect(reloaded.pinnedKindsFor(clusterId)).toEqual([
      'apps/v1/deployments',
      'core/v1/services',
    ])
  })
})

/**
 * Re-imports the module fresh, so its constructor runs #load() again against
 * whatever is in localStorage right now.
 *
 * The store is a module singleton loaded once at import time, same as the
 * real app loads it once at startup — so testing what a FRESH load does with
 * stored data means forcing a fresh module instance rather than calling a
 * private method on the one this file already has.
 */
async function reimportPreferences(): Promise<typeof import('./preferences.svelte')> {
  vi.resetModules()
  return import('./preferences.svelte')
}

describe('the images PodSteer puts into a cluster', () => {
  it('defaults the debug container to a NONROOT image', () => {
    // A debug container is injected into somebody else's pod, in their
    // namespace, so Pod Security admission judges it — and `restricted`
    // rejects a root container outright. A default that only works on
    // unlabelled namespaces would fail on the clusters most likely to have
    // somebody debugging in them.
    expect(DEFAULT_DEBUG_IMAGE).toContain('-nonroot')
  })

  it('defaults the node shell to the ROOT image, deliberately', () => {
    // A node shell is a privileged pod entering the host namespaces with
    // nsenter. That is root by definition, and the nonroot variant could not
    // do the one thing this pod exists for — so the two defaults differ on
    // purpose and must not be "tidied" into one.
    expect(DEFAULT_NODE_SHELL_IMAGE).not.toContain('-nonroot')
  })

  it('pins both to an exact tag rather than a floating one', () => {
    // What PodSteer creates in a cluster must not change because an upstream
    // tag was republished. A new image arrives in a PodSteer release.
    for (const image of [DEFAULT_DEBUG_IMAGE, DEFAULT_NODE_SHELL_IMAGE]) {
      const tag = image.slice(image.lastIndexOf(':') + 1)
      expect(tag).not.toBe('latest')
      expect(tag).toMatch(/^v?\d+\.\d+\.\d+/)
    }
  })

  it('names a registry that answers an anonymous pull', () => {
    // ghcr.io returns 403 to an anonymous pull of this repository, so a ghcr
    // reference would be ImagePullBackOff on every cluster without a
    // credential for it — which is all of them out of the box.
    for (const image of [DEFAULT_DEBUG_IMAGE, DEFAULT_NODE_SHELL_IMAGE]) {
      expect(image.startsWith('docker.io/')).toBe(true)
      expect(image).not.toContain('ghcr.io')
    }
  })
})

describe('remembered debug and node-shell inputs', () => {
  it('remembers the debug image, and a blank one resets to the default', () => {
    preferences.setDebugImage('ubuntu:24.04')
    expect(preferences.debugImage).toBe('ubuntu:24.04')
    // A blank must never be persisted — the backend would reject an empty
    // image — so it falls back to the default.
    preferences.setDebugImage('   ')
    expect(preferences.debugImage).toBe(DEFAULT_DEBUG_IMAGE)
  })

  it('remembers the node-shell image and namespace, each resetting on blank', () => {
    preferences.setNodeShellImage('docker.io/library/ubuntu:24.04')
    preferences.setNodeShellNamespace('ops')
    expect(preferences.nodeShellImage).toBe('docker.io/library/ubuntu:24.04')
    expect(preferences.nodeShellNamespace).toBe('ops')

    preferences.setNodeShellImage('')
    preferences.setNodeShellNamespace('')
    expect(preferences.nodeShellImage).toBe(DEFAULT_NODE_SHELL_IMAGE)
    expect(preferences.nodeShellNamespace).toBe(DEFAULT_NODE_SHELL_NAMESPACE)
  })

  it('persists the debug and node-shell inputs to storage', () => {
    const written = new Map<string, string>()
    vi.stubGlobal('localStorage', {
      getItem: (key: string) => written.get(key) ?? null,
      setItem: (key: string, value: string) => void written.set(key, value),
      removeItem: (key: string) => void written.delete(key),
      clear: () => written.clear(),
    })

    try {
      preferences.setDebugImage('busybox:1.36')
      preferences.setNodeShellNamespace('ops')

      const raw = written.get(STORAGE_KEY) ?? ''
      expect(raw).toContain('busybox:1.36')
      expect(raw).toContain('ops')
    } finally {
      // Restore the file-level storage rather than unstubbing every global:
      // happy-dom has no localStorage of its own, so unstubbing would leave
      // the tests after this one with an undefined global.
      vi.stubGlobal('localStorage', memoryStorage())
    }
  })
})

describe('custom columns', () => {
  const kindId = 'apps/v1/deployments'
  const team: CustomColumnSpec = { source: 'label', key: 'team' }
  const owner: CustomColumnSpec = { source: 'annotation', key: 'acme.io/owner' }

  beforeEach(() => {
    for (const spec of preferences.customColumnsFor(kindId)) {
      preferences.removeCustomColumn(kindId, spec)
    }
  })

  it('adds columns per kind, in order, and refuses a duplicate', () => {
    expect(preferences.addCustomColumn(kindId, team)).toBe(true)
    expect(preferences.addCustomColumn(kindId, owner)).toBe(true)
    expect(preferences.addCustomColumn(kindId, { ...team })).toBe(false)

    expect(preferences.customColumnsFor(kindId)).toEqual([team, owner])
    // Per KIND, not per cluster and not global: a Pods list has its own.
    expect(preferences.customColumnsFor('core/v1/pods')).toEqual([])
  })

  it('refuses an invalid key at the storage layer, whatever the picker did', () => {
    expect(preferences.addCustomColumn(kindId, { source: 'label', key: 'bad key' })).toBe(false)
    expect(
      preferences.addCustomColumn(kindId, { source: 'annotation', key: LAST_APPLIED_ANNOTATION }),
    ).toBe(false)
    expect(preferences.customColumnsFor(kindId)).toEqual([])
  })

  it('reorders by index, ignores an index off the end, and drops an emptied kind', () => {
    preferences.addCustomColumn(kindId, team)
    preferences.addCustomColumn(kindId, owner)

    preferences.moveCustomColumn(kindId, 1, 0)
    expect(preferences.customColumnsFor(kindId)).toEqual([owner, team])

    // A stale index from a menu open across a change must not reorder
    // something else.
    preferences.moveCustomColumn(kindId, 0, 5)
    expect(preferences.customColumnsFor(kindId)).toEqual([owner, team])

    preferences.removeCustomColumn(kindId, owner)
    preferences.removeCustomColumn(kindId, team)
    expect(kindId in preferences.customColumns).toBe(false)
  })

  it('persists them under the kind id — a catalogue id and a key, never an object name', () => {
    preferences.addCustomColumn(kindId, team)

    const stored = JSON.parse(localStorage.getItem(STORAGE_KEY) ?? '{}')
    expect(stored.customColumns).toEqual({ [kindId]: [team] })
  })

  it('validates every stored entry on a fresh load and defaults a missing key to {}', async () => {
    const raw = JSON.parse(localStorage.getItem(STORAGE_KEY) ?? '{}')
    raw.customColumns = {
      [kindId]: [team, { source: 'spec', key: 'replicas' }, 'label:team', { ...team }],
      'core/v1/pods': 'not a list',
    }
    localStorage.setItem(STORAGE_KEY, JSON.stringify(raw))

    const { preferences: reloaded } = await reimportPreferences()
    expect(reloaded.customColumnsFor(kindId)).toEqual([team])
    expect(reloaded.customColumnsFor('core/v1/pods')).toEqual([])

    // BACKWARD COMPATIBILITY, as for pinnedKinds: a blob written before this
    // setting existed loads with no custom columns anywhere.
    delete raw.customColumns
    localStorage.setItem(STORAGE_KEY, JSON.stringify(raw))

    const { preferences: fresh } = await reimportPreferences()
    expect(fresh.customColumns).toEqual({})
  })
})

describe('remembered local ports', () => {
  it('proposes nothing until a forward has been remembered', () => {
    expect(preferences.proposeLocalPort(5432, 'postgres')).toBeUndefined()
  })

  it('proposes by the remote port number when no name is on record', () => {
    preferences.rememberLocalPort(5432, '', 15432)

    expect(preferences.proposeLocalPort(5432, '')).toBe(15432)
    // A caller asking with a name the store has never seen still falls back
    // to what is known about the bare remote port.
    expect(preferences.proposeLocalPort(5432, 'db')).toBe(15432)
  })

  it('lets a port NAME take precedence over the remote port number', () => {
    preferences.rememberLocalPort(5432, 'postgres', 15432)
    // A second pod exposing the same NAMED port on a different remote port —
    // legitimate, since the remote port is only a convention.
    preferences.rememberLocalPort(15432, 'postgres', 25432)

    // The name's memory is what a container port called "postgres" gets,
    // wherever it turns up next, even though the remote port here (15432)
    // has its own separate memory from the write above.
    expect(preferences.proposeLocalPort(15432, 'postgres')).toBe(25432)
    // The bare-number memory for 5432 is untouched by the name-keyed write.
    expect(preferences.proposeLocalPort(5432, '')).toBe(15432)
  })

  it('never writes a pod, namespace or cluster name into the persisted shape', () => {
    // A realistic caller holds a whole Forward — pod, namespace, cluster —
    // but rememberLocalPort's signature has nowhere to put any of them. This
    // asserts that stays true: SECURITY.md enumerates what PodSteer writes to
    // disk, and object names are not on that list.
    //
    // A stub Storage rather than the ambient `localStorage`: this test
    // environment's global is a Node experimental stand-in that throws on
    // access (silently swallowed by #save's own try/catch, which is exactly
    // why every other test here never needed one) — asserting on the actual
    // written bytes needs something that genuinely stores them.
    const written = new Map<string, string>()
    vi.stubGlobal('localStorage', {
      getItem: (key: string) => written.get(key) ?? null,
      setItem: (key: string, value: string) => void written.set(key, value),
      removeItem: (key: string) => void written.delete(key),
      clear: () => written.clear(),
    })

    const fixturePod = 'postgres-primary-0'
    const fixtureNamespace = 'payments-prod'
    const fixtureCluster = 'prod-euc3'

    try {
      preferences.rememberLocalPort(5432, 'postgres', 15432)

      const raw = written.get('podsteer.preferences.v1') ?? ''
      expect(raw).not.toContain(fixturePod)
      expect(raw).not.toContain(fixtureNamespace)
      expect(raw).not.toContain(fixtureCluster)
      // The port numbers are exactly what should have been written.
      expect(raw).toContain('15432')
      expect(raw).toContain('postgres')
    } finally {
      vi.unstubAllGlobals()
    }
  })
})

describe('the navigator sections that fold the other way round', () => {
  /**
   * Pinned and Recent are stored as what is CLOSED, not as what is open, and
   * every assertion here is about that inversion rather than about a toggle
   * working. A kind category is absent until somebody opens it; these two are
   * open until somebody closes them, because Pinned holds what an operator
   * deliberately put one click away and folding it by default would hide the
   * very thing its owner asked to see.
   */
  it('is open before anybody has touched it, and after an upgrade', () => {
    preferences.collapsedSections = []

    expect(preferences.isSectionExpanded('Pinned')).toBe(true)
    expect(preferences.isSectionExpanded('Recent')).toBe(true)
  })

  it('closes and reopens the one section it was asked about', () => {
    preferences.collapsedSections = []

    preferences.toggleSection('Pinned')
    expect(preferences.isSectionExpanded('Pinned')).toBe(false)
    // The neighbour is untouched: one list, one entry per folded section.
    expect(preferences.isSectionExpanded('Recent')).toBe(true)

    preferences.toggleSection('Pinned')
    expect(preferences.isSectionExpanded('Pinned')).toBe(true)
    expect(preferences.collapsedSections).toEqual([])
  })

  it('carries the section labels and nothing that could name an object', () => {
    preferences.collapsedSections = []
    preferences.toggleSection('Pinned')
    preferences.toggleSection('Recent')

    // The guard behind admitting this field to the exported settings file: the
    // only strings that can ever reach it are the two section labels.
    expect(preferences.exportable().collapsedSections.sort()).toEqual(['Pinned', 'Recent'])
  })
})

describe('merging a colleague\'s folded sections', () => {
  /**
   * `expandedCategories` unions on merge, because it is a set of what is OPEN
   * and combining two people's open sections is what merging them means.
   * `collapsedSections` is the same idea written backwards, so it has to merge
   * backwards too: a union of two CLOSED sets hides more than either person
   * had hidden, and importing a colleague's file would fold away a section you
   * had open. Intersecting keeps one closed only where both agree.
   */
  function withCollapsed(sections: string[]) {
    return { ...defaultExportedPreferences(), collapsedSections: sections }
  }

  it('keeps a section open when only the file had folded it', () => {
    const merged = mergeExportedPreferences(
      withCollapsed(['Pinned']),
      { collapsedSections: ['Recent'] },
      'merge',
    )

    // Neither survives: each was open for one of the two people.
    expect(merged.collapsedSections).toEqual([])
  })

  it('keeps a section folded when both had folded it', () => {
    const merged = mergeExportedPreferences(
      withCollapsed(['Pinned', 'Recent']),
      { collapsedSections: ['Pinned'] },
      'merge',
    )

    expect(merged.collapsedSections).toEqual(['Pinned'])
  })

  it('takes the file exactly under replace', () => {
    const merged = mergeExportedPreferences(
      withCollapsed(['Pinned']),
      { collapsedSections: ['Recent'] },
      'replace',
    )

    expect(merged.collapsedSections).toEqual(['Recent'])
  })

  it('leaves what is here alone when the file does not mention them', () => {
    const merged = mergeExportedPreferences(withCollapsed(['Pinned']), {}, 'merge')

    expect(merged.collapsedSections).toEqual(['Pinned'])
  })
})

describe('the columns that stay put while a table scrolls sideways', () => {
  beforeEach(() => {
    // A describe above this one unstubs every global when it is done, and
    // happy-dom has no localStorage of its own — so this restores one rather
    // than reading `undefined.getItem`. A fresh one per test, since these
    // read back exactly what the store wrote.
    vi.stubGlobal('localStorage', memoryStorage())
    preferences.fixedEdges = { select: true, menu: true }
  })

  it('fixes both edges until somebody says otherwise', () => {
    expect(preferences.isEdgeFixed('select')).toBe(true)
    expect(preferences.isEdgeFixed('menu')).toBe(true)
  })

  it('turns one edge off without touching the other', () => {
    preferences.toggleEdgeFixed('menu')
    expect(preferences.isEdgeFixed('menu')).toBe(false)
    expect(preferences.isEdgeFixed('select')).toBe(true)
  })

  it('is application-wide, so resetting one kind of column layout leaves it alone', () => {
    preferences.setColumnWidth('v1/pods', 'name', 400)
    preferences.toggleEdgeFixed('select')

    preferences.resetColumns('v1/pods')

    expect(preferences.columnWidth('v1/pods', 'name')).toBeUndefined()
    expect(preferences.isEdgeFixed('select')).toBe(false)
  })

  it('survives a restart, and an older blob loads with both edges fixed', async () => {
    preferences.toggleEdgeFixed('select')

    const stored = JSON.parse(localStorage.getItem(STORAGE_KEY) ?? '{}')
    expect(stored.fixedEdges).toEqual({ select: false, menu: true })

    const { preferences: reloaded } = await reimportPreferences()
    expect(reloaded.isEdgeFixed('select')).toBe(false)
    expect(reloaded.isEdgeFixed('menu')).toBe(true)

    // BACKWARD COMPATIBILITY, as for pinnedKinds and customColumns: a blob
    // written before this setting existed loads at the default rather than
    // with an undefined that reads as "not fixed".
    delete stored.fixedEdges
    localStorage.setItem(STORAGE_KEY, JSON.stringify(stored))

    const { preferences: fresh } = await reimportPreferences()
    expect(fresh.isEdgeFixed('select')).toBe(true)
    expect(fresh.isEdgeFixed('menu')).toBe(true)
  })

  it('repairs a stored value key by key rather than adopting it whole', async () => {
    const stored = JSON.parse(localStorage.getItem(STORAGE_KEY) ?? '{}')
    // A key this build does not know, and one whose value is not a boolean.
    stored.fixedEdges = { select: 'yes', menu: false, sidebar: true }
    localStorage.setItem(STORAGE_KEY, JSON.stringify(stored))

    const { preferences: reloaded } = await reimportPreferences()
    expect(reloaded.fixedEdges).toEqual({ select: true, menu: false })
  })
})

describe('views somebody named and kept', () => {
  beforeEach(() => {
    // Same reason as the block above: happy-dom has no localStorage, and
    // these tests read back exactly what the store wrote.
    vi.stubGlobal('localStorage', memoryStorage())
    preferences.savedViews = []
  })

  const crashing = {
    kindId: 'core/v1/pods',
    namespace: 'kube-system',
    search: 're:crash',
    statusFilters: ['crashing'],
  }

  it('keeps a view and reads it back after a restart', async () => {
    preferences.saveView('Crashing pods', crashing)

    const { preferences: reloaded } = await reimportPreferences()
    expect(reloaded.savedViews).toEqual([
      { id: 'crashing-pods', name: 'Crashing pods', ...crashing },
    ])
  })

  it('replaces a view saved under a name already in use, in place', () => {
    // Saving "Crashing pods" twice is somebody correcting the view, not
    // collecting two of them — and the row must not jump to the bottom of a
    // list they were reading.
    preferences.saveView('Crashing pods', crashing)
    preferences.saveView('Other', { ...crashing, namespace: 'default' })
    preferences.saveView('crashing PODS', { ...crashing, search: 'oomkilled' })

    expect(preferences.savedViews).toHaveLength(2)
    expect(preferences.savedViews[0]).toMatchObject({
      id: 'crashing-pods',
      name: 'crashing PODS',
      search: 'oomkilled',
    })
  })

  it('refuses a blank name rather than saving something nobody can find', () => {
    expect(preferences.saveView('   ', crashing)).toBeNull()
    expect(preferences.savedViews).toEqual([])
  })

  it('stops at the ceiling, and still lets an existing view be corrected', () => {
    for (let index = 0; index < MAX_SAVED_VIEWS; index++) {
      preferences.saveView(`View ${index}`, crashing)
    }

    expect(preferences.saveView('One more', crashing)).toBeNull()
    expect(preferences.savedViews).toHaveLength(MAX_SAVED_VIEWS)

    // Replacing is not adding, so the ceiling does not block it.
    expect(preferences.saveView('View 0', { ...crashing, search: 'changed' })).not.toBeNull()
    expect(preferences.savedViews[0].search).toBe('changed')
  })

  it('forgets one, and says nothing about one that was never there', () => {
    preferences.saveView('Crashing pods', crashing)
    preferences.deleteView('nothing-like-this')
    expect(preferences.savedViews).toHaveLength(1)

    preferences.deleteView('crashing-pods')
    expect(preferences.savedViews).toEqual([])
  })

  it('drops a stored view that could never be applied', async () => {
    // Validated on the way in by the same function the settings import uses:
    // stored preferences outlive the code that wrote them.
    const stored = JSON.parse(localStorage.getItem(STORAGE_KEY) ?? '{}')
    stored.savedViews = [
      { id: 'good', name: 'Good', kindId: 'core/v1/pods', namespace: '', search: '', statusFilters: [] },
      { id: 'no-kind', name: 'Broken' },
    ]
    localStorage.setItem(STORAGE_KEY, JSON.stringify(stored))

    const { preferences: reloaded } = await reimportPreferences()
    expect(reloaded.savedViews.map((view) => view.id)).toEqual(['good'])
  })
})

describe('clusters pinned to the top of the picker', () => {
  beforeEach(() => {
    vi.stubGlobal('localStorage', memoryStorage())
    preferences.pinnedClusters = []
  })

  it('pins, unpins, and keeps the order they were pinned in', () => {
    preferences.toggleClusterPin('prod')
    preferences.toggleClusterPin('staging')
    expect(preferences.pinnedClusters).toEqual(['prod', 'staging'])
    expect(preferences.isClusterPinned('prod')).toBe(true)

    preferences.toggleClusterPin('prod')
    expect(preferences.pinnedClusters).toEqual(['staging'])
    expect(preferences.isClusterPinned('prod')).toBe(false)
  })

  it('survives a restart', async () => {
    preferences.toggleClusterPin('prod')

    const { preferences: reloaded } = await reimportPreferences()
    expect(reloaded.pinnedClusters).toEqual(['prod'])
  })

  it('keeps only strings out of a hand-edited blob', async () => {
    const stored = JSON.parse(localStorage.getItem(STORAGE_KEY) ?? '{}')
    stored.pinnedClusters = ['prod', 7, null, 'staging']
    localStorage.setItem(STORAGE_KEY, JSON.stringify(stored))

    const { preferences: reloaded } = await reimportPreferences()
    expect(reloaded.pinnedClusters).toEqual(['prod', 'staging'])
  })
})
