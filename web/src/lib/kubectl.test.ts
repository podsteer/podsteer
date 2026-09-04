import { describe, expect, it } from 'vitest'
import {
  apply,
  del,
  describe as describeCmd,
  exec,
  get,
  getYaml,
  logs,
  portForward,
  resourceArg,
  resourceArgForKind,
  revealSecretKey,
  rolloutRestart,
  scale,
  shellQuote,
} from './kubectl'

describe('shellQuote', () => {
  it('leaves a plain context name unquoted', () => {
    // A context name that is already a shell-safe token should not gain
    // decoration nobody asked for — the whole point is a command that reads
    // like something a person would type.
    expect(shellQuote('prod')).toBe('prod')
  })

  it('leaves the DNS-1123-ish characters kubeconfig contexts commonly use unquoted', () => {
    expect(shellQuote('arn:aws:eks:eu-west-1:123456789012:cluster/prod')).toBe(
      'arn:aws:eks:eu-west-1:123456789012:cluster/prod',
    )
    expect(shellQuote('gke_my-project_europe-west1_prod')).toBe('gke_my-project_europe-west1_prod')
  })

  it('quotes a context name containing a space', () => {
    expect(shellQuote('my cluster')).toBe("'my cluster'")
  })

  it('quotes and escapes a context name containing a single quote', () => {
    // Standard POSIX escaping: close the quote, an escaped quote, reopen.
    expect(shellQuote("bob's cluster")).toBe("'bob'\\''s cluster'")
  })

  it('quotes an empty context name', () => {
    expect(shellQuote('')).toBe("''")
  })
})

describe('resourceArg', () => {
  it('appends nothing for the core group', () => {
    expect(resourceArg({ group: '', resource: 'pods' })).toBe('pods')
  })

  it('qualifies a non-core resource with its group', () => {
    expect(resourceArg({ group: 'apps', resource: 'deployments' })).toBe('deployments.apps')
    expect(resourceArg({ group: 'batch', resource: 'cronjobs' })).toBe('cronjobs.batch')
  })
})

describe('resourceArgForKind', () => {
  it('recovers the resource segment from a core-group id', () => {
    // "core/v1/pods" is how ResourceKind.ID() renders the core group, per
    // app/domain/resource.go — never an empty leading segment.
    expect(resourceArgForKind({ group: '', id: 'core/v1/pods' })).toBe('pods')
  })

  it('recovers the resource segment from a grouped id', () => {
    expect(resourceArgForKind({ group: 'apps', id: 'apps/v1/deployments' })).toBe(
      'deployments.apps',
    )
  })
})

describe('get', () => {
  it('builds a namespaced read', () => {
    expect(get('prod', 'pods', 'web-1', 'default')).toBe(
      'kubectl --context prod -n default get pods web-1',
    )
  })

  it('omits -n for a cluster-scoped resource', () => {
    expect(get('prod', 'nodes', 'node-1')).toBe('kubectl --context prod get nodes node-1')
  })

  it('quotes the context, not the namespace or the name', () => {
    expect(get('my cluster', 'pods', 'web-1', 'default')).toBe(
      "kubectl --context 'my cluster' -n default get pods web-1",
    )
  })
})

describe('getYaml', () => {
  it('adds -o yaml after the object', () => {
    expect(getYaml('prod', 'pods', 'web-1', 'default')).toBe(
      'kubectl --context prod -n default get pods web-1 -o yaml',
    )
  })

  it('omits -n for a cluster-scoped resource', () => {
    expect(getYaml('prod', 'namespaces', 'billing')).toBe(
      'kubectl --context prod get namespaces billing -o yaml',
    )
  })
})

describe('describe', () => {
  it('builds a namespaced describe', () => {
    expect(describeCmd('prod', 'deployments.apps', 'web', 'default')).toBe(
      'kubectl --context prod -n default describe deployments.apps web',
    )
  })

  it('omits -n for a cluster-scoped resource', () => {
    expect(describeCmd('prod', 'nodes', 'node-1')).toBe('kubectl --context prod describe nodes node-1')
  })
})

describe('scale', () => {
  it('lowercases the kind and always carries the namespace', () => {
    expect(scale('prod', 'Deployment', 'web', 'default', 3)).toBe(
      'kubectl --context prod -n default scale deployment/web --replicas=3',
    )
  })

  it('allows scaling to zero', () => {
    expect(scale('prod', 'StatefulSet', 'db', 'data', 0)).toBe(
      'kubectl --context prod -n data scale statefulset/db --replicas=0',
    )
  })
})

describe('rolloutRestart', () => {
  it('lowercases the kind and always carries the namespace', () => {
    expect(rolloutRestart('prod', 'DaemonSet', 'agent', 'kube-system')).toBe(
      'kubectl --context prod -n kube-system rollout restart daemonset/agent',
    )
  })
})

describe('del', () => {
  it('builds a namespaced delete', () => {
    expect(del('prod', 'pods', 'web-1', 'default')).toBe(
      'kubectl --context prod -n default delete pods web-1',
    )
  })

  it('omits -n for a cluster-scoped resource', () => {
    expect(del('prod', 'namespaces', 'billing')).toBe('kubectl --context prod delete namespaces billing')
  })
})

describe('logs', () => {
  it('emits nothing beyond the base command when no option is set', () => {
    expect(logs('prod', 'web-1', 'default')).toBe('kubectl --context prod -n default logs web-1')
  })

  it('emits only the flags that were actually set, in a fixed order', () => {
    expect(
      logs('prod', 'web-1', 'default', { container: 'app', tail: 500, follow: true, previous: true }),
    ).toBe('kubectl --context prod -n default logs web-1 -c app --tail=500 -f -p')
  })

  it('omits container when unset but keeps the rest', () => {
    expect(logs('prod', 'web-1', 'default', { follow: true })).toBe(
      'kubectl --context prod -n default logs web-1 -f',
    )
  })

  it('treats tail: 0 as a set value, not an absent one', () => {
    // `options.tail !== undefined` is deliberate: 0 is a real tail length and
    // `if (options.tail)` would silently drop it.
    expect(logs('prod', 'web-1', 'default', { tail: 0 })).toBe(
      'kubectl --context prod -n default logs web-1 --tail=0',
    )
  })
})

describe('exec', () => {
  it('defaults to /bin/sh with no container', () => {
    expect(exec('prod', 'web-1', 'default')).toBe(
      'kubectl --context prod -n default exec -it web-1 -- /bin/sh',
    )
  })

  it('names the container when given one', () => {
    expect(exec('prod', 'web-1', 'default', 'app')).toBe(
      'kubectl --context prod -n default exec -it web-1 -c app -- /bin/sh',
    )
  })

  it('accepts a custom command', () => {
    expect(exec('prod', 'web-1', 'default', 'app', ['/bin/bash', '-l'])).toBe(
      'kubectl --context prod -n default exec -it web-1 -c app -- /bin/bash -l',
    )
  })
})

describe('portForward', () => {
  it('builds pod/<pod> local:remote', () => {
    expect(portForward('prod', 'web-1', 'default', 8080, 80)).toBe(
      'kubectl --context prod -n default port-forward pod/web-1 8080:80',
    )
  })
})

describe('apply', () => {
  it('reads the manifest from stdin', () => {
    expect(apply('prod', 'default')).toBe('kubectl --context prod -n default apply -f -')
  })

  it('omits -n for a cluster-scoped object', () => {
    expect(apply('prod')).toBe('kubectl --context prod apply -f -')
  })
})

describe('revealSecretKey', () => {
  it('builds the jsonpath read piped through base64 -d', () => {
    expect(revealSecretKey('prod', 'db-creds', 'default', 'password')).toBe(
      "kubectl --context prod -n default get secret db-creds -o jsonpath='{.data.password}' | base64 -d",
    )
  })

  it('escapes the dots in a key so jsonpath reads one field, not a path', () => {
    // tls.crt is the commonest Secret key there is, and unescaped it asks
    // jsonpath for "crt" inside "tls" — a command that prints nothing and
    // teaches the wrong lesson.
    expect(revealSecretKey('prod', 'ingress-tls', 'web', 'tls.crt')).toBe(
      "kubectl --context prod -n web get secret ingress-tls -o jsonpath='{.data.tls\\.crt}' | base64 -d",
    )
  })
})
