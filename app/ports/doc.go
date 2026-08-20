// Package ports declares the boundaries of the PodSteer hexagon.
//
// Two families of interface live here, and the distinction is the whole point
// of the pattern:
//
//   - Inbound (driving) ports — inbound.go — are what the application offers
//     to the outside world. The application layer implements them; driving
//     adapters such as the Wails bridge depend on them. They exist so the UI
//     can be swapped (a CLI, a test harness) without the use cases noticing.
//
//   - Outbound (driven) ports — outbound.go — are what the application needs
//     from the outside world. The application layer declares and consumes
//     them; driven adapters such as the client-go implementation satisfy them.
//     They exist so infrastructure can be swapped — a fake cluster in tests,
//     a different Kubernetes client — without the use cases noticing.
//
// The rule that makes this work: interfaces are declared by the consumer, in
// the consumer's vocabulary. That is why ports speak only in domain types and
// this package, like the domain, imports nothing beyond the standard library.
package ports
