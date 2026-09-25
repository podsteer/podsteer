/**
 * Whether the Settings dialog is open.
 *
 * A dedicated module for the same reason `shortcutSheet.svelte.ts` is one:
 * more than one place needs to open it and neither is an ancestor of the
 * other. ClusterTabs.svelte owns the ⌘, shortcut and the header button; the
 * command palette is a second, unrelated opener that cannot reach a
 * `let settingsOpen = $state(false)` local to that component.
 */
class SettingsDialogState {
  open = $state(false)

  show = (): void => {
    this.open = true
  }

  hide = (): void => {
    this.open = false
  }

  toggle = (): void => {
    this.open = !this.open
  }
}

/** The application-wide Settings visibility. A module singleton for the same
    reason `workspace` and `preferences` are: one desktop window, one dialog,
    one truth about whether it is open. */
export const settingsDialog = new SettingsDialogState()
