/**
 * The state of one file copy, and the path arithmetic around it.
 *
 * Pure functions, so FileTransfer.svelte is nothing but wiring: what a
 * progress event does to the state, which of two paths the kubectl hint
 * shows, whether Start may be pressed — each is a rule here with a test,
 * rather than a branch in a template nothing exercises.
 *
 * NOTHING HERE TOUCHES A FILE. Paths are strings the operator typed or a
 * native dialog returned; joining them is display arithmetic for the hint
 * and the result line. The Go side checks every one of them again before
 * reading or writing anything — see app/adapters/archive.
 */

import type { FileCopyDoneEvent, FileCopyProgressEvent } from './api/client'

/** Which way the bytes go. */
export type Direction = 'download' | 'upload'

/**
 * Where a transfer is.
 *
 * `starting` is the gap between pressing Start and the backend handing back
 * an id — real, because the transfer runs in its own goroutine and a small
 * file can FINISH before the promise that names it resolves. Events that
 * arrive in that gap are held (see `finished`) rather than dropped, or a
 * one-byte download would spin forever.
 */
export type Phase = 'idle' | 'starting' | 'running' | 'done' | 'failed' | 'cancelled'

/** What a finished transfer moved, as the backend reported it. */
export interface TransferResult {
  files: number
  entries: number
  bytes: number
  durationMs: number
  notes: string[]
  localPath: string
}

export interface TransferState {
  phase: Phase
  /** The backend's id once known. */
  transferId: string | null
  /** Bytes moved so far, from the last progress event. */
  bytes: number
  result: TransferResult | null
  /** The failure, still in its `[code] message` envelope for toApiError. */
  error: string | null
  /** Done events that arrived before the id did. */
  pending: FileCopyDoneEvent[]
}

export const IDLE: TransferState = {
  phase: 'idle',
  transferId: null,
  bytes: 0,
  result: null,
  error: null,
  pending: [],
}

/** Start was pressed; the backend has not yet answered with an id. */
export function starting(): TransferState {
  return { ...IDLE, phase: 'starting' }
}

/**
 * The backend named the transfer. If its done event already arrived while
 * the id was in flight, it is applied now.
 */
export function started(state: TransferState, transferId: string): TransferState {
  const running: TransferState = { ...state, phase: 'running', transferId, pending: [] }
  const early = state.pending.find((event) => event.transferId === transferId)
  return early ? finished(running, early) : running
}

/** A progress event. Ignored unless it names this transfer. */
export function progressed(state: TransferState, event: FileCopyProgressEvent): TransferState {
  if (state.phase !== 'running' || event.transferId !== state.transferId) return state
  return { ...state, bytes: event.bytes }
}

/** A done event. Held while starting, applied when running, ignored otherwise. */
export function finished(state: TransferState, event: FileCopyDoneEvent): TransferState {
  if (state.phase === 'starting') {
    return { ...state, pending: [...state.pending, event] }
  }
  if (state.phase !== 'running' || event.transferId !== state.transferId) return state

  const result: TransferResult = {
    files: event.files,
    entries: event.entries,
    bytes: event.bytes,
    durationMs: event.durationMs,
    notes: event.notes ?? [],
    localPath: event.localPath,
  }

  if (event.cancelled) {
    return { ...state, phase: 'cancelled', bytes: event.bytes, result, error: null }
  }
  if (event.error) {
    return { ...state, phase: 'failed', bytes: event.bytes, result, error: event.error }
  }
  return { ...state, phase: 'done', bytes: event.bytes, result, error: null }
}

/** Whether a transfer is in flight, in either of its two in-flight phases. */
export function isBusy(state: TransferState): boolean {
  return state.phase === 'starting' || state.phase === 'running'
}

/**
 * The last segment of a path, whichever separator it uses.
 *
 * Local paths on Windows carry backslashes and container paths never do,
 * so both are separators here; a trailing one — a folder picked as
 * `/Users/me/Downloads/` — is ignored rather than yielding an empty name.
 */
export function baseName(path: string): string {
  const trimmed = path.replace(/[\\/]+$/, '')
  const segments = trimmed.split(/[\\/]/)
  return segments[segments.length - 1] ?? ''
}

/**
 * The container path a typed value means.
 *
 * Absolute stays as typed. Relative is resolved against the container's
 * working directory — the same thing `tar -C .` would do inside the exec,
 * and shown here so the hint and the result name the real path. With no
 * working directory known, the root is what a fresh exec starts in.
 */
export function resolveRemotePath(typed: string, workingDir?: string): string {
  const value = typed.trim()
  if (value === '') return ''
  if (value.startsWith('/')) return value
  const dir = (workingDir?.trim() || '/').replace(/\/+$/, '')
  return `${dir}/${value}`
}

/** Where a download lands: inside the chosen folder, under the remote name. */
export function downloadDestination(localDir: string, remotePath: string): string {
  const separator = localDir.includes('\\') ? '\\' : '/'
  const dir = localDir.replace(/[\\/]+$/, '')
  return `${dir}${separator}${baseName(remotePath)}`
}

/** Where an upload lands: inside the remote directory, under the local name. */
export function uploadDestination(remoteDir: string, localPath: string): string {
  const dir = remoteDir === '/' ? '' : remoteDir.replace(/\/+$/, '')
  return `${dir}/${baseName(localPath)}`
}

/** Whether Start may be pressed. */
export function canStart(input: {
  direction: Direction
  remotePath: string
  localPath: string
  state: TransferState
  readOnly: boolean
}): boolean {
  if (isBusy(input.state)) return false
  if (input.direction === 'upload' && input.readOnly) return false
  return input.remotePath.trim() !== '' && input.localPath.trim() !== ''
}

/** Bytes as a person reads them: `12.3 MB`, `840 B`. */
export function formatBytes(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes < 0) return '0 B'
  if (bytes < 1024) return `${Math.round(bytes)} B`
  const units = ['KB', 'MB', 'GB', 'TB']
  let value = bytes / 1024
  let unit = 0
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024
    unit += 1
  }
  // One decimal below 10, none above: "1.5 MB" reads, "153.7 MB" does not.
  const digits = value < 10 ? 1 : 0
  return `${value.toFixed(digits)} ${units[unit]}`
}

/** A duration as a person reads it: `2.1 s`, `340 ms`, `1 min 5 s`. */
export function formatDuration(ms: number): string {
  if (!Number.isFinite(ms) || ms < 0) return '0 ms'
  if (ms < 1000) return `${Math.round(ms)} ms`
  const seconds = ms / 1000
  if (seconds < 60) return `${seconds.toFixed(1)} s`
  const minutes = Math.floor(seconds / 60)
  const rest = Math.round(seconds - minutes * 60)
  return rest === 0 ? `${minutes} min` : `${minutes} min ${rest} s`
}

/** One line for a finished transfer: `3 files, 12.3 MB in 2.1 s`. */
export function describeResult(result: TransferResult): string {
  const files = result.files === 1 ? '1 file' : `${result.files} files`
  return `${files}, ${formatBytes(result.bytes)} in ${formatDuration(result.durationMs)}`
}
