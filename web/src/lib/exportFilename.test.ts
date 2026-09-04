import { describe, expect, it } from 'vitest'
import { buildExportFilename } from './exportFilename'

const FIXED = new Date(2026, 0, 5, 9, 3, 7) // 2026-01-05 09:03:07 local time

describe('buildExportFilename', () => {
  it('joins cluster, kind, namespace and a timestamp', () => {
    expect(buildExportFilename('prod-eu', 'Pod', 'billing', FIXED)).toBe(
      'prod-eu-Pod-billing-20260105-090307.csv',
    )
  })

  it('renders an empty namespace as "all"', () => {
    // ALL_NAMESPACES in $lib/api/client is '' — the namespace filter set to
    // every namespace, which the filename should say in words.
    expect(buildExportFilename('prod-eu', 'Pod', '', FIXED)).toBe(
      'prod-eu-Pod-all-20260105-090307.csv',
    )
  })

  it('pads a single-digit month, day, hour, minute and second', () => {
    const early = new Date(2026, 8, 1, 1, 2, 3) // 2026-09-01 01:02:03
    expect(buildExportFilename('dev', 'Node', 'all', early)).toBe(
      'dev-Node-all-20260901-010203.csv',
    )
  })

  it('replaces characters unsafe for a filename', () => {
    // A cluster id can be a full kubeconfig context name — colons, slashes
    // and spaces have all been seen in the wild.
    expect(buildExportFilename('gke_my-proj_us-east1/cluster', 'Pod', 'kube system', FIXED)).toBe(
      'gke_my-proj_us-east1_cluster-Pod-kube_system-20260105-090307.csv',
    )
  })
})
