package domain

// LogOptions are the choices a caller makes when opening a pod log stream —
// what corev1.PodLogOptions ultimately becomes.
//
// A struct rather than a growing list of positional bool/int64 parameters,
// mirroring DrainOptions: StreamLogs already took Follow and TailLines
// positionally before this, and each field added since (SinceSeconds,
// Previous, Timestamps, LimitBytes) would have made every existing call site
// guess which bare bool or int64 it was passing.
type LogOptions struct {
	// Follow keeps the stream open for new lines as the container writes
	// them, until the caller cancels the context.
	Follow bool
	// TailLines caps how many of the most recent lines are requested. Zero
	// means every available line.
	TailLines int64
	// SinceSeconds requests only lines newer than this many seconds ago.
	// Zero means no lower bound — the same "unset" convention TailLines
	// uses, which is safe here because asking for the last zero seconds of
	// logs is never a real request.
	SinceSeconds int64
	// Previous reads the log of the container's PREVIOUS instantiation —
	// the same thing `kubectl logs -p` does, for a container that crashed or
	// was restarted. It combines with every other option normally; it only
	// changes which run of the container is being read.
	Previous bool
	// Timestamps asks the API server to prefix every line with its RFC 3339
	// timestamp. Every caller sends true today: the frontend decides
	// whether to DISPLAY the timestamp, and re-opening the whole stream just
	// to hide a column the frontend can format itself would cost a fresh
	// tail read for what is purely a display preference. Kept as a field
	// rather than hard-coded in the adapter so a future caller — or a test
	// asserting the request shape — is not left guessing whether it is
	// optional.
	Timestamps bool
	// LimitBytes caps how many bytes of log the API server returns before
	// closing the stream, letting an operator raise the default ceiling on
	// a container whose lines it cut short. Zero means no limit is
	// requested — the API server's own default.
	LimitBytes int64
}
