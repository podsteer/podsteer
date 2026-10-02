/**
 * Which help topic is open, if any.
 *
 * ONE PANEL, NOT ONE PER DIALOG. Every (?) in the application opens the same
 * drawer with a different topic in it, so the panel has one set of behaviours
 * — where it sits, how it closes, what Escape does — instead of twenty
 * dialogs each growing their own. It is a store rather than a prop threaded
 * down because the panel is mounted once, at the top of the application, and
 * the buttons that open it are wherever a dialog happens to be.
 */
import { untrack } from 'svelte'
import type { HelpSection } from '$lib/help'

class HelpStore {
  /** The open topic's id, or null when the panel is closed. */
  topic = $state<string | null>(null)

  /**
   * Sections a page supplies about what is on screen NOW, per topic — the
   * topology's "this drawing" — shown above the topic's standing text. The
   * registry holds what is true every time; this holds what is true of this
   * one drawing, and is withdrawn when the page goes.
   */
  provided = $state.raw<Record<string, HelpSection[]>>({})

  provide(topic: string, sections: HelpSection[]): void {
    // Untracked: a page calls this from an effect, and reading what it is
    // about to replace would make that effect depend on its own write.
    this.provided = { ...untrack(() => this.provided), [topic]: sections }
  }

  withdraw(topic: string): void {
    const held = untrack(() => this.provided)
    if (!(topic in held)) return
    const { [topic]: _gone, ...rest } = held
    void _gone
    this.provided = rest
  }

  open(topic: string): void {
    this.topic = topic
  }

  close(): void {
    this.topic = null
  }
}

export const help = new HelpStore()
