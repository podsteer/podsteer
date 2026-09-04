/**
 * The settings file: everything an operator has arranged locally, as one
 * document they can keep in git and hand to a colleague.
 *
 * WHAT IT MUST NEVER CARRY IS THE WHOLE POINT OF THIS FILE. A settings export
 * is precisely where object names would leak — they are in the two stores it
 * draws from, sitting beside the things it does carry — and SECURITY.md's
 * standing commitment is that PodSteer writes no object name to disk. So the
 * export is an ALLOWLIST built field by field in each store's `exportable()`,
 * never a spread of the persisted shape, and `settingsFile.test.ts` asserts
 * both that the forbidden categories are absent from the text and that the
 * exported key set is exactly the agreed one. A new field joins the export by
 * editing that test, which is the argument this design wants somebody to have
 * to make.
 *
 * Never carried, and each is in one of those stores:
 *
 * - Credentials, kubeconfig contents, cluster server URLs, bearer tokens.
 *   PodSteer never holds any of these outside `adapters/k8s`, and nothing
 *   here reads them.
 * - Object names of any kind: snoozed findings (whose keys are a finding id,
 *   a namespace and an object name), the per-cluster namespace filter, and
 *   the navigator's Recent list, which is deliberately in memory only.
 * - Machine state that is not a setting: when the update check last ran, and
 *   which release the badge was dismissed for. Neither is an arrangement
 *   anybody made, and both are wrong on the machine that receives them.
 *
 * ONE THING ABOUT A CLUSTER DOES TRAVEL: the kubeconfig CONTEXT NAME, as the
 * key of the pinned-kind map and of the organisation's placements. Without it
 * "staging is read-only" cannot be said at all. It is a name the recipient's
 * own kubeconfig already gives them and it identifies no object inside any
 * cluster, but a colleague reading the file will see which contexts a
 * teammate has — so the document says so, in its own header, rather than
 * leaving them to notice.
 *
 * JSON, PRETTY-PRINTED, and not YAML. Three reasons, in order:
 *
 * - The two stores are already JSON in the webview's storage, so the document
 *   is a projection of what they hold rather than a translation, and no value
 *   can change meaning on the way through. YAML's implicit typing would: a
 *   group called `no`, a version-like `1.10`, a colour token quoted in one
 *   writer and bare in another.
 * - `JSON.parse` is the platform's, so nothing stands between an operator's
 *   file and their settings but code in this repository. A YAML parser is a
 *   dependency whose version decides what a document means.
 * - The file exists to live in git, and two-space JSON diffs one setting to a
 *   line, which is what a review of somebody else's change wants.
 *
 * The cost is that JSON has no comments, so the header is a `_readme` array
 * of strings at the top of the document — read by nothing, present for the
 * person who opens the file.
 */

import {
  describePreferenceChanges,
  mergeExportedPreferences,
  preferences,
  readExportedPreferences,
  type ExportedPreferences,
} from '$stores/preferences.svelte'
import {
  describeOrganisationChanges,
  mergeExportedOrganisation,
  organisation,
  readExportedOrganisation,
  type ExportedOrganisation,
} from '$stores/organisation.svelte'
import type { ImportEntry, ImportMode, ImportOutcome } from './settingsDiff'

export type { ImportEntry, ImportMode, ImportOutcome } from './settingsDiff'

/** Marks the document as PodSteer's, so an unrelated JSON file is refused. */
export const SETTINGS_FILE_KIND = 'PodSteerSettings'

/**
 * The document version this build writes and fully understands.
 *
 * Bumped only when the SHAPE changes incompatibly. Adding a field does not
 * need it: an older build ignores what it does not know and says how much it
 * ignored, which is the property the version exists to make safe.
 */
export const SETTINGS_FILE_VERSION = 1

/** The two halves of what the document carries. */
export interface SettingsPayload {
  preferences: ExportedPreferences
  organisation: ExportedOrganisation
}

/** The whole document, as written. */
export interface SettingsDocument extends SettingsPayload {
  _readme: string[]
  kind: typeof SETTINGS_FILE_KIND
  version: number
  exportedAt: string
}

/** A document that parsed, with what this build could not read. */
export interface ParsedDocument {
  version: number
  exportedAt: string
  payload: {
    preferences: Partial<ExportedPreferences>
    organisation: Partial<ExportedOrganisation>
  }
  /** Fields a newer build wrote that this one does not know. */
  unknownFields: number
  /** Fields this build knows and whose value it refused. */
  invalidFields: number
  /** Written by a build whose document version is higher than ours. */
  fromTheFuture: boolean
}

/** Parsing either produced a document or a reason it could not. */
export type ParseResult =
  | { ok: true; document: ParsedDocument }
  | { ok: false; reason: string }

/**
 * The review AND what applying will do, from one computation.
 *
 * `next` is the exact state `applyImport` sets, and `entries` is a diff of it
 * against what is loaded now — so the two cannot disagree. That is the same
 * rule `domain.PlanBulk` follows for a bulk action, and for the same reason:
 * an operator who read "three groups added, nothing removed" must not then
 * watch something else happen.
 */
export interface ImportPreview {
  mode: ImportMode
  next: SettingsPayload
  entries: ImportEntry[]
  unknownFields: number
  invalidFields: number
  version: number
  exportedAt: string
  fromTheFuture: boolean
}

/** The header every exported file carries, for whoever opens it. */
function readme(): string[] {
  return [
    'PodSteer settings. Safe to keep in git and to share with a colleague.',
    'Import it from Settings → Export & import, which reviews every change before applying it.',
    'It carries display preferences and your projects, groups and their guardrails.',
    'It carries NO credentials, NO kubeconfig, NO cluster addresses and NO object names —',
    'no pod, node, namespace or workload appears anywhere in this file.',
    'It DOES carry kubeconfig CONTEXT NAMES, as the keys under "pinnedKinds" and',
    '"assignments": a group cannot be attached to a cluster without naming one. They are',
    'names your own kubeconfig already gives you, and they identify nothing inside a cluster —',
    'but anyone you send this file to will see which contexts you have. That is the whole of',
    'what this file says about your clusters.',
  ]
}

/** Builds the document from what is loaded right now. */
export function buildDocument(now: Date = new Date()): SettingsDocument {
  return {
    _readme: readme(),
    kind: SETTINGS_FILE_KIND,
    version: SETTINGS_FILE_VERSION,
    exportedAt: now.toISOString(),
    preferences: preferences.exportable(),
    organisation: organisation.exportable(),
  }
}

/** The document as it is written to disk: two-space JSON, newline-terminated. */
export function serialiseDocument(document: SettingsDocument): string {
  return `${JSON.stringify(document, null, 2)}\n`
}

/** `podsteer-settings-YYYYMMDD-HHMMSS.json`, in local time. */
export function buildSettingsFilename(now: Date = new Date()): string {
  const pad = (n: number): string => String(n).padStart(2, '0')
  const date = `${now.getFullYear()}${pad(now.getMonth() + 1)}${pad(now.getDate())}`
  const time = `${pad(now.getHours())}${pad(now.getMinutes())}${pad(now.getSeconds())}`
  return `podsteer-settings-${date}-${time}.json`
}

/** Fields the envelope itself accounts for; anything else at the top is unknown. */
const ENVELOPE_FIELDS = new Set([
  '_readme',
  'kind',
  'version',
  'exportedAt',
  'preferences',
  'organisation',
])

/**
 * Reads a document, strict about shape and forgiving about extras.
 *
 * Strict where being wrong would apply somebody else's file to the wrong
 * thing: it must be JSON, an object, marked as PodSteer's, and carry a
 * positive whole version and a timestamp. Any of those missing is a refusal
 * with the reason, and nothing is applied — a half-read document is the one
 * outcome worth more than an inconvenience here.
 *
 * Forgiving everywhere else: an unknown field is counted, never fatal, so a
 * file written by a newer build still imports what this build understands. A
 * KNOWN field carrying a value this build refuses is dropped and counted too,
 * rather than taking the document down with it — the store's own storage read
 * has always worked that way, and a file from a colleague deserves no less
 * trust than the last version of this application to run on this machine.
 */
export function parseDocument(text: string): ParseResult {
  let raw: unknown
  try {
    raw = JSON.parse(text)
  } catch {
    return { ok: false, reason: 'That file is not JSON.' }
  }

  if (typeof raw !== 'object' || raw === null || Array.isArray(raw)) {
    return { ok: false, reason: 'That file does not hold a settings document.' }
  }

  const document = raw as Record<string, unknown>

  if (document.kind !== SETTINGS_FILE_KIND) {
    return {
      ok: false,
      reason: 'That is not a PodSteer settings file — it is missing the PodSteerSettings marker.',
    }
  }

  const version = document.version
  if (typeof version !== 'number' || !Number.isInteger(version) || version < 1) {
    return { ok: false, reason: 'That file does not say which settings version it is.' }
  }

  if (typeof document.exportedAt !== 'string' || document.exportedAt === '') {
    return { ok: false, reason: 'That file does not say when it was exported.' }
  }

  // Present-but-wrong is refused; absent is not. A file carrying only the
  // organisation half is a legitimate thing to hand somebody, and so is one
  // carrying only preferences.
  for (const half of ['preferences', 'organisation'] as const) {
    const value = document[half]
    if (value !== undefined && (typeof value !== 'object' || value === null || Array.isArray(value))) {
      return { ok: false, reason: `The "${half}" section of that file is not a settings object.` }
    }
  }

  const fromPreferences = readExportedPreferences(document.preferences)
  const fromOrganisation = readExportedOrganisation(document.organisation)

  const unknownTop = Object.keys(document).filter((key) => !ENVELOPE_FIELDS.has(key))

  return {
    ok: true,
    document: {
      version,
      exportedAt: document.exportedAt,
      payload: { preferences: fromPreferences.value, organisation: fromOrganisation.value },
      unknownFields: unknownTop.length + fromPreferences.unknown.length + fromOrganisation.unknown.length,
      invalidFields: fromPreferences.invalid.length + fromOrganisation.invalid.length,
      fromTheFuture: version > SETTINGS_FILE_VERSION,
    },
  }
}

/** What is loaded right now, as the same shape a document carries. */
export function currentPayload(): SettingsPayload {
  return { preferences: preferences.exportable(), organisation: organisation.exportable() }
}

/**
 * What the two modes mean, in one place.
 *
 * MERGE (the default) takes every field the file names and keeps every field
 * it does not — including, within a map, every key it does not mention. Two
 * people's arrangements combine rather than one erasing the other, which is
 * what somebody adopting a teammate's project layout actually wants.
 *
 * REPLACE makes the exported surface exactly what the file says: a field the
 * file omits goes back to this build's default, and a map becomes the file's
 * map with the local-only keys gone. It is the mode for "make this machine
 * match that one".
 *
 * NEITHER MODE TOUCHES WHAT THE FILE NEVER CARRIED. Snoozes, the per-cluster
 * namespace filter and everything else outside the allowlist survive a
 * replace untouched, because destroying data on the strength of a document's
 * SILENCE about it would be the export's redaction rules turned into a way to
 * lose things.
 */
function mergePayload(
  current: SettingsPayload,
  incoming: ParsedDocument['payload'],
  mode: ImportMode,
): SettingsPayload {
  return {
    preferences: mergeExportedPreferences(current.preferences, incoming.preferences, mode),
    organisation: mergeExportedOrganisation(current.organisation, incoming.organisation, mode),
  }
}

/**
 * Builds the review and the state it describes.
 *
 * `next` first, `entries` from it — see ImportPreview for why the review is
 * derived from the outcome rather than computed beside it.
 */
export function previewImport(
  current: SettingsPayload,
  parsed: ParsedDocument,
  mode: ImportMode,
): ImportPreview {
  const next = mergePayload(current, parsed.payload, mode)

  return {
    mode,
    next,
    entries: [
      ...describePreferenceChanges(current.preferences, next.preferences),
      ...describeOrganisationChanges(current.organisation, next.organisation),
    ],
    unknownFields: parsed.unknownFields,
    invalidFields: parsed.invalidFields,
    version: parsed.version,
    exportedAt: parsed.exportedAt,
    fromTheFuture: parsed.fromTheFuture,
  }
}

/** Applies exactly what the preview described, both halves or neither. */
export function applyImport(preview: ImportPreview): void {
  preferences.applyExported(preview.next.preferences)
  organisation.applyExported(preview.next.organisation)
}

/** How many lines of a review actually change something. */
export function countChanges(entries: ImportEntry[]): Record<ImportOutcome, number> {
  const counts: Record<ImportOutcome, number> = { add: 0, change: 0, same: 0, remove: 0 }
  for (const entry of entries) counts[entry.outcome] += 1
  return counts
}
