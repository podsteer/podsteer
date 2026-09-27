import { describe, expect, it } from 'vitest'
import { authCanI, type CanIQuestion } from './kubectl'

const base: CanIQuestion = {
  subjectKind: '',
  subjectName: '',
  subjectNamespace: '',
  verb: 'get',
  group: '',
  resource: 'pods',
  subresource: '',
  namespace: 'shop',
  name: '',
}

describe('authCanI', () => {
  it('asks about this account in a namespace', () => {
    expect(authCanI('prod', base)).toBe('kubectl --context prod auth can-i get pods -n shop')
  })

  it('asks at cluster scope with --all-namespaces, never the context default', () => {
    expect(authCanI('prod', { ...base, namespace: '' })).toBe(
      'kubectl --context prod auth can-i get pods --all-namespaces',
    )
  })

  it('qualifies a grouped resource, names an object and a subresource', () => {
    expect(
      authCanI('prod', { ...base, verb: 'create', group: 'apps', resource: 'deployments', name: 'web', subresource: 'scale' }),
    ).toBe('kubectl --context prod auth can-i create deployments.apps/web --subresource scale -n shop')
  })

  it('quotes a wildcard so the shell does not glob it', () => {
    expect(authCanI('prod', { ...base, verb: '*', resource: '*' })).toBe(
      "kubectl --context prod auth can-i '*' '*' -n shop",
    )
  })

  it('impersonates a user and a service account', () => {
    expect(authCanI('prod', { ...base, subjectKind: 'User', subjectName: 'jane@example.com' })).toBe(
      'kubectl --context prod auth can-i get pods -n shop --as jane@example.com',
    )
    expect(
      authCanI('prod', { ...base, subjectKind: 'ServiceAccount', subjectName: 'ops-bot', subjectNamespace: 'platform' }),
    ).toBe('kubectl --context prod auth can-i get pods -n shop --as system:serviceaccount:platform:ops-bot')
  })

  it('offers nothing for a group, rather than a command asking something else', () => {
    expect(authCanI('prod', { ...base, subjectKind: 'Group', subjectName: 'devs' })).toBe('')
  })
})
