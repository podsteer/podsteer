import { describe, expect, it } from 'vitest'
import { changedImages } from './setImage'
import type { PodTemplate } from './podTemplate'

const template: PodTemplate = {
  spec: {
    initContainers: [{ name: 'migrate', image: 'myapp/migrate:1.0.0' }],
    containers: [
      { name: 'app', image: 'myapp/web:1.0.0' },
      { name: 'sidecar', image: 'myapp/proxy:1.0.0' },
    ],
  },
}

describe('changedImages', () => {
  it('returns nothing when no field was edited', () => {
    expect(changedImages(template, {})).toEqual([])
  })

  it('ignores a field left equal to the current image', () => {
    expect(changedImages(template, { app: 'myapp/web:1.0.0' })).toEqual([])
  })

  it('ignores an empty field', () => {
    expect(changedImages(template, { app: '' })).toEqual([])
  })

  it('ignores a whitespace-only field', () => {
    expect(changedImages(template, { app: '   ' })).toEqual([])
  })

  it('includes a genuinely changed container, trimmed', () => {
    expect(changedImages(template, { app: '  myapp/web:2.0.0  ' })).toEqual([
      { container: 'app', initContainer: false, image: 'myapp/web:2.0.0' },
    ])
  })

  it('treats a trimmed value equal to the current image as unchanged', () => {
    expect(changedImages(template, { app: '  myapp/web:1.0.0  ' })).toEqual([])
  })

  it('includes a changed init container, marked initContainer: true', () => {
    expect(changedImages(template, { migrate: 'myapp/migrate:2.0.0' })).toEqual([
      { container: 'migrate', initContainer: true, image: 'myapp/migrate:2.0.0' },
    ])
  })

  it('returns only the rows that actually changed, in template order', () => {
    expect(
      changedImages(template, {
        migrate: 'myapp/migrate:2.0.0',
        app: 'myapp/web:1.0.0', // unchanged
        sidecar: 'myapp/proxy:2.0.0',
      }),
    ).toEqual([
      { container: 'sidecar', initContainer: false, image: 'myapp/proxy:2.0.0' },
      { container: 'migrate', initContainer: true, image: 'myapp/migrate:2.0.0' },
    ])
  })

  it('ignores an edit keyed to a container the template does not have', () => {
    expect(changedImages(template, { ghost: 'myapp/ghost:1.0.0' })).toEqual([])
  })

  it('returns nothing for a null template', () => {
    expect(changedImages(null, { app: 'myapp/web:2.0.0' })).toEqual([])
  })

  it('returns nothing for a template with no spec', () => {
    expect(changedImages({}, { app: 'myapp/web:2.0.0' })).toEqual([])
  })

  it('skips a raw entry missing a name or image rather than throwing', () => {
    const malformed: PodTemplate = {
      spec: { containers: [{ image: 'myapp/web:1.0.0' }, { name: 'app' }] },
    }
    expect(changedImages(malformed, { app: 'myapp/web:2.0.0' })).toEqual([])
  })
})
