/**
 * Pure helpers for the debug-container and node-shell dialogs.
 *
 * Kept out of the components so the normalisation — trim, fall back to the
 * default rather than send the backend a blank it will reject — can be argued
 * with in a test rather than observed in the UI. The dialogs render; these
 * decide what a confirmed dialog actually asks for.
 */

import {
  DEFAULT_DEBUG_IMAGE,
  DEFAULT_NODE_SHELL_IMAGE,
  DEFAULT_NODE_SHELL_NAMESPACE,
} from '$stores/preferences.svelte'

/** What a confirmed debug dialog asks for. */
export interface DebugRequest {
  image: string
  /** The command, split on whitespace; empty means the image's own default
   * (the backend substitutes `sh`). */
  command: string[]
}

/**
 * Normalises a debug dialog's inputs.
 *
 * A blank image falls back to the default rather than being sent as an empty
 * string the backend would reject. The command is split on whitespace so
 * "sh -c 'id'" is not passed as one argument — a blank command is an empty
 * array, which the backend reads as "use the default".
 */
export function debugRequest(image: string, command: string): DebugRequest {
  return {
    image: image.trim() || DEFAULT_DEBUG_IMAGE,
    command: command.trim() === '' ? [] : command.trim().split(/\s+/),
  }
}

/** What a confirmed node-shell dialog asks for. */
export interface NodeShellRequest {
  image: string
  namespace: string
}

/**
 * Normalises a node-shell dialog's inputs, each falling back to its default
 * when left blank.
 */
export function nodeShellRequest(image: string, namespace: string): NodeShellRequest {
  return {
    image: image.trim() || DEFAULT_NODE_SHELL_IMAGE,
    namespace: namespace.trim() || DEFAULT_NODE_SHELL_NAMESPACE,
  }
}
