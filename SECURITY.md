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
inside a container, and copy files into and out of one. It can **promote or
abort an Argo Rollouts `Rollout`** where that operator is installed — the
equivalent of `kubectl argo rollouts promote|abort`, sent as a merge patch of
the fields the controller reads, which changes what is serving traffic. It can
add an **ephemeral debug container** to a running pod (the equivalent of `kubectl
debug`) — which, being a Kubernetes ephemeral container, cannot be removed and
remains in the pod's spec until the pod is deleted — and it can create a
**privileged node-shell pod** that enters a node's host namespaces to open a
root shell on that node (the equivalent of `kubectl node-shell`); PodSteer
deletes that pod when its terminal closes or when it exits, and the pod carries
a one-hour `activeDeadlineSeconds` as a backstop for the case PodSteer cannot.
It can also **run one bounded connect attempt inside a container you name**, as
a reachability probe: a single `sh -c` that resolves a name and tries a TCP
connection, using whatever `nc`, `curl` or `wget` the image already has.
Nothing is created for it — no pod, no sidecar, no file — and it exits within
five seconds. It reads nothing and writes nothing, but it is an **exec into
somebody's container**, which is the same subresource a shell uses and appears
in your cluster's audit log as one, so it is treated as a write here: it is
refused on a cluster you marked read-only, and it leaves one line in PodSteer's
own log naming the cluster, namespace, pod, container and the address that was
probed — never what the probe found. The other half of that feature reaches your
cluster only through the API server your kubeconfig names, either through its
own service proxy or through a port-forward PodSteer opens and closes again.

It does all of this with the credentials your kubeconfig already grants, using
the same client library `kubectl` uses.

**PodSteer can also start a program on your own computer.** This is new, and it
is different in kind from everything above, so it is set out in full below
under "The local terminal, and the program it can start". Nothing in a cluster
is involved: it runs your login shell, or a coding agent you already have
installed, on this machine.

**The same binary is also an MCP server a coding agent can start**, with
`podsteer mcp`. It is set out in full below under "The MCP subprocess, and what
it can read". Two things about it are worth having here: it runs on stdio, so
it opens no port and serves nothing over a network; and every tool it offers is
a read — it cannot delete, scale, restart, apply, exec, port-forward or reveal
a Secret's values.

**PodSteer enforces no permissions of its own, and cannot.** It is a client. If
an account should not be able to delete a namespace, that has to be true in the
cluster's RBAC — there is no setting here that can make it so. Restricting what
PodSteer may do means restricting the credentials it runs with. The per-group
**read-only** toggle in Organise is the same story: it is a local guard against
your own mistakes, set on this machine and checked again by the backend as a
defence against the UI's own bugs, never a permission — turning it on does not
remove anything your credentials could otherwise do.

**The local terminal is deliberately outside that guard, and says so in the
pane.** The read-only setting governs what *PodSteer* writes to a cluster. A
shell you opened on your own machine, with your own credentials, is not
something this application can or should police: it does not sit between that
shell and anything, and a control claiming otherwise would be describing a
restriction that does not exist.

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

**No container registry is contacted, and that is worth saying explicitly**
because the feature it would serve exists. The image pane in a pod's drawer
describes a container's image — its reference, its digest, its size on the node
that pulled it, the other names that node knows it by — entirely from what
Kubernetes itself reports. The layers, the entrypoint and the labels are only in
the image's own manifest in a registry, and reading those would mean PodSteer
opening connections to third-party hosts and, for a private image, using a pull
Secret from your cluster to authenticate them. Neither is on the list above, so
neither happens: the pane states what it did not look at rather than quietly
looking. If that ever changes it will be off by default, per image, initiated by
you, and described here before it ships.

The webview still has no network access at all: a content security
policy in `web/index.html` forbids every remote origin, and all cluster traffic
goes through the Go process rather than the page. Three things are written to
your own machine and transmitted nowhere: sampled capacity history; a settings
file PodSteer itself acts on, described below; both under the per-user
application directory at mode 0600 (`~/Library/Application Support/PodSteer` on
macOS, `~/.config/PodSteer` on Linux, `%AppData%\PodSteer` on Windows); and
display preferences — theme, page size, column widths — which the interface
keeps in the webview's own storage rather than in that directory.

**The settings file is `settings.json` in that directory**, and it holds only
what the Go process itself has to act on before or without a window. What is in
it: how long capacity history is kept and how often it is sampled; the
kubeconfig files and folders you have added, **as paths** — never the contents
of a kubeconfig, never a credential, and never a cluster address; a proxy, if
you configure one; per-cluster switches, keyed by your kubeconfig context name;
and window positions. Everything else — theme, columns, groups, snoozed
findings, the namespace each cluster was last left on — stays in the webview's
own storage, and the two settings that hold OBJECT NAMES are deliberately among
them, so that the claim above about this file stays exhaustive.

How it behaves is as much of the answer as what it holds. It is rewritten
whole and atomically, into a temporary file in the same directory which is
synced and renamed over it, at mode 0600 restored on every write. It is **not**
part of the exported settings file below and the export never reads it. It is
read by `podsteer mcp`, which opens it read-only and writes nothing at all —
see "The MCP subprocess" below. A file that cannot be read is **set aside**
under an `.invalid-<timestamp>` name rather than overwritten, because whatever
you meant by a hand edit is not PodSteer's to destroy. And a file written by a
**newer** version of PodSteer is read for what this one understands and never
saved over, with one line in Settings saying so: an older build cannot know
where a newer one moved a setting to, and refusing to write is the only outcome
that cannot lose anything.

**One cluster-shaped thing is in that directory, in the file names.** A
history file is named after the kubeconfig context it records, sanitised for
the filesystem and suffixed with a short hash so two contexts differing only
in punctuation cannot collide — so somebody with access to your home directory
can see which clusters you have opened, though not what is in them. The
samples themselves hold capacity figures and nothing else: no object names, no
logs, no manifests, and no address or credential for any cluster. This is the
same disclosure `settings.json` and the exported settings file both make, for
the same reason: a context name is a handle your own kubeconfig already gives
you, and naming it is what makes the file readable to you.

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

The same feature is also one of the **three things PodSteer reads from your disk
that are not a kubeconfig**: a file or folder you chose in the native dialog,
uploaded into a container. It follows no symlink that leaves what you chose,
and it is refused on a cluster marked read-only, like every other write into a
cluster. Each transfer, either way, leaves one line in PodSteer's log naming
the cluster, namespace, pod, container, the path inside the container, the
direction and the byte count — never a file's contents, and never the local
path.

A fifth kind of write, and the third thing read from your disk, is the
**exported settings file** — Settings → Export & import. (The second is the
`settings.json` described above, which PodSteer both reads and writes; the two
are unrelated documents and each declares a different `kind` so that one
offered in place of the other is refused rather than misread.) It is the arrangement you have
made on this machine, in one JSON document you can keep in git or send to a
colleague: projects and groups with their environment, colour and read-only
marks, pinned kinds, saved column layouts and custom columns, thresholds,
refresh and appearance, remembered port-forward ports, and the debug and
node-shell image defaults. It goes through the same native save dialog and is
written at mode 0600, like everything else here.

What it carries is deliberately narrower than what PodSteer holds, because the
file is the one artefact here designed to be sent to somebody else:

- **No credentials, no kubeconfig contents, no cluster addresses, no tokens.**
  None of these exist outside the Go process's Kubernetes client, and nothing
  in the export reads them.
- **No object names.** No pod, node, namespace or workload appears in it. The
  two settings that do hold them — a snoozed finding, which is keyed by a
  namespace and an object name, and the namespace filter each cluster was last
  left on — are held back for exactly this reason. The export is an allowlist
  written out field by field rather than a copy of what is stored, and
  `web/src/lib/settingsFile.test.ts` populates every forbidden category and
  asserts none of it reaches the document, so this is a test rather than an
  intention.
- **It does carry your kubeconfig context names**, and nothing else about a
  cluster. A group cannot be marked read-only without naming the cluster it
  applies to. A context name is a handle your own kubeconfig already gives you
  and it identifies nothing inside a cluster — but anyone you send the file to
  will see which contexts you have. The file states this in its own header, and
  so does the pane, before you export rather than after.

Importing one is a review: what will change, what will be added and what will
be left alone, shown before anything is written, and applied only on confirm.
A malformed document is refused with the reason and never partly applied.
Nothing outside what the file carries is touched, even by Replace.

A sixth kind of write is a **desktop notification**, and it is counted as a
write on purpose: your operating system keeps the notifications it has shown
you — on macOS in Notification Centre, which is a database on disk, and on
Linux a notification daemon may log what it displayed. So the same rule
applies to one as to everything else in this list.

- **It is off until you turn it on**, in Settings → Notifications, and it is
  only ever raised for a **critical** finding that was not there on the
  previous refresh. A problem that was already there when you opened the
  cluster never raises one, a failed or partial refresh never raises one, and
  anything you have snoozed never raises one.
- **It carries no object names.** What it says is a count, the name of the
  rule that fired — "CrashLoopBackOff", written in PodSteer's own source —
  and your kubeconfig context name, on the same terms the settings file
  carries one. There is no pod, node, namespace or workload in it, there is
  no field in the request that could hold one, and there is a test asserting
  that field list so a new one cannot be added quietly.
- **A burst is one notification.** Twenty pods failing from the same event
  produce a single notification naming twenty, and one cluster raises at most
  one a minute.
- **Your Do Not Disturb still decides whether you see it.** PodSteer posts a
  notification and your operating system's notification centre applies your
  own Focus, Focus Assist or quiet-mode settings to it, exactly as it does for
  every other application. PodSteer does not attempt to read that state, and
  clicking a notification brings PodSteer forward on that cluster and does
  nothing else.
- **Permission is asked for when you turn it on**, never at startup, and the
  pane says so if your system has not granted it.

## The local terminal, and the program it can start

PodSteer can open a terminal running **a process on your own computer**, rather
than in a cluster. Everything else in this file is about requests to an API
server; this is not, so it is described on its own.

**What is started.** Your login shell — whatever `$SHELL` names — with `-l`, on
a pseudo-terminal, in your home directory. Or, if you choose one, a coding
agent CLI you already have installed: Claude Code, Codex, Gemini CLI or
Copilot CLI, started with an opening prompt naming the cluster tab and the
object you had open.

**Nothing is ever downloaded, bundled or installed.** PodSteer does not ship
kubectl, helm, or any coding agent, and never offers to fetch one. It looks for
binaries already on your PATH and runs those; a machine without them gets a
"command not found" from your own shell.

**What the process inherits.** PodSteer's own environment, with four
additions and no removals:

- `KUBECONFIG`, set to exactly the kubeconfig files PodSteer itself reads — the
  standard resolution, plus anything `PODSTEER_KUBECONFIG_DIR` names, plus the
  files and folders you listed under Settings → Kubeconfig — in the same order.
  Your files, named, never copied.
- `PODSTEER_CONTEXT`, naming the cluster tab that was in front. Informational:
  no Kubernetes tool reads it.
- `TERM` and `COLORTERM`, so the shell is not a dumb terminal.
- `PODSTEER_AGENT` and, only when you leave the read-only default on for an
  agent, `PODSTEER_AGENT_READ_ONLY`.

Everything else you had set is passed through unchanged, including the PATH
PodSteer adopted from your login shell at startup. That means the process has
the same credentials, cloud profiles and credential plugins your own terminal
does — because it is your own shell.

**Your kubeconfig is read, never written.** In particular `current-context` is
left exactly as it was, so kubectl in another terminal does not change target
because you opened a pane here. Since there is no environment variable kubectl
reads for a context, and writing either your kubeconfig or a per-session copy
of your credentials to disk is refused, the terminal prints a one-line notice
naming the context and telling you to pass `--context`. No file is written
anywhere.

**A coding agent has whatever access your kubeconfig grants.** Its opening
prompt says so in those words. The read-only default adds a request — keep to
read-only kubectl unless told otherwise — as a line in that prompt and a marker
in the environment. **It is a request, not a restriction.** The agent runs with
your credentials, and PodSteer cannot narrow them; only your cluster's RBAC can.
If that matters for a given cluster, use credentials that are read-only.

**PodSteer sends nothing anywhere.** Launching an agent is a local process
start. Whatever that agent then does with its own provider is between you and
the tool you installed, exactly as it is when you run it in your own terminal.
There is no PodSteer service in that path, no account, and no telemetry — the
same commitment the rest of this file makes.

**The process has an owner and an end.** It is ended when its pane closes and
when PodSteer exits, by signalling its whole process group, and PodSteer waits
for it to be gone rather than assuming.

**Not available on Windows.** There is no pseudo-terminal for it in this build;
the control is absent and says why, rather than failing when pressed.

## The MCP subprocess, and what it can read

`podsteer mcp` runs the same binary as a **Model Context Protocol server**, so
a coding agent you already use can read your clusters through PodSteer. Your
agent starts it; you do not run it by hand.

**It is a subprocess on stdio, not a server.** It binds no socket, opens no
port, serves nothing over HTTP and contacts nothing we operate. It talks JSON
over its own standard input and output, to the process that started it and to
nothing else, and it exists only for as long as that process keeps the pipe
open. There is no account and no telemetry here either — the same commitment
the rest of this file makes.

**Everything it offers is a read**, and that is structural rather than a
promise: the application code it is given carries no writing methods at all, so
there is no delete, scale, restart, apply, exec, port-forward, file copy,
manifest edit or node shell in it, and none can be added by writing a handler.
The reason there are none is that every write in the interface is guarded by a
confirmation an operator reads — a type-the-name gate, a drain preview, a bulk
review — and an agent cannot be shown one.

What it can read is what the interface already shows you: the clusters in your
kubeconfig, namespaces, kinds, pods, workloads, nodes, any kind at all as the
API server prints it, one object's manifest, a bounded tail of a container's
log, Kubernetes Events, the cluster assessment, a pod's assessment, the
dependency map, and the RBAC reviews.

**It has exactly your permissions.** Every read goes to the API server with
your kubeconfig, so the cluster's own RBAC decides what answers — the server
grants nothing your account does not already have. A refusal is reported as a
refusal naming what was refused, never as an empty list, so an agent cannot
conclude that a namespace is empty because it was not allowed to look.

**A Secret's values never leave**, whichever tool is called. A manifest read
through it has each value replaced by its decoded size before the object is
serialised — the same masking the YAML tab uses, applied in the same place —
and the two calls that can return key material (the per-key reveal and the TLS
certificate inspection) are not reachable from it at all. Tests assert both.

**Nothing is written anywhere.** No file, no kubeconfig — `current-context`
included — no history, and not PodSteer's own `settings.json` either: the
subprocess opens it READ-ONLY, so it creates no directory, saves no change and
does not even perform the one-off adoption of the pre-0.3 settings file that
the window does. That is structural rather than a promise anybody has to
remember, and there is a test asserting that a whole MCP composition leaves the
configuration directory byte-identical. Its log lines go to stderr, never to the transport,
and they name operations and errors rather than the contents of any answer.

The agent's own behaviour remains between you and the tool you installed, as
with the local terminal above: what it does with what it reads, and whatever
else it can reach with your credentials, is not something PodSteer mediates.

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
- A reachability probe running anything in a container other than the bounded
  connect attempt described above, running one in a container you did not name,
  or a probe's target reaching the shell as syntax rather than as data.
- PodSteer opening a connection to anything that is not an API server your
  kubeconfig names or, for the update check, `api.github.com` — a container
  registry included — or to anything other than a proxy you configured, on the
  way to one of those.
- The local terminal starting anything other than the shell or agent you chose,
  or the environment it is given carrying more than the variables listed above
  — in particular a kubeconfig being written, copied to disk, or having its
  `current-context` changed.
- A file downloaded from a container landing anywhere outside the folder you
  chose, or keeping a setuid or setgid bit — however the archive the
  container sent was crafted.
- The MCP subprocess doing anything but read: a tool that changes a cluster,
  a Secret's values reaching it, a network listener of any kind, or anything
  it writes to disk. Also anything by which the process that started it
  reaches a cluster your kubeconfig does not name, or a refusal being
  presented to the agent as an absence.
- An exported settings file containing anything the list above says it does
  not: a credential, a cluster address, or the name of any object in any
  cluster. The file is made to be shared, so anything that leaks into it
  leaks to whoever it was shared with.
- A credential of any kind reaching PodSteer's own `settings.json`, or that
  file carrying the name of any object in any cluster. It is not made to be
  shared, but it is exactly the file that ends up in a support bundle, a
  screenshot or a dotfile repository — so the same rule applies to it, and it
  is stated separately because a different piece of code writes it.
- A desktop notification carrying the name of any object in any cluster, or
  any Secret or credential material. Your operating system retains what it
  has shown you, so anything that reaches a notification reaches whatever
  keeps it.
- Supply-chain problems in what we ship: a compromised dependency in the
  inventory, or a release artefact that does not match its source.

### Out of scope

- Permissions your kubeconfig genuinely grants. PodSteer deleting a resource
  you asked it to delete, with credentials that allow it, is the product
  working.
- What you, or a coding agent you launched, then do in the local terminal. It
  is your shell with your credentials; the read-only request in an agent's
  prompt is a request, and an agent ignoring it is not a PodSteer
  vulnerability. Nor is a vulnerability in an agent CLI you installed — report
  that to whoever ships it.
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
