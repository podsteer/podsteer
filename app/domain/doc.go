// Package domain holds the K8Sense core model: entities, value objects and
// domain events.
//
// This is the innermost layer of the hexagon. It describes what a cluster, a
// namespace and a pod *are* in the language of the application, independently
// of how that information happens to be obtained or displayed.
//
// # Dependency rule
//
// This package MUST NOT import anything outside the Go standard library. In
// particular it must never reach for k8s.io/client-go, Wails, a logger, or a
// serialisation library. Dependencies point inward: adapters know about the
// domain, the domain knows about nobody.
//
// Concretely this means the Kubernetes API types never appear here. Adapters
// translate them at the boundary (see app/adapters/k8s), which keeps a
// client-go upgrade from rippling through the model.
//
// # Immutability
//
// Entities are modelled as immutable values. Constructors validate invariants
// and return a value; state transitions return a modified copy (the With*
// methods) rather than mutating in place. Read models fetched from a cluster
// are snapshots by nature, and immutability makes them safe to hand to the
// UI layer and to share across goroutines without locking.
package domain
