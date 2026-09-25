/**
 * Whether the keyboard shortcut sheet is open.
 *
 * A dedicated module rather than local state in one component, because two
 * unrelated places need it and neither is an ancestor of the other:
 * App.svelte owns the window-level keydown that opens it (⌘/, and a bare "?"
 * when focus is not in a field) alongside the other shortcuts that are not
 * scoped to one cluster tab, and the status bar offers a small, discoverable
 * button for the same thing — see StatusBar.svelte. `windowState` and
 * `activeTable` are the same shape of fix for the same shape of problem.
 */
class ShortcutSheetState {
  open = $state(false)

  show = (): void => {
    this.open = true
  }

  hide = (): void => {
    this.open = false
  }
}

/** The application-wide shortcut sheet visibility. A module singleton for the
    same reason `workspace` and `preferences` are: one desktop window, one
    sheet, one truth about whether it is open. */
export const shortcutSheet = new ShortcutSheetState()
