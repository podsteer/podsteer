package domain

import "time"

// UsagePoint is one measurement of one object's usage, taken from a read
// that happened anyway.
type UsagePoint struct {
	At          time.Time
	CPUMilli    int64
	MemoryBytes int64
}

// UsageRingCapacity is the most points kept per pod: a little over half an
// hour at the default ten-second refresh — the drawer chart's span — and a
// bound on memory whatever the cadence.
const UsageRingCapacity = 200

// UsageRingMaxAge is how long a pod no read has measured keeps its series,
// and how old a point may be and still be served. Past it the pod is
// forgotten: it has gone, or its namespace is no longer being read.
const UsageRingMaxAge = time.Hour
