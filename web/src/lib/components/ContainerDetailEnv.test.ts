import { afterEach, describe, expect, it } from 'vitest'
import { cleanup, render } from '@testing-library/svelte'

import ContainerDetail from './ContainerDetail.svelte'

afterEach(cleanup)

const words = (container: HTMLElement) => (container.textContent ?? '').replace(/\s+/g, ' ')

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

describe('a container\'s image', () => {
  const renderImage = (image: string) =>
    render(ContainerDetail, {
      props: { spec: { name: 'app', image }, context: 'template', clusterId: '', namespace: 'shop' },
    })

  it('carries a warning icon on :latest', () => {
    const { container } = renderImage('shop/web:latest')
    expect(container.querySelector('[data-row-warning]')).not.toBeNull()
  })

  it('carries none on a version tag', () => {
    const { container } = renderImage('shop/web:1.2.3')
    expect(container.querySelector('[data-row-warning]')).toBeNull()
  })
})

describe('the sub-headings inside a container', () => {
  it('are all set bold, the container name included', () => {
    const { container } = render(ContainerDetail, {
      props: {
        spec: { name: 'app', image: 'nginx:1.27', ports: [{ name: 'http', containerPort: 80 }], env: [{ name: 'A', value: '1' }] },
        context: 'template',
        clusterId: '',
        namespace: 'shop',
      },
    })
    const bold = [...container.querySelectorAll('.font-semibold')].map((node) => (node.textContent ?? '').trim())
    expect(bold).toContain('app')
    expect(bold).toContain('Ports')
    expect(bold.some((text) => text.startsWith('Environment variables'))).toBe(true)
  })
})

describe('a running container\'s Status row', () => {
  const renderStatus = (status: Record<string, unknown>) =>
    render(ContainerDetail, {
      props: {
        spec: { name: 'app', image: 'nginx:1.27' },
        status: { name: 'app', state: 'Running', reason: '', ready: true, started: true, ...status } as never,
        clusterId: '',
        namespace: 'shop',
      },
    })

  it('says the state on one line and readiness on the next', () => {
    const { container } = renderStatus({})
    const lines = [...container.querySelectorAll('[data-row-line]')].map((line) => line.textContent)
    // Two lines of the value itself, not a muted footnote.
    expect(lines.slice(0, 2)).toEqual(['Running', 'Ready'])
    expect(container.querySelector('[data-row-detail]')).toBeNull()
  })

  it('tells started-but-not-ready apart from still starting', () => {
    const started = renderStatus({ ready: false, started: true })
    expect(started.container.querySelectorAll('[data-row-line]')[1]?.textContent).toContain('readiness check')
    cleanup()
    const starting = renderStatus({ ready: false, started: false })
    expect(starting.container.querySelectorAll('[data-row-line]')[1]?.textContent).toContain('still starting')
  })

  it('carries the reason with the state', () => {
    const { container } = renderStatus({ state: 'Waiting', reason: 'CrashLoopBackOff', ready: false, started: false })
    expect(words(container)).toContain('Waiting (CrashLoopBackOff)')
  })
})

describe('requests, limits and usage', () => {
  const lines = (container: HTMLElement) => [...container.querySelectorAll('[data-row-line]')].map((line) => line.textContent)

  it('put CPU and memory on separate lines for a running container', () => {
    const { container } = render(ContainerDetail, {
      props: {
        spec: { name: 'app', image: 'nginx:1.27' },
        status: {
          name: 'app', state: 'Running', reason: '', ready: true, started: true,
          requests: 'cpu: 0.05, memory: 128.0MiB', limits: 'cpu: 0.25, memory: 128.0MiB',
          hasMetrics: true, cpu: '0.001', memory: '19.9MiB',
        } as never,
        clusterId: '',
        namespace: 'shop',
      },
    })
    const all = lines(container)
    expect(all).toContain('CPU: 0.05')
    expect(all).toContain('Memory: 128.0MiB')
    expect(all).toContain('CPU: 0.001')
    expect(all).toContain('Memory: 19.9MiB')
  })

  it('quote a template\'s declared figures, which it used not to show at all', () => {
    const { container } = render(ContainerDetail, {
      props: {
        spec: { name: 'app', image: 'nginx:1.27', resources: { requests: { memory: '256Mi', cpu: '100m' }, limits: { memory: '512Mi' } } },
        context: 'template',
        clusterId: '',
        namespace: 'shop',
      },
    })
    expect(lines(container)).toEqual(['CPU: 100m', 'Memory: 256Mi', 'Memory: 512Mi'])
  })
})
