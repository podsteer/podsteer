<!--
  MD3 outlined select.

  Built on a native <select> rather than a custom listbox: it inherits keyboard
  navigation, type-ahead and the platform's own popup, which for a namespace
  list of several hundred entries is both faster and more familiar than
  anything reimplemented here.
-->
<script lang="ts">
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

<label class="relative block {className}">
  <span
    class="absolute -top-2 left-3 z-10 bg-surface px-1 text-body-small text-on-surface-variant"
  >
    {label}
  </span>

  <select
    {value}
    {disabled}
    onchange={handleChange}
    class="no-drag h-14 w-full appearance-none rounded-xs border border-outline bg-transparent
           py-2 pr-10 pl-4 text-body-large text-on-surface
           transition-colors duration-150 ease-standard
           hover:border-on-surface focus:border-primary focus:outline-none
           disabled:pointer-events-none disabled:opacity-38"
  >
    {#each options as option (option.value)}
      <option value={option.value} class="bg-surface-container text-on-surface">
        {option.label}{option.hint ? ` — ${option.hint}` : ''}
      </option>
    {/each}
  </select>

  <!-- Trailing chevron. Inline SVG keeps the app free of an icon font, which
       would be another asset to embed for a handful of glyphs. -->
  <svg
    class="pointer-events-none absolute top-1/2 right-3 size-5 -translate-y-1/2 text-on-surface-variant"
    viewBox="0 0 24 24"
    fill="none"
    stroke="currentColor"
    stroke-width="2"
    stroke-linecap="round"
    stroke-linejoin="round"
    aria-hidden="true"
  >
    <path d="m6 9 6 6 6-6" />
  </svg>
</label>
