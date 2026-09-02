<!--
  The navigator: the namespace filter, then the cluster's browsable kinds
  grouped into collapsible sections.

  The namespace selector sits at the top of the sidebar rather than in the
  content toolbar because it scopes everything below it. Putting it beside the
  table implied it filtered that one table; here its reach is obvious at a
  glance — the whole tree under it is what it narrows.

  The tree is built entirely from what the backend reports for THIS cluster, so
  a cluster's own operators appear under Custom Resources with no frontend
  change, and a cluster with none simply has no such section.

  Every section starts collapsed. Which ones an operator opens is persisted
  (see preferences.svelte.ts), so a cluster with a dozen categories does not
  greet them with a wall of kinds every time, and whatever they left open
  (say, just Workloads) is exactly what is open next time too.
-->
<script lang="ts">
  import { ALL_NAMESPACES, type ResourceKind } from '$lib/api/client'
  import {
    APPLICATIONS_KIND_ID,
    OVERVIEW_KIND_ID,
    type ClusterSession,
  } from '$stores/session.svelte'
  import { clampNavigatorWidth, preferences } from '$stores/preferences.svelte'
  import { categoryMeta, iconForKind } from '$lib/kindIcons'
  import Select from './Select.svelte'
  import { Blocks, ChevronDown, LayoutDashboard, AlertTriangle } from '@lucide/svelte'

  interface Props {
    session: ClusterSession
  }

  let { session }: Props = $props()

  const namespaceOptions = $derived.by(() => {
    const options = [
      { value: ALL_NAMESPACES, label: 'All namespaces' },
      ...session.namespaces.map((namespace) => ({
        value: namespace.name,
        label: namespace.name,
        hint: namespace.isActive ? undefined : namespace.phase.toLowerCase(),
      })),
    ]

    // KEEP A FILTER THAT NO LONGER MATCHES ANYTHING VISIBLE.
    //
    // The list refreshes now (session.refreshNamespaces), so the namespace
    // being filtered on can disappear from under the selection — deleted
    // while the tab was open, or still named by a preference remembered from
    // before it was. A Select whose value is absent from its options falls
    // back to the placeholder, so the trigger would read as EMPTY while the
    // whole tree below it was still scoped to that namespace: an operator
    // looking at nothing, with nothing on screen saying why.
    //
    // The same case covers RBAC that permits listing objects in one namespace
    // but not listing namespaces at all, where this is the only entry there
    // will ever be.
    const selected = session.namespace
    if (selected !== ALL_NAMESPACES && !options.some((option) => option.value === selected)) {
      options.push({ value: selected, label: selected, hint: 'not found' })
    }

    return options
  })

  const onOverview = $derived(session.selectedKindId === OVERVIEW_KIND_ID)
  const onApplications = $derived(session.selectedKindId === APPLICATIONS_KIND_ID)

  /**
   * Kinds grouped by category and then by who publishes them.
   *
   * A CLUSTER RUNNING SEVERAL OPERATORS HAS SIXTY CUSTOM RESOURCES, and they
   * were one flat alphabetical list — an Argo CD Application next to a
   * Prometheus Alertmanager next to a cert-manager Certificate, with nothing
   * saying which controller any of them belonged to. Grouped by API group
   * they arrive already sorted into the things that installed them.
   *
   * The subgroup is the RAW API GROUP. A curated table of project names was
   * tried and removed: it covered five of the twenty-five groups on a real
   * cluster, which left the navigator speaking two vocabularies at once with
   * no way to tell which kind of thing a heading was. The group is the only
   * label that can never be wrong and never needs a maintainer, and it is
   * what `kubectl api-resources` prints.
   */
  const sections = $derived.by(() => {
    const grouped = new Map<string, ResourceKind[]>()
    for (const kind of session.kinds) {
      const bucket = grouped.get(kind.category)
      if (bucket) bucket.push(kind)
      else grouped.set(kind.category, [kind])
    }

    return [...grouped.entries()].map(([category, kinds]) => ({
      category,
      kinds,
      groups: subgroupsOf(kinds),
    }))
  })

  /**
   * A category's kinds split by publisher, or null when they all share one.
   *
   * Null rather than a single group holding everything: a heading over the
   * whole list adds a line and says nothing, and every built-in category is
   * exactly that case.
   */
  function subgroupsOf(kinds: ResourceKind[]): { name: string; kinds: ResourceKind[] }[] | null {
    if (new Set(kinds.map((kind) => kind.subcategory)).size <= 1) return null

    const grouped = new Map<string, ResourceKind[]>()
    for (const kind of kinds) {
      const bucket = grouped.get(kind.subcategory)
      if (bucket) bucket.push(kind)
      else grouped.set(kind.subcategory, [kind])
    }
    return [...grouped.entries()].map(([name, entries]) => ({ name, kinds: entries }))
  }

  /**
   * The kinds in a category that belong to no group, shown above the groups.
   *
   * One entry today: the CustomResourceDefinitions themselves, which are
   * Kubernetes' own and are the index to everything below them.
   */
  function ungroupedIn(section: { groups: { name: string; kinds: ResourceKind[] }[] | null }) {
    return section.groups?.find((group) => group.name === '')?.kinds ?? []
  }

  // --- Resize logic ---
  let resizing = $state(false)
  /**
   * The width during a drag, before it becomes a stored preference.
   *
   * Writing it to preferences on every pointermove serialised the entire
   * preferences payload into a synchronous localStorage.setItem sixty-plus
   * times a second. The gesture only has one outcome worth persisting: where
   * it ended.
   */
  let draggedWidth = $state<number | null>(null)
  let resizeStartX = 0
  let resizeStartWidth = 0

  function startResize(event: PointerEvent): void {
    event.preventDefault()
    resizing = true
    resizeStartX = event.clientX
    resizeStartWidth = preferences.navigatorWidth
    ;(event.currentTarget as HTMLElement).setPointerCapture(event.pointerId)
  }

  function onResizeMove(event: PointerEvent): void {
    if (!resizing) return
    draggedWidth = clampNavigatorWidth(resizeStartWidth + (event.clientX - resizeStartX))
  }

  function endResize(): void {
    if (draggedWidth !== null) preferences.setNavigatorWidth(draggedWidth)
    draggedWidth = null
    resizing = false
  }

  /**
   * Arrow keys resize the navigator, which is the only way a keyboard can.
   *
   * It was pointer-only, so an operator working from the keyboard could not
   * narrow a sidebar that was taking half their window. Enter restores the
   * default, matching what a double-click does.
   */
  function onResizeKeydown(event: KeyboardEvent): void {
    const STEP = 16
    let width: number
    switch (event.key) {
      case 'ArrowLeft':
        width = preferences.navigatorWidth - STEP
        break
      case 'ArrowRight':
        width = preferences.navigatorWidth + STEP
        break
      case 'Home':
        width = 0
        break
      case 'End':
        width = Number.MAX_SAFE_INTEGER
        break
      case 'Enter':
        width = 240
        break
      default:
        return
    }
    event.preventDefault()
    preferences.setNavigatorWidth(width)
  }
</script>

<nav
  class="relative flex shrink-0 flex-col border-r border-outline-variant/60 bg-surface-container-low"
  style="width: {draggedWidth ?? preferences.navigatorWidth}px"
  aria-label="Cluster resources"
>
  <!-- Namespace selector area: same height and border as the main toolbar,
       so the two form one continuous line.

       The title tests `selectedKind` as well as `isNamespaced`: the overview is
       deliberately not a catalog entry, so `selectedKind` is undefined there
       AND `isNamespaced` is false — testing only the latter renders
       "undefined are cluster-scoped" on the view every session opens with. -->
  <div
    class="flex h-14 shrink-0 items-center border-b border-outline-variant/60 px-3"
    title={session.selectedKind && !session.isNamespaced
      ? `${session.selectedKind.title} are cluster-scoped`
      : undefined}
  >
    <Select
      label="Namespace"
      value={session.namespace}
      options={namespaceOptions}
      onchange={(value) => session.selectNamespace(value)}
      onopen={() => void session.refreshNamespaces()}
      class="w-full"
    />
  </div>

  <!-- Resource tree -->
  <div class="min-h-0 flex-1 overflow-y-auto py-1.5">
    <!-- The dashboard is pinned above the categories rather than filed inside
         one: it is not a kind, and it is where an operator starts. The badge
         carries the assessment's own verdict, so the sidebar answers "is
         anything wrong" from any view. -->
    <div class="px-1.5 pb-1">
      <button
        type="button"
        onclick={() => session.selectKind(OVERVIEW_KIND_ID)}
        aria-current={onOverview ? 'page' : undefined}
        class="group/item flex w-full items-center gap-2 rounded-sm px-2 py-[7px] text-left
               transition-all duration-100 ease-standard
               {onOverview
                 ? 'bg-primary/12 text-primary'
                 : 'text-on-surface-variant hover:bg-surface-container hover:text-on-surface'}"
      >
        <LayoutDashboard
          class="size-4 shrink-0 transition-colors duration-100
                 {onOverview ? 'text-primary' : 'text-on-surface-variant/60 group-hover/item:text-on-surface-variant'}"
          strokeWidth={1.8}
        />
        <span class="flex-1 truncate text-body-medium font-medium">Overview</span>
        {#if session.issueCount > 0}
          <span
            class="flex items-center gap-1 rounded-full px-1.5 py-0.5 text-label-small tabular-nums
                   {session.hasCriticalIssues
                     ? 'bg-error-container text-on-error-container'
                     : 'bg-warning-container text-on-warning-container'}"
            title="{session.issueCount} findings need attention"
          >
            <AlertTriangle class="size-3" strokeWidth={2.2} />
            {session.issueCount}
          </span>
        {/if}
      </button>
    </div>

    {#if session.kinds.length === 0}
      <div class="flex flex-col items-center gap-2 px-4 py-8">
        <div class="size-8 animate-pulse rounded-full bg-surface-container-high"></div>
        <p class="text-body-small text-on-surface-variant/70">Loading resources…</p>
      </div>
    {/if}

    <!-- Applications, pinned beside the dashboard for the same reason: there
         is no object called an application. It is a grouping of what is
         there by the labels Kubernetes recommends they carry, so it belongs
         with the other view that is not a kind rather than filed among the
         kinds. -->
    <div class="px-1.5 pb-1">
      <button
        type="button"
        onclick={() => session.selectKind(APPLICATIONS_KIND_ID)}
        aria-current={onApplications ? 'page' : undefined}
        class="group/item flex w-full items-center gap-2 rounded-sm px-2 py-[7px] text-left
               transition-all duration-100 ease-standard
               {onApplications
                 ? 'bg-primary/12 text-primary'
                 : 'text-on-surface-variant hover:bg-surface-container hover:text-on-surface'}"
      >
        <!-- Not the stacked boxes: those mark the Workloads category two
             entries below, and an application is not a workload — it is
             several of them agreeing about a label. Two entries wearing one
             glyph is a navigator asking to be misread. -->
        <Blocks
          class="size-4 shrink-0 transition-colors duration-100
                 {onApplications ? 'text-primary' : 'text-on-surface-variant/60 group-hover/item:text-on-surface-variant'}"
          strokeWidth={1.8}
        />
        <span class="flex-1 truncate text-body-medium">Applications</span>
      </button>
    </div>


    {#each sections as section (section.category)}
      {@const open = preferences.isCategoryExpanded(section.category)}
      {@const CategoryIcon = categoryMeta(section.category).icon}

      <div class="px-1.5 py-0.5">
        <!-- Section header -->
        <button
          type="button"
          onclick={() => preferences.toggleCategory(section.category)}
          aria-expanded={open}
          class="state-layer group flex w-full items-center gap-2 rounded-sm px-2 py-1.5
                 text-on-surface-variant transition-colors duration-100 hover:bg-surface-container"
        >
          <ChevronDown
            class="size-3.5 shrink-0 text-on-surface-variant/60 transition-transform duration-150 ease-standard
                   {open ? '' : '-rotate-90'}"
            strokeWidth={2.5}
          />
          <CategoryIcon class="size-4 shrink-0 text-on-surface-variant/70" strokeWidth={1.8} />
          <span class="flex-1 truncate text-left text-body-small font-semibold uppercase tracking-wider">
            {section.category}
          </span>
          <span
            class="rounded-full bg-surface-container-high px-1.5 py-0.5 text-label-small
                   tabular-nums text-on-surface-variant/70"
          >
            {section.kinds.length}
          </span>
        </button>

        <!-- Kind items. The leading spacer plus this wrapper's own left
             padding is sized so the icon and label columns line up exactly
             with the section header's — a child row that starts even a few
             pixels off from its parent's icon and text reads as broken, not
             as "indented". -->
        {#if open}
          <div class="mt-0.5 border-l border-outline-variant/30 pl-2">
            {#if section.groups}
              <!--
                One collapsible heading per API group. Twenty-five of them on
                a cluster running Elastic, cert-manager, Argo and KEDA — a
                flat list of that inside an already-open section is a wall,
                and each one folds so somebody can keep open only the
                operators they are working with.

                Folded state goes through the same store the categories use,
                keyed "Custom Resources/<group>" — a namespaced key in a
                mechanism that was already there, rather than a second thing
                to persist and migrate.
              -->
              <ul>
                {#each ungroupedIn(section) as kind (kind.id)}
                  {@const selected = kind.id === session.selectedKindId}
                  {@const KindIcon = iconForKind(kind)}
                  <li>
                    <button
                      type="button"
                      onclick={() => session.selectKind(kind.id)}
                      aria-current={selected ? 'page' : undefined}
                      title={kind.group ? `${kind.kind} · ${kind.group}/${kind.version}` : kind.kind}
                      class="group/item flex w-full items-center gap-2 rounded-sm px-2 py-[5px] text-left
                             transition-all duration-100 ease-standard
                             {selected
                               ? 'bg-primary/12 text-primary'
                               : 'text-on-surface-variant hover:bg-surface-container hover:text-on-surface'}"
                    >
                      <span class="w-1.5 shrink-0" aria-hidden="true"></span>
                      <KindIcon
                        class="size-4 shrink-0 transition-colors duration-100
                               {selected ? 'text-primary' : 'text-on-surface-variant/60 group-hover/item:text-on-surface-variant'}"
                        strokeWidth={1.8}
                      />
                      <span class="flex-1 truncate text-body-medium">{kind.title}</span>
                      {#if !kind.namespaced}
                        <span
                          class="rounded bg-surface-container-high px-1 py-px text-label-small uppercase
                                 text-on-surface-variant/50"
                          title="Cluster-scoped"
                        >
                          C
                        </span>
                      {/if}
                    </button>
                  </li>
                {/each}
              </ul>

              {#each section.groups.filter((group) => group.name !== '') as group (group.name)}
                {@const groupKey = `${section.category}/${group.name}`}
                {@const groupOpen = preferences.isCategoryExpanded(groupKey)}
                <button
                  type="button"
                  onclick={() => preferences.toggleCategory(groupKey)}
                  aria-expanded={groupOpen}
                  class="state-layer mt-0.5 flex w-full items-center gap-2 rounded-sm px-2
                         py-[5px] text-left text-on-surface-variant transition-colors duration-100
                         hover:bg-surface-container hover:text-on-surface"
                >
                  <!-- The same leading spacer every kind row carries, so this
                       chevron lands in the column their icons are in. Without
                       it the fold controls sat a whole icon-width to the left
                       of the rows they fold, which reads as two lists rather
                       than as one indented under the other. -->
                  <span class="w-1.5 shrink-0" aria-hidden="true"></span>
                  <ChevronDown
                    class="size-4 shrink-0 text-on-surface-variant/60 transition-transform
                           duration-150 ease-standard {groupOpen ? '' : '-rotate-90'}"
                    strokeWidth={2}
                  />
                  <!-- The kind rows' own size. A group heading naming an API
                       group is read as often as the kinds under it, and set
                       smaller it read as a caption on them. -->
                  <span class="flex-1 truncate text-body-medium">{group.name}</span>
                  <span class="shrink-0 text-body-small tabular-nums text-on-surface-variant/50">
                    {group.kinds.length}
                  </span>
                </button>

                {#if groupOpen}
                  <ul class="ml-1.5 border-l border-outline-variant/20 pl-1.5">
                    {#each group.kinds as kind (kind.id)}
                  {@const selected = kind.id === session.selectedKindId}
                  {@const KindIcon = iconForKind(kind)}
                  <li>
                    <button
                      type="button"
                      onclick={() => session.selectKind(kind.id)}
                      aria-current={selected ? 'page' : undefined}
                      title={kind.group ? `${kind.kind} · ${kind.group}/${kind.version}` : kind.kind}
                      class="group/item flex w-full items-center gap-2 rounded-sm px-2 py-[5px] text-left
                             transition-all duration-100 ease-standard
                             {selected
                               ? 'bg-primary/12 text-primary'
                               : 'text-on-surface-variant hover:bg-surface-container hover:text-on-surface'}"
                    >
                      <span class="w-1.5 shrink-0" aria-hidden="true"></span>
                      <KindIcon
                        class="size-4 shrink-0 transition-colors duration-100
                               {selected ? 'text-primary' : 'text-on-surface-variant/60 group-hover/item:text-on-surface-variant'}"
                        strokeWidth={1.8}
                      />
                      <span class="flex-1 truncate text-body-medium">{kind.title}</span>
                      {#if !kind.namespaced}
                        <span
                          class="rounded bg-surface-container-high px-1 py-px text-label-small uppercase
                                 text-on-surface-variant/50"
                          title="Cluster-scoped"
                        >
                          C
                        </span>
                      {/if}
                    </button>
                  </li>
                    {/each}
                  </ul>
                {/if}
              {/each}
            {:else}
              <ul>
                {#each section.kinds as kind (kind.id)}
                {@const selected = kind.id === session.selectedKindId}
                {@const KindIcon = iconForKind(kind)}
                <li>
                  <button
                    type="button"
                    onclick={() => session.selectKind(kind.id)}
                    aria-current={selected ? 'page' : undefined}
                    title={kind.group ? `${kind.kind} · ${kind.group}/${kind.version}` : kind.kind}
                    class="group/item flex w-full items-center gap-2 rounded-sm px-2 py-[5px] text-left
                           transition-all duration-100 ease-standard
                           {selected
                             ? 'bg-primary/12 text-primary'
                             : 'text-on-surface-variant hover:bg-surface-container hover:text-on-surface'}"
                  >
                    <span class="w-1.5 shrink-0" aria-hidden="true"></span>
                    <KindIcon
                      class="size-4 shrink-0 transition-colors duration-100
                             {selected ? 'text-primary' : 'text-on-surface-variant/60 group-hover/item:text-on-surface-variant'}"
                      strokeWidth={1.8}
                    />
                    <span class="flex-1 truncate text-body-medium">{kind.title}</span>
                    {#if !kind.namespaced}
                      <span
                        class="rounded bg-surface-container-high px-1 py-px text-label-small uppercase
                               text-on-surface-variant/50"
                        title="Cluster-scoped"
                      >
                        C
                      </span>
                    {/if}
                  </button>
                </li>
                {/each}
              </ul>
            {/if}
          </div>
        {/if}
      </div>
    {/each}
  </div>

  <!-- Resize handle -->
  <!--
    svelte-ignore a11y_no_noninteractive_tabindex, a11y_no_noninteractive_element_interactions
  
    Both warnings are false here. ARIA's `separator` is non-interactive ONLY
    when it is not focusable; a focusable one is the window-splitter pattern,
    which is what this is — it carries a value, it has bounds, and the arrow
    keys change it. See ColumnDivider.svelte for the same note in full.
  -->
  <span
    role="separator"
    aria-orientation="vertical"
    aria-label="Resize sidebar"
    aria-valuenow={preferences.navigatorWidth}
    aria-valuemin={180}
    aria-valuemax={400}
    aria-valuetext="{preferences.navigatorWidth} pixels"
    tabindex="0"
    onkeydown={onResizeKeydown}
    class="absolute top-0 -right-1 z-20 h-full w-2 cursor-col-resize
           after:absolute after:top-0 after:left-1/2 after:h-full after:w-px
           after:-translate-x-1/2 after:bg-transparent after:transition-colors after:duration-100
           hover:after:bg-primary/50 {resizing ? 'after:bg-primary after:w-0.5' : ''}"
    onpointerdown={startResize}
    onpointermove={onResizeMove}
    onpointerup={endResize}
    onpointercancel={endResize}
    ondblclick={() => preferences.setNavigatorWidth(240)}
  ></span>
</nav>
