import { render } from '@testing-library/svelte'
import { describe, expect, it, vi } from 'vitest'

import EventDetail from './EventDetail.svelte'

/** A kubelet event: the generation that carries source.host and count. */
const kubeletEvent = {
  metadata: { name: 'nginx-7d8f.17a2b3c4d5e6f7', namespace: 'web' },
  involvedObject: { kind: 'Pod', name: 'nginx-7d8f', namespace: 'web' },
  type: 'Warning',
  reason: 'BackOff',
  message: 'Back-off restarting failed container',
  count: 12,
  firstTimestamp: '2026-09-02T08:00:00Z',
  lastTimestamp: '2026-09-02T08:20:00Z',
  source: { component: 'kubelet', host: 'node-1' },
}

/** Every kind is reachable — the ordinary cluster. */
const servesEverything = (kind: string) => `core/v1/${kind.toLowerCase()}s`

function linkFor(container: HTMLElement, text: string): HTMLButtonElement | undefined {
  return [...container.querySelectorAll('button')].find(
    (button) => button.textContent?.trim() === text,
  ) as HTMLButtonElement | undefined
}

describe('EventDetail', () => {
  it('follows the node and the namespace, but not the event itself', () => {
    // The three rows somebody reads an event for that name something else.
    // The event's OWN name names the record already open, so it must stay
    // text — a link there is a link back to where you are.
    const onopen = vi.fn()
    const onnamespace = vi.fn()

    const { container } = render(EventDetail, {
      event: kubeletEvent,
      canOpen: servesEverything,
      onopen,
      onnamespace,
    })

    linkFor(container, 'node-1')?.click()
    expect(onopen).toHaveBeenCalledWith('Node', 'node-1', '')

    linkFor(container, 'web')?.click()
    expect(onnamespace).toHaveBeenCalledWith('web')

    expect(linkFor(container, 'nginx-7d8f.17a2b3c4d5e6f7')).toBeUndefined()
  })

  it('leaves a reference as plain text when the cluster does not serve it', () => {
    // An account that cannot list nodes must see the node's name, not a link
    // that fails when followed. Offering a dead link is worse than none.
    const { container } = render(EventDetail, {
      event: kubeletEvent,
      canOpen: (kind: string) => (kind === 'Node' ? null : 'core/v1/pods'),
      onopen: vi.fn(),
      onnamespace: vi.fn(),
    })

    expect(linkFor(container, 'node-1')).toBeUndefined()
    expect(container.textContent).toContain('node-1')

    // The involved object is a Pod, which this cluster does serve.
    expect(linkFor(container, 'nginx-7d8f')).toBeDefined()
  })

  it('drops the node row for an event no kubelet reported', () => {
    // A scheduler event has a component and no host. A blank row there reads
    // as a node that could not be determined rather than one that never
    // applied.
    const { container } = render(EventDetail, {
      event: {
        ...kubeletEvent,
        source: { component: 'default-scheduler' },
      },
      canOpen: servesEverything,
    })

    expect(container.textContent).toContain('default-scheduler')
    expect(container.textContent).not.toContain('Node')
  })
})
