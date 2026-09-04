/**
 * Durable UI preferences.
 *
 * Everything an operator adjusts about how PodSteer *looks* lives here and
 * survives a restart: the colour theme, page size, which columns are shown and
 * how wide, whether the navigator is collapsed, how often views refresh.
 *
 * Column settings are keyed by kind, not global. An operator who widens the
 * name column on Pods has said nothing about Ingresses, and applying it there
 * would undo the adjustment they actually wanted.
 *
 * Persistence is localStorage rather than the Go side. These are per-machine
 * display choices with no security or correctness weight, and keeping them in
 * the webview means adjusting a column width costs no IPC round trip.
 */

import {
  ALERT_SEVERITIES,
  DEFAULT_ALERT_SOUNDS,
  alertPlayer,
  isAlertSound,
  type AlertSeverity,
} from './alerts.svelte'
import { customColumnId, normaliseSpecs, type CustomColumnSpec } from '$lib/customColumns'
import {
  entryFor,
  describeValue,
  mergeRecord,
  mergeValue,
  type FieldRead,
  type ImportEntry,
  type ImportMode,
} from '$lib/settingsDiff'

/** Page sizes offered. 25 is the default; 100 is the ceiling. */
export const PAGE_SIZES = [10, 25, 50, 100] as const

/** How many rows a page holds. */
export type PageSize = (typeof PAGE_SIZES)[number]

/**
 * The image an ephemeral debug container proposes before the operator edits
 * it. Matches the Go default (domain.DefaultDebugImage) so the dialog and the
 * backend agree on what "default" is.
 */
export const DEFAULT_DEBUG_IMAGE = 'busybox:1.37'

/**
 * The image a node shell runs. alpine carries nsenter via busybox, which is
 * all the node shell needs to enter the host namespaces; it pulls in a second
 * and is tiny.
 */
export const DEFAULT_NODE_SHELL_IMAGE = 'docker.io/library/alpine:3.20'

/** The namespace a node-shell pod is created in, matching kubectl node-shell. */
export const DEFAULT_NODE_SHELL_NAMESPACE = 'kube-system'

/** The colour schemes PodSteer can render in. */
export const THEMES = ['dark', 'light'] as const

/** A resolved colour scheme — what is actually painted. */
export type Theme = (typeof THEMES)[number]

/**
 * What the operator chose, which is not the same thing as what is painted.
 *
 * Ordered as a brightness ramp so that cycling through them with the header
 * button feels like a dimmer rather than a shuffle.
 */
export const THEME_PREFERENCES = ['light', 'system', 'dark'] as const

/** A theme choice: a specific scheme, or "whatever the OS is set to". */
export type ThemePreference = (typeof THEME_PREFERENCES)[number]

/** Display names for the theme choices. */
export const THEME_LABELS: Record<ThemePreference, string> = {
  light: 'Light',
  system: 'System',
  dark: 'Dark',
}

/** Matches the OS-level dark mode setting. */
const DARK_QUERY = '(prefers-color-scheme: dark)'

/** Reads the OS's current scheme, defaulting to dark where it cannot be read. */
function systemTheme(): Theme {
  if (typeof window === 'undefined' || !window.matchMedia) return 'dark'
  return window.matchMedia(DARK_QUERY).matches ? 'dark' : 'light'
}

/** Refresh intervals offered in Settings, in milliseconds. */
export const REFRESH_INTERVALS = [
  { label: 'Every 5 seconds', value: 5_000 },
  { label: 'Every 10 seconds', value: 10_000 },
  { label: 'Every 30 seconds', value: 30_000 },
  { label: 'Every minute', value: 60_000 },
  { label: 'Manual only', value: 0 },
] as const

/**
 * How wide the detail panel opens, as a share of the window.
 *
 * A SHARE RATHER THAN A SIZE, because the complaint the setting answers is
 * relative: the panel covers too much of the list behind it. How much of the
 * list is left is a proportion, and a fixed 704px leaves half a laptop and a
 * fifth of an ultrawide.
 *
 * Both ends are clamped in CSS all the same — see DETAIL_MIN_REM and
 * DETAIL_MAX_REM — because a proportion alone breaks at the extremes in the
 * other direction: a quarter of a small laptop is narrower than one row of
 * this panel's own two columns, and half an ultrawide is a page of whitespace.
 *
 * Stored as the number rather than as which button was pressed, so that a
 * drag handle can later write any width in range without a stored setting
 * that has to be migrated out of three categories.
 */
export const DETAIL_WIDTHS = [
  { id: 'wide', label: 'Wide', fraction: 0.5, detail: 'About half the window' },
  { id: 'medium', label: 'Medium', fraction: 1 / 3, detail: 'About a third' },
  { id: 'compact', label: 'Compact', fraction: 0.25, detail: 'About a quarter' },
] as const

/**
 * The share the panel opens at until somebody changes it.
 *
 * Half, which is what it already was on a laptop: it opened at a fixed 44rem,
 * and 704 of a 1440-wide window is very close to this. Somebody who never
 * touches the setting sees no change. It is also what a double-click on the
 * panel's edge restores.
 */
export const DEFAULT_DETAIL_FRACTION = 0.5

/**
 * The label column's share of a detail pane, until somebody drags it.
 *
 * 26% is what the fixed 11rem this column used to be worked out to at the
 * panel's own default width, so a panel nobody has touched looks unchanged.
 */
export const DEFAULT_DETAIL_LABEL_SHARE = 0.26

/** The label column's bounds, in rem, and as a share of the pane. */
export const DETAIL_LABEL_MIN_REM = 5
export const DETAIL_LABEL_MAX_REM = 24
export const DETAIL_LABEL_MAX_SHARE = 0.6

/**
 * What the label column may be, in pixels, in a pane this wide.
 *
 * The same shape as detailWidthBounds and for the same reason: the drag and
 * the resting style must agree exactly, or a divider dropped at the end of its
 * travel would let go of the pointer and land somewhere else.
 *
 * The ceiling is a share rather than a length because it is about the OTHER
 * column: past 60% the values have less room than their labels, which is the
 * wrong way round whatever the pane is.
 */
export function detailLabelBounds(
  paneWidth: number,
  rootFontSize: number,
): { min: number; max: number } {
  const ceiling = paneWidth * DETAIL_LABEL_MAX_SHARE
  const min = Math.min(DETAIL_LABEL_MIN_REM * rootFontSize, ceiling)
  const max = Math.max(min, Math.min(DETAIL_LABEL_MAX_REM * rootFontSize, ceiling))
  return { min, max }
}

/** The CSS this share becomes, matching detailLabelBounds exactly. */
export function detailLabelWidthCSS(share: number): string {
  return `min(${DETAIL_LABEL_MAX_SHARE * 100}%, clamp(${DETAIL_LABEL_MIN_REM}rem, ${
    share * 100
  }%, ${DETAIL_LABEL_MAX_REM}rem))`
}

/** The detail panel's width bounds, in rem, applied whatever the share says. */
export const DETAIL_MIN_REM = 26
export const DETAIL_MAX_REM = 72

/** The most of the window the panel may ever cover, dragged or not. */
export const DETAIL_MAX_SHARE = 0.9

/**
 * What the panel may actually be, in pixels, on a window this wide.
 *
 * ONE FUNCTION FOR BOTH the resting width and a drag in progress. They have to
 * agree exactly: a drag that could be released outside what the resting style
 * allows would let go of the pointer and snap somewhere else, which reads as
 * the application refusing the size rather than as a limit.
 *
 * rootFontSize is passed rather than assumed, because the bounds are in rem
 * and a user who has scaled their interface up has scaled the floor up with
 * it — the floor exists to fit a row of text, and that is what changed.
 */
export function detailWidthBounds(
  windowWidth: number,
  rootFontSize: number,
): { min: number; max: number } {
  const ceiling = windowWidth * DETAIL_MAX_SHARE
  // The floor gives way to the ceiling on a very narrow window rather than
  // the other way round: a panel wider than the window it is in has covered
  // the list completely, and the whole point of the setting is what is left
  // of the list.
  const min = Math.min(DETAIL_MIN_REM * rootFontSize, ceiling)
  const max = Math.max(min, Math.min(DETAIL_MAX_REM * rootFontSize, ceiling))
  return { min, max }
}

const STORAGE_KEY = 'podsteer.preferences.v1'

/**
 * The navigator's width bounds, in pixels.
 *
 * Exported because a drag has to apply them while it is in progress: the
 * clamp used to live only in the setter, so moving the live width out of the
 * store would have let the sidebar follow the pointer past either end and
 * snap back on release.
 */
export function clampNavigatorWidth(width: number): number {
  return Math.max(180, Math.min(400, Math.round(width)))
}

/** The range a threshold may sensibly take, in whole per cent. */
export const THRESHOLD_RANGE = { min: 50, max: 99 } as const

function clampThreshold(value: number): number {
  if (!Number.isFinite(value)) return 80
  return Math.min(THRESHOLD_RANGE.max, Math.max(THRESHOLD_RANGE.min, Math.round(value)))
}

/**
 * Which surface a pair of threshold lines governs.
 *
 * Scoped by SURFACE rather than one pair for the whole application, because
 * the surfaces are read differently. The overview is glanced at to decide
 * whether anything needs attention, and wants its warning early. The lists are
 * stared at for an hour while working, and a colour that fires early there is
 * a colour that stops being read. An operator who wants them identical sets
 * them identical; one who does not now can say so.
 *
 * Scoped by the PAGE, not by what is measured — "Nodes" is the node list, and
 * the node load bars on the overview are the overview's. That is what makes it
 * predictable: the section named after the screen changes that screen.
 */
export type ThresholdScope = 'overview' | 'nodes' | 'pods'

/**
 * Which denominator the pod list's bars divide by.
 *
 * The one measurement in the application that genuinely has two right
 * answers, because it is asked by two different people. Somebody sizing
 * workloads wants usage against the REQUEST — did I reserve about the right
 * amount — where a full bar is a success. Somebody chasing a slow job wants
 * usage against the LIMIT — how much headroom is left before the kernel
 * throttles or kills it — where a full bar is the problem.
 *
 * It also decides whether the threshold ticks can be drawn at all. A tick
 * marks a position on the track, and the thresholds belong to the limit: on a
 * request-denominated bar there is nowhere honest to put them, so they are
 * left off and the colour alone carries the warning.
 *
 * LIMITS by default. A request-denominated bar cannot warn anybody about
 * anything: it is a right-sizing instrument, and right-sizing is a thing done
 * occasionally and deliberately, not the question somebody has open while an
 * application is misbehaving. Reading 130% of a request tells you a pod is
 * bursting, which is normal; the first real signal that it was in trouble is
 * then the OOMKill. Against the limit, the same pod is visibly at 90% of its
 * ceiling with time to act.
 *
 * This defaulted to requests for one build, on the grounds that more pods
 * declare a request than a limit so a limits-first default shows emptier
 * cells. That is an argument about how much of the column is painted, and it
 * was allowed to outrank an argument about what the paint means. A pod with
 * no limit is not a gap in the data either — an unbounded container is one
 * that can take a node down with it, and saying so is worth more than a bar
 * measuring it against a reservation it is already past.
 *
 * It is also the only mode in which the threshold ticks can be drawn, because
 * it is the only one where the bar and the lines count the same thing. A
 * default that cannot show the lines the operator set in Settings is the
 * wrong default.
 */
export type PodMeasure = 'requests' | 'limits'

/**
 * The windows Settings offers for retained usage.
 *
 * Zero first because it is a legitimate choice rather than a disabled state:
 * nothing is held about any object, and every chart starts empty and fills as
 * you watch. The rest stop at half an hour because the samples are held in
 * memory and a chart in a drawer is a "what is it doing right now" instrument
 * — a longer history is the job of the cluster trend, which is on disk.
 */
export const USAGE_WINDOWS = [0, 5, 15, 30]

/** The scopes, in the order Settings lists them. */
export const THRESHOLD_SCOPES: ThresholdScope[] = ['overview', 'nodes', 'pods']

/** Where one surface's bars turn amber and red, and whether they do at all. */
export interface ThresholdSet {
  warn: number
  critical: number
  warnEnabled: boolean
  criticalEnabled: boolean
}

/**
 * The default pair, applied to every scope.
 *
 * 80 and 90 because they are where Kubernetes itself starts to behave
 * differently — the kubelet's default eviction threshold leaves 10% free —
 * and because they are the numbers most operators already run alerts on.
 * They are a starting point, not a claim: every scope is adjustable, which is
 * the point of there being scopes.
 */
function defaultThresholds(): ThresholdSet {
  return { warn: 80, critical: 90, warnEnabled: true, criticalEnabled: true }
}

function defaultThresholdsByScope(): Record<ThresholdScope, ThresholdSet> {
  return {
    overview: defaultThresholds(),
    nodes: defaultThresholds(),
    pods: defaultThresholds(),
  }
}

/**
 * Reads one persisted set, repairing anything storage handed back wrong.
 *
 * Storage outlives the code that wrote it, so nothing here trusts its input:
 * a missing field falls back to the default, and a crossed pair is separated
 * rather than kept — a warning line above the critical one would colour
 * nothing at all, and silently rendering that is worse than adjusting it.
 */
function readThresholdSet(raw: unknown): ThresholdSet {
  const set = defaultThresholds()
  if (!raw || typeof raw !== 'object') return set

  const stored = raw as Partial<ThresholdSet>
  if (typeof stored.warn === 'number') set.warn = clampThreshold(stored.warn)
  if (typeof stored.critical === 'number') set.critical = clampThreshold(stored.critical)
  if (typeof stored.warnEnabled === 'boolean') set.warnEnabled = stored.warnEnabled
  if (typeof stored.criticalEnabled === 'boolean') set.criticalEnabled = stored.criticalEnabled

  if (set.critical <= set.warn) set.critical = clampThreshold(set.warn + 5)
  return set
}

/**
 * Identifies one snoozed object within one finding.
 *
 * JSON rather than a joined string: every part is free text a cluster decides
 * — a finding id already containing ':' and '|', a namespace, an object name —
 * and any separator character picked would eventually turn up inside one of
 * them and merge two unrelated snoozes. This also stays legible in devtools,
 * which a delimiter nobody can type does not.
 */
function snoozeKey(findingId: string, namespace: string, name: string): string {
  return JSON.stringify([findingId, namespace, name])
}

/** Per-column overrides, keyed by column id within a kind. */
interface ColumnPreference {
  /** Pixel width after the operator dragged the divider. */
  width?: number
  /** True when the operator hid the column. */
  hidden?: boolean
}

interface PersistedShape {
  /**
   * The theme *choice*.
   *
   * Deliberately a different key from the `theme` earlier builds wrote. That
   * field always held a concrete scheme — it defaulted to 'dark' and was
   * rewritten on every save — so every existing entry says "dark" whether or
   * not anybody chose it. Reading those as explicit choices would mean nobody
   * ever got the new System default, so the old key is ignored.
   */
  themePreference: ThemePreference
  pageSize: PageSize
  refreshIntervalMs: number
  autoRefresh: boolean
  navigatorCollapsed: boolean
  /** Width of the navigator sidebar in pixels. */
  navigatorWidth: number
  /** How wide the detail panel opens, as a share of the window. */
  detailWidthFraction: number
  /** How wide the label column is, as a share of a detail pane. */
  detailLabelFraction: number
  /** Category names the operator has expanded in the navigator tree. */
  expandedCategories: string[]
  /** Whether the overview's verdict card shows its findings. */
  findingsExpanded: boolean
  /**
   * Whether monospaced panes wrap long lines instead of scrolling sideways.
   *
   * One setting for every such pane rather than one per tab. Wrapping is a
   * reading habit, not a property of a particular manifest, and somebody who
   * turns it off to line up a log's columns wants the YAML tab to stop
   * reflowing too.
   */
  wrapLines: boolean
  /**
   * Whether a manifest shows `metadata.managedFields`.
   *
   * Off, as in kubectl since 1.21. It is server-side apply's bookkeeping, it
   * is half the length of a real object, and somebody who opens the YAML tab
   * is looking for spec.
   */
  showManagedFields: boolean
  /** clusterId -> the namespace filter it was last left on. */
  namespaceByCluster: Record<string, string>
  /**
   * clusterId -> pinned kind ids, in the order the operator pinned them.
   *
   * KIND IDS ONLY — a catalog identifier like "apps/v1/deployments", never an
   * object name — so this is exactly the same shape of fact as
   * namespaceByCluster above and belongs in the same place, the webview's own
   * storage. Nothing here says which Deployment exists, only that this
   * operator watches Deployments on this cluster. Objects opened in the
   * detail drawer are a different kind of fact — see ClusterSession's
   * recentObjects, which is deliberately NOT persisted here or anywhere else.
   */
  pinnedKinds: Record<string, string[]>
  /**
   * Remembered local ports for the port-forward dialog, by the REMOTE port
   * number. See localPortByPortName below for why there are two of these, and
   * the class field for what is deliberately never in either one.
   */
  localPortByRemotePort: Record<string, number>
  /** The same idea, keyed by the container port's NAME when it has one. */
  localPortByPortName: Record<string, number>
  /**
   * The last-used image for an ephemeral debug container, the node-shell
   * image, and the node-shell namespace.
   *
   * The SAME KIND OF FACT as localPortByRemotePort above — a workflow
   * preference (which debugger image, which node-shell namespace), never an
   * object name — which is why it belongs in the webview's own storage
   * alongside them and not in the no-object-names disk file.
   */
  debugImage: string
  nodeShellImage: string
  nodeShellNamespace: string
  /** clusterId -> snoozeKey() -> epoch milliseconds when the snooze lapses. */
  snoozes: Record<string, Record<string, number>>
  /** Per-surface threshold lines. */
  thresholds: Record<ThresholdScope, ThresholdSet>
  /** Which denominator the pod list's bars use. */
  podMeasure: PodMeasure
  /** How much recent usage to keep for the drawer's charts, in minutes. */
  usageWindowMinutes: number
  /** Whether to ask GitHub about newer releases. See UpdateBadge.svelte. */
  /** Which way the dependency map lays out its tiers. */
  mapOrientation: 'horizontal' | 'vertical'
  updateChecksEnabled: boolean
  /** When the last check happened, so a restart does not trigger another. */
  lastUpdateCheck: number
  /** A version the operator has dismissed, so the badge stays gone. */
  dismissedUpdate: string
  /**
   * Detail-pane sections the operator has opened or closed, by id.
   *
   * Only DEVIATIONS are stored, never the whole set. Each section declares
   * its own default, so a section added later opens or closes as its author
   * intended rather than inheriting whatever an old preferences blob happened
   * to record — and somebody who has never touched this has no entries at all.
   */
  sections: Record<string, boolean>
  /**
   * The single pair every surface shared before the setting grew scopes.
   *
   * Read once, to seed all three, and never written again. Somebody who had
   * already moved these to 90/95 for a cluster that runs hot must not have
   * that silently revert to 80/90 because the setting was reorganised.
   */
  warnThreshold?: number
  criticalThreshold?: number
  warnEnabled?: boolean
  criticalEnabled?: boolean
  /** Whether a newly raised finding makes a sound at all. */
  alertSoundsEnabled: boolean
  /**
   * Whether a newly raised CRITICAL finding also posts an OS notification.
   *
   * Separate from alertSoundsEnabled rather than one "tell me" switch: a
   * sound is over in half a second and is heard only by somebody at the
   * window, while a notification persists in a tray and is the thing that
   * reaches somebody who has walked away. People want them on different
   * terms, and a machine that can do one and not the other is ordinary.
   */
  desktopNotificationsEnabled: boolean
  /** severity -> the id of the motif it plays, or SILENT. */
  alertSounds: Record<AlertSeverity, string>
  /**
   * The single sound earlier builds played for every severity.
   *
   * Read once, as the warning sound, and never written again. Somebody who
   * had already chosen Marimba should not have that quietly become Chime
   * because the setting grew a second half.
   */
  alertSound?: string
  /** kindId -> columnId -> preference */
  columns: Record<string, Record<string, ColumnPreference>>
  /**
   * kindId -> the operator's own columns, in the order they are shown.
   *
   * Per KIND and not per cluster: a label key means the same thing on every
   * cluster, and somebody who wants `team` beside every Deployment wants it
   * on the staging tab too. A kind id and a label key are the same order of
   * fact as pinnedKinds above — never an object name — which is what lets
   * them live here. See $lib/customColumns.
   */
  customColumns: Record<string, CustomColumnSpec[]>
}

const DEFAULTS: PersistedShape = {
  // System, so PodSteer matches the desktop it was launched from rather than
  // asserting a preference nobody expressed.
  themePreference: 'system',
  pageSize: 25,
  refreshIntervalMs: 10_000,
  autoRefresh: true,
  navigatorCollapsed: false,
  navigatorWidth: 240,
  detailWidthFraction: DEFAULT_DETAIL_FRACTION,
  detailLabelFraction: DEFAULT_DETAIL_LABEL_SHARE,
  expandedCategories: [],
  findingsExpanded: false,
  wrapLines: true,
  showManagedFields: false,
  namespaceByCluster: {},
  pinnedKinds: {},
  localPortByRemotePort: {},
  localPortByPortName: {},
  debugImage: DEFAULT_DEBUG_IMAGE,
  nodeShellImage: DEFAULT_NODE_SHELL_IMAGE,
  nodeShellNamespace: DEFAULT_NODE_SHELL_NAMESPACE,
  snoozes: {},
  // Both lines on, everywhere. An operator who only wants to hear about the
  // serious case can turn the first one off, but a default that says nothing
  // until something is already critical is a default that arrives too late.
  thresholds: defaultThresholdsByScope(),
  podMeasure: 'limits',
  // Five minutes: enough to show a shape the moment a drawer opens, and small
  // enough that nobody has to think about what it costs. It is bounded by a
  // per-object sample cap as well, so a fast refresh cannot turn it into
  // something expensive.
  usageWindowMinutes: 5,
  // ON BY DEFAULT. Off by default would mean
  // almost nobody ever learns a security fix shipped, which is the outcome the
  // feature exists to prevent. It is one switch away, the switch is in
  // Settings → Notifications, and PODSTEER_UPDATE_CHECK=false overrides it for
  // a whole machine.
  mapOrientation: 'horizontal',
  updateChecksEnabled: true,
  lastUpdateCheck: 0,
  dismissedUpdate: '',
  sections: {},
  // Off. An application that starts making noise nobody asked for is one
  // people mute at the operating system, taking the alarm they DID want with
  // it. Whoever wants this turns it on, and hears the sound as they choose it.
  alertSoundsEnabled: false,
  // Off, for the reason above and one more that is specific to this: on macOS
  // the first notification triggers a system permission prompt, and a prompt
  // nobody asked for is one people deny permanently — taking with it the
  // notification they would have wanted later. Turning it on is what asks.
  desktopNotificationsEnabled: false,
  alertSounds: DEFAULT_ALERT_SOUNDS,
  columns: {},
  customColumns: {},
}

/**
 * The preferences half of a settings file — an ALLOWLIST, not the persisted
 * shape.
 *
 * Three fields of `PersistedShape` are deliberately absent, and each would be
 * a bug rather than an omission if it appeared here:
 *
 * - `snoozes`, whose inner keys are a finding id, a NAMESPACE and an OBJECT
 *   NAME. That is exactly what SECURITY.md says PodSteer does not write, and
 *   an export file is where it would leave the machine.
 * - `namespaceByCluster`, which is a namespace name per cluster — a namespace
 *   is an object, and "which namespace this operator was last reading" is a
 *   fact about their cluster's contents, not about how they like PodSteer to
 *   look.
 * - `lastUpdateCheck` and `dismissedUpdate`, which are machine state rather
 *   than an arrangement anybody made, and are wrong the moment they land on
 *   another machine.
 *
 * See `$lib/settingsFile` for the whole rule, and `settingsFile.test.ts` for
 * the test that fails if this set grows without somebody arguing for it.
 */
export interface ExportedPreferences {
  themePreference: ThemePreference
  pageSize: PageSize
  refreshIntervalMs: number
  autoRefresh: boolean
  navigatorCollapsed: boolean
  navigatorWidth: number
  detailWidthFraction: number
  detailLabelFraction: number
  expandedCategories: string[]
  findingsExpanded: boolean
  wrapLines: boolean
  showManagedFields: boolean
  /** clusterId -> pinned kind ids. A CONTEXT NAME and catalogue ids only. */
  pinnedKinds: Record<string, string[]>
  localPortByRemotePort: Record<string, number>
  localPortByPortName: Record<string, number>
  debugImage: string
  nodeShellImage: string
  nodeShellNamespace: string
  thresholds: Record<ThresholdScope, ThresholdSet>
  podMeasure: PodMeasure
  usageWindowMinutes: number
  mapOrientation: 'horizontal' | 'vertical'
  updateChecksEnabled: boolean
  sections: Record<string, boolean>
  alertSoundsEnabled: boolean
  desktopNotificationsEnabled: boolean
  alertSounds: Record<AlertSeverity, string>
  columns: Record<string, Record<string, ColumnPreference>>
  customColumns: Record<string, CustomColumnSpec[]>
}

class Preferences {
  /** What the operator chose: a scheme, or to follow the OS. */
  themePreference = $state<ThemePreference>(DEFAULTS.themePreference)

  /** The OS's current scheme, kept in step with it while the app runs. */
  #systemTheme = $state<Theme>(systemTheme())
  pageSize = $state<PageSize>(DEFAULTS.pageSize)
  refreshIntervalMs = $state<number>(DEFAULTS.refreshIntervalMs)
  autoRefresh = $state<boolean>(DEFAULTS.autoRefresh)
  navigatorCollapsed = $state<boolean>(DEFAULTS.navigatorCollapsed)
  navigatorWidth = $state<number>(DEFAULTS.navigatorWidth)
  detailWidthFraction = $state<number>(DEFAULTS.detailWidthFraction)
  detailLabelFraction = $state<number>(DEFAULTS.detailLabelFraction)

  /**
   * The label column's share while a divider is being dragged.
   *
   * DELIBERATELY NOT PERSISTED, and deliberately here rather than in the list
   * holding the pointer. Every pane's divider is the same divider — dragging
   * one has to move all of them, which is the whole point of the gesture —
   * and the element that carries the width is the panel, not the list. This
   * is the only place both can see. It holds a share rather than a pixel
   * width for the same reason: a pane nested inside a card is narrower than
   * the one around it, and both have to stay in proportion while the pointer
   * moves, not only after it is released.
   */
  labelShareDrag = $state<number | null>(null)

  /**
   * Category names currently expanded in the navigator.
   *
   * Everything starts collapsed — the tracked set is what is *open*, not
   * what is closed — so a cluster with a dozen categories opens tidy rather
   * than as a wall of kinds, and whatever the operator chooses to expand
   * (say, just Workloads) is exactly what greets them next time.
   */
  expandedCategories = $state<string[]>(DEFAULTS.expandedCategories)

  /**
   * Collapsed by default: the verdict and the count are the alarm, and the
   * findings below them were taking a screenful before the operator had read
   * either.
   */
  findingsExpanded = $state<boolean>(DEFAULTS.findingsExpanded)

  /** Whether monospaced panes wrap long lines. See the shape above. */
  wrapLines = $state<boolean>(DEFAULTS.wrapLines)

  /** Whether a manifest shows managed fields. See the shape above. */
  showManagedFields = $state<boolean>(DEFAULTS.showManagedFields)

  /** clusterId -> last-selected namespace filter. */
  namespaceByCluster = $state<Record<string, string>>({})

  /** clusterId -> pinned kind ids, in the order pinned. See the shape above. */
  pinnedKinds = $state<Record<string, string[]>>({})

  /**
   * Remembered local ports for the port-forward dialog.
   *
   * ONLY remotePort -> localPort and portName -> localPort live here — never
   * the pod, the workload, the namespace or the cluster a forward was to.
   * SECURITY.md enumerates what PodSteer writes to this machine's disk, and
   * object names are deliberately not on that list; a mapping keyed by one
   * would put them there through the back door of "remembering a port".
   * remotePort and portName are properties of a CONTAINER IMAGE, not of any
   * particular cluster's objects, which is what makes them safe to keep and
   * useful across every pod that exposes them.
   *
   * Two maps rather than one because the two keys answer different
   * questions: 5432 means Postgres everywhere it is a container's remote
   * port, so it is worth keying on alone; a NAME is more specific still — see
   * proposeLocalPort for which one wins when both are on record.
   */
  localPortByRemotePort = $state<Record<string, number>>({})
  /** The same idea, keyed by the container port's NAME when it has one. */
  localPortByPortName = $state<Record<string, number>>({})

  /**
   * Remembered debug and node-shell inputs. See the PersistedShape fields of
   * the same names: a workflow preference, never an object name.
   */
  debugImage = $state<string>(DEFAULT_DEBUG_IMAGE)
  nodeShellImage = $state<string>(DEFAULT_NODE_SHELL_IMAGE)
  nodeShellNamespace = $state<string>(DEFAULT_NODE_SHELL_NAMESPACE)

  /** Remembers the debug image the operator last used. Blank resets it to the
   * default rather than persisting an empty image the backend would reject. */
  setDebugImage = (image: string): void => {
    this.debugImage = image.trim() || DEFAULT_DEBUG_IMAGE
    this.#save()
  }

  /** Remembers the node-shell image. Blank resets to the default. */
  setNodeShellImage = (image: string): void => {
    this.nodeShellImage = image.trim() || DEFAULT_NODE_SHELL_IMAGE
    this.#save()
  }

  /** Remembers the node-shell namespace. Blank resets to the default. */
  setNodeShellNamespace = (namespace: string): void => {
    this.nodeShellNamespace = namespace.trim() || DEFAULT_NODE_SHELL_NAMESPACE
    this.#save()
  }

  /**
   * Objects the operator has deliberately quietened, per cluster.
   *
   * Snoozing is per object within a finding, not per finding: "CrashLoopBackOff
   * on twelve pods" is rarely twelve things anybody wants to defer together,
   * and the one pod nobody may restart until the freeze ends should not take
   * the other eleven's alarm with it.
   *
   * Scoped to the finding as well as the object, so quietening a pod's restart
   * loop says nothing about the same pod failing to mount a volume tomorrow.
   *
   * A snooze silences what it covers for as long as it lasts: once every
   * object of a finding is quiet the finding drops out of the verdict, the
   * count and the navigator badge, because half a dismissal — still counted,
   * merely folded away — is the version nobody trusts. Everything stays
   * listed, dimmed and reversible, so quietening something is never the same
   * as losing it.
   */
  snoozes = $state<Record<string, Record<string, number>>>({})

  /**
   * Where a utilisation bar stops being comfortable, per surface.
   *
   * Turning a line off does not move it: somebody who wants only the critical
   * line gets blue all the way to it, and their chosen position for the one
   * they are not using is still there when they switch it back on.
   */
  thresholds = $state<Record<ThresholdScope, ThresholdSet>>(defaultThresholdsByScope())

  /** What the pod list's bars are a proportion of. */
  podMeasure = $state<PodMeasure>(DEFAULTS.podMeasure)

  /**
   * How much recent usage to retain for the drawer's charts, in minutes.
   *
   * Zero turns it off entirely, which is a real choice rather than a
   * degenerate one: it means a chart starts empty when a drawer opens and
   * fills as you watch, and nothing about any object is held in memory.
   */
  usageWindowMinutes = $state<number>(DEFAULTS.usageWindowMinutes)
  mapOrientation = $state<'horizontal' | 'vertical'>(DEFAULTS.mapOrientation)
  updateChecksEnabled = $state<boolean>(DEFAULTS.updateChecksEnabled)
  lastUpdateCheck = $state<number>(DEFAULTS.lastUpdateCheck)
  dismissedUpdate = $state<string>(DEFAULTS.dismissedUpdate)

  /** Detail-pane sections the operator has opened or closed, by id. */
  sections = $state<Record<string, boolean>>({})

  /** Whether a newly raised warning or critical finding makes a sound. */
  alertSoundsEnabled = $state<boolean>(DEFAULTS.alertSoundsEnabled)

  /** Whether a newly raised CRITICAL finding also posts an OS notification. */
  desktopNotificationsEnabled = $state<boolean>(DEFAULTS.desktopNotificationsEnabled)

  /** Which motif each severity plays, by id, or SILENT for none. */
  alertSounds = $state<Record<AlertSeverity, string>>({ ...DEFAULTS.alertSounds })

  /** kindId -> columnId -> preference. */
  columns = $state<Record<string, Record<string, ColumnPreference>>>({})
  customColumns = $state<Record<string, CustomColumnSpec[]>>({})

  constructor() {
    this.#load()
    this.#watchSystemTheme()
    // Apply before first paint: this module is evaluated while the document
    // is still being parsed, so the theme never visibly flips after load.
    this.#applyTheme()
  }

  /**
   * The scheme actually painted.
   *
   * Everything that needs to know what the UI *looks* like reads this;
   * themePreference only says what was asked for.
   */
  readonly resolvedTheme = $derived<Theme>(
    this.themePreference === 'system' ? this.#systemTheme : this.themePreference,
  )

  /** Effective auto-refresh interval, or 0 when refreshing is manual. */
  readonly effectiveIntervalMs = $derived(this.autoRefresh ? this.refreshIntervalMs : 0)

  setTheme = (preference: ThemePreference): void => {
    this.themePreference = preference
    this.#applyTheme()
    this.#save()
  }

  /**
   * Advances to the next theme choice: light → system → dark → light.
   *
   * A three-state control needs a defined order, and brightness is the only
   * one an operator can predict without looking.
   */
  cycleTheme = (): void => {
    const current = THEME_PREFERENCES.indexOf(this.themePreference)
    this.setTheme(THEME_PREFERENCES[(current + 1) % THEME_PREFERENCES.length])
  }

  /**
   * Sets how wide the detail panel opens.
   *
   * Clamped to something a panel can be: a stored value from a future build,
   * or a hand-edited one, must not be able to open a panel over the whole
   * window or reduce it to a sliver.
   */
  /** The label column's share, live while a divider is being dragged. */
  readonly detailLabelShare = $derived(this.labelShareDrag ?? this.detailLabelFraction)

  /** Stores the label column's share. See setDetailWidth on why loosely. */
  setDetailLabelShare = (fraction: number): void => {
    this.detailLabelFraction = Math.min(0.9, Math.max(0.05, fraction))
    this.labelShareDrag = null
    this.#save()
  }

  setDetailWidth = (fraction: number): void => {
    // Sanity only. What the panel may actually be is decided per window by
    // detailWidthBounds, in pixels — clamping the SHARE narrowly here as well
    // would fight it: 90% of a small laptop is a legitimate drag and 0.9 is
    // not a legitimate share of an ultrawide, and the same number cannot be
    // wrong in one place and right in the other.
    this.detailWidthFraction = Math.min(0.95, Math.max(0.05, fraction))
    this.#save()
  }

  setPageSize = (size: PageSize): void => {
    this.pageSize = size
    this.#save()
  }

  setRefreshInterval = (intervalMs: number): void => {
    this.refreshIntervalMs = intervalMs
    // "Manual only" is expressed as a zero interval, which is the same thing
    // as auto-refresh being off — so keep the two in step rather than letting
    // the UI show "off" and "every 5s" simultaneously.
    this.autoRefresh = intervalMs > 0
    this.#save()
  }

  setAutoRefresh = (enabled: boolean): void => {
    this.autoRefresh = enabled
    if (enabled && this.refreshIntervalMs === 0) {
      this.refreshIntervalMs = DEFAULTS.refreshIntervalMs
    }
    this.#save()
  }

  toggleNavigator = (): void => {
    this.navigatorCollapsed = !this.navigatorCollapsed
    this.#save()
  }

  setNavigatorWidth = (width: number): void => {
    this.navigatorWidth = clampNavigatorWidth(width)
    this.#save()
  }

  /** Whether a navigator category is currently expanded. */
  isCategoryExpanded = (category: string): boolean => this.expandedCategories.includes(category)

  /**
   * Whether the overview's verdict card shows its findings.
   *
   * Persisted like every other collapse in the application, and for a sharper
   * reason here: the workspace remounts on every tab switch, so a choice held
   * in the view would be forgotten each time somebody looked at another
   * cluster and came back.
   */
  toggleFindings = (): void => {
    this.findingsExpanded = !this.findingsExpanded
    this.#save()
  }

  toggleWrapLines = (): void => {
    this.wrapLines = !this.wrapLines
    this.#save()
  }

  toggleManagedFields = (): void => {
    this.showManagedFields = !this.showManagedFields
    this.#save()
  }

  toggleCategory = (category: string): void => {
    this.expandedCategories = this.expandedCategories.includes(category)
      ? this.expandedCategories.filter((entry) => entry !== category)
      : [...this.expandedCategories, category]
    this.#save()
  }

  // --- Per-cluster namespace --------------------------------------------------

  /**
   * The namespace an operator was last looking at in this cluster, if any.
   *
   * Falling back to the kubeconfig context's own default namespace only
   * happens once, on a cluster nobody has opened before — after that, the
   * operator's own choice always wins, because kubeconfig's "default" is a
   * fallback for kubectl, not a statement about which namespace matters to
   * whoever is looking at PodSteer.
   */
  getClusterNamespace = (clusterId: string): string | undefined => this.namespaceByCluster[clusterId]

  setClusterNamespace = (clusterId: string, namespace: string): void => {
    this.namespaceByCluster = { ...this.namespaceByCluster, [clusterId]: namespace }
    this.#save()
  }

  // --- Pinned kinds -----------------------------------------------------------

  /** Kind ids pinned for one cluster, in the order the operator pinned them. */
  pinnedKindsFor = (clusterId: string): string[] => this.pinnedKinds[clusterId] ?? []

  /** Whether a kind is currently pinned for a cluster. */
  isKindPinned = (clusterId: string, kindId: string): boolean =>
    this.pinnedKindsFor(clusterId).includes(kindId)

  /**
   * Pins a kind, appended after whatever is already pinned.
   *
   * A kind already pinned is left exactly where it is rather than moved to
   * the end — clicking a filled star twice in a row must not reorder the
   * section out from under somebody who did not ask to reorder anything.
   */
  pinKind = (clusterId: string, kindId: string): void => {
    const existing = this.pinnedKindsFor(clusterId)
    if (existing.includes(kindId)) return
    this.pinnedKinds = { ...this.pinnedKinds, [clusterId]: [...existing, kindId] }
    this.#save()
  }

  /** Unpins a kind. Not present is not an error — unpinning is idempotent. */
  unpinKind = (clusterId: string, kindId: string): void => {
    const existing = this.pinnedKindsFor(clusterId)
    if (!existing.includes(kindId)) return
    this.pinnedKinds = {
      ...this.pinnedKinds,
      [clusterId]: existing.filter((id) => id !== kindId),
    }
    this.#save()
  }

  // --- Remembered local ports ------------------------------------------------

  /**
   * What the port-forward dialog should propose for a container port, if
   * anything is on record.
   *
   * NAME TAKES PRECEDENCE. A container port named "postgres" keeps whatever
   * local port an operator settled on wherever it turns up, which is more
   * specific than the bare remote port number — 5432 is also Postgres on a
   * pod nobody has decided should share that mapping. Falling back to the
   * remote port when there is no name (or no memory of that name yet) is
   * still useful: it is the majority of what "remember the port" means in
   * practice, most containers exposing one port they care about.
   */
  proposeLocalPort = (remotePort: number, portName: string): number | undefined => {
    const byName = portName ? this.localPortByPortName[portName] : undefined
    return byName ?? this.localPortByRemotePort[String(remotePort)]
  }

  /**
   * Records the local port a forward actually bound, so the next forward to
   * this remote port — or to this NAMED port on any other pod — proposes it
   * again.
   *
   * Called with whatever the forward actually bound, whether the operator
   * typed it or the operating system chose it: both are worth remembering,
   * and treating only a deliberate choice as worth keeping would mean the
   * common case — nobody types anything, ever — never builds up any memory
   * at all.
   */
  rememberLocalPort = (remotePort: number, portName: string, localPort: number): void => {
    this.localPortByRemotePort = {
      ...this.localPortByRemotePort,
      [String(remotePort)]: localPort,
    }
    if (portName) {
      this.localPortByPortName = { ...this.localPortByPortName, [portName]: localPort }
    }
    this.#save()
  }

  // --- Thresholds -----------------------------------------------------------

  /** The pair governing one surface. */
  thresholdsFor = (scope: ThresholdScope): ThresholdSet => this.thresholds[scope]

  /**
   * Moves one line, pushing the other out of the way if it has to.
   *
   * The two cannot cross: a warning that fires after the critical would
   * colour nothing, and silently accepting that is worse than adjusting the
   * other end where the operator can see it happen. Which one gives way
   * depends on which was grabbed — the line being dragged is the one the
   * operator is thinking about, so it keeps the value they asked for.
   */
  setThreshold = (scope: ThresholdScope, line: 'warn' | 'critical', value: number): void => {
    const set = { ...this.thresholds[scope] }

    if (line === 'warn') {
      set.warn = clampThreshold(value)
      if (set.critical <= set.warn) set.critical = clampThreshold(set.warn + 5)
    } else {
      set.critical = clampThreshold(value)
      if (set.warn >= set.critical) set.warn = clampThreshold(set.critical - 5)
    }

    // Replaced rather than mutated, so one assignment invalidates the derived
    // values that read it instead of each field doing so separately.
    this.thresholds = { ...this.thresholds, [scope]: set }
    this.#save()
  }

  /** Whether a detail-pane section is open, falling back to its own default. */
  sectionOpen = (id: string, fallback: boolean): boolean => this.sections[id] ?? fallback

  /** Records that a section was opened or closed. */
  setSectionOpen = (id: string, open: boolean): void => {
    this.sections = { ...this.sections, [id]: open }
    this.#save()
  }

  /** Sets how much recent usage the drawer's charts start with. */
  setUsageWindowMinutes = (minutes: number): void => {
    this.usageWindowMinutes = USAGE_WINDOWS.includes(minutes) ? minutes : DEFAULTS.usageWindowMinutes
  }

  /**
   * Lays the dependency map out along one axis or the other.
   *
   * Remembered rather than reset per pod: somebody who prefers the map on its
   * side prefers it on its side, and having to turn every map they open is
   * the setting failing to be one.
   */
  setMapOrientation(value: 'horizontal' | 'vertical'): void {
    this.mapOrientation = value
    this.#save()
  }

  /**
   * Turns the update check on or off.
   *
   * Turning it OFF forgets when the last one happened, so switching it back on
   * checks immediately rather than waiting out the remainder of a day nobody
   * was checking during.
   */
  setUpdateChecksEnabled(enabled: boolean): void {
    this.updateChecksEnabled = enabled
    if (!enabled) this.lastUpdateCheck = 0
    this.#save()
  }

  /** Records that a check just happened. */
  markUpdateChecked(at: number): void {
    this.lastUpdateCheck = at
    this.#save()
  }

  /**
   * Stops the badge nagging about one particular version.
   *
   * Per VERSION rather than a blanket "never show me this": somebody who is
   * not upgrading today still wants to hear about the release after this one,
   * and a permanent dismissal is what the Settings switch is for.
   */
  dismissUpdate(version: string): void {
    this.dismissedUpdate = version
    this.#save()
    this.#save()
  }

  /** Chooses what the pod list's bars measure against. */
  setPodMeasure = (measure: PodMeasure): void => {
    this.podMeasure = measure
    this.#save()
  }

  /** Switches one line on or off, leaving where it sits untouched. */
  setThresholdEnabled = (
    scope: ThresholdScope,
    line: 'warn' | 'critical',
    enabled: boolean,
  ): void => {
    const set = { ...this.thresholds[scope] }
    if (line === 'warn') set.warnEnabled = enabled
    else set.criticalEnabled = enabled

    this.thresholds = { ...this.thresholds, [scope]: set }
    this.#save()
  }

  // --- Alert sounds ---------------------------------------------------------

  setAlertSoundsEnabled = (enabled: boolean): void => {
    this.alertSoundsEnabled = enabled
    this.#save()
  }

  /**
   * Records the desktop-notification choice.
   *
   * Only the choice. Asking the operating system for permission is the
   * Settings pane's job, because it is a visible prompt on macOS and belongs
   * to the gesture that caused it — a store that requested permission as a
   * side effect of an import would pop one at somebody who was restoring a
   * colleague's column layout.
   */
  setDesktopNotificationsEnabled = (enabled: boolean): void => {
    this.desktopNotificationsEnabled = enabled
    this.#save()
  }

  /**
   * Chooses one severity's motif, and plays it.
   *
   * Choosing a sound without hearing it is choosing from a few words, so the
   * selection IS the preview. It also does the browser's unlocking for free:
   * this is a click, so the audio context it creates is allowed to run, and
   * the first real alert is not the one that discovers it was suspended.
   */
  setAlertSound = (severity: AlertSeverity, id: string): void => {
    this.alertSounds = { ...this.alertSounds, [severity]: id }
    this.#save()
    void alertPlayer.play(id)
  }

  /** The motif a severity plays, falling back to its default. */
  alertSoundFor = (severity: AlertSeverity): string =>
    this.alertSounds[severity] ?? DEFAULT_ALERT_SOUNDS[severity]

  // --- Snoozed findings -----------------------------------------------------

  /**
   * When an object's snooze lapses within a finding, or 0 when it is not
   * snoozed.
   *
   * Expiry is evaluated on read rather than on a timer. Nothing here needs to
   * fire at the moment it lapses — the overview is re-derived on every
   * assessment, so a snooze that ran out is gone by the next refresh, and on
   * "Manual only" it reappears the moment somebody asks for fresh data, which
   * is exactly when they are looking.
   */
  snoozedUntil = (clusterId: string, findingId: string, namespace: string, name: string): number => {
    const until = this.snoozes[clusterId]?.[snoozeKey(findingId, namespace, name)] ?? 0
    return until > Date.now() ? until : 0
  }

  /** Quietens one object of a finding for `durationMs` from now. */
  snooze = (
    clusterId: string,
    findingId: string,
    namespace: string,
    name: string,
    durationMs: number,
  ): void => {
    const forCluster = {
      ...(this.snoozes[clusterId] ?? {}),
      [snoozeKey(findingId, namespace, name)]: Date.now() + durationMs,
    }
    this.snoozes = { ...this.snoozes, [clusterId]: forCluster }
    this.#save()
  }

  /** Brings one object back before its snooze lapses. */
  unsnooze = (clusterId: string, findingId: string, namespace: string, name: string): void => {
    const forCluster = { ...(this.snoozes[clusterId] ?? {}) }
    delete forCluster[snoozeKey(findingId, namespace, name)]
    this.snoozes = { ...this.snoozes, [clusterId]: forCluster }
    this.#save()
  }

  /**
   * Drops lapsed entries.
   *
   * Called before every write, so the stored object cannot grow without bound
   * on a cluster whose findings churn — each new pod problem gets a new id,
   * and without this the file would accumulate one dead key per snooze
   * forever.
   */
  #pruneSnoozes(): Record<string, Record<string, number>> {
    const now = Date.now()
    const kept: Record<string, Record<string, number>> = {}
    for (const [clusterId, findings] of Object.entries(this.snoozes)) {
      const live = Object.fromEntries(
        Object.entries(findings).filter(([, until]) => typeof until === 'number' && until > now),
      )
      if (Object.keys(live).length > 0) kept[clusterId] = live
    }
    return kept
  }

  // --- Columns --------------------------------------------------------------

  /** Returns the stored width for a column, or undefined to use its default. */
  columnWidth = (kindId: string, columnId: string): number | undefined =>
    this.columns[kindId]?.[columnId]?.width

  /** Reports whether the operator has hidden a column. */
  isColumnHidden = (kindId: string, columnId: string): boolean =>
    this.columns[kindId]?.[columnId]?.hidden === true

  setColumnWidth = (kindId: string, columnId: string, width: number): void => {
    this.#mutateColumn(kindId, columnId, (preference) => {
      preference.width = Math.round(width)
    })
  }

  toggleColumn = (kindId: string, columnId: string): void => {
    this.#mutateColumn(kindId, columnId, (preference) => {
      preference.hidden = !preference.hidden
    })
  }

  /** Drops every column override for a kind, restoring its defaults. */
  resetColumns = (kindId: string): void => {
    const { [kindId]: _dropped, ...rest } = this.columns
    this.columns = rest
    this.#save()
  }

  // --- Custom columns -------------------------------------------------------

  /** The operator's own columns for a kind, in display order. */
  customColumnsFor = (kindId: string): CustomColumnSpec[] => this.customColumns[kindId] ?? []

  /**
   * Adds a column to a kind, at the end. Adding one that already exists is
   * a no-op rather than a duplicate, and an invalid key is refused the same
   * way — the picker validates too, but storage is the layer that has to
   * hold.
   */
  addCustomColumn = (kindId: string, spec: CustomColumnSpec): boolean => {
    const next = normaliseSpecs([...this.customColumnsFor(kindId), spec])
    if (next.length === this.customColumnsFor(kindId).length) return false
    this.#setCustomColumns(kindId, next)
    return true
  }

  removeCustomColumn = (kindId: string, spec: CustomColumnSpec): void => {
    const id = customColumnId(spec)
    this.#setCustomColumns(
      kindId,
      this.customColumnsFor(kindId).filter((existing) => customColumnId(existing) !== id),
    )
  }

  /**
   * Moves a column from one position to another within a kind. Indices
   * outside the list are ignored rather than clamped: a stale index from a
   * menu that was open across a change must not silently reorder something.
   */
  moveCustomColumn = (kindId: string, from: number, to: number): void => {
    const existing = this.customColumnsFor(kindId)
    if (from === to || from < 0 || to < 0 || from >= existing.length || to >= existing.length) return
    const next = [...existing]
    const [moved] = next.splice(from, 1)
    next.splice(to, 0, moved)
    this.#setCustomColumns(kindId, next)
  }

  #setCustomColumns(kindId: string, specs: CustomColumnSpec[]): void {
    // Reassign rather than mutate, for the reason #mutateColumn gives. A
    // kind with no columns left loses its key entirely, so the stored
    // object does not grow one empty list per kind ever visited.
    const { [kindId]: _dropped, ...rest } = this.customColumns
    this.customColumns = specs.length > 0 ? { ...rest, [kindId]: specs } : rest
    this.#save()
  }

  #mutateColumn(kindId: string, columnId: string, mutate: (preference: ColumnPreference) => void) {
    // Reassign rather than mutate in place: $state tracks the root reference,
    // and a nested write would not notify anything reading a derived value.
    const forKind = { ...(this.columns[kindId] ?? {}) }
    const preference = { ...(forKind[columnId] ?? {}) }
    mutate(preference)
    forKind[columnId] = preference
    this.columns = { ...this.columns, [kindId]: forKind }
    this.#save()
  }

  // --- Settings file --------------------------------------------------------

  /**
   * What travels in a settings file.
   *
   * WRITTEN OUT FIELD BY FIELD, never spread from a persisted blob. A spread
   * would carry whatever the shape grows next, and two of the things it has
   * already grown — the snooze map and the per-cluster namespace — hold
   * object names. The explicit list is what makes adding one a decision
   * somebody makes rather than one that happens to them. See
   * ExportedPreferences.
   */
  exportable = (): ExportedPreferences => ({
    themePreference: this.themePreference,
    pageSize: this.pageSize,
    refreshIntervalMs: this.refreshIntervalMs,
    autoRefresh: this.autoRefresh,
    navigatorCollapsed: this.navigatorCollapsed,
    navigatorWidth: this.navigatorWidth,
    detailWidthFraction: this.detailWidthFraction,
    detailLabelFraction: this.detailLabelFraction,
    expandedCategories: [...this.expandedCategories],
    findingsExpanded: this.findingsExpanded,
    wrapLines: this.wrapLines,
    showManagedFields: this.showManagedFields,
    pinnedKinds: plainCopy(this.pinnedKinds),
    localPortByRemotePort: { ...this.localPortByRemotePort },
    localPortByPortName: { ...this.localPortByPortName },
    debugImage: this.debugImage,
    nodeShellImage: this.nodeShellImage,
    nodeShellNamespace: this.nodeShellNamespace,
    thresholds: plainCopy(this.thresholds),
    podMeasure: this.podMeasure,
    usageWindowMinutes: this.usageWindowMinutes,
    mapOrientation: this.mapOrientation,
    updateChecksEnabled: this.updateChecksEnabled,
    sections: { ...this.sections },
    alertSoundsEnabled: this.alertSoundsEnabled,
    desktopNotificationsEnabled: this.desktopNotificationsEnabled,
    alertSounds: { ...this.alertSounds },
    columns: plainCopy(this.columns),
    customColumns: plainCopy(this.customColumns),
  })

  /**
   * Adopts an imported set wholesale, then persists once.
   *
   * The caller passes the COMPLETE result of the merge — see
   * `mergeExportedPreferences` — so nothing here decides anything about
   * merge versus replace. The theme is reapplied because it is the one field
   * with an effect outside this object.
   */
  applyExported = (next: ExportedPreferences): void => {
    this.themePreference = next.themePreference
    this.pageSize = next.pageSize
    this.refreshIntervalMs = next.refreshIntervalMs
    this.autoRefresh = next.autoRefresh
    this.navigatorCollapsed = next.navigatorCollapsed
    this.navigatorWidth = next.navigatorWidth
    this.detailWidthFraction = next.detailWidthFraction
    this.detailLabelFraction = next.detailLabelFraction
    this.expandedCategories = [...next.expandedCategories]
    this.findingsExpanded = next.findingsExpanded
    this.wrapLines = next.wrapLines
    this.showManagedFields = next.showManagedFields
    this.pinnedKinds = plainCopy(next.pinnedKinds)
    this.localPortByRemotePort = { ...next.localPortByRemotePort }
    this.localPortByPortName = { ...next.localPortByPortName }
    this.debugImage = next.debugImage
    this.nodeShellImage = next.nodeShellImage
    this.nodeShellNamespace = next.nodeShellNamespace
    this.thresholds = plainCopy(next.thresholds)
    this.podMeasure = next.podMeasure
    this.usageWindowMinutes = next.usageWindowMinutes
    this.mapOrientation = next.mapOrientation
    this.updateChecksEnabled = next.updateChecksEnabled
    this.sections = { ...next.sections }
    this.alertSoundsEnabled = next.alertSoundsEnabled
    this.desktopNotificationsEnabled = next.desktopNotificationsEnabled
    this.alertSounds = { ...next.alertSounds }
    this.columns = plainCopy(next.columns)
    this.customColumns = plainCopy(next.customColumns)

    this.#applyTheme()
    this.#save()
  }

  // --- Persistence ----------------------------------------------------------

  #load(): void {
    try {
      const raw = localStorage.getItem(STORAGE_KEY)
      if (!raw) return

      const stored = JSON.parse(raw) as Partial<PersistedShape>

      // Validate rather than trust: stored preferences outlive the code that
      // wrote them, and a page size of 5000 read from an older build would
      // render fifty thousand rows and hang the window.
      if (
        stored.themePreference &&
        (THEME_PREFERENCES as readonly string[]).includes(stored.themePreference)
      ) {
        this.themePreference = stored.themePreference
      }
      if (stored.pageSize && (PAGE_SIZES as readonly number[]).includes(stored.pageSize)) {
        this.pageSize = stored.pageSize
      }
      if (typeof stored.refreshIntervalMs === 'number' && stored.refreshIntervalMs >= 0) {
        this.refreshIntervalMs = stored.refreshIntervalMs
      }
      if (typeof stored.autoRefresh === 'boolean') this.autoRefresh = stored.autoRefresh
      if (typeof stored.navigatorCollapsed === 'boolean') {
        this.navigatorCollapsed = stored.navigatorCollapsed
      }

      if (typeof stored.findingsExpanded === 'boolean') {
        this.findingsExpanded = stored.findingsExpanded
      }
      if (typeof stored.wrapLines === 'boolean') {
        this.wrapLines = stored.wrapLines
      }
      if (typeof stored.showManagedFields === 'boolean') {
        this.showManagedFields = stored.showManagedFields
      }
      if (typeof stored.navigatorWidth === 'number' && stored.navigatorWidth >= 180 && stored.navigatorWidth <= 400) {
        this.navigatorWidth = stored.navigatorWidth
      }
      if (
        typeof stored.detailWidthFraction === 'number' &&
        stored.detailWidthFraction >= 0.05 &&
        stored.detailWidthFraction <= 0.95
      ) {
        this.detailWidthFraction = stored.detailWidthFraction
      }
      if (
        typeof stored.detailLabelFraction === 'number' &&
        stored.detailLabelFraction >= 0.05 &&
        stored.detailLabelFraction <= 0.9
      ) {
        this.detailLabelFraction = stored.detailLabelFraction
      }
      if (Array.isArray(stored.expandedCategories)) {
        this.expandedCategories = stored.expandedCategories.filter(
          (entry): entry is string => typeof entry === 'string',
        )
      }
      if (stored.namespaceByCluster && typeof stored.namespaceByCluster === 'object') {
        this.namespaceByCluster = stored.namespaceByCluster
      }
      // BACKWARD-COMPATIBLE: a preferences blob written before this setting
      // existed has no `pinnedKinds` key at all, and the field stays at its
      // default of {} — nobody's navigator gains a Pinned section they never
      // asked for. Each cluster's list is filtered to strings so a corrupted
      // or hand-edited entry cannot smuggle something other than a kind id in.
      if (stored.pinnedKinds && typeof stored.pinnedKinds === 'object') {
        const cleaned: Record<string, string[]> = {}
        for (const [clusterId, ids] of Object.entries(stored.pinnedKinds)) {
          if (Array.isArray(ids)) {
            cleaned[clusterId] = ids.filter((id): id is string => typeof id === 'string')
          }
        }
        this.pinnedKinds = cleaned
      }
      if (stored.localPortByRemotePort && typeof stored.localPortByRemotePort === 'object') {
        this.localPortByRemotePort = stored.localPortByRemotePort
      }
      if (stored.localPortByPortName && typeof stored.localPortByPortName === 'object') {
        this.localPortByPortName = stored.localPortByPortName
      }
      // A stored empty string is ignored rather than trusted: it would send
      // the backend an image or namespace it will reject, so the default
      // stands until the operator sets a real one.
      if (typeof stored.debugImage === 'string' && stored.debugImage.trim() !== '') {
        this.debugImage = stored.debugImage
      }
      if (typeof stored.nodeShellImage === 'string' && stored.nodeShellImage.trim() !== '') {
        this.nodeShellImage = stored.nodeShellImage
      }
      if (typeof stored.nodeShellNamespace === 'string' && stored.nodeShellNamespace.trim() !== '') {
        this.nodeShellNamespace = stored.nodeShellNamespace
      }
      if (stored.snoozes && typeof stored.snoozes === 'object') {
        this.snoozes = stored.snoozes
      }
      // Thresholds, per scope, validated on the way in — storage outlives the
      // code that wrote it and a pair read back crossed would colour nothing.
      //
      // MIGRATION. Before this setting had scopes there was one pair for the
      // whole application. When only that is present it seeds all three, so
      // an operator who had already moved the lines to 90/95 for a cluster
      // that runs hot keeps them everywhere rather than silently reverting to
      // the defaults because the setting was reorganised.
      const legacy = readThresholdSet({
        warn: stored.warnThreshold,
        critical: stored.criticalThreshold,
        warnEnabled: stored.warnEnabled,
        criticalEnabled: stored.criticalEnabled,
      })
      if (stored.podMeasure === 'requests' || stored.podMeasure === 'limits') {
        this.podMeasure = stored.podMeasure
      }
      if (USAGE_WINDOWS.includes(stored.usageWindowMinutes as number)) {
        this.usageWindowMinutes = stored.usageWindowMinutes as number
      }
      if (stored.mapOrientation === 'horizontal' || stored.mapOrientation === 'vertical') {
        this.mapOrientation = stored.mapOrientation
      }
      if (typeof stored.updateChecksEnabled === 'boolean') {
        this.updateChecksEnabled = stored.updateChecksEnabled
      }
      if (typeof stored.lastUpdateCheck === 'number') {
        this.lastUpdateCheck = stored.lastUpdateCheck
      }
      if (typeof stored.dismissedUpdate === 'string') {
        this.dismissedUpdate = stored.dismissedUpdate
      }
      if (stored.sections && typeof stored.sections === 'object') {
        this.sections = stored.sections
      }

      const scoped = (stored.thresholds ?? {}) as Partial<Record<ThresholdScope, unknown>>

      this.thresholds = {
        overview: scoped.overview ? readThresholdSet(scoped.overview) : legacy,
        nodes: scoped.nodes ? readThresholdSet(scoped.nodes) : legacy,
        pods: scoped.pods ? readThresholdSet(scoped.pods) : legacy,
      }
      if (typeof stored.alertSoundsEnabled === 'boolean') {
        this.alertSoundsEnabled = stored.alertSoundsEnabled
      }
      if (typeof stored.desktopNotificationsEnabled === 'boolean') {
        this.desktopNotificationsEnabled = stored.desktopNotificationsEnabled
      }
      // The one sound earlier builds stored becomes the warning sound, before
      // the per-severity map is read over it.
      if (isAlertSound(stored.alertSound)) {
        this.alertSounds = { ...this.alertSounds, warning: stored.alertSound }
      }
      // Each half is validated on its own against the catalogue: a sound
      // dropped in a later build must fall back to one that exists rather
      // than leaving that severity mute without saying so.
      if (stored.alertSounds && typeof stored.alertSounds === 'object') {
        const restored = { ...this.alertSounds }
        for (const severity of ['warning', 'critical'] as const) {
          if (isAlertSound(stored.alertSounds[severity])) {
            restored[severity] = stored.alertSounds[severity]
          }
        }
        this.alertSounds = restored
      }
      if (stored.columns && typeof stored.columns === 'object') this.columns = stored.columns
      // Validated entry by entry — see normaliseSpecs — because these are
      // read straight into column definitions, and a malformed one would
      // otherwise put a column on screen that nothing can read a value for.
      if (stored.customColumns && typeof stored.customColumns === 'object') {
        const cleaned: Record<string, CustomColumnSpec[]> = {}
        for (const [kindId, specs] of Object.entries(stored.customColumns)) {
          const valid = normaliseSpecs(specs)
          if (valid.length > 0) cleaned[kindId] = valid
        }
        this.customColumns = cleaned
      }
    } catch {
      // Corrupt or unavailable storage must not stop the app starting. The
      // defaults are perfectly usable, and the next save repairs the entry.
    }
  }

  #save(): void {
    try {
      const payload: PersistedShape = {
        themePreference: this.themePreference,
        pageSize: this.pageSize,
        detailWidthFraction: this.detailWidthFraction,
        detailLabelFraction: this.detailLabelFraction,
        refreshIntervalMs: this.refreshIntervalMs,
        autoRefresh: this.autoRefresh,
        navigatorCollapsed: this.navigatorCollapsed,
        navigatorWidth: this.navigatorWidth,
        expandedCategories: this.expandedCategories,
        findingsExpanded: this.findingsExpanded,
        wrapLines: this.wrapLines,
        showManagedFields: this.showManagedFields,
        namespaceByCluster: this.namespaceByCluster,
        pinnedKinds: this.pinnedKinds,
        localPortByRemotePort: this.localPortByRemotePort,
        localPortByPortName: this.localPortByPortName,
        debugImage: this.debugImage,
        nodeShellImage: this.nodeShellImage,
        nodeShellNamespace: this.nodeShellNamespace,
        snoozes: this.#pruneSnoozes(),
        thresholds: this.thresholds,
        podMeasure: this.podMeasure,
        usageWindowMinutes: this.usageWindowMinutes,
        mapOrientation: this.mapOrientation,
        updateChecksEnabled: this.updateChecksEnabled,
        lastUpdateCheck: this.lastUpdateCheck,
        dismissedUpdate: this.dismissedUpdate,
        sections: this.sections,
        alertSoundsEnabled: this.alertSoundsEnabled,
        desktopNotificationsEnabled: this.desktopNotificationsEnabled,
        alertSounds: this.alertSounds,
        columns: this.columns,
        customColumns: this.customColumns,
      }
      localStorage.setItem(STORAGE_KEY, JSON.stringify(payload))
    } catch {
      // Storage full or blocked. The session still works; only persistence
      // across restarts is lost, which is not worth interrupting anyone over.
    }
  }

  /**
   * Follows the OS while the preference is System.
   *
   * The listener is never removed: preferences live for the life of the
   * document, and a desktop app that stopped following the system theme after
   * some teardown would be harder to explain than the listener is to keep.
   */
  #watchSystemTheme(): void {
    if (typeof window === 'undefined' || !window.matchMedia) return

    window.matchMedia(DARK_QUERY).addEventListener('change', (event) => {
      this.#systemTheme = event.matches ? 'dark' : 'light'
      this.#applyTheme()
    })
  }

  /**
   * Points <html> at the resolved theme. Every theme token resolves through
   * this attribute — see the comment header in app.css.
   *
   * The attribute always names a concrete scheme, never "system": the
   * stylesheet should not have to know that following the OS is even an
   * option.
   */
  #applyTheme(): void {
    if (typeof document === 'undefined') return
    document.documentElement.dataset.theme =
      this.themePreference === 'system' ? this.#systemTheme : this.themePreference
  }
}

/**
 * A plain, detached copy of a JSON-safe value.
 *
 * A round trip rather than `structuredClone`, for one reason that matters:
 * the fields being copied are `$state` PROXIES, and an exported document must
 * be a snapshot rather than a live view into the store — a later edit to a
 * column width must not change a document already written. Everything passing
 * through here came from storage or from a document, so it is JSON-safe by
 * construction.
 */
function plainCopy<T>(value: T): T {
  return JSON.parse(JSON.stringify(value)) as T
}

/**
 * Every field of the preferences half, in the order a review lists them.
 *
 * The list `exportable()` writes out by hand has to agree with this one, and
 * `settingsFile.test.ts` asserts that it does — the hand-written version is
 * what makes adding a field deliberate, and this one is what the reader, the
 * merge and the review are driven from.
 */
export const EXPORTED_PREFERENCE_FIELDS = [
  'themePreference',
  'pageSize',
  'refreshIntervalMs',
  'autoRefresh',
  'navigatorCollapsed',
  'navigatorWidth',
  'detailWidthFraction',
  'detailLabelFraction',
  'expandedCategories',
  'findingsExpanded',
  'wrapLines',
  'showManagedFields',
  'pinnedKinds',
  'localPortByRemotePort',
  'localPortByPortName',
  'debugImage',
  'nodeShellImage',
  'nodeShellNamespace',
  'thresholds',
  'podMeasure',
  'usageWindowMinutes',
  'mapOrientation',
  'updateChecksEnabled',
  'sections',
  'alertSoundsEnabled',
  'desktopNotificationsEnabled',
  'alertSounds',
  'columns',
  'customColumns',
] as const satisfies readonly (keyof ExportedPreferences)[]

/** What this build sets when a replacing document does not mention a field. */
export function defaultExportedPreferences(): ExportedPreferences {
  const out: Record<string, unknown> = {}
  for (const field of EXPORTED_PREFERENCE_FIELDS) out[field] = plainCopy(DEFAULTS[field])
  // Assembled key by key from the field list above, so the cast asserts what
  // the loop guarantees: every field of the shape, and only those.
  return out as unknown as ExportedPreferences
}

// --- Reading a document's preferences half ----------------------------------

/** A value this build refuses, reported rather than silently coerced. */
const REJECT = undefined

function asBoolean(raw: unknown): boolean | undefined {
  return typeof raw === 'boolean' ? raw : REJECT
}

function asNumberIn(min: number, max: number): (raw: unknown) => number | undefined {
  return (raw) =>
    typeof raw === 'number' && Number.isFinite(raw) && raw >= min && raw <= max ? raw : REJECT
}

function asNonEmptyString(raw: unknown): string | undefined {
  return typeof raw === 'string' && raw.trim() !== '' ? raw : REJECT
}

function asOneOf<T extends string | number>(allowed: readonly T[]): (raw: unknown) => T | undefined {
  return (raw) => (allowed.includes(raw as T) ? (raw as T) : REJECT)
}

function asStringArray(raw: unknown): string[] | undefined {
  if (!Array.isArray(raw)) return REJECT
  return raw.filter((entry): entry is string => typeof entry === 'string')
}

/**
 * A keyed map whose values each pass `read`.
 *
 * An entry that fails is DROPPED rather than failing the whole map: one
 * hand-edited column width must not cost somebody every other column they had
 * arranged. The map itself being the wrong type is a refusal, because that is
 * the field, not one row of it.
 */
function asRecordOf<V>(read: (raw: unknown) => V | undefined): (raw: unknown) => Record<string, V> | undefined {
  return (raw) => {
    if (!raw || typeof raw !== 'object' || Array.isArray(raw)) return REJECT
    const out: Record<string, V> = {}
    for (const [key, value] of Object.entries(raw as Record<string, unknown>)) {
      const read1 = read(value)
      if (read1 !== undefined) out[key] = read1
    }
    return out
  }
}

/** One column's stored overrides, keeping only the two fields that exist. */
function asColumnPreference(raw: unknown): ColumnPreference | undefined {
  if (!raw || typeof raw !== 'object' || Array.isArray(raw)) return REJECT
  const stored = raw as Partial<ColumnPreference>
  const out: ColumnPreference = {}
  if (typeof stored.width === 'number' && Number.isFinite(stored.width)) {
    out.width = Math.round(stored.width)
  }
  if (typeof stored.hidden === 'boolean') out.hidden = stored.hidden
  return out
}

/** The three surfaces, each repaired the way storage's own read repairs them. */
function asThresholds(raw: unknown): Record<ThresholdScope, ThresholdSet> | undefined {
  if (!raw || typeof raw !== 'object' || Array.isArray(raw)) return REJECT
  const scoped = raw as Partial<Record<ThresholdScope, unknown>>
  return {
    overview: readThresholdSet(scoped.overview),
    nodes: readThresholdSet(scoped.nodes),
    pods: readThresholdSet(scoped.pods),
  }
}

/** Per-severity motifs, each checked against the catalogue that exists here. */
function asAlertSounds(raw: unknown): Record<AlertSeverity, string> | undefined {
  if (!raw || typeof raw !== 'object' || Array.isArray(raw)) return REJECT
  const stored = raw as Partial<Record<AlertSeverity, unknown>>
  const out = { ...DEFAULT_ALERT_SOUNDS }
  for (const severity of ALERT_SEVERITIES) {
    if (isAlertSound(stored[severity])) out[severity] = stored[severity]
  }
  return out
}

/**
 * How each field of the preferences half is read out of a document.
 *
 * The same rules `#load` applies to this machine's own storage, for the same
 * reason: a document from a colleague deserves no more trust and no less than
 * the last version of PodSteer to run here. A refusal is counted, not fatal.
 */
const PREFERENCE_READERS: {
  [K in keyof ExportedPreferences]: (raw: unknown) => ExportedPreferences[K] | undefined
} = {
  themePreference: asOneOf(THEME_PREFERENCES),
  pageSize: asOneOf(PAGE_SIZES),
  refreshIntervalMs: asNumberIn(0, Number.MAX_SAFE_INTEGER),
  autoRefresh: asBoolean,
  navigatorCollapsed: asBoolean,
  navigatorWidth: asNumberIn(180, 400),
  detailWidthFraction: asNumberIn(0.05, 0.95),
  detailLabelFraction: asNumberIn(0.05, 0.9),
  expandedCategories: asStringArray,
  findingsExpanded: asBoolean,
  wrapLines: asBoolean,
  showManagedFields: asBoolean,
  pinnedKinds: asRecordOf(asStringArray),
  localPortByRemotePort: asRecordOf(asNumberIn(1, 65535)),
  localPortByPortName: asRecordOf(asNumberIn(1, 65535)),
  debugImage: asNonEmptyString,
  nodeShellImage: asNonEmptyString,
  nodeShellNamespace: asNonEmptyString,
  thresholds: asThresholds,
  podMeasure: asOneOf(['requests', 'limits'] as const),
  usageWindowMinutes: asOneOf(USAGE_WINDOWS),
  mapOrientation: asOneOf(['horizontal', 'vertical'] as const),
  updateChecksEnabled: asBoolean,
  sections: asRecordOf(asBoolean),
  alertSoundsEnabled: asBoolean,
  desktopNotificationsEnabled: asBoolean,
  alertSounds: asAlertSounds,
  columns: asRecordOf(asRecordOf(asColumnPreference)),
  // Validated spec by spec by the same function the column picker and storage
  // both go through, so a column that would render as a permanent dash cannot
  // arrive by import when it cannot arrive any other way.
  customColumns: (raw) => {
    const map = asRecordOf((specs: unknown) => {
      const valid = normaliseSpecs(specs)
      return valid.length > 0 ? valid : REJECT
    })(raw)
    return map as Record<string, CustomColumnSpec[]> | undefined
  },
}

/** Reads the preferences half, reporting what it did not know or accept. */
export function readExportedPreferences(raw: unknown): FieldRead<ExportedPreferences> {
  const value: Record<string, unknown> = {}
  const unknown: string[] = []
  const invalid: string[] = []

  if (!raw || typeof raw !== 'object' || Array.isArray(raw)) {
    return { value: {}, unknown, invalid }
  }

  const known = new Set<string>(EXPORTED_PREFERENCE_FIELDS)
  for (const [field, entry] of Object.entries(raw as Record<string, unknown>)) {
    if (!known.has(field)) {
      unknown.push(field)
      continue
    }
    const read = (PREFERENCE_READERS[field as keyof ExportedPreferences] as (raw: unknown) => unknown)(
      entry,
    )
    if (read === undefined) invalid.push(field)
    else value[field] = read
  }

  return { value: value as Partial<ExportedPreferences>, unknown, invalid }
}

// --- Merging and reviewing --------------------------------------------------

/** Fields that are keyed maps, merged key by key rather than wholesale. */
const RECORD_FIELDS = new Set<keyof ExportedPreferences>([
  'pinnedKinds',
  'localPortByRemotePort',
  'localPortByPortName',
  'thresholds',
  'sections',
  'alertSounds',
  'columns',
  'customColumns',
])

/**
 * Combines the current preferences with a document's, under an import mode.
 *
 * Maps merge key by key so importing a colleague's Deployment columns keeps
 * the Pod columns already arranged here; scalars take the file's value where
 * it has one. `expandedCategories` is the one array, and it unions rather
 * than replacing under merge: it is a set of what is OPEN, and combining two
 * people's open sections is what merging them means.
 */
export function mergeExportedPreferences(
  current: ExportedPreferences,
  incoming: Partial<ExportedPreferences>,
  mode: ImportMode,
): ExportedPreferences {
  const defaults = defaultExportedPreferences()
  const out: Record<string, unknown> = {}

  for (const field of EXPORTED_PREFERENCE_FIELDS) {
    if (field === 'expandedCategories') {
      const arriving = incoming.expandedCategories
      if (!arriving) {
        out[field] = mode === 'replace' ? [...defaults.expandedCategories] : [...current.expandedCategories]
      } else {
        out[field] =
          mode === 'replace' ? [...arriving] : [...new Set([...current.expandedCategories, ...arriving])]
      }
      continue
    }

    if (RECORD_FIELDS.has(field)) {
      out[field] = mergeRecord(
        current[field] as Record<string, unknown>,
        incoming[field] as Record<string, unknown> | undefined,
        mode,
        defaults[field] as Record<string, unknown>,
      )
      continue
    }

    out[field] = mergeValue<unknown>(current[field], incoming[field], mode, defaults[field])
  }

  return plainCopy(out as unknown as ExportedPreferences)
}

/** How each field is named and counted in a review. */
const PREFERENCE_LABELS: Record<keyof ExportedPreferences, { label: string; unit?: string }> = {
  themePreference: { label: 'Theme' },
  pageSize: { label: 'Rows per page' },
  refreshIntervalMs: { label: 'Refresh interval (ms)' },
  autoRefresh: { label: 'Auto refresh' },
  navigatorCollapsed: { label: 'Navigator collapsed' },
  navigatorWidth: { label: 'Navigator width (px)' },
  detailWidthFraction: { label: 'Detail panel width' },
  detailLabelFraction: { label: 'Detail label column' },
  expandedCategories: { label: 'Expanded navigator categories', unit: 'categories' },
  findingsExpanded: { label: 'Findings expanded' },
  wrapLines: { label: 'Wrap long lines' },
  showManagedFields: { label: 'Show managed fields' },
  pinnedKinds: { label: 'Pinned kinds', unit: 'clusters' },
  localPortByRemotePort: { label: 'Remembered ports, by remote port', unit: 'ports' },
  localPortByPortName: { label: 'Remembered ports, by port name', unit: 'ports' },
  debugImage: { label: 'Debug container image' },
  nodeShellImage: { label: 'Node shell image' },
  nodeShellNamespace: { label: 'Node shell namespace' },
  thresholds: { label: 'Threshold lines' },
  podMeasure: { label: 'Pod bars measure against' },
  usageWindowMinutes: { label: 'Retained usage (minutes)' },
  mapOrientation: { label: 'Dependency map orientation' },
  updateChecksEnabled: { label: 'Check for updates' },
  sections: { label: 'Detail sections opened or closed', unit: 'sections' },
  alertSoundsEnabled: { label: 'Sound on a new finding' },
  desktopNotificationsEnabled: { label: 'Desktop notification on a new critical finding' },
  alertSounds: { label: 'Sound per severity' },
  columns: { label: 'Saved column layouts', unit: 'kinds' },
  customColumns: { label: 'Custom columns', unit: 'kinds' },
}

/** The three surfaces spelled out, because the numbers ARE the decision. */
function describeThresholds(value: unknown): string {
  const scoped = value as Record<ThresholdScope, ThresholdSet>
  return THRESHOLD_SCOPES.map((scope) => {
    const set = scoped[scope]
    const warn = set.warnEnabled ? String(set.warn) : 'off'
    const critical = set.criticalEnabled ? String(set.critical) : 'off'
    return `${scope} ${warn}/${critical}`
  }).join(' · ')
}

/** Which motif each severity plays, spelled out for the same reason. */
function describeAlertSounds(value: unknown): string {
  const sounds = value as Record<AlertSeverity, string>
  return ALERT_SEVERITIES.map((severity) => `${severity} ${sounds[severity]}`).join(' · ')
}

/** One review line per preference field, changed or not. */
export function describePreferenceChanges(
  current: ExportedPreferences,
  next: ExportedPreferences,
): ImportEntry[] {
  return EXPORTED_PREFERENCE_FIELDS.map((field) => {
    const meta = PREFERENCE_LABELS[field]
    const render =
      field === 'thresholds'
        ? describeThresholds
        : field === 'alertSounds'
          ? describeAlertSounds
          : (value: unknown) => describeValue(value, meta.unit)
    return entryFor('Preferences', meta.label, current[field], next[field], render)
  })
}

/** The application-wide preferences, shared by every tab. */
export const preferences = new Preferences()
