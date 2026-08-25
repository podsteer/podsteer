<!--
  Word wrap, for any pane that shows monospaced text.

  A component rather than three copies of the same ToolbarToggle, because the
  icon, the wording and the preference it reads should be identical everywhere
  it appears — the control means the same thing on a manifest as on a log, and
  three hand-wired instances is three chances for it not to.

  It reads and writes the shared preference directly instead of taking props.
  The setting is global by design (see `wrapLines` in the preferences store),
  so threading it through every host would be ceremony around a value none of
  them own.
-->
<script lang="ts">
  import { TextWrap } from '@lucide/svelte'
  import { preferences } from '$stores/preferences.svelte'
  import ToolbarToggle from './ToolbarToggle.svelte'
</script>

<ToolbarToggle
  icon={TextWrap}
  label="Wrap lines"
  pressed={preferences.wrapLines}
  title={preferences.wrapLines
    ? 'Wrapping long lines — click to let them run off to the right'
    : 'Long lines run off to the right — click to wrap them'}
  onclick={preferences.toggleWrapLines}
/>
