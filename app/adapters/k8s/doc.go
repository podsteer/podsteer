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
package k8s
