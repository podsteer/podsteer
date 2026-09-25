import { afterEach, describe, expect, it } from 'vitest'
import { sessionLauncher } from './sessionLauncher.svelte'
import type { CodingAgent } from '$lib/localShell'

const CLAUDE: CodingAgent = { id: 'claude', label: 'Claude Code', path: '/opt/bin/claude' }
const GEMINI: CodingAgent = { id: 'gemini', label: 'Gemini CLI', path: '/usr/local/bin/gemini' }

const SUBJECT = { kind: 'Deployment', namespace: 'shop', name: 'web' }

afterEach(() => {
  sessionLauncher.cancel()
  sessionLauncher.close()
})

describe('the local terminal launcher', () => {
  it('opens the dialog for the tab in front', () => {
    sessionLauncher.requestLocal({ clusterId: 'prod-eu', agents: [CLAUDE], subject: SUBJECT })

    expect(sessionLauncher.pending).toEqual({
      kind: 'local',
      clusterId: 'prod-eu',
      agents: [CLAUDE],
      subject: SUBJECT,
    })
    expect(sessionLauncher.running).toBeNull()
  })

  it('confirming a plain shell closes the dialog and opens the terminal', () => {
    sessionLauncher.requestLocal({ clusterId: 'prod-eu', agents: [CLAUDE], subject: SUBJECT })

    sessionLauncher.startLocal('', true)

    expect(sessionLauncher.pending).toBeNull()
    expect(sessionLauncher.running).toEqual({
      kind: 'local',
      clusterId: 'prod-eu',
      agent: null,
      // Normalised off: the marker means somebody asked an AGENT to stay
      // read-only, and a shell was asked nothing.
      readOnly: false,
      subject: SUBJECT,
      title: 'Local shell',
    })
  })

  it('confirming an agent carries the read-only default and titles the pane', () => {
    sessionLauncher.requestLocal({
      clusterId: 'prod-eu',
      agents: [CLAUDE, GEMINI],
      subject: SUBJECT,
    })

    sessionLauncher.startLocal('gemini', true)

    expect(sessionLauncher.running).toMatchObject({
      kind: 'local',
      agent: 'gemini',
      readOnly: true,
      subject: SUBJECT,
      title: 'Gemini CLI',
    })
  })

  it('lets the operator turn the read-only default off', () => {
    sessionLauncher.requestLocal({ clusterId: 'prod-eu', agents: [CLAUDE], subject: SUBJECT })

    sessionLauncher.startLocal('claude', false)

    expect(sessionLauncher.running).toMatchObject({ agent: 'claude', readOnly: false })
  })

  it('falls back to a plain shell for an agent that has gone away', () => {
    // Detection ran when the dialog opened. An id that is no longer in the
    // list means the binary was removed since, and opening the shell they
    // would otherwise have had beats an error about a tool they did not know
    // had gone.
    sessionLauncher.requestLocal({ clusterId: 'prod-eu', agents: [CLAUDE], subject: SUBJECT })

    sessionLauncher.startLocal('codex', true)

    expect(sessionLauncher.running).toMatchObject({ agent: null, readOnly: false })
  })

  it('ignores a local confirmation while a different dialog is pending', () => {
    // The two phases are one at a time; confirming the wrong one would open a
    // terminal nobody asked for.
    sessionLauncher.requestNodeShell({
      clusterId: 'prod-eu',
      node: 'node-1',
      readOnly: false,
      productionGroup: null,
    })

    sessionLauncher.startLocal('claude', true)

    expect(sessionLauncher.running).toBeNull()
    expect(sessionLauncher.pending).toMatchObject({ kind: 'nodeshell' })
  })

  it('cancelling opens nothing', () => {
    sessionLauncher.requestLocal({ clusterId: 'prod-eu', agents: [CLAUDE], subject: SUBJECT })

    sessionLauncher.cancel()

    expect(sessionLauncher.pending).toBeNull()
    expect(sessionLauncher.running).toBeNull()
  })

  it('closing the terminal clears it', () => {
    // The Terminal component's own teardown stops the session, which for a
    // local shell ends the process on this machine.
    sessionLauncher.requestLocal({ clusterId: 'prod-eu', agents: [CLAUDE], subject: SUBJECT })
    sessionLauncher.startLocal('claude', true)

    sessionLauncher.close()

    expect(sessionLauncher.running).toBeNull()
  })
})

describe('the in-cluster shell launcher', () => {
  it("opens the dialog on the tab's namespace", () => {
    sessionLauncher.requestClusterShell({ clusterId: 'prod-eu', namespace: 'shop' })

    expect(sessionLauncher.pending).toEqual({
      kind: 'clustershell',
      clusterId: 'prod-eu',
      namespace: 'shop',
    })
    expect(sessionLauncher.running).toBeNull()
  })

  it('opens it with NO namespace when the tab is on every one, so the dialog asks', () => {
    // "All namespaces" reaches the launcher as '' rather than as a system
    // namespace — see clusterShellNamespaceFor, which is where that decision
    // is made and argued.
    sessionLauncher.requestClusterShell({ clusterId: 'prod-eu', namespace: '' })

    expect(sessionLauncher.pending).toEqual({
      kind: 'clustershell',
      clusterId: 'prod-eu',
      namespace: '',
    })
  })

  it('confirming CREATES a pod: the image travels and the pod name is empty', () => {
    sessionLauncher.requestClusterShell({ clusterId: 'prod-eu', namespace: '' })

    // The namespace comes from the DIALOG, not from the pending request: the
    // tab was on every namespace and the operator typed one.
    sessionLauncher.startClusterShell('registry.internal/shell:1.0.0', 'shop')

    expect(sessionLauncher.pending).toBeNull()
    expect(sessionLauncher.running).toEqual({
      kind: 'clustershell',
      clusterId: 'prod-eu',
      namespace: 'shop',
      image: 'registry.internal/shell:1.0.0',
      pod: '',
    })
  })

  it('attaching carries the pod and NO image, because that pod already has one', () => {
    // Sending the dialog's image on the reuse path would claim something about
    // a pod this pane did not create.
    sessionLauncher.requestClusterShell({ clusterId: 'prod-eu', namespace: 'shop' })

    sessionLauncher.attachClusterShell('shop', 'podsteer-shell-aaaaa')

    expect(sessionLauncher.running).toEqual({
      kind: 'clustershell',
      clusterId: 'prod-eu',
      namespace: 'shop',
      image: '',
      pod: 'podsteer-shell-aaaaa',
    })
  })

  it('leaving the workspace drops both phases', () => {
    // The launcher is a singleton and the overlay that renders it is not: it
    // is rebuilt on every tab switch. Anything still held here would be shown
    // under — and confirmed against — the NEXT cluster's tab.
    sessionLauncher.requestClusterShell({ clusterId: 'prod-eu', namespace: 'shop' })
    sessionLauncher.startClusterShell('registry.internal/shell:1.0.0', 'shop')
    sessionLauncher.requestLocal({ clusterId: 'prod-eu', agents: [CLAUDE], subject: SUBJECT })

    sessionLauncher.leave()

    expect(sessionLauncher.pending).toBeNull()
    expect(sessionLauncher.running).toBeNull()
  })

  it('neither confirmation fires without its own dialog pending', () => {
    sessionLauncher.requestLocal({ clusterId: 'prod-eu', agents: [CLAUDE], subject: SUBJECT })

    sessionLauncher.startClusterShell('img:1', 'shop')
    sessionLauncher.attachClusterShell('shop', 'podsteer-shell-aaaaa')

    expect(sessionLauncher.running).toBeNull()
    expect(sessionLauncher.pending?.kind).toBe('local')
  })
})
