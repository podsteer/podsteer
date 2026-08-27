// Package k8s is the driven (outbound) adapter that satisfies the Kubernetes
// ports using k8s.io/client-go and k8s.io/cli-runtime.
//
// It is the only package in PodSteer allowed to know that Kubernetes has an API
// server, a kubeconfig or a corev1.Pod. Everything it returns has already been
// translated into the domain model, so a client-go major upgrade is contained
// here (see mapper.go, which is the anti-corruption layer).
//
// # Performance
//
// The adapter carries most of the work behind PodSteer's resource footprint
// goal, so three decisions are deliberate:
//
//   - Clients are built once per cluster and cached (client.go). Constructing
//     one re-reads the kubeconfig and may re-run an exec credential plugin —
//     a process spawn — which is far too expensive to repeat per keystroke.
//
//   - Requests negotiate protobuf rather than JSON. For core/v1 lists this is
//     both markedly faster to decode and much lighter on allocations than the
//     JSON path, which matters when a namespace holds thousands of pods.
//
//   - client-go's default rate limit (5 QPS) is raised, because it is sized
//     for controllers rather than for a UI that fans out on navigation.
//
// # JSON
//
// This package stays on encoding/json (v1) deliberately, and should not be
// migrated to encoding/json/v2 the way app/adapters/history was.
//
// Its JSON is not PodSteer's own: it is the Kubernetes wire format, decoded
// into k8s.io/api types and — for RestartRollout — sent back to a live API
// server as a strategic merge patch. Those types are built against v1's
// semantics, and v2 deliberately changes several of them: field matching is
// case-sensitive, duplicate object names are rejected, and `omitempty` no
// longer means "omit the zero value". A patch body is exactly where a silent
// difference in what gets omitted stops being a decoding curiosity and starts
// being a mutation sent to somebody's production cluster.
//
// The upside would be small in any case: the hot paths here negotiate
// protobuf, not JSON, for the reason given above.
package k8s
