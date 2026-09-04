package domain_test

import (
	"strings"
	"testing"
	"time"

	"github.com/podsteer/podsteer/app/domain"
)

// renewalNow anchors every window below. A rule about dates cannot be argued
// with unless "now" is one of the arguments.
var renewalNow = time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)

// healthyRenewal is a Certificate doing exactly what it should: issued,
// Ready, months from expiry, with a renewal cert-manager has scheduled and
// not yet reached. Every case below is this one with something changed, so
// what the case is actually testing is the diff.
func healthyRenewal() domain.CertificateRenewal {
	return domain.CertificateRenewal{
		NotAfter:      renewalNow.Add(60 * 24 * time.Hour),
		RenewalTime:   renewalNow.Add(30 * 24 * time.Hour),
		ReadyStatus:   "True",
		ReadyReason:   "Ready",
		IssuingStatus: "False",
	}
}

func TestAssessCertificateRenewalSaysNothingAboutAHealthyCertificate(t *testing.T) {
	// The negative case, and the one that matters most: a panel that always
	// has something to say is one people stop reading. Almost every
	// Certificate in a working cluster looks like this.
	if insights := domain.AssessCertificateRenewal(healthyRenewal(), renewalNow); len(insights) != 0 {
		t.Fatalf("AssessCertificateRenewal() = %+v, want nothing for a healthy certificate", insights)
	}
}

func TestAssessCertificateRenewalSaysNothingWhileRenewingOnSchedule(t *testing.T) {
	// Inside the renewal window AND Ready is cert-manager working normally:
	// it renews ahead of expiry and the old certificate keeps serving until
	// the new one lands. Flagging this would flag every certificate in the
	// cluster for a third of its life.
	cert := healthyRenewal()
	cert.RenewalTime = renewalNow.Add(-2 * time.Hour)

	if insights := domain.AssessCertificateRenewal(cert, renewalNow); len(insights) != 0 {
		t.Fatalf("AssessCertificateRenewal() = %+v, want nothing while renewing on schedule", insights)
	}
}

func TestAssessCertificateRenewalFlagsARenewalDueAndNotReady(t *testing.T) {
	// The verdict the rule exists for: the renewal cert-manager itself
	// scheduled has come due and it is not reporting the certificate Ready.
	cert := healthyRenewal()
	cert.RenewalTime = renewalNow.Add(-3 * 24 * time.Hour)
	cert.NotAfter = renewalNow.Add(5 * 24 * time.Hour)
	cert.ReadyStatus = "False"
	cert.ReadyReason = "Failed"

	insights := domain.AssessCertificateRenewal(cert, renewalNow)
	if len(insights) != 1 {
		t.Fatalf("AssessCertificateRenewal() returned %d insights, want exactly 1", len(insights))
	}
	if insights[0].Severity != domain.SeverityCritical {
		t.Errorf("severity = %q, want critical", insights[0].Severity)
	}
	if !strings.Contains(insights[0].Detail, "3 days ago") {
		t.Errorf("detail %q does not say how overdue the renewal is", insights[0].Detail)
	}
	// cert-manager's own reason, carried rather than paraphrased.
	if !strings.Contains(insights[0].Detail, "Failed") {
		t.Errorf("detail %q drops cert-manager's own reason", insights[0].Detail)
	}
	if insights[0].Advice == "" {
		t.Error("advice is empty — an insight without one is an observation")
	}
}

func TestAssessCertificateRenewalReportsAnExpiredCertificateAsExpired(t *testing.T) {
	// Expired and expiring are the same fact said twice when both fire, and
	// only one of them is true. Expired wins.
	cert := healthyRenewal()
	cert.NotAfter = renewalNow.Add(-24 * time.Hour)
	cert.RenewalTime = renewalNow.Add(-10 * 24 * time.Hour)
	cert.ReadyStatus = "False"

	insights := domain.AssessCertificateRenewal(cert, renewalNow)
	if len(insights) != 1 {
		t.Fatalf("AssessCertificateRenewal() returned %d insights, want exactly 1", len(insights))
	}
	if insights[0].Title != "Certificate has expired" {
		t.Errorf("title = %q, want the expired one", insights[0].Title)
	}
	if insights[0].Severity != domain.SeverityCritical {
		t.Errorf("severity = %q, want critical", insights[0].Severity)
	}
}

func TestAssessCertificateRenewalNamesAFailingIssuanceAheadOfTheSymptom(t *testing.T) {
	// "Issuance is failing" says the cause; "renewal is due and not ready"
	// says the symptom. When both are true the cause is the more useful
	// sentence, and printing both would be the same finding twice.
	cert := healthyRenewal()
	cert.RenewalTime = renewalNow.Add(-2 * 24 * time.Hour)
	cert.NotAfter = renewalNow.Add(4 * 24 * time.Hour)
	cert.ReadyStatus = "False"
	cert.FailedIssuanceAttempts = 7

	insights := domain.AssessCertificateRenewal(cert, renewalNow)
	if len(insights) != 1 {
		t.Fatalf("AssessCertificateRenewal() returned %d insights, want exactly 1", len(insights))
	}
	if !strings.Contains(insights[0].Title, "Issuance is failing") {
		t.Errorf("title = %q, want the failing-issuance one", insights[0].Title)
	}
	if !strings.Contains(insights[0].Detail, "7 failed issuance attempts") {
		t.Errorf("detail %q does not carry the attempt count", insights[0].Detail)
	}
}

func TestAssessCertificateRenewalIgnoresAFailingIssuanceThatIsNotUrgentYet(t *testing.T) {
	// A retrying issuance on a certificate with two months left is
	// cert-manager backing off, not an outage. It becomes worth saying only
	// once the clock is nearly out — which is what the window is for.
	cert := healthyRenewal()
	cert.ReadyStatus = "False"
	cert.FailedIssuanceAttempts = 3

	if insights := domain.AssessCertificateRenewal(cert, renewalNow); len(insights) != 0 {
		t.Fatalf("AssessCertificateRenewal() = %+v, want nothing two months from expiry", insights)
	}
}

func TestAssessCertificateRenewalWarnsWhenNothingIsScheduledAndExpiryIsClose(t *testing.T) {
	// No renewal on the books at all. This one will not fix itself, and the
	// clock rather than a missing field is what makes it worth saying.
	cert := healthyRenewal()
	cert.RenewalTime = time.Time{}
	cert.NotAfter = renewalNow.Add(6 * 24 * time.Hour)
	cert.ReadyStatus = "False"
	cert.ReadyReason = "DoesNotExist"

	insights := domain.AssessCertificateRenewal(cert, renewalNow)
	if len(insights) != 1 {
		t.Fatalf("AssessCertificateRenewal() returned %d insights, want exactly 1", len(insights))
	}
	if insights[0].Severity != domain.SeverityWarning {
		t.Errorf("severity = %q, want a warning", insights[0].Severity)
	}
	if !strings.Contains(insights[0].Detail, "no renewal scheduled") {
		t.Errorf("detail %q does not say that nothing is scheduled", insights[0].Detail)
	}
}

func TestAssessCertificateRenewalStaysQuietWhileAnIssuanceIsInFlight(t *testing.T) {
	// cert-manager CLEARS renewalTime while it is replacing a certificate,
	// and Ready is briefly not True by design. Reading that pair as an
	// abandoned certificate would flag every renewal in the cluster for the
	// seconds it takes to complete.
	cert := healthyRenewal()
	cert.RenewalTime = time.Time{}
	cert.NotAfter = renewalNow.Add(6 * 24 * time.Hour)
	cert.ReadyStatus = "False"
	cert.IssuingStatus = "True"

	if insights := domain.AssessCertificateRenewal(cert, renewalNow); len(insights) != 0 {
		t.Fatalf("AssessCertificateRenewal() = %+v, want nothing mid-issuance", insights)
	}
}

func TestAssessCertificateRenewalSaysNothingBeforeAnythingIsIssued(t *testing.T) {
	// A Certificate created a second ago has no notAfter. There is nothing
	// to be running out, so there is nothing to say — the panel quotes the
	// Ready condition instead.
	cert := domain.CertificateRenewal{ReadyStatus: "False", ReadyReason: "DoesNotExist"}

	if insights := domain.AssessCertificateRenewal(cert, renewalNow); len(insights) != 0 {
		t.Fatalf("AssessCertificateRenewal() = %+v, want nothing before first issuance", insights)
	}
}

func TestAssessCertificateRenewalReadsTheReadyStatusCaseInsensitively(t *testing.T) {
	// The status is quoted verbatim into the sentence, but the COMPARISON
	// must not depend on a controller's capitalisation: a Ready condition
	// written "true" is still ready, and treating it as unready would flag a
	// healthy certificate.
	cert := healthyRenewal()
	cert.RenewalTime = renewalNow.Add(-2 * 24 * time.Hour)
	cert.NotAfter = renewalNow.Add(5 * 24 * time.Hour)
	cert.ReadyStatus = "true"

	if insights := domain.AssessCertificateRenewal(cert, renewalNow); len(insights) != 0 {
		t.Fatalf("AssessCertificateRenewal() = %+v, want nothing for a lowercase True", insights)
	}
}

func TestAssessCertificateRenewalNamesAnUnreportedReadyStatus(t *testing.T) {
	// An empty status is a fact — cert-manager has said nothing — and
	// printing it into the middle of a sentence as an empty string reads as
	// a bug in PodSteer rather than as silence from the controller.
	cert := healthyRenewal()
	cert.NotAfter = renewalNow.Add(-time.Hour)
	cert.ReadyStatus = ""
	cert.ReadyReason = ""

	insights := domain.AssessCertificateRenewal(cert, renewalNow)
	if len(insights) != 1 {
		t.Fatalf("AssessCertificateRenewal() returned %d insights, want exactly 1", len(insights))
	}
	if !strings.Contains(insights[0].Detail, "(not reported)") {
		t.Errorf("detail %q does not name the missing condition", insights[0].Detail)
	}
}
