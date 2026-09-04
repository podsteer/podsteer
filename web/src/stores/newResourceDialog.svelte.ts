/**
 * Whether the "New <kind>" dialog is open.
 *
 * A dedicated module for the same reason `shortcutSheet.svelte.ts` is one:
 * ClusterWorkspace.svelte owns the New button and the skeleton it seeds the
 * dialog with, both of which are properties of whichever kind is currently
 * selected — but the command palette, a sibling of ClusterWorkspace rather
 * than an ancestor of it, needs to open the SAME dialog from its own "New
 * <kind>" command. A single boolean is safe for the same reason
 * `activeTable`'s own registration is: App.svelte renders exactly one
 * ClusterWorkspace at a time, so at most one such dialog exists to open.
 */
class NewResourceDialogState {
  open = $state(false)

  show = (): void => {
    this.open = true
  }

  hide = (): void => {
    this.open = false
  }
}

/** The application-wide "New <kind>" visibility. A module singleton for the
    same reason `workspace` and `preferences` are. */
export const newResourceDialog = new NewResourceDialogState()
