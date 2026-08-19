<!--
  The Hacker News mark — the same "Y" wordmark Y Combinator itself uses, just
  rendered in whatever colour the caller sets rather than HN's orange, since
  every icon in this app is monochrome. See GithubIcon.svelte for why this
  app carries its own brand SVGs rather than sourcing them from lucide.
-->
<script lang="ts">
  interface Props {
    class?: string
  }

  let { class: className = '' }: Props = $props()
</script>

<svg viewBox="0 0 24 24" fill="currentColor" class={className} aria-hidden="true">
  <path
    d="M0 24V0h24v24H0zM6.951 5.896l4.112 7.708v5.064h1.583v-4.972l4.148-7.799h-1.749l-2.457 4.875c-.372.745-.688 1.434-.688 1.434s-.297-.708-.651-1.434L8.831 5.896h-1.88z"
  />
</svg>
