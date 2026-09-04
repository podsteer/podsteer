# Security Policy

## Reporting a vulnerability

Please report privately, through GitHub's
[private vulnerability reporting](https://github.com/podsteer/podsteer/security/advisories/new)
— the **Security → Report a vulnerability** button on this repository. It opens
a channel visible only to the maintainers, so nothing is disclosed while a fix
is being prepared.

Do not open a public issue for a vulnerability, and please do not report one by
posting a proof of concept anywhere public first.

Include what you would want to receive: what an attacker can do, how to
reproduce it, the PodSteer version, and your platform. You can expect an
acknowledgement within a few days and an honest estimate of when a fix will
ship. We will credit you in the advisory unless you would rather we did not.

## Supported versions

PodSteer has not had a stable release yet. Until `v1.0.0`, only the latest
release is supported, and fixes ship in the next release rather than as
backports.

## What PodSteer can do, and what that means

This is worth stating plainly, because it shapes what counts as a
vulnerability.

**PodSteer both reads and writes.** It lists and inspects resources, and it can
also delete objects, scale and restart workloads, apply edited manifests, write
a single decoded key of a Secret or a ConfigMap, open an interactive shell
inside a container, and copy files into and out of one. It can add an
**ephemeral debug container** to a running pod (the equivalent of `kubectl
debug`) — which, being a Kubernetes ephemeral container, cannot be removed and
remains in the pod's spec until the pod is deleted — and it can create a
**privileged node-shell pod** that enters a node's host namespaces to open a
root shell on that node (the equivalent of `kubectl node-shell`); PodSteer
deletes that pod when its terminal closes or when it exits, and the pod carries
a one-hour `activeDeadlineSeconds` as a backstop for the case PodSteer cannot.
It does all of this with the credentials your kubeconfig already grants, using
the same client library `kubectl` uses.

**PodSteer enforces no permissions of its own, and cannot.** It is a client. If
an account should not be able to delete a namespace, that has to be true in the
cluster's RBAC — there is no setting here that can make it so. Restricting what
PodSteer may do means restricting the credentials it runs with. The per-group
**read-only** toggle in Organise is the same story: it is a local guard against
your own mistakes, set on this machine and checked again by the backend as a
defence against the UI's own bugs, never a permission — turning it on does not
remove anything your credentials could otherwise do.

**It talks to your clusters, and to GitHub only if you let it.** No account and
no telemetry — those remain absolute, and there is no code here that could send
either.

The one exception is an **update check**, added in v0.1.2. It asks
`api.github.com` once a day whether a newer release has been published.

**This is new in v0.1.2**, and it is called out because v0.1.0 and v0.1.1
stated the opposite here: anyone who reviewed those releases against this file
should re-read the list below rather than assume it still applies.

What the check does and does not do:

- **It sends no identifier.** No installed version, no platform, no machine id,
  no query string. GitHub requires a `User-Agent` and refuses requests without
  one, so `podsteer` is sent and nothing else — deliberately not the version,
  which would turn every check into a report of what you are running. The
  comparison happens locally, on the answer.
- **It goes to GitHub, not to us.** We have no access to `api.github.com`'s
  logs, so this produces no data anyone here can hold, correlate, or be
  compelled to produce. A PodSteer-operated endpoint would produce all three,
  which is why there isn't one.
- **It is unauthenticated**, so there is no credential at rest.
- **It never runs on the startup path** and nothing waits on it. It cannot
  delay or prevent the application starting.
- **Turn it off two ways.** Settings → Notifications switches it off for you.
  `PODSTEER_UPDATE_CHECK=false` switches it off for the whole machine,
  regardless of that setting, for packagers and for deny-by-default egress
  policies. Off means no request is made — not a request that is discarded —
  and there are tests in `app/application/updates_test.go` and
  `web/src/stores/updates.test.ts` asserting exactly that.
- **PodSteer never installs anything.** The badge opens the release page in
  your browser. It does not download, replace its own binary, or run an
  installer.

The webview still has no network access at all: a content security
policy in `web/index.html` forbids every remote origin, and all cluster traffic
goes through the Go process rather than the page. Two things are written to
your own machine and transmitted nowhere: sampled capacity history and its
retention setting, under the per-user application directory at mode 0600
(`~/Library/Application Support/PodSteer` on macOS, `~/.config/PodSteer` on
Linux, `%AppData%\PodSteer` on Windows); and display preferences — theme,
page size, column widths — which the interface keeps in the webview's own
storage rather than in that directory.

A third kind of write is a CSV export, only where you choose to save it,
containing exactly the rows and columns a table is showing you at the moment
you export it — filtered by whatever you searched for, in whatever order you
sorted them, limited to the columns you have not hidden. It goes through the
same native save dialog `kubectl` and every other desktop tool uses, is
written at mode 0600 like everything else here, and is never written anywhere
PodSteer chose on its own.

A fourth kind of write is a **file downloaded from a container** —
`kubectl cp`, from the pod drawer. It is worth more words than
the others, because what gets written is decided by the container, not by
you: the container runs `tar` and PodSteer unpacks the stream it sends. That
stream lands only inside a folder you chose in the native dialog, and only
after every entry has been checked in Go, never in the interface: an absolute
name, a `..` component or a symlink pointing outside the chosen folder ends
the transfer with an error naming the entry; a file is never written through
a symlink already in that folder; setuid and setgid bits are never
reproduced; and a transfer stops at 1 GiB or 100,000 entries unless
`PODSTEER_COPY_MAX_BYTES` and `PODSTEER_COPY_MAX_ENTRIES` say otherwise. The
tests for each of those are in `app/adapters/archive/archive_test.go`.

The same feature is also the **one thing PodSteer reads from your disk that is
not a kubeconfig**: a file or folder you chose in the native dialog, uploaded
into a container. It follows no symlink that leaves what you chose, and it is
refused on a cluster marked read-only, like every other write into a cluster.
Each transfer, either way, leaves one line in PodSteer's log naming the
cluster, namespace, pod, container, the path inside the container, the
direction and the byte count — never a file's contents, and never the local
path.

### In scope

- Anything that lets PodSteer act on a cluster beyond what the loaded
  kubeconfig authorises, or act on a cluster the user did not select.
- Exposure of kubeconfig contents, bearer tokens, or credential-plugin output —
  in logs, in the recorded history, in an error surfaced to the frontend, or
  anywhere on disk.
- A Secret value — including a TLS Secret's private key, when its certificate
  is inspected — resolved anywhere other than the deliberate, per-key
  reveal or per-Secret inspection the operator asked for.
- A bypass of the webview CSP, or any path by which page content reaches the
  network directly.
- Injection through cluster-controlled data — resource names, labels,
  annotations, log output or an API server's table columns rendered in a way
  that executes, or that escapes into the terminal or the manifest editor.
- Code execution from opening a manifest, a log stream, or an exec session.
- A file downloaded from a container landing anywhere outside the folder you
  chose, or keeping a setuid or setgid bit — however the archive the
  container sent was crafted.
- Supply-chain problems in what we ship: a compromised dependency in the
  inventory, or a release artefact that does not match its source.

### Out of scope

- Permissions your kubeconfig genuinely grants. PodSteer deleting a resource
  you asked it to delete, with credentials that allow it, is the product
  working.
- Vulnerabilities in Kubernetes itself, in your cluster's configuration, or in
  the operating system's webview — report those upstream, though we would still
  like to hear if PodSteer makes one materially worse.
- Anything requiring an attacker who already has local access to your user
  account. At that point they have your kubeconfig regardless of PodSteer.
- Findings from an automated scanner with no demonstrated impact.

## Supply chain

Every release publishes a CycloneDX SBOM, and the dependency inventory shipped
in the application under **Settings → Credits** is generated and licence-checked
by the same build gate (`make notices`, enforcing
[docs/LICENCE-POLICY.md](docs/LICENCE-POLICY.md)). A dependency whose licence
cannot be classified stops the build rather than being omitted from the
inventory.
