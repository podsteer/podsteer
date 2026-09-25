<script lang="ts">
  import DialogFooter from '$lib/components/DialogFooter.svelte'
  import Button from '$lib/components/Button.svelte'

  let { command = '' }: { command?: string } = $props()
</script>

<DialogFooter {command}>
  <Button variant="outlined" onclick={() => {}}>Cancel</Button>
  <Button variant="filled" onclick={() => {}}>Confirm</Button>
</DialogFooter>
