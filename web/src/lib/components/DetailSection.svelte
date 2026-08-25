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
    children: Snippet
  }

  let { title, children }: Props = $props()
</script>

<section class="flex flex-col gap-2.5">
  <h4
    class="border-b border-outline-variant/40 pb-1.5 text-body-medium font-semibold text-on-surface"
  >
    {title}
  </h4>
  {@render children()}
</section>
