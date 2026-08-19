/**
 * Facts about the running application.
 *
 * Loaded once at startup and then read as a plain value: the version and
 * platform cannot change while the process is alive, so making them reactive
 * state would suggest an update that never comes.
 */

import { appInfo as fetchAppInfo, openURL, type AppInfo } from '$lib/api/client'

/** Shown until the backend answers, and if it never does. */
const UNKNOWN: AppInfo = {
  name: 'K8Sense',
  version: '—',
  platform: '',
  website: 'https://k8sense.com',
}

/**
 * The running application's identity.
 *
 * A mutable module binding rather than a store: it is written exactly once,
 * before the first render that reads it, and never again.
 */
export let appInfo = $state<AppInfo>({ ...UNKNOWN })

/** Loads the application info. Safe to call more than once. */
export async function loadAppInfo(): Promise<void> {
  try {
    const info = await fetchAppInfo()
    Object.assign(appInfo, info)
  } catch {
    // The status bar showing a dash for the version is a far better outcome
    // than the application failing to start over a cosmetic lookup.
  }
}

/** Opens the project website in the operator's browser. */
export async function openWebsite(): Promise<void> {
  try {
    await openURL(appInfo.website || UNKNOWN.website)
  } catch {
    // Nothing useful to do if the shell refuses; the link is not load-bearing.
  }
}
