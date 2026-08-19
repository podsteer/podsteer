<!--
  The X (formerly Twitter) mark. See GithubIcon.svelte for why this app
  carries its own brand SVGs rather than sourcing them from lucide.
-->
<script lang="ts">
  interface Props {
    class?: string
  }

  let { class: className = '' }: Props = $props()
</script>

<svg viewBox="0 0 24 24" fill="currentColor" class={className} aria-hidden="true">
  <path
    d="M18.901 1.153h3.68l-8.04 9.19L24 22.846h-7.406l-5.8-7.584-6.638 7.584H.474l8.6-9.83L0 1.154h7.594l5.243 6.932ZM17.61 20.644h2.039L6.486 3.24H4.298Z"
  />
</svg>
