import { beforeAll, beforeEach, describe, expect, it, vi } from 'vitest'
import { ALERT_SOUNDS, alertPlayer } from './alerts.svelte'

/**
 * Records WHEN each oscillator is told to start.
 *
 * The timing is the whole subject. Every note used to be scheduled at the
 * motif's start, so a two-note chime rang as one chord and "three urgent
 * blips" rang as a single louder blip. That is audible by ear and invisible
 * to any test that checks the sound definitions rather than the scheduling —
 * the definitions were always right.
 */
const starts: number[] = []

/**
 * ONE context for the whole file, because the player keeps one for the life
 * of the process — creating it lazily and caching it. Restubbing per test
 * would hand the second test a fake the player never looks at again.
 */
beforeAll(() => {
  const gain = () => ({
    gain: { value: 0, setValueAtTime: vi.fn(), exponentialRampToValueAtTime: vi.fn() },
    connect: vi.fn(),
    disconnect: vi.fn(),
  })

  const context = {
    currentTime: 0,
    state: 'running',
    resume: vi.fn(),
    destination: {},
    createGain: vi.fn(gain),
    createOscillator: vi.fn(() => ({
      type: 'sine',
      frequency: { setValueAtTime: vi.fn(), linearRampToValueAtTime: vi.fn() },
      connect: vi.fn(),
      start: vi.fn((at: number) => starts.push(at)),
      stop: vi.fn(),
    })),
  }

  // A REAL `function`, not `vi.fn(() => context)`. A vi.fn wraps an arrow,
  // arrows are not constructors, and `new` on one throws a TypeError — which
  // #awake's own try/catch swallows into silence. The test then records no
  // notes and looks exactly like the bug it was written to catch.
  vi.stubGlobal('AudioContext', function () {
    return context
  })
})

beforeEach(() => {
  starts.length = 0
})

describe('alert sounds', () => {
  it('schedules every note at its own offset, not all at once', async () => {
    // "Three urgent blips" is three identical 880Hz tones. Played together
    // they are one blip, which is exactly the bug this asserts against.
    await alertPlayer.play('alarm')

    const alarm = ALERT_SOUNDS.find((sound) => sound.id === 'alarm')!
    expect(starts).toHaveLength(alarm.notes.length)
    expect(new Set(starts).size).toBe(3)

    const spread = Math.max(...starts) - Math.min(...starts)
    expect(spread).toBeCloseTo(0.24, 5)
  })

  it('keeps a deliberate chord as a chord', async () => {
    // `bell` stacks a fundamental and an overtone, both at 0, to colour one
    // strike. Spreading those apart would be the opposite mistake.
    await alertPlayer.play('bell')

    expect(new Set(starts).size).toBe(1)
  })

  it('rings each motif for as many notes as it declares', async () => {
    for (const sound of ALERT_SOUNDS) {
      starts.length = 0
      await alertPlayer.play(sound.id)

      expect(starts, `${sound.id} played the wrong number of notes`).toHaveLength(
        sound.notes.length,
      )
    }
  })

  it('plays nothing for silence', async () => {
    await alertPlayer.play('silent')

    expect(starts).toHaveLength(0)
  })

  it('every description that claims a count matches what is heard', () => {
    // The descriptions in Settings are a promise about what will be heard,
    // and they are the only thing an operator has to choose between motifs.
    const words: Record<string, number> = { One: 1, Two: 2, Three: 3 }

    for (const sound of ALERT_SOUNDS) {
      const claimed = words[sound.describe.split(' ')[0]]
      if (claimed === undefined) continue

      // DISTINCT ONSETS, not notes: `bell` says "One clear note" and is two
      // oscillators struck together. A listener counts onsets.
      const onsets = new Set(sound.notes.map((note) => note.at)).size
      expect(onsets, `${sound.id}: "${sound.describe}"`).toBe(claimed)
    }
  })
})
