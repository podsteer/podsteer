package domain

import (
	"fmt"
	"strings"
	"time"
)

// This file holds the ONE verdict the cert-manager panel is allowed to draw.
//
// Everything else that panel shows — the issuer reference, the DNS names, the
// Secret it writes, the Ready condition's own words — is quotation, parsed
// client-side out of the manifest the drawer already fetched, exactly as the
// GitOps panels quote Argo CD and Flux. Two fields are different:
// status.notAfter and status.renewalTime are DATES, and the question an
// operator opens a Certificate to ask is a comparison between them and the
// clock. A comparison is a verdict, verdicts live here (see CLAUDE.md, "the
// line is quotation versus verdict"), and this one is worth arguing with in a
// test rather than discovering during an outage.
//
// The judgement is deliberately narrow: "this certificate is running out and
// cert-manager is not fixing it". A certificate inside its renewal window
// that cert-manager reports Ready is doing exactly what it is supposed to do
// and produces nothing at all — the same rule pod_assessment.go holds to,
// because a panel that always has something to say is one people stop
// reading.

// certRenewalWarning is how close to expiry a certificate has to be before a
// Ready=False is worth raising on its own, when no renewal is scheduled.
//
// The same fourteen days certExpiryWarning uses for an inspected TLS Secret,
// and shared with it deliberately: an operator who sees "expires soon" on a
// Secret and nothing on the Certificate that writes it would reasonably
// conclude the two disagree. Where cert-manager has computed a renewal time
// of its own, THAT is used instead — the controller knows its own schedule
// better than a constant here does.
const certRenewalWarning = certExpiryWarning

// CertificateRenewal is what a cert-manager Certificate says about its own
// expiry and renewal, as read off the object's status.
//
// FIELDS, NOT AN OBJECT. Nothing here is a cert-manager type: the frontend
// parses the manifest it already holds and hands over the four facts this
// rule compares, the same way ConditionRef carries a condition across for
// ClassifyConditions. That keeps app/domain free of any dependency on a CRD
// whose schema PodSteer does not control.
type CertificateRenewal struct {
	// NotAfter is status.notAfter — when the issued certificate stops being
	// valid. Zero when cert-manager has not issued one yet, which produces no
	// verdict at all: there is nothing to be running out.
	NotAfter time.Time
	// RenewalTime is status.renewalTime — when cert-manager intends to renew.
	//
	// Zero means NO RENEWAL IS SCHEDULED, which is not the same as "renewal
	// is overdue": cert-manager also clears the field while an issuance is
	// actually in flight. Issuing is what tells those apart.
	RenewalTime time.Time
	// ReadyStatus is the Ready condition's status verbatim — "True", "False",
	// "Unknown", or empty when cert-manager has written no condition.
	// Compared case-insensitively and never re-worded.
	ReadyStatus string
	// ReadyReason is cert-manager's own machine-readable reason, quoted into
	// the insight so the operator is given the controller's word for it
	// rather than a paraphrase of it.
	ReadyReason string
	// IssuingStatus is the Issuing condition's status, "True" while a
	// (re)issuance is in flight. It is what keeps an absent RenewalTime from
	// reading as an abandoned certificate during the seconds cert-manager is
	// busy replacing it.
	IssuingStatus string
	// FailedIssuanceAttempts is status.failedIssuanceAttempts, which
	// cert-manager sets while issuance keeps failing and CLEARS on success.
	// Non-zero is therefore a current fact, not a historical one.
	FailedIssuanceAttempts int
}

// AssessCertificateRenewal reports whether a cert-manager Certificate is
// running out without being renewed.
//
// AT MOST ONE INSIGHT, NEVER TWO — the same discipline expiryInsights holds
// to for an inspected chain. A certificate cannot be both expired and
// expiring, and "issuance is failing" and "expires soon" are the same fact
// said twice when they arrive together; the more specific one wins and
// carries the other's numbers in its detail.
//
// A PURE FUNCTION of the facts and a clock, with no I/O and no clock of its
// own, so every rule below is argued with in certmanager_test.go.
func AssessCertificateRenewal(cert CertificateRenewal, now time.Time) []CertificateInsight {
	if cert.NotAfter.IsZero() {
		// Nothing has been issued. The panel says so by quoting the Ready
		// condition; inventing a verdict about a certificate that does not
		// exist yet would flag every Certificate in the seconds after it is
		// created.
		return nil
	}

	remaining := cert.NotAfter.Sub(now)
	ready := strings.EqualFold(cert.ReadyStatus, "True")
	issuing := strings.EqualFold(cert.IssuingStatus, "True")

	switch {
	case remaining <= 0:
		return []CertificateInsight{{
			Severity: SeverityCritical,
			Title:    "Certificate has expired",
			Detail: fmt.Sprintf("It expired on %s, and cert-manager reports Ready=%s%s.",
				cert.NotAfter.Format(time.RFC3339), statusWord(cert.ReadyStatus), reasonSuffix(cert.ReadyReason)),
			Advice: "Anything that checks validity is already refusing connections that present it. Look at the Issuing condition and the issuer this Certificate names — a renewal that could not complete is the usual cause.",
		}}

	case cert.FailedIssuanceAttempts > 0 && remaining <= certRenewalWarning:
		// The most specific statement available: cert-manager has TRIED and
		// is failing, and the clock has nearly run out. Said ahead of the
		// renewal-window rule below because it names the cause rather than
		// the symptom.
		return []CertificateInsight{{
			Severity: SeverityCritical,
			Title:    "Issuance is failing and the certificate is nearly out",
			Detail: fmt.Sprintf("cert-manager records %d failed issuance %s, and the certificate expires %s, on %s.",
				cert.FailedIssuanceAttempts, attemptWord(cert.FailedIssuanceAttempts),
				formatRemaining(remaining), cert.NotAfter.Format(time.RFC3339)),
			Advice: "Read the CertificateRequest and the Order this Certificate produced — a failing issuance is nearly always the issuer refusing, a DNS-01 or HTTP-01 challenge that cannot complete, or a rate limit.",
		}}

	case !ready && renewalDue(cert, now):
		// THE VERDICT THIS FILE EXISTS FOR. The renewal cert-manager itself
		// scheduled has come due and the controller is not reporting the
		// certificate Ready — the state an operator opens a Certificate to
		// find, and the one that is invisible in a list of dates.
		return []CertificateInsight{{
			Severity: SeverityCritical,
			Title:    "Renewal is due and the certificate is not ready",
			Detail: fmt.Sprintf("%s, and the certificate expires %s, on %s. cert-manager reports Ready=%s%s.",
				renewalPhrase(cert, now), formatRemaining(remaining),
				cert.NotAfter.Format(time.RFC3339), statusWord(cert.ReadyStatus), reasonSuffix(cert.ReadyReason)),
			Advice: "The renewal is not completing on its own. Check the issuer this Certificate names, and the CertificateRequest cert-manager created for the renewal — the Secret keeps serving the old certificate until one succeeds.",
		}}

	case !ready && !issuing && remaining <= certRenewalWarning:
		// Not yet inside a scheduled renewal — or with no schedule at all —
		// but close enough to expiry that a Ready=False is worth saying.
		// `issuing` excludes the seconds cert-manager is legitimately
		// mid-replacement, when Ready is briefly not True by design.
		return []CertificateInsight{{
			Severity: SeverityWarning,
			Title:    "Expires soon and is not ready",
			Detail: fmt.Sprintf("It expires %s, on %s, %s. cert-manager reports Ready=%s%s.",
				formatRemaining(remaining), cert.NotAfter.Format(time.RFC3339),
				schedulePhrase(cert), statusWord(cert.ReadyStatus), reasonSuffix(cert.ReadyReason)),
			Advice: "Confirm the issuer is reachable and the Certificate's spec still validates. A certificate with no renewal scheduled will not replace itself.",
		}}

	default:
		// Ready, or renewing on schedule, or nowhere near expiry. Nothing to
		// say, which is the answer for almost every Certificate in a healthy
		// cluster and is why this returns nil rather than an "all clear".
		return nil
	}
}

// renewalDue reports whether cert-manager's own renewal time has passed.
//
// A ZERO RenewalTime IS NOT "DUE". cert-manager clears the field both when it
// has begun an issuance and when it has no schedule at all, so treating its
// absence as an overdue renewal would flag every certificate in the moments
// after one is requested. The absent-schedule case is caught by the expiry
// window instead, where the clock — not a missing field — is the evidence.
func renewalDue(cert CertificateRenewal, now time.Time) bool {
	return !cert.RenewalTime.IsZero() && !now.Before(cert.RenewalTime)
}

// renewalPhrase says how overdue the renewal is, in the operator's units.
func renewalPhrase(cert CertificateRenewal, now time.Time) string {
	overdue := now.Sub(cert.RenewalTime)
	days := int(overdue.Hours() / 24)
	switch {
	case days <= 0:
		return "Renewal was due today"
	case days == 1:
		return "Renewal was due 1 day ago"
	default:
		return fmt.Sprintf("Renewal was due %d days ago", days)
	}
}

// schedulePhrase states whether a renewal is on the books at all, which is
// the difference between a certificate that will fix itself and one that
// will not.
func schedulePhrase(cert CertificateRenewal) string {
	if cert.RenewalTime.IsZero() {
		return "and cert-manager has no renewal scheduled"
	}
	return fmt.Sprintf("with renewal scheduled for %s", cert.RenewalTime.Format(time.RFC3339))
}

// statusWord renders a condition status for a sentence, naming the absence of
// one rather than printing an empty string into the middle of it.
func statusWord(status string) string {
	if strings.TrimSpace(status) == "" {
		return "(not reported)"
	}
	return status
}

// reasonSuffix appends cert-manager's own reason when it wrote one. Its
// vocabulary is the controller's — DoesNotExist, Failed, Issuing — and is
// carried verbatim rather than translated.
func reasonSuffix(reason string) string {
	if strings.TrimSpace(reason) == "" {
		return ""
	}
	return fmt.Sprintf(" (%s)", reason)
}

// attemptWord keeps the count and its noun in agreement.
func attemptWord(attempts int) string {
	if attempts == 1 {
		return "attempt"
	}
	return "attempts"
}
