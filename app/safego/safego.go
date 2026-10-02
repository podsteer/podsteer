// Package safego keeps one panic in a background goroutine from taking the
// process down.
//
// Wails recovers a panic in a BOUND METHOD and nothing else. Every goroutine
// this application starts itself — the history sampler, the reflectors, the
// port-forward supervisors, the terminal pumps — is outside that net, and a
// nil dereference in any of them ends the process and with it every
// port-forward and shell the operator had open. A recovered panic is logged
// with its stack and the goroutine ends; it is never re-raised.
//
// Standard library only, so any layer may import it.
package safego

import (
	"fmt"
	"log/slog"
	"runtime/debug"
)

// Recover is for `defer safego.Recover("what")` at the top of a goroutine. It
// must be deferred directly: recover() only works in the deferred function
// itself, which is why this is not a helper called from one.
func Recover(what string) {
	if r := recover(); r != nil {
		report(what, r)
	}
}

// Run calls fn and reports whether it panicked. For the body of a loop that
// must survive one bad iteration — one cluster's assessment failing must not
// stop the sampler for the others.
func Run(what string, fn func()) (panicked bool) {
	defer func() {
		if r := recover(); r != nil {
			report(what, r)
			panicked = true
		}
	}()
	fn()
	return false
}

// Error converts a recovered value into an error carrying the stack, for the
// caller that has somebody waiting on an answer. It logs as well.
func Error(what string, r any) error {
	report(what, r)
	return fmt.Errorf("%s: panic: %v", what, r)
}

func report(what string, r any) {
	slog.Error("recovered panic",
		slog.String("where", what),
		slog.String("panic", fmt.Sprint(r)),
		slog.String("stack", string(debug.Stack())))
}
