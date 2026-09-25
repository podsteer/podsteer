/**
 * Whether the Organise dialog is open.
 *
 * A dedicated module for the same reason `shortcutSheet.svelte.ts` is one:
 * more than one place needs to open it and neither is an ancestor of the
 * other. ClusterView.svelte (the picker) owns its own "Organise" button; the
 * command palette is a second, unrelated opener that has to reach it from
 * ANY tab, not only the picker — which is exactly what a local
 * `let organiseOpen = $state(false)` cannot do.
 */
class OrganiseDialogState {
  open = $state(false)

  show = (): void => {
    this.open = true
  }

  hide = (): void => {
    this.open = false
  }
}

/** The application-wide Organise visibility. A module singleton for the same
    reason `workspace` and `preferences` are: one desktop window, one
    dialog, one truth about whether it is open. */
export const organiseDialog = new OrganiseDialogState()
