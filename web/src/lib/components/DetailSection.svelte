<!--
  One named section of a detail pane, openable and closable.

  A heading, a rule beneath it, and whatever the caller puts below. Every pane
  in the drawer is built from these, which is the whole point: the panes
  differ in what they know about a kind, and should differ in nothing else. A
  section that decided its own spacing or heading weight would drift from its
  neighbours the first time either was touched.

  COLLAPSING IS WHAT MAKES THE ORDER USEFUL. The pane grew from four sections
  to a dozen, and a fixed order only helps if the sections nobody is reading
  can get out of the way. Each declares its own default — findings and
  containers open, annotations closed — and whatever the operator does to it
  is remembered, so a pane opens the way they last left it rather than the way
  it was designed.

  The rule does the separating, so the content inside needs no card of its own.
  Boxing a paragraph draws a second border around something the heading has
  already delimited, and turns a sentence into an exhibit.
-->
<script lang="ts">
  import type { Snippet } from 'svelte'
  import { preferences } from '$stores/preferences.svelte'
  import { ChevronRight } from '@lucide/svelte'

  interface Props {
    /** Names what the section holds. */
    title: string
    /**
     * Stable id, for remembering whether it is open.
     *
     * Never the title: a title is copy and gets reworded, and a reworded
     * title would silently discard the operator's choice about that section.
     */
    id: string
    /** Whether it starts open, when the operator has expressed no preference. */
    defaultOpen?: boolean
    /**
     * A short summary shown beside the heading.
     *
     * The point of a closed section: "3 containers" or "9 annotations" is
     * often all somebody wanted, and a section that reveals nothing until
     * opened makes them open every one to find out.
     *
     * WRITTEN AS A VALUE, NOT AS A WHISPER. It sits at the RIGHT of the
     * heading, in the body size every value in the panel below it is set in
     * and in the same receding colour — so a figure in a heading and a figure
     * in a row read as the same kind of thing. Tucked against the title it
     * read as an aside about the heading rather than as the section's own
     * figure, and a column of them down the panel lined up against nothing.
     *
     * The heading's own size was too much: a heading and its figure set the
     * same size are two headings, and the eye stops being able to tell which
     * of them names the section.
     */
    hint?: string
    /**
     * The heading level, for the document outline rather than for the look.
     *
     * Every section renders identically whichever this is — the style is the
     * component's, the level is the caller's, because only the caller knows
     * what it is nested inside.
     */
    level?: 'h3' | 'h4'
    /**
     * Called when the section becomes visible, including on first render if
     * it is already open.
     *
     * For a section whose contents cost a request. Fetching in the caller's
     * `$effect` instead would read the cluster for every collapsed section on
     * the panel — on a node list, one cross-namespace query per row nobody
     * expanded.
     */
    onopen?: () => void
    children: Snippet
  }

  let {
    title,
    id,
    defaultOpen = true,
    hint = '',
    level = 'h4',
    onopen,
    children,
  }: Props = $props()

  const open = $derived(preferences.sectionOpen(id, defaultOpen))

  // Fires on mount too, since a section remembered as open is visible without
  // anybody clicking it — and its contents have to arrive all the same.
  $effect(() => {
    if (open) onopen?.()
  })
</script>

<section class="flex flex-col gap-2.5">
  <!--
    A button wrapping the heading rather than beside it, so the whole line is
    the target. A chevron-sized hit area on a row this wide is a control that
    is technically present and practically missed.
  -->
  <svelte:element this={level} class="contents">
    <button
      type="button"
      onclick={() => preferences.setSectionOpen(id, !open)}
      aria-expanded={open}
      class="group flex w-full items-baseline gap-2 border-b border-outline-variant/40 pb-1.5
             text-left text-title-medium font-semibold text-on-surface"
    >
      <ChevronRight
        class="size-4 shrink-0 self-center text-on-surface-variant transition-transform
               duration-150 ease-standard {open ? 'rotate-90' : ''}"
        strokeWidth={2}
      />
      <span class="min-w-0 truncate">{title}</span>
      {#if hint}
        <span
          class="ml-auto shrink-0 pl-3 text-body-medium font-normal text-on-surface-variant/70"
        >
          {hint}
        </span>
      {/if}
    </button>
  </svelte:element>

  {#if open}
    {@render children()}
  {/if}
</section>
