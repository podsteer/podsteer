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
class HelpStore {
  /** The open topic's id, or null when the panel is closed. */
  topic = $state<string | null>(null)

  open(topic: string): void {
    this.topic = topic
  }

  close(): void {
    this.topic = null
  }
}

export const help = new HelpStore()
