/**
 * Durable UI preferences.
 *
 * Everything an operator adjusts about how K8Sense *looks* lives here and
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

/** Page sizes offered. 25 is the default; 100 is the ceiling. */
export const PAGE_SIZES = [10, 25, 50, 100] as const

/** How many rows a page holds. */
export type PageSize = (typeof PAGE_SIZES)[number]

/** The colour schemes K8Sense can render in. */
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

const STORAGE_KEY = 'k8sense.preferences.v1'

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
  /** clusterId -> the namespace filter it was last left on. */
  namespaceByCluster: Record<string, string>
  /** kindId -> columnId -> preference */
  columns: Record<string, Record<string, ColumnPreference>>
}

const DEFAULTS: PersistedShape = {
  // System, so K8Sense matches the desktop it was launched from rather than
  // asserting a preference nobody expressed.
  themePreference: 'system',
  pageSize: 25,
  refreshIntervalMs: 10_000,
  autoRefresh: true,
  navigatorCollapsed: false,
  navigatorWidth: 240,
  expandedCategories: [],
  namespaceByCluster: {},
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

  /** clusterId -> last-selected namespace filter. */
  namespaceByCluster = $state<Record<string, string>>({})

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
   * whoever is looking at K8Sense.
   */
  getClusterNamespace = (clusterId: string): string | undefined => this.namespaceByCluster[clusterId]

  setClusterNamespace = (clusterId: string, namespace: string): void => {
    this.namespaceByCluster = { ...this.namespaceByCluster, [clusterId]: namespace }
    this.#save()
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
        namespaceByCluster: this.namespaceByCluster,
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
