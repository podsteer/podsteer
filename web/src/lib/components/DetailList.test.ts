import { render } from '@testing-library/svelte'
import { tick } from 'svelte'
import { describe, expect, it } from 'vitest'

import DetailList from './DetailList.svelte'

/**
 * Makes text past `fits` characters report as overflowing its box.
 *
 * happy-dom has no layout engine, so every element measures zero by zero and
 * the clipping branch is unreachable without this. Returns the undo.
 */
function stubLayout(fits: number): () => void {
  // On HTMLElement, not Element: that is where happy-dom defines its own, and
  // a getter added to the base class is simply shadowed by it — which reads
  // as "everything overflows" rather than as a stub that did not take.
  const scroll = Object.getOwnPropertyDescriptor(HTMLElement.prototype, 'scrollWidth')
  const client = Object.getOwnPropertyDescriptor(HTMLElement.prototype, 'clientWidth')

  Object.defineProperty(HTMLElement.prototype, 'scrollWidth', {
    configurable: true,
    get(this: HTMLElement) {
      return (this.textContent ?? '').trim().length
    },
  })
  Object.defineProperty(HTMLElement.prototype, 'clientWidth', {
    configurable: true,
    get: () => fits,
  })

  // THE UNDO HAS TO HANDLE "THERE WAS NOTHING THERE". happy-dom may not
  // define these at all, in which case restoring the saved descriptor is a
  // no-op and the stub survives into every test that follows — which showed
  // up as a row reporting itself clipped in a test that never stubbed
  // anything.
  return () => {
    restore('scrollWidth', scroll)
    restore('clientWidth', client)
  }
}

function restore(property: string, original: PropertyDescriptor | undefined): void {
  if (original) {
    Object.defineProperty(HTMLElement.prototype, property, original)
    return
  }
  delete (HTMLElement.prototype as unknown as Record<string, unknown>)[property]
}

describe('DetailList', () => {
  it('renders repeated labels without throwing', () => {
    // THE REGRESSION THIS SUITE EXISTS FOR. This list keyed its rows by
    // label, and Svelte throws on a duplicate key in a keyed each — so the
    // whole pod overview stopped rendering for any pod with two volume
    // mounts, which is every pod once the service-account token is counted.
    //
    // `svelte-check` passed and the build passed. Only mounting it fails.
    const { getAllByText } = render(DetailList, {
      rows: [
        { label: 'Mount', value: '/etc/config from settings (ro)' },
        { label: 'Mount', value: '/var/run/secrets/kubernetes.io/serviceaccount from kube-api-access (ro)' },
        { label: 'Mount', value: '/tmp from scratch (rw)' },
      ],
    })

    expect(getAllByText('Mount')).toHaveLength(3)
  })

  it('renders a value as a button only when it can be followed', () => {
    // A row with no handler must be plain text. Offering a link that fails
    // when followed is worse than offering none — which is why `follow()`
    // returns undefined rather than a no-op for a kind the cluster does not
    // serve.
    const { container } = render(DetailList, {
      rows: [
        { label: 'Node', value: 'node-1', onclick: () => {} },
        { label: 'Pod IP', value: '10.0.0.1' },
      ],
    })

    const buttons = container.querySelectorAll('button')
    expect(buttons).toHaveLength(1)
    expect(buttons[0].textContent?.trim()).toBe('node-1')
  })

  it('offers an expander only for a value that did not fit, and opens the row', async () => {
    // THE REQUIREMENT THIS LIST EXISTS TO MEET: one line per row, and
    // whatever was cut off is one click away. A chevron on every row would be
    // a column of controls that mostly do nothing.
    //
    // happy-dom performs no layout, so scrollWidth and clientWidth are both
    // zero and nothing ever appears clipped. They are stubbed to a rule that
    // stands in for the real one — anything past thirty characters does not
    // fit — which is what lets the branch be exercised at all.
    const wide = stubLayout(30)
    try {
      const { container } = render(DetailList, {
        rows: [
          { label: 'Pod IP', value: '10.0.0.1' },
          { label: 'Liveness', value: 'http-get http://:8080/healthz delay=10s timeout=1s period=10s' },
        ],
      })

      const toggles = container.querySelectorAll('dd button')
      expect(toggles).toHaveLength(1)
      expect(toggles[0].getAttribute('aria-label')).toBe('Expand Liveness')

      const liveness = () => container.querySelectorAll('dd')[1].querySelector('span')!
      expect(liveness().className).toContain('truncate')

      toggles[0].dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await tick()

      expect(liveness().className).toContain('break-words')
      expect(container.querySelector('dd button')?.getAttribute('aria-label')).toBe(
        'Collapse Liveness',
      )
    } finally {
      wide()
    }
  })

  it('keeps the expander on an open row, which no longer overflows', async () => {
    // The trap in measuring: an open row wraps, so the browser reports it as
    // fitting, and re-measuring it would remove the control that closes it
    // again — leaving the row stuck open.
    const wide = stubLayout(30)
    try {
      const { container } = render(DetailList, {
        rows: [{ label: 'Annotation', value: 'a'.repeat(200) }],
      })

      container.querySelector('dd button')?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await tick()

      expect(container.querySelector('dd button')?.getAttribute('aria-label')).toBe(
        'Collapse Annotation',
      )
    } finally {
      wide()
    }
  })

  it('lays out a JSON value when the row is opened, and leaves other text alone', async () => {
    // The annotation case: what operators write is one line of minified JSON,
    // and expanding one line of minified JSON produces a longer line.
    const wide = stubLayout(30)
    try {
      const { container } = render(DetailList, {
        rows: [
          {
            label: 'kubectl.kubernetes.io/last-applied-configuration',
            value: '{"apiVersion":"v1","metadata":{"name":"web","labels":{"app":"web"}}}',
          },
          { label: 'deployment.kubernetes.io/revision', value: '000000000000000000000004' },
        ],
      })

      const open = async (row: number) => {
        container.querySelectorAll('dd')[row]
          .querySelector('button')
          ?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
        await tick()
      }

      await open(0)
      const laidOut = container.querySelector('pre')
      expect(laidOut?.textContent).toContain('\n  "apiVersion": "v1"')

      // A long value that is not JSON stays text: laying out is for structure,
      // and there is none here to lay out.
      await open(1)
      expect(container.querySelectorAll('pre')).toHaveLength(1)
    } finally {
      wide()
    }
  })

  it("gathers a row's controls at the end of the value column", async () => {
    // THE ARRANGEMENT THIS GUARDS. The reveal button used to sit wherever the
    // masked value happened to end and the expander at the far right, so a
    // column of rows had controls at different distances from the edge and no
    // two agreed. They are one cluster now, in one order: what the row can
    // do, what it is, whether it fits.
    const wide = stubLayout(30)
    try {
      const { container } = render(DetailList, {
        rows: [
          {
            label: 'DB_PASSWORD',
            value: '••••••••',
            info: "Set to the key 'p' in secret 'db'",
          },
          { label: 'Long', value: 'a'.repeat(200) },
        ],
      })

      // The info button is present only on the row that has a source to name.
      const cells = container.querySelectorAll('dd')
      expect(cells[0].querySelectorAll('[title]')).toHaveLength(1)
      expect(cells[1].querySelectorAll('[title]')).toHaveLength(1)

      // And every control sits after the value, in the trailing group.
      const group = cells[0].lastElementChild
      expect(group?.querySelector('svg')).not.toBeNull()
    } finally {
      wide()
    }
  })

  it('does not offer a source note on a row that has none', () => {
    const { container } = render(DetailList, {
      rows: [{ label: 'Pod IP', value: '10.0.0.1' }],
    })

    expect(container.querySelector('dd')?.querySelectorAll('[title]')).toHaveLength(0)
  })
})
