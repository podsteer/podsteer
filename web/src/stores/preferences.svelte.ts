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
  DEFAULT_ALERT_SOUNDS,
  alertPlayer,
  isAlertSound,
  type AlertSeverity,
} from './alerts.svelte'

/** Page sizes offered. 25 is the default; 100 is the ceiling. */
export const PAGE_SIZES = [10, 25, 50, 100] as const

/** How many rows a page holds. */
export type PageSize = (typeof PAGE_SIZES)[number]

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

const STORAGE_KEY = 'podsteer.preferences.v1'

/** The range a threshold may sensibly take, in whole per cent. */
export const THRESHOLD_RANGE = { min: 50, max: 99 } as const

function clampThreshold(value: number): number {
  if (!Number.isFinite(value)) return 80
  return Math.min(THRESHOLD_RANGE.max, Math.max(THRESHOLD_RANGE.min, Math.round(value)))
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
  /** clusterId -> snoozeKey() -> epoch milliseconds when the snooze lapses. */
  snoozes: Record<string, Record<string, number>>
  /** Where a utilisation bar turns amber, and where it turns red. */
  warnThreshold: number
  criticalThreshold: number
  /** Whether each line is drawn at all. */
  warnEnabled: boolean
  criticalEnabled: boolean
  /** Whether a newly raised finding makes a sound at all. */
  alertSoundsEnabled: boolean
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
  expandedCategories: [],
  findingsExpanded: false,
  wrapLines: true,
  showManagedFields: false,
  namespaceByCluster: {},
  snoozes: {},
  // 80 and 90 because they are where Kubernetes itself starts to behave
  // differently — the kubelet's default eviction threshold leaves 10% free —
  // and because they are the numbers most operators already run alerts on.
  warnThreshold: 80,
  criticalThreshold: 90,
  // Both on. An operator who only wants to hear about the serious case can
  // turn the first one off, but a default that says nothing until something
  // is already critical is a default that arrives too late.
  warnEnabled: true,
  criticalEnabled: true,
  // Off. An application that starts making noise nobody asked for is one
  // people mute at the operating system, taking the alarm they DID want with
  // it. Whoever wants this turns it on, and hears the sound as they choose it.
  alertSoundsEnabled: false,
  alertSounds: DEFAULT_ALERT_SOUNDS,
  columns: {},
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
   * Where a utilisation bar stops being comfortable.
   *
   * One pair for the whole application. A cluster does not have one meaning
   * of "nearly full" per card, and an operator who moves the line because
   * their own nodes run hot means it everywhere.
   */
  warnThreshold = $state<number>(DEFAULTS.warnThreshold)
  criticalThreshold = $state<number>(DEFAULTS.criticalThreshold)

  /**
   * Whether each line is drawn.
   *
   * Turning one off does not move the other: somebody who wants only the
   * critical line gets blue all the way to it, and their choice of where it
   * sits is untouched by the one they are not using.
   */
  warnEnabled = $state<boolean>(DEFAULTS.warnEnabled)
  criticalEnabled = $state<boolean>(DEFAULTS.criticalEnabled)

  /** Whether a newly raised warning or critical finding makes a sound. */
  alertSoundsEnabled = $state<boolean>(DEFAULTS.alertSoundsEnabled)

  /** Which motif each severity plays, by id, or SILENT for none. */
  alertSounds = $state<Record<AlertSeverity, string>>({ ...DEFAULTS.alertSounds })

  /** kindId -> columnId -> preference. */
  columns = $state<Record<string, Record<string, ColumnPreference>>>({})

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
    this.navigatorWidth = Math.max(180, Math.min(400, Math.round(width)))
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

  // --- Thresholds -----------------------------------------------------------

  /**
   * Moves the amber line, pushing the red one ahead of it if it has to.
   *
   * The two cannot cross: a warning that fires after the critical would
   * colour nothing, and silently accepting that is worse than adjusting the
   * other end where the operator can see it happen.
   */
  setWarnThreshold = (value: number): void => {
    this.warnThreshold = clampThreshold(value)
    if (this.criticalThreshold <= this.warnThreshold) {
      this.criticalThreshold = clampThreshold(this.warnThreshold + 5)
    }
    this.#save()
  }

  setWarnEnabled = (enabled: boolean): void => {
    this.warnEnabled = enabled
    this.#save()
  }

  setCriticalEnabled = (enabled: boolean): void => {
    this.criticalEnabled = enabled
    this.#save()
  }

  /** Moves the red line, pulling the amber one back if it has to. */
  setCriticalThreshold = (value: number): void => {
    this.criticalThreshold = clampThreshold(value)
    if (this.warnThreshold >= this.criticalThreshold) {
      this.warnThreshold = clampThreshold(this.criticalThreshold - 5)
    }
    this.#save()
  }

  // --- Alert sounds ---------------------------------------------------------

  setAlertSoundsEnabled = (enabled: boolean): void => {
    this.alertSoundsEnabled = enabled
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
      if (Array.isArray(stored.expandedCategories)) {
        this.expandedCategories = stored.expandedCategories.filter(
          (entry): entry is string => typeof entry === 'string',
        )
      }
      if (stored.namespaceByCluster && typeof stored.namespaceByCluster === 'object') {
        this.namespaceByCluster = stored.namespaceByCluster
      }
      if (stored.snoozes && typeof stored.snoozes === 'object') {
        this.snoozes = stored.snoozes
      }
      // Validated and re-ordered on the way in, because storage outlives the
      // code that wrote it and a pair read back crossed would colour nothing.
      if (typeof stored.warnThreshold === 'number') {
        this.warnThreshold = clampThreshold(stored.warnThreshold)
      }
      if (typeof stored.criticalThreshold === 'number') {
        this.criticalThreshold = clampThreshold(stored.criticalThreshold)
      }
      if (this.criticalThreshold <= this.warnThreshold) {
        this.criticalThreshold = clampThreshold(this.warnThreshold + 5)
      }
      if (typeof stored.warnEnabled === 'boolean') this.warnEnabled = stored.warnEnabled
      if (typeof stored.criticalEnabled === 'boolean') {
        this.criticalEnabled = stored.criticalEnabled
      }
      if (typeof stored.alertSoundsEnabled === 'boolean') {
        this.alertSoundsEnabled = stored.alertSoundsEnabled
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
        refreshIntervalMs: this.refreshIntervalMs,
        autoRefresh: this.autoRefresh,
        navigatorCollapsed: this.navigatorCollapsed,
        navigatorWidth: this.navigatorWidth,
        expandedCategories: this.expandedCategories,
        findingsExpanded: this.findingsExpanded,
        wrapLines: this.wrapLines,
        showManagedFields: this.showManagedFields,
        namespaceByCluster: this.namespaceByCluster,
        snoozes: this.#pruneSnoozes(),
        warnThreshold: this.warnThreshold,
        criticalThreshold: this.criticalThreshold,
        warnEnabled: this.warnEnabled,
        criticalEnabled: this.criticalEnabled,
        alertSoundsEnabled: this.alertSoundsEnabled,
        alertSounds: this.alertSounds,
        columns: this.columns,
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

/** The application-wide preferences, shared by every tab. */
export const preferences = new Preferences()
