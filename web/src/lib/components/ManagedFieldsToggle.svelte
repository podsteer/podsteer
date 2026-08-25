<!--
  Show or hide `metadata.managedFields` on a manifest.

  Sits beside the wrap control and works the same way: pressed means the extra
  thing is on. It starts off, matching kubectl, so the YAML tab opens on the
  object rather than on the API server's bookkeeping about it.

  A fingerprint, because that is what the field records — which controller
  last touched which key. The glyph is not self-explanatory and is not trying
  to be; the accessible name and the tooltip carry the meaning, and there is
  no conventional icon for this in the way there is for wrapping.
-->
<script lang="ts">
  import { Fingerprint } from '@lucide/svelte'
  import { preferences } from '$stores/preferences.svelte'
  import ToolbarToggle from './ToolbarToggle.svelte'
</script>

<ToolbarToggle
  icon={Fingerprint}
  label="Managed fields"
  pressed={preferences.showManagedFields}
  title={preferences.showManagedFields
    ? 'Showing managedFields — the API server’s record of which controller owns which field'
    : 'Hiding managedFields — click to show the API server’s field-ownership record'}
  onclick={preferences.toggleManagedFields}
/>
