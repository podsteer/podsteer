import { render } from '@testing-library/svelte'
import { tick } from 'svelte'
import { describe, expect, it, vi } from 'vitest'

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

    // Scoped to links, because every row now carries a menu control too.
    const links = container.querySelectorAll('.resource-link')
    expect(links).toHaveLength(1)
    expect(links[0].textContent?.trim()).toBe('node-1')
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

      const toggles = container.querySelectorAll('[aria-label^="Expand"]')
      expect(toggles).toHaveLength(1)
      expect(toggles[0].getAttribute('aria-label')).toBe('Expand Liveness')

      const liveness = () => container.querySelectorAll('dd')[1].querySelector('span')!
      expect(liveness().className).toContain('truncate')

      toggles[0].dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await tick()

      expect(liveness().className).toContain('break-words')
      expect(container.querySelectorAll('[aria-label^="Collapse"]')).toHaveLength(1)
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

      container
        .querySelector('[aria-label^="Expand"]')
        ?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await tick()

      expect(container.querySelectorAll('[aria-label^="Collapse"]')).toHaveLength(1)
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

  it('offers the same two controls on every row', async () => {
    // THE ARRANGEMENT THIS GUARDS. The trailing controls were a cluster of up
    // to three icons that changed from row to row — a reveal here, an
    // information note there — which had to be learnt one at a time and read
    // as clutter in a column of values. Now: whether it fits, and what it can
    // do, in that order, on every row.
    const wide = stubLayout(30)
    try {
      const { container } = render(DetailList, {
        rows: [
          { label: 'Pod IP', value: '10.0.0.1' },
          { label: 'Long', value: 'a'.repeat(200) },
        ],
      })

      // The menu is on both; the expander only on the one that lost something.
      expect(container.querySelectorAll('[data-row-menu]')).toHaveLength(2)
      expect(container.querySelectorAll('[aria-label^="Expand"]')).toHaveLength(1)

      // And the controls are the last thing in the row, after the value.
      const trailing = container.querySelectorAll('dd')[1].lastElementChild
      expect(trailing?.querySelector('[data-row-menu]')).not.toBeNull()
    } finally {
      wide()
    }
  })

  it('offers Reference only when there is somewhere to go', async () => {
    const { container } = render(DetailList, {
      rows: [
        { label: 'Node', value: 'node-1', onclick: () => {} },
        { label: 'Pod IP', value: '10.0.0.1' },
      ],
    })

    const menus = container.querySelectorAll('[data-row-menu] button')
    menus[0].dispatchEvent(new MouseEvent('click', { bubbles: true }))
    await tick()

    const items = [...container.querySelectorAll('[role="menuitem"]')].map((item) =>
      item.textContent?.trim(),
    )
    // Copy is on every row, because every row has a value and copying it is
    // what an operator does with a panel more than anything else.
    expect(items).toEqual(['Reference', 'Copy value'])
  })

  it("carries the row's own action, worded by the caller", async () => {
    // Revealing a Secret is a deliberate, audited read whose wording depends
    // on whether it is currently shown — which the list cannot know.
    const reveal = vi.fn()
    const { container } = render(DetailList, {
      rows: [
        {
          label: 'DB_PASSWORD',
          value: '••••••••',
          action: { label: 'Reveal value', onclick: reveal },
        },
      ],
    })

    container
      .querySelector('[data-row-menu] button')
      ?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    await tick()

    const items = [...container.querySelectorAll('[role="menuitem"]')]
    expect(items.map((item) => item.textContent?.trim())).toEqual(['Copy value', 'Reveal value'])

    items[1].dispatchEvent(new MouseEvent('click', { bubbles: true }))
    expect(reveal).toHaveBeenCalledOnce()
  })

  it('confirms a copy in place, because the clipboard says nothing', async () => {
    // Every other item has a visible result — a panel changes, a value
    // appears. Copying leaves the row identical, so the menu says so before
    // it closes, the way the status bar's share menu does.
    const { container } = render(DetailList, {
      rows: [{ label: 'Pod IP', value: '10.0.0.1' }],
    })

    container
      .querySelector('[data-row-menu] button')
      ?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    await tick()

    const copy = container.querySelector('[role="menuitem"]')!
    copy.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    await tick()

    expect(container.querySelector('[role="menuitem"]')?.textContent).toContain('Copied!')
  })

  it('keeps only one menu open at a time', async () => {
    // THE BUG THIS GUARDS. Each menu kept its own open state and the
    // outside-click handler asked only whether the pointer had landed in *a*
    // row menu — so opening a second left the first standing, and a panel
    // ended up with four open at once.
    const { container } = render(DetailList, {
      rows: [
        { label: 'Pod IP', value: '10.0.0.1' },
        { label: 'Node', value: 'node-1' },
      ],
    })

    const triggers = container.querySelectorAll('[data-row-menu] button')
    triggers[0].dispatchEvent(new MouseEvent('click', { bubbles: true }))
    await tick()
    expect(container.querySelectorAll('[role="menu"]')).toHaveLength(1)

    triggers[1].dispatchEvent(new MouseEvent('click', { bubbles: true }))
    await tick()

    const open = container.querySelectorAll('[role="menu"]')
    expect(open).toHaveLength(1)
    // And it is the second one: the first closed rather than both standing.
    expect(triggers[1].getAttribute('aria-expanded')).toBe('true')
    expect(triggers[0].getAttribute('aria-expanded')).toBe('false')
  })

  it('puts the source of a resolved value in its tooltip', () => {
    // A value resolved out of a ConfigMap no longer names its source — that
    // is the point of resolving it — and a third control to say so was more
    // to learn than it was worth.
    const { container } = render(DetailList, {
      rows: [{ label: 'REDIS_HOST', value: 'redis-master', info: "From the 'redis' config map" }],
    })

    expect(container.querySelector('dd span')?.getAttribute('title')).toBe(
      "From the 'redis' config map",
    )
  })
})
