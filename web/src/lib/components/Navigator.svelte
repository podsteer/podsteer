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
  import { OVERVIEW_KIND_ID, type ClusterSession } from '$stores/session.svelte'
  import { preferences } from '$stores/preferences.svelte'
  import { categoryMeta, iconForKind } from '$lib/kindIcons'
  import Select from './Select.svelte'
  import { ChevronDown, LayoutDashboard, AlertTriangle } from '@lucide/svelte'

  interface Props {
    session: ClusterSession
  }

  let { session }: Props = $props()

  const namespaceOptions = $derived([
    { value: ALL_NAMESPACES, label: 'All namespaces' },
    ...session.namespaces.map((namespace) => ({
      value: namespace.name,
      label: namespace.name,
      hint: namespace.isActive ? undefined : namespace.phase.toLowerCase(),
    })),
  ])

  const onOverview = $derived(session.selectedKindId === OVERVIEW_KIND_ID)

  /** Kinds grouped by category, preserving the backend's ordering. */
  const sections = $derived.by(() => {
    const grouped = new Map<string, ResourceKind[]>()
    for (const kind of session.kinds) {
      const bucket = grouped.get(kind.category)
      if (bucket) bucket.push(kind)
      else grouped.set(kind.category, [kind])
    }
    return [...grouped.entries()].map(([category, kinds]) => ({ category, kinds }))
  })

  // --- Resize logic ---
  let resizing = $state(false)
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
    const newWidth = resizeStartWidth + (event.clientX - resizeStartX)
    preferences.setNavigatorWidth(newWidth)
  }

  function endResize(): void {
    resizing = false
  }
</script>

<nav
  class="relative flex shrink-0 flex-col border-r border-outline-variant/60 bg-surface-container-low"
  style="width: {preferences.navigatorWidth}px"
  aria-label="Cluster resources"
>
  <!-- Namespace selector area: same height and border as the main toolbar,
       so the two form one continuous line. -->
  <div
    class="flex h-14 shrink-0 items-center border-b border-outline-variant/60 px-3"
    title={!session.isNamespaced ? `${session.selectedKind?.title} are cluster-scoped` : undefined}
  >
    <Select
      label="Namespace"
      value={session.namespace}
      options={namespaceOptions}
      onchange={(value) => session.selectNamespace(value)}
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
        class="group/item flex w-full items-center gap-2 rounded-md px-2 py-[7px] text-left
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

    {#each sections as section (section.category)}
      {@const open = preferences.isCategoryExpanded(section.category)}
      {@const CategoryIcon = categoryMeta(section.category).icon}

      <div class="px-1.5 py-0.5">
        <!-- Section header -->
        <button
          type="button"
          onclick={() => preferences.toggleCategory(section.category)}
          aria-expanded={open}
          class="state-layer group flex w-full items-center gap-2 rounded-md px-2 py-1.5
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
                    class="group/item flex w-full items-center gap-2 rounded-md px-2 py-[5px] text-left
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
          </div>
        {/if}
      </div>
    {/each}
  </div>

  <!-- Resize handle -->
  <span
    role="separator"
    aria-orientation="vertical"
    aria-label="Resize sidebar"
    tabindex="-1"
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
