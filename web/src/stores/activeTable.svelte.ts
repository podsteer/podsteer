/**
 * The resource table currently on screen, published for controls that sit
 * outside it.
 *
 * The column chooser lives in the toolbar, and the toolbar is a sibling of
 * the view that owns the table — so it cannot be handed the columns as a
 * prop. Threading them up would mean every view exporting its column list to
 * ClusterWorkspace, and one of those lists does not exist until runtime:
 * GenericTableView derives its columns from whatever the API server's table
 * printer returned for a kind nobody compiled in.
 *
 * A single registration is safe for the same reason DataTable's own
 * focusFirstRow is: App.svelte renders exactly one ClusterWorkspace, keyed on
 * the cluster id, so at most one table exists at any moment. The claim/release
 * pair is tokenised anyway, because a keyed remount can construct the
 * replacement before tearing down what it replaces, and an unguarded release
 * would then clear the newcomer's registration and leave the toolbar empty.
 */

import type { Column } from '$lib/components/DataTable.svelte'

class ActiveTable {
  /** Identifies the kind, for persisting the operator's choices. */
  kindId = $state('')

  /**
   * `$state.raw` because this is replaced wholesale, never mutated in place,
   * and deep-proxying a column list that the menu reads on every render buys
   * nothing.
   */
  columns = $state.raw<Column[]>([])

  /** Whether there is a table to offer choices about. */
  readonly present = $derived(this.columns.length > 0)

  /** Whoever registered last. Compared on release, never read for display. */
  #owner = 0

  /** Registers a table and returns the token that can retire it. */
  claim(kindId: string, columns: Column[]): number {
    this.kindId = kindId
    this.columns = columns
    this.#owner += 1
    return this.#owner
  }

  /** Retires a registration, unless something newer has already replaced it. */
  release(token: number): void {
    if (this.#owner !== token) return
    this.kindId = ''
    this.columns = []
  }
}

export const activeTable = new ActiveTable()
