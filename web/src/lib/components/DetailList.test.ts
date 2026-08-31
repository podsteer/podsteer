import { render } from '@testing-library/svelte'
import { describe, expect, it } from 'vitest'

import DetailList from './DetailList.svelte'

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

  it('truncates only the rows that ask for it', () => {
    // A UID's length is noise and is truncated; a probe string is content and
    // wraps. Getting this backwards hides the half of a value that matters.
    const { container } = render(DetailList, {
      rows: [
        { label: 'UID', value: 'a'.repeat(40), truncate: true },
        { label: 'Liveness', value: 'http-get http://:8080/healthz delay=10s' },
      ],
    })

    const values = container.querySelectorAll('dd')
    expect(values[0].className).toContain('truncate')
    expect(values[1].className).toContain('break-words')
  })
})
