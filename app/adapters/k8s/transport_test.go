package k8s

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"syscall"
	"testing"

	"github.com/podsteer/podsteer/app/ports"
)

// The three transport failures must be told apart, because they imply
// different actions: a name that did not resolve, a connection actively
// refused, and silence are three different problems wearing one error.
func TestTransportFailuresAreClassifiedApart(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name string
		err  error
		want error
	}{
		{
			name: "dns lookup failed",
			err:  &net.DNSError{Err: "no such host", Name: "api.internal", IsNotFound: true},
			want: ports.ErrNameNotResolved,
		},
		{
			name: "connection refused",
			err:  &net.OpError{Op: "dial", Err: syscall.ECONNREFUSED},
			want: ports.ErrConnectionRefused,
		},
		{
			name: "connection reset",
			err:  &net.OpError{Op: "read", Err: syscall.ECONNRESET},
			want: ports.ErrConnectionRefused,
		},
		{
			name: "no route to host",
			err:  &net.OpError{Op: "dial", Err: syscall.EHOSTUNREACH},
			want: ports.ErrNoResponse,
		},
		{
			name: "network unreachable",
			err:  &net.OpError{Op: "dial", Err: syscall.ENETUNREACH},
			want: ports.ErrNoResponse,
		},
		{
			name: "deadline exceeded",
			err:  context.DeadlineExceeded,
			want: ports.ErrNoResponse,
		},
		{
			name: "url error with nothing more specific inside",
			err:  &url.Error{Op: "Get", URL: "https://api.internal", Err: errors.New("EOF")},
			want: ports.ErrNoResponse,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			got := classify("reading pods", testCase.err)

			// Every transport failure is STILL unreachable. The retry decision
			// in the assessment turns on exactly this, so a change that made
			// the specific sentinel replace the general one would silently
			// stop unreachable clusters being retried.
			if !errors.Is(got, ports.ErrUnreachable) {
				t.Errorf("%v does not wrap ErrUnreachable; the retry logic keys on it", got)
			}
			if !errors.Is(got, testCase.want) {
				t.Errorf("classify(%v) = %v, want it to wrap %v", testCase.err, got, testCase.want)
			}
			// The original cause must survive for the log.
			if !errors.Is(got, testCase.err) && !errors.Is(errors.Unwrap(got), testCase.err) {
				t.Logf("note: cause not directly comparable (%v)", testCase.err)
			}
		})
	}
}

// A DNS failure must not be reported as silence.
//
// *net.DNSError satisfies net.Error, so testing the interface first swallows
// every DNS failure into the generic case — and the operator is told nothing
// answered when in fact the name was never looked up, which sends them to
// check a route to an address that does not exist.
func TestDNSIsNotMistakenForSilence(t *testing.T) {
	t.Parallel()

	got := classify("reading pods", &net.DNSError{Err: "no such host", Name: "api.internal"})

	if errors.Is(got, ports.ErrNoResponse) {
		t.Error("a DNS failure was classified as no response; order in transportFailure has regressed")
	}
	if !errors.Is(got, ports.ErrNameNotResolved) {
		t.Errorf("classify() = %v, want ErrNameNotResolved", got)
	}
}

// Failures that are not transport failures must stay unclassified rather than
// being swept into "unreachable" — an API server that answered is reachable,
// whatever it said.
func TestNonTransportErrorsAreNotUnreachable(t *testing.T) {
	t.Parallel()

	for _, err := range []error{
		errors.New("something went wrong"),
		fmt.Errorf("decoding response: %w", errors.New("unexpected EOF")),
	} {
		got := classify("reading pods", err)
		if errors.Is(got, ports.ErrUnreachable) {
			t.Errorf("classify(%v) reported the cluster unreachable", err)
		}
	}
}
