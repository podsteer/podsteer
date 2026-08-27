package wails

import (
	"sync"
	"testing"

	"github.com/podsteer/podsteer/app/ports"
)

// The package these live in had no tests at all, which is why `go test -race`
// never saw the two most concurrent files in the application. The size queue
// is the piece with a genuine happens-before requirement, so it is the piece
// worth asserting on.

// TestSizeQueueSendAfterCloseDoesNotPanic pins the crash this type exists to
// prevent.
//
// A send on a closed channel panics, and `select` with a `default` does not
// change that — it only avoids blocking. Before the queue owned its own
// closing, Resize read a session out of the map, the exec goroutine finished
// and closed the channel, and the send that followed took the whole desktop
// process down.
func TestSizeQueueSendAfterCloseDoesNotPanic(t *testing.T) {
	t.Parallel()

	q := newTerminalSizeQueue()
	q.close()

	// The assertion is that this returns at all.
	q.send(ports.TerminalSize{Width: 80, Height: 24})
}

// TestSizeQueueCloseIsIdempotent covers the cleanup path running twice.
func TestSizeQueueCloseIsIdempotent(t *testing.T) {
	t.Parallel()

	q := newTerminalSizeQueue()
	q.close()
	q.close()

	if size := q.Next(); size != nil {
		t.Fatalf("Next() after close = %v, want nil", size)
	}
}

// TestSizeQueueConcurrentSendAndClose is the one that needs -race.
//
// It reproduces the real sequence — a window being dragged while the shell
// exits — with enough repetitions that an unsynchronised implementation fails
// reliably rather than occasionally.
func TestSizeQueueConcurrentSendAndClose(t *testing.T) {
	t.Parallel()

	for range 200 {
		q := newTerminalSizeQueue()

		var wg sync.WaitGroup

		// Two senders, because xterm.js emits resizes continuously during a
		// drag rather than one at a time.
		for range 2 {
			wg.Go(func() {
				for range 20 {
					q.send(ports.TerminalSize{Width: 80, Height: 24})
				}
			})
		}

		// The exec goroutine's cleanup, racing them.
		wg.Go(q.close)

		wg.Wait()
	}
}

// TestSizeQueueNextDrainsThenReportsClose checks the reader contract the
// remote shell depends on: sizes come through, and a closed queue ends the
// loop rather than yielding a zero size forever.
func TestSizeQueueNextDrainsThenReportsClose(t *testing.T) {
	t.Parallel()

	q := newTerminalSizeQueue()
	q.send(ports.TerminalSize{Width: 120, Height: 40})

	got := q.Next()
	if got == nil {
		t.Fatal("Next() = nil, want a size")
	}
	if got.Width != 120 || got.Height != 40 {
		t.Fatalf("Next() = %dx%d, want 120x40", got.Width, got.Height)
	}

	q.close()
	if size := q.Next(); size != nil {
		t.Fatalf("Next() after close = %v, want nil", size)
	}
}

// TestSizeQueueDropsWhenFull documents that a backlog is not queued.
//
// Only the latest size matters: an intermediate size from halfway through a
// drag is of no interest by the time the shell reads it.
func TestSizeQueueDropsWhenFull(t *testing.T) {
	t.Parallel()

	q := newTerminalSizeQueue()
	q.send(ports.TerminalSize{Width: 1, Height: 1})
	q.send(ports.TerminalSize{Width: 2, Height: 2})

	got := q.Next()
	if got == nil || got.Width != 1 {
		t.Fatalf("Next() = %v, want the first size", got)
	}
}
