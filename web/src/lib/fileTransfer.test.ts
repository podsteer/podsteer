import { describe, expect, it } from 'vitest'
import type { FileCopyDoneEvent } from './api/client'
import {
  IDLE,
  baseName,
  canStart,
  describeResult,
  downloadDestination,
  finished,
  formatBytes,
  formatDuration,
  isBusy,
  progressed,
  resolveRemotePath,
  started,
  starting,
  uploadDestination,
} from './fileTransfer'

function doneEvent(overrides: Partial<FileCopyDoneEvent> = {}): FileCopyDoneEvent {
  return {
    transferId: 'copy_1',
    direction: 'download',
    files: 3,
    entries: 4,
    bytes: 12_900_000,
    durationMs: 2100,
    notes: [],
    localPath: '/Users/me/Downloads/nginx',
    cancelled: false,
    error: '',
    ...overrides,
  }
}

describe('the transfer state machine', () => {
  it('runs from starting through progress to done', () => {
    let state = starting()
    expect(isBusy(state)).toBe(true)

    state = started(state, 'copy_1')
    expect(state.phase).toBe('running')

    state = progressed(state, { transferId: 'copy_1', bytes: 500 })
    expect(state.bytes).toBe(500)

    state = finished(state, doneEvent())
    expect(state.phase).toBe('done')
    expect(state.result?.files).toBe(3)
    expect(state.bytes).toBe(12_900_000)
    expect(isBusy(state)).toBe(false)
  })

  it('ignores progress for another transfer', () => {
    const state = started(starting(), 'copy_1')
    expect(progressed(state, { transferId: 'copy_2', bytes: 999 }).bytes).toBe(0)
  })

  it('holds a done event that beats the id, and applies it once the id lands', () => {
    // A one-byte download finishes before the promise naming it resolves;
    // dropping that event would leave the bar spinning forever.
    let state = starting()
    state = finished(state, doneEvent({ transferId: 'copy_1' }))
    expect(state.phase).toBe('starting')
    expect(state.pending).toHaveLength(1)

    state = started(state, 'copy_1')
    expect(state.phase).toBe('done')
    expect(state.pending).toHaveLength(0)
  })

  it('does not apply an early done event for a different transfer', () => {
    let state = starting()
    state = finished(state, doneEvent({ transferId: 'copy_other' }))
    state = started(state, 'copy_1')
    expect(state.phase).toBe('running')
  })

  it('reports a cancellation as its own phase, with no error', () => {
    const state = finished(started(starting(), 'copy_1'), doneEvent({ cancelled: true, bytes: 100 }))
    expect(state.phase).toBe('cancelled')
    expect(state.error).toBeNull()
    expect(state.bytes).toBe(100)
  })

  it('keeps a failure in its envelope for toApiError', () => {
    const state = finished(
      started(starting(), 'copy_1'),
      doneEvent({ error: '[tar_missing] The container has no tar binary' }),
    )
    expect(state.phase).toBe('failed')
    expect(state.error).toBe('[tar_missing] The container has no tar binary')
  })

  it('ignores a done event once already finished', () => {
    const done = finished(started(starting(), 'copy_1'), doneEvent())
    expect(finished(done, doneEvent({ cancelled: true }))).toBe(done)
  })
})

describe('canStart', () => {
  const idle = IDLE

  it('needs both paths', () => {
    expect(canStart({ direction: 'download', remotePath: '', localPath: '/tmp', state: idle, readOnly: false })).toBe(false)
    expect(canStart({ direction: 'download', remotePath: '/etc/hosts', localPath: '', state: idle, readOnly: false })).toBe(false)
    expect(canStart({ direction: 'download', remotePath: '/etc/hosts', localPath: '/tmp', state: idle, readOnly: false })).toBe(true)
  })

  it('refuses an upload on a read-only cluster and allows a download', () => {
    expect(canStart({ direction: 'upload', remotePath: '/app', localPath: '/tmp/x', state: idle, readOnly: true })).toBe(false)
    expect(canStart({ direction: 'download', remotePath: '/app', localPath: '/tmp', state: idle, readOnly: true })).toBe(true)
  })

  it('refuses while a transfer is in flight', () => {
    expect(canStart({ direction: 'download', remotePath: '/a', localPath: '/b', state: starting(), readOnly: false })).toBe(false)
  })
})

describe('path arithmetic', () => {
  it('takes the last segment whichever separator, ignoring a trailing one', () => {
    expect(baseName('/etc/nginx/nginx.conf')).toBe('nginx.conf')
    expect(baseName('/etc/nginx/')).toBe('nginx')
    expect(baseName('C:\\Users\\me\\config.yaml')).toBe('config.yaml')
    expect(baseName('hosts')).toBe('hosts')
  })

  it('resolves a relative container path against the working directory', () => {
    expect(resolveRemotePath('config.yaml', '/app')).toBe('/app/config.yaml')
    expect(resolveRemotePath('config.yaml', '/app/')).toBe('/app/config.yaml')
    expect(resolveRemotePath('/etc/hosts', '/app')).toBe('/etc/hosts')
    // No working directory known: a fresh exec starts at the root.
    expect(resolveRemotePath('config.yaml')).toBe('/config.yaml')
    expect(resolveRemotePath('   ')).toBe('')
  })

  it('names where a download lands, in the local separator', () => {
    expect(downloadDestination('/Users/me/Downloads', '/etc/nginx')).toBe('/Users/me/Downloads/nginx')
    expect(downloadDestination('/Users/me/Downloads/', '/etc/nginx/')).toBe('/Users/me/Downloads/nginx')
    expect(downloadDestination('C:\\Users\\me\\Downloads', '/etc/hosts')).toBe('C:\\Users\\me\\Downloads\\hosts')
  })

  it('names where an upload lands, root included', () => {
    expect(uploadDestination('/app', '/Users/me/config.yaml')).toBe('/app/config.yaml')
    expect(uploadDestination('/app/', 'C:\\Users\\me\\dir')).toBe('/app/dir')
    expect(uploadDestination('/', '/tmp/x')).toBe('/x')
  })
})

describe('formatting', () => {
  it('formats bytes with one decimal only below ten', () => {
    expect(formatBytes(0)).toBe('0 B')
    expect(formatBytes(840)).toBe('840 B')
    expect(formatBytes(1536)).toBe('1.5 KB')
    expect(formatBytes(12_900_000)).toBe('12 MB')
    expect(formatBytes(1024 ** 3 * 1.25)).toBe('1.3 GB')
    expect(formatBytes(-1)).toBe('0 B')
  })

  it('formats durations at the scale a person reads', () => {
    expect(formatDuration(340)).toBe('340 ms')
    expect(formatDuration(2100)).toBe('2.1 s')
    expect(formatDuration(65_000)).toBe('1 min 5 s')
    expect(formatDuration(120_000)).toBe('2 min')
  })

  it('describes a result in one line', () => {
    expect(
      describeResult({ files: 3, entries: 4, bytes: 12_900_000, durationMs: 2100, notes: [], localPath: '' }),
    ).toBe('3 files, 12 MB in 2.1 s')
    expect(
      describeResult({ files: 1, entries: 1, bytes: 840, durationMs: 90, notes: [], localPath: '' }),
    ).toBe('1 file, 840 B in 90 ms')
  })
})
