# 3. Secret values are read only when somebody asks

Decided 2026-08-31. Status: **accepted**.

## Context

A pod's environment variables routinely come from Secrets, and an operator
debugging one genuinely needs to see a value. Every client in this category
offers that, and each of them gets some part of it wrong in a way worth
learning from.

**Lens and Freelens render the raw base64 in a plain text input and treat
that as masking.** Base64 is an encoding, not a cipher: a screenshot of that
pane has leaked the credential to anybody who can type `base64 -d`. Their
reveal for a `secretKeyRef` is also one-way — once shown, the control is
replaced by the value with no way back short of remounting the pane — and the
reveal state is global, so a value unmasked in private is still unmasked when
an unrelated pod is opened in front of an audience, showing the wrong value
besides (`freelensapp/freelens#2416`).

**The Kubernetes Dashboard resolved values server-side into the response
body**, so the material was in the JSON before the UI decided to hide it, and
was resolved with the dashboard's own permissions rather than the viewer's.

**Headlamp does not resolve at all** — it renders a link to the Secret — which
is the safest of the five, but it has an open issue where a user with `list`
but not `get` sees the value flash on screen before the authorisation error
arrives (`#2790`).

Three constraints shaped what we built instead.

**Reading a Secret is an audited action.** Kubernetes' own Secret
good-practices page tells cluster operators to "implement audit rules that
alert on specific events, such as concurrent reading of multiple Secrets by a
single user", and Falco ships a rule for it, enabled, at ERROR severity. A
client that resolves every Secret a pod references when a pane opens produces
exactly that signature on somebody else's security dashboard — Freelens fires
one GET per referenced Secret on open, and the Dashboard LISTs every Secret in
the namespace.

**Many engineers deliberately hold no `get secrets`.** A pane that requires it
to render is a pane that fails for the people whose permissions are configured
correctly.

**The value in a Secret is not the value in the process.** Environment
variables are injected when a container starts and are never updated. A Secret
edited since has a current value the running process has never seen, so
displaying it labelled as the pod's environment is wrong, not merely risky.

## Decision

**Nothing is read unless somebody clicks.** The default rendering is
`kubectl describe`'s: `<set to the key 'pw' in secret 'creds'>`. No API call,
no permission required, and the form operators already recognise.

**One key crosses the boundary.** `RevealSecretKey` fetches the Secret because
the API offers nothing narrower, and discards every other key in the adapter —
not in the application, not in the UI — so a value never travels through
layers that could log, cache or serialise it. The typed client is used so
client-go does the decoding and no encoded copy exists in the process either.

**Nothing renders before authorisation resolves**, so a denial shows a denial
and never a glimpse.

**Reveal is re-hideable and expires** — after thirty seconds, and on window
blur, which is when a screen share usually starts.

**Nothing is ever rendered encoded.** In the YAML tab a Secret's values are
replaced in the adapter with their decoded size — `<hidden, 8 bytes>`, the
form `kubectl describe secret` prints — before the object is serialised.
Editing is blocked while they are hidden, because saving would write the
placeholders over the real values.

**Literal values that look like credentials are masked with a warning rather
than a reveal control.** An AWS key or a JWT pasted into `env[].value` sits in
the pod spec, readable by anyone with `get pod`; there is nothing to unlock,
and the honest thing to say is that the spec is carrying a secret in the
clear. No other client does this, because Kubernetes does not consider it a
secret. The detection is deliberately narrow — masking half an environment as
suspected credentials would train people to reveal everything by reflex.

## What this rules out

Resolving `configMapKeyRef` eagerly, which Freelens and Aptakube both do. A
ConfigMap is not a Secret, but resolving it still means a read on pane open,
and the same "what the object holds now is not what the process was started
with" objection applies unchanged.

Masking anything other than core/v1 Secrets. Guessing which fields of an
arbitrary kind are sensitive would mask arbitrarily and still miss what
matters; a ConfigMap holding a password is returned untouched, and a test
says so, because the alternative is somebody later turning this into a
heuristic.

## What is still open

Showing that a Secret has changed since the container started would be a
genuinely novel finding — `resourceVersion` against `state.running.startedAt`
— and it was on the list until it became clear that reading the
`resourceVersion` means reading the Secret. That fires the audit pattern this
decision exists to avoid, so it is not built.
