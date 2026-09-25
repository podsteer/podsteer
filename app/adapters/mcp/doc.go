// Package mcp serves PodSteer's reads to a coding agent over the Model
// Context Protocol, as a local subprocess speaking JSON-RPC on stdio.
//
// # It is a subprocess, not a server
//
// The agent spawns `podsteer mcp` itself and owns the pipes. No socket is
// bound, no port is opened, nothing is served over HTTP, and nothing PodSteer
// operates is contacted — the only traffic is the same cluster traffic the
// desktop window makes, with the same kubeconfig. That is what keeps this
// consistent with the no-account, no-telemetry commitment in CLAUDE.md and
// SECURITY.md: a listener here, even one bound to loopback, would make
// PodSteer reachable by anything else running on the machine and would need
// an authentication story this application has deliberately never had.
//
// It is also why this is a subcommand of the same binary rather than a
// background service the window starts. A server the desktop app ran would
// have to be discovered, would outlive the tab whose credentials it was
// using, and would be running whether or not anybody was asking it anything.
// A subprocess starts when the agent starts, ends when the agent ends, and is
// visible in the process list for exactly as long as it exists.
//
// # Every tool is a read, and the type system says so
//
// The use cases this package is handed are narrowed to reading interfaces —
// ClusterReader, ResourceReader and the rest below. That is not
// documentation: ports.ClusterService also carries AddKubeconfig, and
// ports.ResourceService carries RevealSecretKey, and a server that cannot
// NAME those methods cannot call them however a tool is later written.
//
// There is no delete, scale, apply, exec or port-forward here, and adding one
// would not be a matter of writing a handler. Every write in the UI is
// guarded by a confirmation an operator reads — a type-the-name gate on a
// production rollout, a drain preview, a bulk review dialog — and an agent
// cannot be shown one. The honest choices are a write with no confirmation or
// a confirmation nobody sees, so there are no writes at all.
//
// # RBAC decides, and a refusal arrives as a refusal
//
// Every tool goes through the same use cases the window calls, with the
// operator's own kubeconfig, so this grants nothing the account did not
// already have. A 403 comes back as a refusal naming what was refused rather
// than as an empty list: an agent handed an empty array concludes the cluster
// holds no such objects and says so confidently, which is the worst answer
// available. See toolFailure.
//
// # Secrets
//
// A Secret's values never leave, whichever tool is called. Manifests are read
// with revealSecrets false, so each value is replaced by its decoded size
// before anything is serialised (maskSecretData, app/adapters/k8s/resource.go),
// and there is no tool that reveals a key or parses a TLS Secret — the two
// calls that can return key material are absent from ResourceReader entirely.
package mcp
