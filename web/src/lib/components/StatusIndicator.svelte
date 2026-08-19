<!--
  A tonal status indicator with dot + label. Used in tables and cards.

  Colour is never the only carrier of meaning: the label is always rendered
  alongside, so the status survives a monochrome display and a colour-blind
  reader.
-->
<script lang="ts">
  import type { Tone } from '$lib/format'
  import {
    CheckCircle,
    AlertTriangle,
    XCircle,
    Info,
    Circle,
  } from '@lucide/svelte'

  interface Props {
    /** Semantic tone driving the colour. */
    tone?: Tone
    /** The status text. */
    label: string
    /** Renders the dot only, with the label as an accessible name. */
    compact?: boolean
    /** Animates the dot, for states that are actively changing. */
    pulse?: boolean
    class?: string
  }

  let { tone = 'neutral', label, compact = false, pulse = false, class: className = '' }: Props =
    $props()

  const ICON_MAP = {
    success: CheckCircle,
    warning: AlertTriangle,
    error: XCircle,
    info: Info,
    neutral: Circle,
  }

  const COLOR_CLASSES: Record<Tone, string> = {
    success: 'text-success',
    warning: 'text-warning',
    error: 'text-error',
    info: 'text-primary',
    neutral: 'text-on-surface-variant/60',
  }

  const BG_CLASSES: Record<Tone, string> = {
    success: 'bg-success/10',
    warning: 'bg-warning/10',
    error: 'bg-error/10',
    info: 'bg-primary/10',
    neutral: 'bg-surface-container',
  }

  const StatusIcon = $derived(ICON_MAP[tone])
</script>

{#if compact}
  <span
    class="inline-flex {className}"
    title={label}
    aria-label={label}
  >
    <StatusIcon
      class="size-3.5 {COLOR_CLASSES[tone]} {pulse ? 'animate-pulse' : ''}"
      strokeWidth={2}
    />
  </span>
{:else}
  <span
    class="inline-flex items-center gap-1.5 rounded-full px-2 py-0.5 {BG_CLASSES[tone]} {className}"
  >
    <StatusIcon
      class="size-3 shrink-0 {COLOR_CLASSES[tone]} {pulse ? 'animate-pulse' : ''}"
      strokeWidth={2.2}
    />
    <span class="text-body-medium font-medium {COLOR_CLASSES[tone]}">{label}</span>
  </span>
{/if}
