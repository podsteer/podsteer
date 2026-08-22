/**
 * The sound a new finding makes.
 *
 * Synthesised with the Web Audio API rather than played from files. Three
 * reasons, in order of how much they matter: a sound file is a third-party
 * asset with a licence to account for in a project that ships its own licence
 * inventory; the binary stays the size it is; and a tone described as numbers
 * can be varied for severity — the same motif, heavier for a critical — where a
 * pair of recordings would have to be two unrelated sounds.
 *
 * Nothing here reaches the network, which is the point of it being an
 * oscillator and not a fetch.
 */

/** A single tone within a motif. */
interface Note {
  /** Hertz. */
  frequency: number
  /** Seconds after the motif starts. */
  at: number
  /** Seconds the tone rings for. */
  duration: number
  /** Peak gain, before the master level. Square waves need less. */
  peak: number
  type?: OscillatorType
  /** Hertz to glide to across the tone's life, for a sweep. */
  glideTo?: number
}

export interface AlertSound {
  id: string
  label: string
  /** What it sounds like, for somebody choosing without listening yet. */
  describe: string
  notes: Note[]
}

/**
 * The variants offered in Settings.
 *
 * Deliberately short and pitched above the room rather than musical: this
 * plays while somebody is reading, and the job is to make them look up once,
 * not to be enjoyed. Nothing here rings for more than about half a second.
 */
export const ALERT_SOUNDS: readonly AlertSound[] = [
  {
    id: 'chime',
    label: 'Chime',
    describe: 'Two soft rising notes',
    notes: [
      { frequency: 880, at: 0, duration: 0.18, peak: 0.5, type: 'sine' },
      { frequency: 1318.5, at: 0.09, duration: 0.34, peak: 0.45, type: 'sine' },
    ],
  },
  {
    id: 'ping',
    label: 'Ping',
    describe: 'One short high tone',
    notes: [{ frequency: 1568, at: 0, duration: 0.24, peak: 0.42, type: 'sine' }],
  },
  {
    id: 'marimba',
    label: 'Marimba',
    describe: 'A woody two-note figure',
    notes: [
      { frequency: 659.25, at: 0, duration: 0.3, peak: 0.5, type: 'triangle' },
      { frequency: 987.77, at: 0.07, duration: 0.26, peak: 0.4, type: 'triangle' },
    ],
  },
  {
    id: 'blip',
    label: 'Blip',
    describe: 'Two flat electronic beeps',
    notes: [
      { frequency: 740, at: 0, duration: 0.08, peak: 0.16, type: 'square' },
      { frequency: 740, at: 0.14, duration: 0.08, peak: 0.16, type: 'square' },
    ],
  },
  {
    id: 'sonar',
    label: 'Sonar',
    describe: 'A low tone sliding upwards',
    notes: [
      { frequency: 196, at: 0, duration: 0.5, peak: 0.5, type: 'sine', glideTo: 294 },
    ],
  },
] as const

export type AlertSoundID = (typeof ALERT_SOUNDS)[number]['id']

export const DEFAULT_ALERT_SOUND: AlertSoundID = 'chime'

/** Severity decides how insistent the same motif is. */
export type AlertSeverity = 'warning' | 'critical'

/** Master level per severity. A critical is louder as well as repeated. */
const LEVEL: Record<AlertSeverity, number> = { warning: 0.16, critical: 0.22 }

/** Seconds between the two passes a critical gets. */
const CRITICAL_GAP = 0.34

function soundFor(id: string): AlertSound {
  return ALERT_SOUNDS.find((sound) => sound.id === id) ?? ALERT_SOUNDS[0]
}

/**
 * Plays alert motifs, and owns the one AudioContext that does it.
 *
 * The context is created on first use rather than at import: constructing one
 * before any user interaction leaves it suspended under the browser's autoplay
 * policy, and a suspended context started at module load is one that stays
 * suspended. It is also resumed on the first click or keypress anywhere, so
 * that by the time an alert has something to say the output is already awake —
 * an operator who never touches the window hears nothing, which is the one
 * case where a silent alarm is the browser's decision and not ours.
 */
class AlertPlayer {
  #context: AudioContext | null = null
  #unlocking = false

  /** Whether the browser has ever let us make a sound. */
  get available(): boolean {
    return typeof globalThis.AudioContext !== 'undefined'
  }

  /**
   * Arms playback on the first interaction of the session.
   *
   * Called once from the application root. Listeners are removed after the
   * first hit: keeping them would mean a resume() call on every click for the
   * life of the process.
   */
  arm(): void {
    if (this.#unlocking || !this.available) return
    this.#unlocking = true

    const unlock = (): void => {
      void this.#awake()
      window.removeEventListener('pointerdown', unlock)
      window.removeEventListener('keydown', unlock)
    }
    window.addEventListener('pointerdown', unlock, { once: false })
    window.addEventListener('keydown', unlock, { once: false })
  }

  /** Returns a running context, or null when the browser will not give one. */
  async #awake(): Promise<AudioContext | null> {
    if (!this.available) return null

    try {
      this.#context ??= new AudioContext()
      if (this.#context.state === 'suspended') await this.#context.resume()
      return this.#context.state === 'running' ? this.#context : null
    } catch {
      // A machine with no output device, or a policy that forbids audio
      // entirely. Silence is the correct outcome, not an error anybody needs.
      return null
    }
  }

  /**
   * Plays one motif.
   *
   * Failures are swallowed throughout: an alert that cannot be heard must
   * never be an alert that breaks the assessment it came from.
   */
  async play(id: string, severity: AlertSeverity = 'warning'): Promise<void> {
    const context = await this.#awake()
    if (!context) return

    const sound = soundFor(id)
    const master = context.createGain()
    master.gain.value = LEVEL[severity]
    master.connect(context.destination)

    const passes = severity === 'critical' ? 2 : 1
    const start = context.currentTime + 0.02

    for (let pass = 0; pass < passes; pass += 1) {
      for (const note of sound.notes) {
        this.#ring(context, master, note, start + pass * CRITICAL_GAP)
      }
    }

    // Releasing the node graph once the tail has died keeps a long session
    // from accumulating one gain node per alert.
    const tail = start + (passes - 1) * CRITICAL_GAP + this.#length(sound) + 0.1
    master.gain.setValueAtTime(master.gain.value, tail)
    window.setTimeout(() => master.disconnect(), (tail - context.currentTime) * 1000 + 200)
  }

  /** Schedules one tone with a percussive envelope. */
  #ring(context: AudioContext, into: GainNode, note: Note, at: number): void {
    const oscillator = context.createOscillator()
    oscillator.type = note.type ?? 'sine'
    oscillator.frequency.setValueAtTime(note.frequency, at)
    if (note.glideTo !== undefined) {
      oscillator.frequency.linearRampToValueAtTime(note.glideTo, at + note.duration)
    }

    // An 8ms attack rather than an instant one: a gain that steps from zero
    // clicks, and the click is the part people find unpleasant.
    const envelope = context.createGain()
    envelope.gain.setValueAtTime(0.0001, at)
    envelope.gain.exponentialRampToValueAtTime(note.peak, at + 0.008)
    envelope.gain.exponentialRampToValueAtTime(0.0001, at + note.duration)

    oscillator.connect(envelope)
    envelope.connect(into)
    oscillator.start(at)
    oscillator.stop(at + note.duration + 0.02)
  }

  /** How long a motif rings for, in seconds. */
  #length(sound: AlertSound): number {
    return sound.notes.reduce((longest, note) => Math.max(longest, note.at + note.duration), 0)
  }
}

export const alertPlayer = new AlertPlayer()
