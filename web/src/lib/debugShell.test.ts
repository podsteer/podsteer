import { describe, expect, it } from 'vitest'

import { debugRequest, nodeShellRequest } from './debugShell'
import {
  DEFAULT_DEBUG_IMAGE,
  DEFAULT_NODE_SHELL_IMAGE,
  DEFAULT_NODE_SHELL_NAMESPACE,
} from '$stores/preferences.svelte'

describe('debugRequest', () => {
  it('keeps an explicit image and splits the command on whitespace', () => {
    expect(debugRequest('busybox:1.37', 'sh -c id')).toEqual({
      image: 'busybox:1.37',
      command: ['sh', '-c', 'id'],
    })
  })

  it('falls back to the default image when blank, never an empty string', () => {
    expect(debugRequest('   ', 'sh').image).toBe(DEFAULT_DEBUG_IMAGE)
  })

  it('reads a blank command as "use the default", not an empty argument', () => {
    // The backend substitutes `sh` for an empty command; a `['']` here would
    // ask it to run a program named "".
    expect(debugRequest('busybox:1.37', '   ').command).toEqual([])
  })
})

describe('nodeShellRequest', () => {
  it('keeps explicit values', () => {
    expect(nodeShellRequest('docker.io/library/ubuntu:24.04', 'ops')).toEqual({
      image: 'docker.io/library/ubuntu:24.04',
      namespace: 'ops',
    })
  })

  it('falls back to the defaults when either is blank', () => {
    expect(nodeShellRequest('', '')).toEqual({
      image: DEFAULT_NODE_SHELL_IMAGE,
      namespace: DEFAULT_NODE_SHELL_NAMESPACE,
    })
  })
})
