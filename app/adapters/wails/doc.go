// Package wails is the driving (inbound) adapter that exposes the PodSteer use
// cases to the Svelte frontend.
//
// Wails binds the exported methods of the structs it is given and generates
// TypeScript declarations for them, so the types and signatures in this
// package *are* the frontend API contract. Two consequences follow, and both
// shape the code here:
//
//   - Domain types never cross this boundary. The DTOs in dto.go are the wire
//     format, which keeps the frontend from being recompiled every time an
//     internal model is refined, and lets the UI receive values already shaped
//     for display (a "2/3" ready count, an age in seconds) instead of
//     re-deriving them in JavaScript.
//
//   - Bound methods receive no context.Context — Wails calls them with the
//     arguments JavaScript passed and nothing else. Each therefore derives its
//     own deadline from the application-lifetime context captured at startup,
//     so a request cannot outlive the window it was made from.
//
// The adapter also implements ports.EventPublisher, turning domain events into
// Wails events the frontend subscribes to by name.
package wails
