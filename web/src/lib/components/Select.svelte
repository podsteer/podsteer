<!--
  MD3 outlined select.

  Built on a native <select> rather than a custom listbox: it inherits keyboard
  navigation, type-ahead and the platform's own popup, which for a namespace
  list of several hundred entries is both faster and more familiar than
  anything reimplemented here.
-->
<script lang="ts">
  import { ChevronDown } from '@lucide/svelte'

  interface Option {
    value: string
    label: string
    /** Optional trailing detail, e.g. a count. */
    hint?: string
  }

  interface Props {
    /** Floating label describing the field. */
    label: string
    value: string
    options: Option[]
    disabled?: boolean
    /** Called with the newly chosen value. */
    onchange?: (value: string) => void
    class?: string
  }

  let { label, value, options, disabled = false, onchange, class: className = '' }: Props = $props()

  function handleChange(event: Event & { currentTarget: HTMLSelectElement }) {
    onchange?.(event.currentTarget.value)
  }
</script>

<label class="group relative block {className}">
  <span
    class="absolute -top-2 left-2.5 z-10 rounded bg-surface-container-low px-1 text-[10px]
           font-medium uppercase tracking-wide text-on-surface-variant/70
           transition-colors duration-100 group-focus-within:text-primary"
  >
    {label}
  </span>

  <select
    {value}
    {disabled}
    onchange={handleChange}
    class="no-drag h-10 w-full appearance-none rounded-lg border border-outline-variant/60 bg-transparent
           py-1.5 pr-8 pl-3 text-body-medium text-on-surface
           transition-all duration-150 ease-standard
           hover:border-outline focus:border-primary focus:shadow-sm focus:outline-none
           disabled:pointer-events-none disabled:opacity-38"
  >
    {#each options as option (option.value)}
      <option value={option.value} class="bg-surface-container text-on-surface">
        {option.label}{option.hint ? ` — ${option.hint}` : ''}
      </option>
    {/each}
  </select>

  <ChevronDown
    class="pointer-events-none absolute top-1/2 right-2.5 size-4 -translate-y-1/2
           text-on-surface-variant/60 transition-colors duration-100 group-focus-within:text-primary"
    strokeWidth={2}
  />
</label>
