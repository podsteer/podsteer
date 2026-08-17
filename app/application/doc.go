// Package application holds the K8Sense use cases.
//
// A use case orchestrates: it validates input, calls outbound ports in the
// right order, applies the domain's rules to the results and publishes events.
// It contains no business rules of its own — those belong to the domain — and
// no knowledge of infrastructure — that belongs to the adapters.
//
// Everything here depends on the interfaces in app/ports and on app/domain,
// and on nothing else. That is what makes the layer testable with hand-written
// fakes and no cluster in sight.
//
// # Concurrency
//
// The services are safe for concurrent use. A desktop UI fires overlapping
// requests routinely — the operator switches namespace while a pod list is
// still loading — so every service is designed to be a long-lived singleton
// shared by all callers.
package application
