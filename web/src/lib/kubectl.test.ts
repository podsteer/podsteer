import { describe, expect, it } from 'vitest'
import {
  apply,
  attach,
  applyDryRun,
  debug,
  debugNode,
  cordon,
  cpFromPod,
  cpToPod,
  del,
  delMany,
  describe as describeCmd,
  exec,
  get,
  getYaml,
  helmGetValues,
  helmHistory,
  helmRollback,
  helmUninstall,
  helmUpgrade,
  logs,
  portForward,
  resourceArg,
  resourceArgForKind,
  revealSecretKey,
  rolloutRestart,
  rolloutRestartMany,
  rolloutUndo,
  scale,
  scaleMany,
  setImage,
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

describe('setImage', () => {
  it('lowercases the kind and always carries the namespace', () => {
    expect(setImage('prod', 'Deployment', 'web', 'default', 'app', 'nginx:1.25')).toBe(
      'kubectl --context prod -n default set image deployment/web app=nginx:1.25',
    )
  })

  it('leaves a digest reference unquoted', () => {
    expect(
      setImage(
        'prod',
        'StatefulSet',
        'db',
        'data',
        'db',
        'postgres@sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b85',
      ),
    ).toBe(
      'kubectl --context prod -n data set image statefulset/db db=postgres@sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b85',
    )
  })

  it('quotes an image containing a space', () => {
    expect(setImage('prod', 'DaemonSet', 'agent', 'kube-system', 'agent', 'my registry/app:v1')).toBe(
      "kubectl --context prod -n kube-system set image daemonset/agent agent='my registry/app:v1'",
    )
  })
})

describe('rolloutUndo', () => {
  it('lowercases the kind and always carries the namespace', () => {
    expect(rolloutUndo('prod', 'Deployment', 'web', 'default', 3)).toBe(
      'kubectl --context prod -n default rollout undo deployment/web --to-revision=3',
    )
  })

  it('names statefulset and daemonset the same way', () => {
    expect(rolloutUndo('prod', 'StatefulSet', 'db', 'data', 1)).toBe(
      'kubectl --context prod -n data rollout undo statefulset/db --to-revision=1',
    )
    expect(rolloutUndo('prod', 'DaemonSet', 'agent', 'kube-system', 2)).toBe(
      'kubectl --context prod -n kube-system rollout undo daemonset/agent --to-revision=2',
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

describe('delMany', () => {
  it('names every object on one line within a namespace', () => {
    expect(
      delMany('prod', 'pods', [
        { name: 'a', ns: 'default' },
        { name: 'b', ns: 'default' },
        { name: 'c', ns: 'default' },
      ]),
    ).toBe('kubectl --context prod -n default delete pods a b c')
  })

  it('emits one line per namespace, in the order each first appears', () => {
    // kubectl takes one -n per invocation: a selection made under "All
    // namespaces" is one command per namespace it spans, never one command
    // with three flags.
    expect(
      delMany('prod', 'pods', [
        { name: 'a', ns: 'web' },
        { name: 'b', ns: 'data' },
        { name: 'c', ns: 'web' },
      ]),
    ).toBe('kubectl --context prod -n web delete pods a c\nkubectl --context prod -n data delete pods b')
  })

  it('omits -n for cluster-scoped objects', () => {
    expect(delMany('prod', 'nodes', [{ name: 'node-1' }, { name: 'node-2' }])).toBe(
      'kubectl --context prod delete nodes node-1 node-2',
    )
  })
})

describe('scaleMany', () => {
  it('names every workload as kind/name with one --replicas flag', () => {
    expect(
      scaleMany('prod', 'Deployment', [{ name: 'web', ns: 'default' }, { name: 'api', ns: 'default' }], 0),
    ).toBe('kubectl --context prod -n default scale deployment/web deployment/api --replicas=0')
  })
})

describe('rolloutRestartMany', () => {
  it('names every workload as kind/name, one line per namespace', () => {
    expect(
      rolloutRestartMany('prod', 'StatefulSet', [{ name: 'db', ns: 'data' }, { name: 'cache', ns: 'web' }]),
    ).toBe(
      'kubectl --context prod -n data rollout restart statefulset/db\nkubectl --context prod -n web rollout restart statefulset/cache',
    )
  })
})

describe('cordon', () => {
  it('names every node on one line, with no namespace', () => {
    expect(cordon('prod', ['node-1', 'node-2'], true)).toBe('kubectl --context prod cordon node-1 node-2')
  })

  it('is uncordon when switched off', () => {
    expect(cordon('prod', ['node-1'], false)).toBe('kubectl --context prod uncordon node-1')
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

describe('attach', () => {
  it('defaults to no container flag', () => {
    expect(attach('prod', 'web-1', 'default')).toBe('kubectl --context prod -n default attach -it web-1')
  })

  it('names the container when given one, with no trailing command the way exec has', () => {
    // attach connects to the container's own process rather than starting a
    // new one, so there is no `-- <command>` for it to carry.
    expect(attach('prod', 'web-1', 'default', 'app')).toBe(
      'kubectl --context prod -n default attach -it web-1 -c app',
    )
  })
})

describe('debug', () => {
  it('builds the ephemeral debug command with an image and a target', () => {
    expect(debug('prod', 'web-1', 'default', 'busybox:1.37', 'app')).toBe(
      'kubectl --context prod -n default debug -it web-1 --image=busybox:1.37 --target=app -- sh',
    )
  })

  it('omits --target when nothing is targeted', () => {
    expect(debug('prod', 'web-1', 'default', 'busybox:1.37')).toBe(
      'kubectl --context prod -n default debug -it web-1 --image=busybox:1.37 -- sh',
    )
  })

  it('quotes an image an operator could paste something surprising into', () => {
    expect(debug('prod', 'web-1', 'default', 'my image', 'app')).toContain("--image='my image'")
  })

  it('accepts a custom command', () => {
    expect(debug('prod', 'web-1', 'default', 'busybox:1.37', 'app', ['bash', '-l'])).toBe(
      'kubectl --context prod -n default debug -it web-1 --image=busybox:1.37 --target=app -- bash -l',
    )
  })
})

describe('debugNode', () => {
  it('builds the node debug command with the sysadmin profile and no namespace', () => {
    expect(debugNode('prod', 'node-1', 'docker.io/library/alpine:3.20')).toBe(
      'kubectl --context prod debug node/node-1 -it --image=docker.io/library/alpine:3.20 --profile=sysadmin',
    )
  })

  it('is cluster-scoped — it never carries -n', () => {
    expect(debugNode('prod', 'node-1', 'alpine:3.20')).not.toContain('-n ')
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

describe('applyDryRun', () => {
  it('adds --dry-run=server after the same apply -f - as apply()', () => {
    expect(applyDryRun('prod', 'default')).toBe(
      'kubectl --context prod -n default apply -f - --dry-run=server',
    )
  })

  it('omits -n for a cluster-scoped object', () => {
    expect(applyDryRun('prod')).toBe('kubectl --context prod apply -f - --dry-run=server')
  })

  it('is server-side, not client-side — a client dry run only checks the manifest parses', () => {
    expect(applyDryRun('prod', 'default')).not.toContain('--dry-run=client')
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

describe('cpFromPod', () => {
  it('names the pod-side source and the full local destination', () => {
    expect(cpFromPod('prod', 'web-0', 'default', '/etc/nginx', '/Users/me/Downloads/nginx', 'app')).toBe(
      'kubectl --context prod -n default cp web-0:/etc/nginx /Users/me/Downloads/nginx -c app',
    )
  })

  it('omits -c when no container is named', () => {
    expect(cpFromPod('prod', 'web-0', 'default', '/etc/hosts', '/tmp/hosts')).toBe(
      'kubectl --context prod -n default cp web-0:/etc/hosts /tmp/hosts',
    )
  })

  it('quotes a local path with a space in it', () => {
    // A Downloads folder called "My Files" is the ordinary case on a
    // desktop, and unquoted it would be two arguments.
    expect(cpFromPod('prod', 'web-0', 'default', '/etc/hosts', '/Users/me/My Files/hosts')).toBe(
      "kubectl --context prod -n default cp web-0:/etc/hosts '/Users/me/My Files/hosts'",
    )
  })
})

describe('cpToPod', () => {
  it('names the local source and the full pod-side destination', () => {
    expect(cpToPod('prod', 'web-0', 'default', '/Users/me/config.yaml', '/app/config.yaml', 'app')).toBe(
      'kubectl --context prod -n default cp /Users/me/config.yaml web-0:/app/config.yaml -c app',
    )
  })

  it('quotes a remote path with a space in it', () => {
    expect(cpToPod('prod', 'web-0', 'default', '/tmp/x', '/data/my dir/x')).toBe(
      "kubectl --context prod -n default cp /tmp/x 'web-0:/data/my dir/x'",
    )
  })
})

describe('helmRollback', () => {
  it('names the release and the revision, with helm flag spelling', () => {
    // `--kube-context`, not kubectl's `--context`: helm is its own binary with
    // its own flag parser, and the wrong spelling is a command that does not
    // run.
    expect(helmRollback('prod', 'ingress-nginx', 'ingress', 4)).toBe(
      'helm rollback ingress-nginx 4 -n ingress --kube-context prod',
    )
  })

  it('quotes a context containing a space', () => {
    expect(helmRollback('my cluster', 'ingress-nginx', 'ingress', 4)).toBe(
      "helm rollback ingress-nginx 4 -n ingress --kube-context 'my cluster'",
    )
  })

  it('renders the revision as a plain number rather than helm\'s 0 shorthand', () => {
    // A specific revision is always what the strip is shown beside; 0 means
    // "the previous release" to helm and would be a different act.
    expect(helmRollback('prod', 'web', 'shop', 1)).toContain(' web 1 ')
  })
})

describe('helmUninstall', () => {
  it('names the release and its namespace', () => {
    expect(helmUninstall('prod', 'ingress-nginx', 'ingress')).toBe(
      'helm uninstall ingress-nginx -n ingress --kube-context prod',
    )
  })

  it('quotes a context containing a space', () => {
    expect(helmUninstall('my cluster', 'web', 'shop')).toBe(
      "helm uninstall web -n shop --kube-context 'my cluster'",
    )
  })

  it('adds no flag PodSteer chose on the operator\'s behalf', () => {
    expect(helmUninstall('prod', 'web', 'shop')).not.toContain('--keep-history')
  })
})

describe('helmGetValues', () => {
  it('writes the supplied values to the file the upgrade reads', () => {
    // The two halves have to agree on the filename, so the redirect is part
    // of the command rather than an instruction beside it.
    expect(helmGetValues('prod', 'ingress-nginx', 'ingress')).toBe(
      'helm get values ingress-nginx -n ingress --kube-context prod > values.yaml',
    )
  })

  it('asks for the SUPPLIED values, never --all', () => {
    // `--all` merges in every default the chart happens to ship today, which
    // would pin them into the operator's own values file forever.
    expect(helmGetValues('prod', 'web', 'shop')).not.toContain('--all')
  })

  it('quotes a context containing a space', () => {
    expect(helmGetValues('my cluster', 'web', 'shop')).toContain("--kube-context 'my cluster'")
  })
})

describe('helmUpgrade', () => {
  it('names the release, the chart, and the values file that keeps the upgrade honest', () => {
    // THE `-f` IS THE POINT. Helm 3 does not carry the previous release's
    // values forward: an upgrade run without them reverts every value the
    // operator ever set, in a command that looks like it only changed a
    // version.
    expect(helmUpgrade('prod', 'ingress-nginx', 'ingress', 'ingress-nginx')).toBe(
      'helm upgrade ingress-nginx REPO/ingress-nginx --version VERSION -n ingress ' +
        '--kube-context prod -f values.yaml',
    )
  })

  it('uses capitals for what PodSteer cannot know, not angle brackets', () => {
    // A copied command is pasted. `<repo>` is a shell redirection and fails
    // with a syntax error about a file; REPO reaches helm, which says the
    // repository is not found — naming the thing to fill in.
    const command = helmUpgrade('prod', 'web', 'shop', 'my-chart')
    expect(command).not.toContain('<')
    expect(command).not.toContain('>')
    expect(command).toContain('REPO/my-chart')
    expect(command).toContain('--version VERSION')
  })

  it('makes the chart a placeholder too when no revision has been read', () => {
    // The release list carries no chart name — it is not a label, and lives
    // only inside a payload somebody explicitly read. See HelmChartIdentity.
    expect(helmUpgrade('prod', 'web', 'shop', '')).toContain('REPO/CHART')
  })

  it('adds no --reuse-values on the operator\'s behalf', () => {
    // It is not equivalent to the values file: --reuse-values keeps the old
    // values AND the old computed defaults, which is a different upgrade and
    // one Helm's own documentation warns about when the chart changed.
    expect(helmUpgrade('prod', 'web', 'shop', 'chart')).not.toContain('--reuse-values')
  })

  it('quotes a context containing a space', () => {
    expect(helmUpgrade('my cluster', 'web', 'shop', 'chart')).toContain(
      "--kube-context 'my cluster'",
    )
  })
})

describe('helmHistory', () => {
  it('names the release and its namespace', () => {
    expect(helmHistory('prod', 'ingress-nginx', 'ingress')).toBe(
      'helm history ingress-nginx -n ingress --kube-context prod',
    )
  })

  it('quotes a context containing a space', () => {
    expect(helmHistory('my cluster', 'web', 'shop')).toBe(
      "helm history web -n shop --kube-context 'my cluster'",
    )
  })
})
