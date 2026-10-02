import { describe, expect, it } from 'vitest'

import type { DebugInfo } from '$lib/api/client'
import { engineFromUserAgent, formatDebugInfo } from './debugInfo'

const info = {
  version: 'v0.6.0',
  commit: '',
  os: 'macOS 15.1',
  platform: 'darwin/arm64',
  goVersion: 'go1.27',
  wailsVersion: 'v3.0.0-beta.25',
  webview: '',
} as DebugInfo

describe('formatDebugInfo', () => {
  it('reports versions and a cluster count, never a name', () => {
    const text = formatDebugInfo(info, ['v1.31.2', 'v1.29.0+k3s1', ''], 'Mozilla/5.0 Chrome/126.0.0.0 Safari/537')
    expect(text).toContain('PodSteer: v0.6.0')
    expect(text).toContain('Open clusters: 3')
    expect(text).toContain('Kubernetes versions: v1.29.0+k3s1, v1.31.2')
    expect(text).toContain('Webview: Chrome/126.0.0.0')
  })

  it('says so when no cluster has reported a version', () => {
    expect(formatDebugInfo(info, [], '')).toContain('Kubernetes versions: none reported')
  })

  it('prefers the backend webview version', () => {
    expect(formatDebugInfo({ ...info, webview: '126.0.1' }, [], 'Chrome/1.0')).toContain('Webview: 126.0.1')
  })
})

describe('engineFromUserAgent', () => {
  it('finds WebKit-only agents', () => {
    expect(engineFromUserAgent('Mozilla/5.0 AppleWebKit/605.1.15')).toBe('AppleWebKit/605.1.15')
  })
})
