package wails

import (
	"time"

	"github.com/podsteer/podsteer/app/domain"
)

// The DTOs for the operator panels — the typed detail views PodSteer ships
// for the controllers people actually run, in place of the plugin API this
// project decided not to have.
//
// ALMOST NOTHING CROSSES THIS BOUNDARY, and that is the point. Each panel is
// rendered from the ONE manifest the drawer already fetched, parsed in the
// frontend, because quoting a field is not a decision and does not need a
// round trip. Only two things are here: the severity counts a scanner wrote
// (which no manifest on screen contains) and the one certificate verdict,
// which is a comparison against the clock and therefore belongs in the Go
// domain with a test.

// VulnerabilitySummary is what a scanner already running in the cluster
// recorded about ONE workload, as a pod row reads it.
//
// FIVE NUMBERS AND A COUNT, never a verdict. Nothing here ranks a workload
// against another or decides what "acceptable" is: an operator running a
// scanner has a policy already, and the row shows what their scanner found.
type VulnerabilitySummary struct {
	// Subject is "Kind/name" — the same shape Pod.ControlledBy carries, which
	// is how a row finds its own summary without either side re-deriving the
	// other's format.
	Subject  string `json:"subject"`
	Critical int    `json:"critical"`
	High     int    `json:"high"`
	Medium   int    `json:"medium"`
	Low      int    `json:"low"`
	// Unknown is the scanner's own bucket for a finding whose severity its
	// sources do not state. Carried rather than folded into Low.
	Unknown int `json:"unknown"`
	// Reports is how many reports were summed — one per container. It is what
	// keeps "scanned, and clean" distinguishable from "not scanned", which a
	// row of five zeroes cannot say on its own.
	Reports int `json:"reports"`
}

// toVulnerabilitySummaries converts the domain's per-workload sums.
func toVulnerabilitySummaries(summaries []domain.VulnerabilitySummary) []VulnerabilitySummary {
	out := make([]VulnerabilitySummary, 0, len(summaries))
	for _, summary := range summaries {
		out = append(out, VulnerabilitySummary{
			Subject:  summary.Subject,
			Critical: summary.Counts.Critical,
			High:     summary.Counts.High,
			Medium:   summary.Counts.Medium,
			Low:      summary.Counts.Low,
			Unknown:  summary.Counts.Unknown,
			Reports:  summary.Reports,
		})
	}
	return out
}

// CertificateRenewalRef is what a cert-manager Certificate's status says
// about its own expiry, as the panel read it off the manifest it already
// holds.
//
// The certificate equivalent of ConditionRef, and here for the same reason:
// the panel is pure quotation, but "this is running out and nothing is
// renewing it" is a COMPARISON against the clock, and comparisons are
// verdicts that live in the Go domain where a test can argue with them. The
// timestamps cross as RFC 3339 strings — what the object itself carries — and
// one that does not parse is treated as absent rather than as an error, since
// the panel is showing that same field verbatim beside this.
type CertificateRenewalRef struct {
	// NotAfter and RenewalTime are status.notAfter and status.renewalTime.
	// Empty is meaningful for both: nothing issued yet, and no renewal
	// scheduled.
	NotAfter    string `json:"notAfter"`
	RenewalTime string `json:"renewalTime"`
	// ReadyStatus and ReadyReason are the Ready condition's own words.
	ReadyStatus string `json:"readyStatus"`
	ReadyReason string `json:"readyReason"`
	// IssuingStatus is the Issuing condition's status, "True" while a
	// (re)issuance is in flight.
	IssuingStatus string `json:"issuingStatus"`
	// FailedIssuanceAttempts is status.failedIssuanceAttempts, which
	// cert-manager clears on success — so a non-zero value is a current fact.
	FailedIssuanceAttempts int `json:"failedIssuanceAttempts"`
}

// toCertificateRenewal parses the reference into the domain's own value.
//
// An unparseable timestamp becomes the zero time, which is exactly how the
// domain reads an absent one, and is the honest reading: a date this cannot
// read is not a date to draw conclusions from.
func toCertificateRenewal(ref CertificateRenewalRef) domain.CertificateRenewal {
	return domain.CertificateRenewal{
		NotAfter:               parseTimestamp(ref.NotAfter),
		RenewalTime:            parseTimestamp(ref.RenewalTime),
		ReadyStatus:            ref.ReadyStatus,
		ReadyReason:            ref.ReadyReason,
		IssuingStatus:          ref.IssuingStatus,
		FailedIssuanceAttempts: ref.FailedIssuanceAttempts,
	}
}

// parseTimestamp reads an RFC 3339 timestamp, yielding the zero time for
// anything it cannot read.
func parseTimestamp(value string) time.Time {
	if value == "" {
		return time.Time{}
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}
	}
	return parsed
}
