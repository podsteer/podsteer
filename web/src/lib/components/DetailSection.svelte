<!--
  One named section of a detail pane.

  A heading, a rule beneath it, and whatever the caller puts below. Every pane
  in the drawer is built from these, which is the whole point: the panes differ
  in what they know about a kind, and should differ in nothing else. A section
  that decided its own spacing or its own heading weight would drift from its
  neighbours the first time either was touched.

  The rule does the separating, so the content inside needs no card of its own.
  Boxing a paragraph draws a second border around something the heading has
  already delimited, and turns a sentence into an exhibit.
-->
<script lang="ts">
  import type { Snippet } from 'svelte'

  interface Props {
    /** Names what the section holds. */
    title: string
    /**
     * The heading level, for the document outline rather than for the look.
     *
     * Every section renders identically whichever this is — the style is the
     * component's, the level is the caller's, because only the caller knows
     * what it is nested inside. A pane whose own title is an h3 needs h4
     * here; one whose sections sit directly under the drawer's h2 needs h3,
     * and hard-coding either would make one of them skip a level for anybody
     * navigating by headings.
     */
    level?: 'h3' | 'h4'
    children: Snippet
  }

  let { title, level = 'h4', children }: Props = $props()
</script>

<section class="flex flex-col gap-2.5">
  <!--
    Sized to match the pane's own title rather than set a step below it.

    These were body-sized and read as captions: the eye went to the values
    and never registered that the pane was divided at all. A heading that has
    to be looked for is not doing the job of a heading, and a detail pane is
    read by scanning for the section you came for.

    The rule is what keeps the outline legible even though the sizes now
    match — it, not a size step, is what separates one section from the next.
  -->
  <svelte:element
    this={level}
    class="border-b border-outline-variant/40 pb-1.5 text-title-medium font-semibold text-on-surface"
  >
    {title}
  </svelte:element>
  {@render children()}
</section>
