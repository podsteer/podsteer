import { afterEach, describe, expect, it } from 'vitest'
import { cleanup, render } from '@testing-library/svelte'

import ContainerDetail from './ContainerDetail.svelte'

afterEach(cleanup)

describe('a template container\'s environment', () => {
  it('shows a downward-API value resolved, with no marker on the row', () => {
    // Under `props`: the component's own `context` prop is also one of the
    // testing library's options, and a flat object sends it to the wrong one.
    const { container } = render(ContainerDetail, { props: {
      spec: {
        name: 'app',
        image: 'nginx',
        env: [
          { name: 'ENVIRONMENT', valueFrom: { fieldRef: { fieldPath: "metadata.annotations['env']" } } },
          { name: 'LITERAL', value: 'development' },
          { name: 'POD_NAME', valueFrom: { fieldRef: { fieldPath: 'metadata.name' } } },
        ],
      },
      context: 'template',
      clusterId: '',
      namespace: 'shop',
      pod: { metadata: { namespace: 'shop', annotations: { env: 'development' } } },
    } })

    const text = (container.textContent ?? '').replace(/\s+/g, ' ')
    expect(text).toContain('ENVIRONMENT development')
    expect(text).not.toContain('(resolved)')
    expect(text).toContain('LITERAL development')
    expect(text).toContain('Environment variables (3)')
    // A template's pod has no name yet, so the path stays.
    expect(text).toContain('POD_NAME <metadata.name>')
    expect(container.querySelectorAll('[data-row-suffix]')).toHaveLength(0)
  })
})
