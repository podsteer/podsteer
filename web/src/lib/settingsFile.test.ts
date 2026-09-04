import { beforeEach, describe, expect, it, vi } from 'vitest'

import {
  applyImport,
  buildDocument,
  buildSettingsFilename,
  countChanges,
  currentPayload,
  parseDocument,
  previewImport,
  serialiseDocument,
  SETTINGS_FILE_KIND,
  SETTINGS_FILE_VERSION,
  type SettingsDocument,
} from './settingsFile'
import {
  EXPORTED_PREFERENCE_FIELDS,
  defaultExportedPreferences,
  preferences,
} from '$stores/preferences.svelte'
import {
  DEFAULT_GROUP_ID,
  DEFAULT_PROJECT_ID,
  EXPORTED_ORGANISATION_FIELDS,
  defaultExportedOrganisation,
  organisation,
} from '$stores/organisation.svelte'

/**
 * happy-dom does not implement `localStorage`, and both stores treat that as
 * "storage unavailable" through their own try/catch — see the same helper in
 * preferences.test.ts. These tests write through the stores, so they need a
 * real (if minimal) Storage for the saves not to throw.
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

/** Puts both stores back to what a fresh install holds. */
function resetStores(): void {
  preferences.applyExported(defaultExportedPreferences())
  organisation.applyExported(defaultExportedOrganisation())
}

/**
 * The forbidden categories, each a string that could only have arrived from
 * something the export must never carry.
 *
 * Deliberately not a context name: a CONTEXT NAME does travel, on purpose,
 * and the document says so in its own header. Everything here is either an
 * object inside a cluster, a credential, or a cluster's address.
 */
const FORBIDDEN = {
  namespace: 'ns-should-never-travel',
  pod: 'pod-should-never-travel',
  node: 'node-should-never-travel',
  findingId: 'finding-should-never-travel',
  server: 'https://api.cluster.example.invalid:6443',
  token: 'eyJhbGciOiJSUzI1NiIsInR5cCI6IkpXVCJ9-should-never-travel',
} as const

/** A context name, which is the one cluster-shaped thing that DOES travel. */
const CONTEXT = 'prod-eu-west-1'

/** Fills both stores with everything a real install would hold, forbidden included. */
function populateEverything(): void {
  resetStores()

  // Things that must travel.
  preferences.setTheme('light')
  preferences.setPageSize(50)
  preferences.setRefreshInterval(30_000)
  preferences.setNavigatorWidth(300)
  preferences.setDetailWidth(0.4)
  preferences.setDetailLabelShare(0.3)
  preferences.toggleCategory('Workloads')
  preferences.toggleWrapLines()
  preferences.toggleManagedFields()
  preferences.pinKind(CONTEXT, 'apps/v1/deployments')
  preferences.rememberLocalPort(5432, 'postgres', 15432)
  preferences.setDebugImage('ghcr.io/example/debug:2')
  preferences.setNodeShellImage('docker.io/library/alpine:3.21')
  preferences.setNodeShellNamespace('ops')
  preferences.setThreshold('pods', 'warn', 85)
  preferences.setThresholdEnabled('nodes', 'critical', false)
  preferences.setPodMeasure('requests')
  preferences.setMapOrientation('vertical')
  preferences.setUpdateChecksEnabled(false)
  preferences.setSectionOpen('pod.volumes', true)
  preferences.setAlertSoundsEnabled(true)
  preferences.setColumnWidth('v1/pods', 'name', 320)
  preferences.toggleColumn('v1/pods', 'age')
  preferences.addCustomColumn('apps/v1/deployments', { source: 'label', key: 'team' })

  const projectError = organisation.createProject('Checkout')
  expect(projectError).toBeNull()
  const project = organisation.allProjects().find((entry) => entry.name === 'Checkout')
  expect(project).toBeDefined()
  expect(organisation.createGroup('Production', project!.id)).toBeNull()
  const group = organisation.groupsIn(project!.id).find((entry) => entry.name === 'Production')
  expect(group).toBeDefined()
  organisation.setGroupSettings(project!.id, group!.id, {
    environment: 'production',
    colour: 'red',
    readOnly: true,
  })
  organisation.place(CONTEXT, project!.id, group!.id)
  organisation.toggleCollapsed(project!.id)
  organisation.renameProject(DEFAULT_PROJECT_ID, 'Personal')
  organisation.renameGroup(DEFAULT_GROUP_ID, 'Unsorted', DEFAULT_PROJECT_ID)

  // Things that must NOT travel, each written where the application really
  // writes it — so this test fails if any of these fields ever joins the
  // export, rather than passing on a shape nobody populated.
  preferences.setClusterNamespace(CONTEXT, FORBIDDEN.namespace)
  preferences.snooze(CONTEXT, FORBIDDEN.findingId, FORBIDDEN.namespace, FORBIDDEN.pod, 3_600_000)
  preferences.snooze(CONTEXT, FORBIDDEN.findingId, '', FORBIDDEN.node, 3_600_000)
  preferences.markUpdateChecked(1_700_000_000_000)
  preferences.dismissUpdate('v9.9.9')
}

beforeEach(() => {
  resetStores()
})

describe('what a settings file must never carry', () => {
  it('carries no object name, credential or cluster address from a fully populated store', () => {
    populateEverything()

    const text = serialiseDocument(buildDocument(new Date('2026-09-04T12:00:00.000Z')))

    for (const [category, secret] of Object.entries(FORBIDDEN)) {
      expect(
        text.includes(secret),
        `the export leaked the ${category} category: ${secret}`,
      ).toBe(false)
    }

    // The categories are checked by their VALUES above and by their field
    // names here, so a rename of either half still fails the test.
    for (const field of ['snoozes', 'namespaceByCluster', 'recentObjects', 'kubeconfig']) {
      expect(text.includes(`"${field}"`), `the export gained a ${field} field`).toBe(false)
    }

    // What DOES travel, so the test cannot pass by exporting nothing.
    expect(text).toContain(CONTEXT)
    expect(text).toContain('Checkout')
    expect(text).toContain('production')
  })

  it('exports exactly the agreed preference fields, so a new one must be argued for', () => {
    // A LITERAL LIST, not derived from the type. Adding a field to the export
    // means editing this line, which is the point: two of the fields this
    // store already persists hold object names, and the next one might.
    expect(Object.keys(preferences.exportable()).sort()).toEqual(
      [
        'alertSounds',
        'alertSoundsEnabled',
        'autoRefresh',
        'columns',
        'customColumns',
        'debugImage',
        'desktopNotificationsEnabled',
        'detailLabelFraction',
        'detailWidthFraction',
        'expandedCategories',
        'findingsExpanded',
        'localPortByPortName',
        'localPortByRemotePort',
        'mapOrientation',
        'navigatorCollapsed',
        'navigatorWidth',
        'nodeShellImage',
        'nodeShellNamespace',
        'pageSize',
        'pinnedKinds',
        'podMeasure',
        'refreshIntervalMs',
        'sections',
        'showManagedFields',
        'themePreference',
        'thresholds',
        'updateChecksEnabled',
        'usageWindowMinutes',
        'wrapLines',
      ].sort(),
    )
  })

  it('exports exactly the agreed organisation fields', () => {
    expect(Object.keys(organisation.exportable()).sort()).toEqual(
      [
        'assignments',
        'collapsed',
        'defaultGroupNames',
        'defaultGroupSettings',
        'defaultProjectName',
        'groups',
        'projects',
      ].sort(),
    )
  })

  // The hand-written exportable() and the list the reader, the merge and the
  // review are driven from have to name the same fields, or a field would be
  // written and never read back.
  it('keeps the hand-written export and the field list in step', () => {
    expect(Object.keys(preferences.exportable()).sort()).toEqual(
      [...EXPORTED_PREFERENCE_FIELDS].sort(),
    )
    expect(Object.keys(organisation.exportable()).sort()).toEqual(
      [...EXPORTED_ORGANISATION_FIELDS].sort(),
    )
  })

  it('says in the file itself that context names are in it', () => {
    const document = buildDocument()
    const header = document._readme.join(' ')
    expect(header).toMatch(/CONTEXT NAMES/)
    expect(header).toMatch(/NO credentials/)
    expect(header).toMatch(/NO object names/)
  })
})

describe('the round trip', () => {
  it('restores every setting an export carried', () => {
    populateEverything()
    const exported = serialiseDocument(buildDocument())
    const before = currentPayload()

    // A different machine: everything back to what a fresh install holds.
    resetStores()
    expect(currentPayload()).not.toEqual(before)

    const parsed = parseDocument(exported)
    expect(parsed.ok).toBe(true)
    if (!parsed.ok) return

    applyImport(previewImport(currentPayload(), parsed.document, 'merge'))
    expect(currentPayload()).toEqual(before)
  })

  it('leaves the categories it never carried untouched on the receiving machine', () => {
    // Replace is the destructive mode, and even it must not erase what the
    // document was silent about — the snoozes and the namespace filter are
    // held back from the export precisely so they never leave the machine,
    // not so that importing can delete them.
    populateEverything()
    const exported = serialiseDocument(buildDocument())

    const parsed = parseDocument(exported)
    expect(parsed.ok).toBe(true)
    if (!parsed.ok) return

    applyImport(previewImport(currentPayload(), parsed.document, 'replace'))

    expect(preferences.getClusterNamespace(CONTEXT)).toBe(FORBIDDEN.namespace)
    expect(
      preferences.snoozedUntil(CONTEXT, FORBIDDEN.findingId, FORBIDDEN.namespace, FORBIDDEN.pod),
    ).toBeGreaterThan(0)
  })

  it('names the file after when it was written', () => {
    expect(buildSettingsFilename(new Date(2026, 8, 4, 20, 34, 5))).toBe(
      'podsteer-settings-20260904-203405.json',
    )
  })
})

/** A document carrying one preference and one project, for the import tests. */
function documentWith(overrides: Partial<SettingsDocument> = {}): SettingsDocument {
  return {
    _readme: ['test fixture'],
    kind: SETTINGS_FILE_KIND,
    version: SETTINGS_FILE_VERSION,
    exportedAt: '2026-09-04T12:00:00.000Z',
    preferences: { ...defaultExportedPreferences(), pageSize: 100, debugImage: 'busybox:9' },
    organisation: {
      ...defaultExportedOrganisation(),
      projects: [{ id: 'project-incoming', name: 'Data platform' }],
    },
    ...overrides,
  }
}

describe('merge versus replace', () => {
  it('merge keeps what the file does not mention', () => {
    resetStores()
    organisation.createProject('Local only')
    preferences.pinKind('staging', 'v1/pods')

    const parsed = parseDocument(JSON.stringify(documentWith()))
    expect(parsed.ok).toBe(true)
    if (!parsed.ok) return

    const preview = previewImport(currentPayload(), parsed.document, 'merge')
    applyImport(preview)

    // Both projects, and both people's pinned kinds.
    expect(organisation.exportable().projects.map((project) => project.name)).toEqual([
      'Local only',
      'Data platform',
    ])
    expect(preferences.exportable().pinnedKinds).toEqual({ staging: ['v1/pods'] })
    expect(preferences.pageSize).toBe(100)
  })

  it('replace makes the exported surface exactly the file', () => {
    resetStores()
    organisation.createProject('Local only')
    preferences.pinKind('staging', 'v1/pods')

    const parsed = parseDocument(JSON.stringify(documentWith()))
    expect(parsed.ok).toBe(true)
    if (!parsed.ok) return

    applyImport(previewImport(currentPayload(), parsed.document, 'replace'))

    expect(organisation.exportable().projects.map((project) => project.name)).toEqual([
      'Data platform',
    ])
    expect(preferences.exportable().pinnedKinds).toEqual({})
  })

  it('replace returns a field the file omits to this build default, not to empty', () => {
    resetStores()
    preferences.setPageSize(10)

    const document = documentWith()
    // A document written by a build that had no page size at all.
    const raw = JSON.parse(JSON.stringify(document)) as Record<string, unknown>
    delete (raw.preferences as Record<string, unknown>).pageSize

    const parsed = parseDocument(JSON.stringify(raw))
    expect(parsed.ok).toBe(true)
    if (!parsed.ok) return

    applyImport(previewImport(currentPayload(), parsed.document, 'replace'))
    expect(preferences.pageSize).toBe(defaultExportedPreferences().pageSize)
  })

  it('reviews exactly what applying will do', () => {
    resetStores()
    organisation.createProject('Local only')

    const parsed = parseDocument(JSON.stringify(documentWith()))
    expect(parsed.ok).toBe(true)
    if (!parsed.ok) return

    const preview = previewImport(currentPayload(), parsed.document, 'merge')
    const counts = countChanges(preview.entries)
    expect(counts.add).toBeGreaterThan(0)
    expect(counts.change).toBeGreaterThan(0)
    expect(counts.same).toBeGreaterThan(0)

    // The review is a diff of `next`, so applying it must land on `next`.
    applyImport(preview)
    expect(currentPayload()).toEqual(preview.next)
  })

  it('reports a removal under replace and never under merge', () => {
    resetStores()
    organisation.createProject('Local only')

    const parsed = parseDocument(JSON.stringify(documentWith()))
    expect(parsed.ok).toBe(true)
    if (!parsed.ok) return

    const merged = previewImport(currentPayload(), parsed.document, 'merge')
    expect(countChanges(merged.entries).remove).toBe(0)

    const replaced = previewImport(currentPayload(), parsed.document, 'replace')
    expect(countChanges(replaced.entries).remove).toBeGreaterThan(0)
  })
})

describe('reading a document', () => {
  it('counts unknown fields instead of refusing them', () => {
    const raw = JSON.parse(JSON.stringify(documentWith())) as Record<string, unknown>
    raw.somethingNewerBuildsWrite = { anything: true }
    ;(raw.preferences as Record<string, unknown>).colourBlindPalette = 'deuteranopia'
    ;(raw.organisation as Record<string, unknown>).tags = ['a', 'b']

    const parsed = parseDocument(JSON.stringify(raw))
    expect(parsed.ok).toBe(true)
    if (!parsed.ok) return

    expect(parsed.document.unknownFields).toBe(3)
    expect(parsed.document.invalidFields).toBe(0)
    // What this build DOES understand still arrived.
    expect(parsed.document.payload.preferences.pageSize).toBe(100)
  })

  it('drops a known field whose value it refuses, and says how many', () => {
    const raw = JSON.parse(JSON.stringify(documentWith())) as Record<string, unknown>
    const prefs = raw.preferences as Record<string, unknown>
    prefs.pageSize = 5000
    prefs.themePreference = 'ultraviolet'
    prefs.debugImage = '   '

    const parsed = parseDocument(JSON.stringify(raw))
    expect(parsed.ok).toBe(true)
    if (!parsed.ok) return

    expect(parsed.document.invalidFields).toBe(3)
    expect(parsed.document.payload.preferences.pageSize).toBeUndefined()
    // And the rest of the document is intact.
    expect(parsed.document.payload.preferences.nodeShellNamespace).toBeDefined()
  })

  it('handles a version from the future honestly rather than refusing it', () => {
    const parsed = parseDocument(
      JSON.stringify(documentWith({ version: SETTINGS_FILE_VERSION + 7 })),
    )
    expect(parsed.ok).toBe(true)
    if (!parsed.ok) return

    expect(parsed.document.fromTheFuture).toBe(true)
    expect(parsed.document.version).toBe(SETTINGS_FILE_VERSION + 7)
    // Still imports what this build understands.
    expect(parsed.document.payload.preferences.pageSize).toBe(100)
    expect(previewImport(currentPayload(), parsed.document, 'merge').fromTheFuture).toBe(true)
  })

  it("does not treat this build's own version as a future one", () => {
    const parsed = parseDocument(JSON.stringify(documentWith()))
    expect(parsed.ok).toBe(true)
    if (!parsed.ok) return
    expect(parsed.document.fromTheFuture).toBe(false)
  })
})

describe('a malformed document is refused with the reason', () => {
  const cases: Array<{ name: string; text: string; expect: RegExp }> = [
    { name: 'not JSON at all', text: 'projects:\n  - name: Checkout\n', expect: /not JSON/ },
    { name: 'a JSON array', text: '[1, 2, 3]', expect: /does not hold a settings document/ },
    { name: 'JSON null', text: 'null', expect: /does not hold a settings document/ },
    { name: 'somebody else JSON', text: '{"apiVersion":"v1","kind":"Pod"}', expect: /not a PodSteer settings file/ },
    {
      name: 'no version',
      text: JSON.stringify({ kind: SETTINGS_FILE_KIND, exportedAt: 'now' }),
      expect: /which settings version/,
    },
    {
      name: 'a version that is not a whole number',
      text: JSON.stringify({ kind: SETTINGS_FILE_KIND, version: 1.5, exportedAt: 'now' }),
      expect: /which settings version/,
    },
    {
      name: 'a version before the first one',
      text: JSON.stringify({ kind: SETTINGS_FILE_KIND, version: 0, exportedAt: 'now' }),
      expect: /which settings version/,
    },
    {
      name: 'no export time',
      text: JSON.stringify({ kind: SETTINGS_FILE_KIND, version: 1 }),
      expect: /when it was exported/,
    },
    {
      name: 'a preferences section that is not an object',
      text: JSON.stringify({
        kind: SETTINGS_FILE_KIND,
        version: 1,
        exportedAt: 'now',
        preferences: [],
      }),
      expect: /"preferences" section/,
    },
  ]

  for (const testCase of cases) {
    it(`refuses ${testCase.name}`, () => {
      const parsed = parseDocument(testCase.text)
      expect(parsed.ok).toBe(false)
      if (parsed.ok) return
      expect(parsed.reason).toMatch(testCase.expect)
    })
  }

  it('changes nothing when a document is refused', () => {
    resetStores()
    preferences.setPageSize(10)
    const before = currentPayload()

    expect(parseDocument('not json at all').ok).toBe(false)
    expect(currentPayload()).toEqual(before)
  })

  // A document carrying only one half is legitimate — somebody sharing their
  // project layout and nothing else — so it is not a malformed one.
  it('accepts a document carrying only one half', () => {
    const parsed = parseDocument(
      JSON.stringify({
        kind: SETTINGS_FILE_KIND,
        version: SETTINGS_FILE_VERSION,
        exportedAt: '2026-09-04T12:00:00.000Z',
        organisation: { defaultProjectName: 'Personal' },
      }),
    )
    expect(parsed.ok).toBe(true)
    if (!parsed.ok) return
    expect(parsed.document.payload.preferences).toEqual({})
    expect(parsed.document.payload.organisation.defaultProjectName).toBe('Personal')
  })
})

describe('the document itself', () => {
  it('is pretty-printed JSON with the version and time before the payload', () => {
    const text = serialiseDocument(buildDocument(new Date('2026-09-04T12:00:00.000Z')))

    expect(text.endsWith('\n')).toBe(true)
    expect(text).toContain('\n  "kind": "PodSteerSettings",')
    expect(text).toContain('\n  "version": 1,')
    expect(text).toContain('\n  "exportedAt": "2026-09-04T12:00:00.000Z",')
    expect(text.indexOf('"exportedAt"')).toBeLessThan(text.indexOf('"preferences"'))
  })

  it('is a snapshot, not a live view into the stores', () => {
    resetStores()
    preferences.setPageSize(10)
    const document = buildDocument()

    preferences.setPageSize(100)
    expect(document.preferences.pageSize).toBe(10)
  })
})
