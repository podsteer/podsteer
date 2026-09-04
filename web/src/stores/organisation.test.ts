import { beforeEach, describe, expect, it, vi } from 'vitest'

const STORAGE_KEY = 'podsteer.organisation.v1'

/**
 * A minimal in-memory Storage, stood in for the real localStorage.
 *
 * happy-dom's own `window.localStorage` does not reach the bare global
 * `localStorage` identifier this test environment resolves against — the
 * getter lives on `BrowserWindow.prototype` and vitest's global wiring does
 * not proxy it — so `organisation.svelte.ts`'s unqualified references to
 * `localStorage` would otherwise hit Node's own experimental global, which
 * is unconfigured here and throws. Stubbing it directly is what every other
 * store here gets for free from a real browser, so a test outliving this
 * gap only has to delete this file's top half.
 */
class MemoryStorage implements Storage {
  #store = new Map<string, string>()
  getItem(key: string): string | null {
    return this.#store.has(key) ? this.#store.get(key)! : null
  }
  setItem(key: string, value: string): void {
    this.#store.set(key, value)
  }
  removeItem(key: string): void {
    this.#store.delete(key)
  }
  clear(): void {
    this.#store.clear()
  }
  key(index: number): string | null {
    return [...this.#store.keys()][index] ?? null
  }
  get length(): number {
    return this.#store.size
  }
}

/**
 * `organisation` is a module-level singleton that reads localStorage once, in
 * its constructor, at import time — the same shape every other store in this
 * directory follows. A test that wants a particular starting state therefore
 * has to seed localStorage and THEN import a fresh module instance, which is
 * what `vi.resetModules()` plus a dynamic `import()` buys: the previous
 * import stays cached and unaffected, and this test gets its own.
 */
async function freshOrganisation() {
  vi.resetModules()
  return import('./organisation.svelte')
}

beforeEach(() => {
  vi.stubGlobal('localStorage', new MemoryStorage())
})

describe('group guardrail settings — defaults', () => {
  it('reports the unmarked default for a group nothing has touched', async () => {
    const { organisation, DEFAULT_PROJECT_ID, DEFAULT_GROUP_ID } = await freshOrganisation()

    expect(organisation.settingsFor(DEFAULT_PROJECT_ID, DEFAULT_GROUP_ID)).toEqual({
      environment: '',
      colour: '',
      readOnly: false,
    })
  })

  it('reports the unmarked default for a group id that does not exist', async () => {
    // Mirrors placementOf's own repair rule: a stale caller — a session for a
    // group deleted a moment ago — is an ordinary race, not a reason to throw.
    const { organisation, DEFAULT_PROJECT_ID } = await freshOrganisation()

    expect(organisation.settingsFor(DEFAULT_PROJECT_ID, 'group-does-not-exist')).toEqual({
      environment: '',
      colour: '',
      readOnly: false,
    })
  })
})

describe('group guardrail settings — a custom group', () => {
  it('stores and reads back a patch, leaving the rest alone', async () => {
    const { organisation, DEFAULT_PROJECT_ID } = await freshOrganisation()

    organisation.createGroup('Production', DEFAULT_PROJECT_ID)
    const groupId = organisation.groupsIn(DEFAULT_PROJECT_ID).find((g) => g.name === 'Production')!.id

    organisation.setGroupSettings(DEFAULT_PROJECT_ID, groupId, {
      environment: 'production',
      colour: 'red',
    })

    // The patch applied...
    expect(organisation.settingsFor(DEFAULT_PROJECT_ID, groupId)).toEqual({
      environment: 'production',
      colour: 'red',
      readOnly: false,
    })

    // ...and a second, narrower patch leaves what the first one set alone.
    organisation.setGroupSettings(DEFAULT_PROJECT_ID, groupId, { readOnly: true })
    expect(organisation.settingsFor(DEFAULT_PROJECT_ID, groupId)).toEqual({
      environment: 'production',
      colour: 'red',
      readOnly: true,
    })
  })

  it('survives a project the group belongs to, since the fields live on the group itself', async () => {
    const { organisation, DEFAULT_PROJECT_ID } = await freshOrganisation()

    organisation.createGroup('Staging', DEFAULT_PROJECT_ID)
    const groupId = organisation.groupsIn(DEFAULT_PROJECT_ID).find((g) => g.name === 'Staging')!.id
    organisation.setGroupSettings(DEFAULT_PROJECT_ID, groupId, { environment: 'staging' })

    organisation.createProject('Data platform')
    const target = organisation.allProjects().find((p) => p.name === 'Data platform')!.id
    organisation.moveGroupToProject(groupId, target)

    // Moving a group carries its settings with it, the same way it already
    // carries its name — both live on the one Group record.
    expect(organisation.settingsFor(target, groupId)).toEqual({
      environment: 'staging',
      colour: '',
      readOnly: false,
    })
  })
})

describe('group guardrail settings — a project Default group', () => {
  it('keys by project, not by the shared DEFAULT_GROUP_ID', async () => {
    // The exact trap defaultGroupNames already has to avoid: DEFAULT_GROUP_ID
    // is the same string in every project, so setting one project's Default
    // to read-only must not mark every other project's Default too.
    const { organisation, DEFAULT_PROJECT_ID, DEFAULT_GROUP_ID } = await freshOrganisation()

    organisation.createProject('Checkout')
    const checkout = organisation.allProjects().find((p) => p.name === 'Checkout')!.id

    organisation.setGroupSettings(DEFAULT_PROJECT_ID, DEFAULT_GROUP_ID, { readOnly: true })

    expect(organisation.settingsFor(DEFAULT_PROJECT_ID, DEFAULT_GROUP_ID).readOnly).toBe(true)
    expect(organisation.settingsFor(checkout, DEFAULT_GROUP_ID).readOnly).toBe(false)
  })

  it('deleting a project forgets its Default group settings too', async () => {
    const { organisation, DEFAULT_GROUP_ID } = await freshOrganisation()

    organisation.createProject('Temporary')
    const temp = organisation.allProjects().find((p) => p.name === 'Temporary')!.id
    organisation.setGroupSettings(temp, DEFAULT_GROUP_ID, { environment: 'other', readOnly: true })

    organisation.removeProject(temp)

    // The project no longer exists, so its id means nothing — asking about it
    // must not throw, and must report the unmarked default rather than
    // something leaked from before the deletion.
    expect(organisation.settingsFor(temp, DEFAULT_GROUP_ID)).toEqual({
      environment: '',
      colour: '',
      readOnly: false,
    })
  })
})

describe('group guardrail settings — persistence and migration', () => {
  it('round-trips through localStorage across a simulated restart', async () => {
    const first = await freshOrganisation()
    first.organisation.createGroup('Production', first.DEFAULT_PROJECT_ID)
    const groupId = first.organisation
      .groupsIn(first.DEFAULT_PROJECT_ID)
      .find((g) => g.name === 'Production')!.id
    first.organisation.setGroupSettings(first.DEFAULT_PROJECT_ID, groupId, {
      environment: 'production',
      colour: 'red',
      readOnly: true,
    })
    first.organisation.setGroupSettings(first.DEFAULT_PROJECT_ID, first.DEFAULT_GROUP_ID, {
      environment: 'development',
    })

    // A fresh module instance reading the SAME localStorage is the closest
    // thing to relaunching the application without actually doing it.
    const second = await freshOrganisation()

    expect(second.organisation.settingsFor(second.DEFAULT_PROJECT_ID, groupId)).toEqual({
      environment: 'production',
      colour: 'red',
      readOnly: true,
    })
    expect(
      second.organisation.settingsFor(second.DEFAULT_PROJECT_ID, second.DEFAULT_GROUP_ID),
    ).toEqual({ environment: 'development', colour: '', readOnly: false })
  })

  it('defaults a group persisted before guardrail settings existed, rather than dropping it', async () => {
    // The exact shape a pre-feature install has on disk: a group record with
    // only id/name/projectId, no environment/colour/readOnly at all.
    localStorage.setItem(
      STORAGE_KEY,
      JSON.stringify({
        projects: [],
        groups: [{ id: 'group-legacy', name: 'Legacy', projectId: 'default-project' }],
        assignments: {},
        collapsed: [],
      }),
    )

    const { organisation, DEFAULT_PROJECT_ID } = await freshOrganisation()

    const group = organisation.groupsIn(DEFAULT_PROJECT_ID).find((g) => g.id === 'group-legacy')
    expect(group).toBeDefined()
    expect(group!.name).toBe('Legacy')
    expect(group!.settings).toEqual({ environment: '', colour: '', readOnly: false })
  })

  it('discards a malformed environment or colour rather than trusting hand-edited storage', async () => {
    localStorage.setItem(
      STORAGE_KEY,
      JSON.stringify({
        projects: [],
        groups: [
          {
            id: 'group-bad',
            name: 'Bad',
            projectId: 'default-project',
            environment: 'production; DROP TABLE clusters',
            colour: 'magenta',
            readOnly: 'yes',
          },
        ],
        assignments: {},
        collapsed: [],
      }),
    )

    const { organisation, DEFAULT_PROJECT_ID } = await freshOrganisation()

    expect(organisation.settingsFor(DEFAULT_PROJECT_ID, 'group-bad')).toEqual({
      environment: '',
      colour: '',
      readOnly: false,
    })
  })

  it('drops a defaultGroupSettings entry for a project that no longer exists', async () => {
    localStorage.setItem(
      STORAGE_KEY,
      JSON.stringify({
        projects: [],
        groups: [],
        defaultGroupSettings: { 'project-gone': { readOnly: true } },
        assignments: {},
        collapsed: [],
      }),
    )

    const { organisation } = await freshOrganisation()

    // Reading it directly, rather than through settingsFor (which would
    // report the same unmarked default whether the entry was pruned or never
    // existed) — this asserts the storage was actually cleaned, the way
    // defaultGroupNames already prunes an entry for a deleted project.
    expect(organisation.defaultGroupSettings).toEqual({})
  })
})
