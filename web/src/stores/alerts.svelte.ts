/**
 * The sound a new finding makes.
 *
 * Synthesised with the Web Audio API rather than played from files. Three
 * reasons, in order of how much they matter: a sound file is a third-party
 * asset with a licence to account for in a project that ships its own licence
 * inventory; the binary stays the size it is; and a catalogue described as
 * numbers costs nothing to extend, which is what lets each severity have a
 * sound of its own rather than a shared one played louder.
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
  /**
   * Master level for this motif.
   *
   * Part of the sound rather than of the severity playing it. A sawtooth at
   * the level of a sine is painful, so loudness is designed alongside the
   * waveform — and it means what somebody auditions in Settings is exactly
   * what they will hear at three in the morning.
   */
  level: number
  notes: Note[]
}

/** Chosen when a severity should raise nothing audible. */
export const SILENT = 'silent'

/**
 * The variants offered in Settings.
 *
 * Deliberately short and pitched to carry rather than to be enjoyed: this
 * plays while somebody is reading, and the job is to make them look up once.
 * Nothing here rings for much more than half a second.
 *
 * They are ordered gentle to urgent, which is the order somebody assigning one
 * to warnings and another to criticals is choosing along.
 */
export const ALERT_SOUNDS: readonly AlertSound[] = [
  {
    id: 'chime',
    label: 'Chime',
    describe: 'Two soft rising notes',
    level: 0.16,
    notes: [
      { frequency: 880, at: 0, duration: 0.18, peak: 0.5, type: 'sine' },
      { frequency: 1318.5, at: 0.09, duration: 0.34, peak: 0.45, type: 'sine' },
    ],
  },
  {
    id: 'ping',
    label: 'Ping',
    describe: 'One short high tone',
    level: 0.16,
    notes: [{ frequency: 1568, at: 0, duration: 0.24, peak: 0.42, type: 'sine' }],
  },
  {
    id: 'marimba',
    label: 'Marimba',
    describe: 'A woody two-note figure',
    level: 0.18,
    notes: [
      { frequency: 659.25, at: 0, duration: 0.3, peak: 0.5, type: 'triangle' },
      { frequency: 987.77, at: 0.07, duration: 0.26, peak: 0.4, type: 'triangle' },
    ],
  },
  {
    id: 'knock',
    label: 'Knock',
    describe: 'Two low thuds, easy to ignore',
    level: 0.2,
    notes: [
      { frequency: 180, at: 0, duration: 0.14, peak: 0.5, type: 'triangle' },
      { frequency: 150, at: 0.13, duration: 0.16, peak: 0.45, type: 'triangle' },
    ],
  },
  {
    id: 'bell',
    label: 'Bell',
    describe: 'One clear note with a long tail',
    level: 0.15,
    notes: [
      { frequency: 1046.5, at: 0, duration: 0.7, peak: 0.45, type: 'sine' },
      { frequency: 1568, at: 0, duration: 0.4, peak: 0.16, type: 'sine' },
    ],
  },
  {
    id: 'sonar',
    label: 'Sonar',
    describe: 'A low tone sliding upwards',
    level: 0.18,
    notes: [{ frequency: 196, at: 0, duration: 0.5, peak: 0.5, type: 'sine', glideTo: 294 }],
  },
  {
    id: 'pulse',
    label: 'Pulse',
    describe: 'Three even beats',
    level: 0.16,
    notes: [
      { frequency: 660, at: 0, duration: 0.09, peak: 0.42, type: 'sine' },
      { frequency: 660, at: 0.15, duration: 0.09, peak: 0.42, type: 'sine' },
      { frequency: 660, at: 0.3, duration: 0.09, peak: 0.42, type: 'sine' },
    ],
  },
  {
    id: 'blip',
    label: 'Blip',
    describe: 'Two flat electronic beeps',
    level: 0.16,
    notes: [
      { frequency: 740, at: 0, duration: 0.08, peak: 0.16, type: 'square' },
      { frequency: 740, at: 0.14, duration: 0.08, peak: 0.16, type: 'square' },
    ],
  },
  {
    id: 'descend',
    label: 'Descend',
    describe: 'Three notes falling — something got worse',
    level: 0.18,
    notes: [
      { frequency: 1046.5, at: 0, duration: 0.14, peak: 0.45, type: 'triangle' },
      { frequency: 783.99, at: 0.13, duration: 0.14, peak: 0.45, type: 'triangle' },
      { frequency: 587.33, at: 0.26, duration: 0.34, peak: 0.45, type: 'triangle' },
    ],
  },
  {
    id: 'alarm',
    label: 'Alarm',
    describe: 'Three urgent blips',
    level: 0.15,
    notes: [
      { frequency: 880, at: 0, duration: 0.08, peak: 0.2, type: 'square' },
      { frequency: 880, at: 0.12, duration: 0.08, peak: 0.2, type: 'square' },
      { frequency: 880, at: 0.24, duration: 0.14, peak: 0.2, type: 'square' },
    ],
  },
  {
    id: 'klaxon',
    label: 'Klaxon',
    describe: 'Two harsh alternating tones — hard to miss',
    level: 0.12,
    notes: [
      { frequency: 466.16, at: 0, duration: 0.2, peak: 0.22, type: 'sawtooth' },
      { frequency: 349.23, at: 0.21, duration: 0.26, peak: 0.22, type: 'sawtooth' },
    ],
  },
] as const

/** The severities that can raise a sound. Info findings never do. */
export const ALERT_SEVERITIES = ['warning', 'critical'] as const

export type AlertSeverity = (typeof ALERT_SEVERITIES)[number]

export const SEVERITY_LABELS: Record<AlertSeverity, string> = {
  warning: 'Warning',
  critical: 'Critical',
}

/**
 * What each severity plays before anybody chooses.
 *
 * Two different sounds, not one sound played twice: the same motif repeated
 * says "again", and what a critical needs to say is "worse". Descend falls
 * where Chime rises, which is audible from another room without being learned.
 */
export const DEFAULT_ALERT_SOUNDS: Record<AlertSeverity, string> = {
  warning: 'chime',
  critical: 'descend',
}

/** Whether an id names a real sound, or silence, and nothing else. */
export function isAlertSound(id: unknown): id is string {
  return id === SILENT || ALERT_SOUNDS.some((sound) => sound.id === id)
}

function soundFor(id: string): AlertSound | null {
  return ALERT_SOUNDS.find((sound) => sound.id === id) ?? null
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
   * Plays one motif, exactly as it sounds in Settings.
   *
   * No severity argument: which sound a severity plays is the operator's
   * choice, and nothing here dresses it up afterwards. An earlier version
   * repeated the motif and raised the level for criticals, which meant the
   * sound somebody auditioned was not the sound they would be woken by.
   *
   * Failures are swallowed throughout: an alert that cannot be heard must
   * never be an alert that breaks the assessment it came from.
   */
  async play(id: string): Promise<void> {
    if (id === SILENT) return

    const sound = soundFor(id)
    if (!sound) return

    const context = await this.#awake()
    if (!context) return

    const master = context.createGain()
    master.gain.value = sound.level
    master.connect(context.destination)

    const start = context.currentTime + 0.02
    for (const note of sound.notes) {
      // `note.at` IS THE MOTIF. Every note used to be scheduled at `start`,
      // so a two-note chime rang as one chord and "three urgent blips" rang
      // as a single louder blip — every multi-note sound collapsed into its
      // first instant, and the descriptions in Settings described something
      // nobody could hear. #length below was already reading `at`, so the
      // node graph was being torn down on the correct schedule for a motif
      // that was never actually played.
      //
      // A note with `at: 0` alongside another is still deliberate: `bell`
      // stacks a fundamental and an overtone to colour one strike.
      this.#ring(context, master, note, start + note.at)
    }

    // Releasing the node graph once the tail has died keeps a long session
    // from accumulating one gain node per alert.
    const tail = start + this.#length(sound) + 0.1
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
